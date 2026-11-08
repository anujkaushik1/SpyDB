package main

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// The query tab adds RETURNING * to an UPDATE you run without one, so the
// rows it changed show up without a separate SELECT. It does so only where
// that can't make the UPDATE fail: the target must be an ordinary or
// partitioned table you may SELECT, without row-level security or rules.
// (UPDATE … RETURNING needs SELECT privilege, applies SELECT policies, and
// fails under a rule without a RETURNING clause.) Anything else runs as
// written.

const (
	returningClause = " RETURNING *"
	returningNote   = "rows your UPDATE changed (SpyDB added RETURNING *)"
)

// returnable is an UPDATE in a run that could get RETURNING *.
type returnable struct {
	table   string // the target as written, quoted, for to_regclass
	end     int    // byte offset just after the statement's last token
	ordinal int    // which of the run's UPDATE statements it is, from 0
}

// Statements that may come before a rewritten UPDATE in the same run: they
// can't change what a table name means or who runs what follows (unlike SET,
// DDL, DO or CALL), since targets are looked up before the run starts.
var harmlessStatements = map[string]bool{
	"BEGIN": true, "START": true, "COMMIT": true, "END": true, "ROLLBACK": true, "ABORT": true,
	"SAVEPOINT": true, "RELEASE": true, "SELECT": true, "INSERT": true, "UPDATE": true,
	"DELETE": true, "MERGE": true, "WITH": true, "VALUES": true, "TABLE": true, "EXPLAIN": true,
}

// findReturnable lists the plain UPDATEs in sql that have no RETURNING
// clause, numbered among all the run's UPDATE statements (whose results say
// "UPDATE n"). It stops at the first statement that isn't harmless, or that
// calls set_config.
func findReturnable(sql string, backslashQuotes bool) []returnable {
	tokens, ok := lexSQL(sql, backslashQuotes)
	if !ok {
		return nil
	}
	var found []returnable
	ordinal := 0
	for len(tokens) > 0 {
		end := len(tokens)
		for i, t := range tokens {
			if t.punct(";") && t.depth == 0 {
				end = i
				break
			}
		}
		stmt := tokens[:end]
		tokens = tokens[min(end+1, len(tokens)):]
		if len(stmt) == 0 {
			continue
		}
		if stmt[0].kind != 'w' || !harmlessStatements[strings.ToUpper(stmt[0].text)] {
			return found
		}
		for _, t := range stmt {
			if t.is("set_config") {
				return found
			}
		}
		if mainVerb(stmt) != "UPDATE" {
			continue
		}
		// WITH … UPDATE counts (its result says "UPDATE n") but is left alone.
		if table, ok := returnableUpdate(stmt); ok && stmt[0].is("UPDATE") {
			last := stmt[len(stmt)-1]
			found = append(found, returnable{table: table, end: last.start + len(last.text), ordinal: ordinal})
		}
		ordinal++
	}
	return found
}

// mainVerb is the statement's command: its first word, or for WITH … the
// first top-level SELECT / INSERT / UPDATE / DELETE / MERGE / VALUES / TABLE.
func mainVerb(stmt []sqlToken) string {
	if !stmt[0].is("WITH") {
		return strings.ToUpper(stmt[0].text)
	}
	for _, t := range stmt[1:] {
		if t.depth == 0 && t.kind == 'w' {
			switch word := strings.ToUpper(t.text); word {
			case "SELECT", "INSERT", "UPDATE", "DELETE", "MERGE", "VALUES", "TABLE":
				return word
			}
		}
	}
	return "WITH"
}

// returnableUpdate reads UPDATE [ONLY] [schema.]table … SET …, without
// RETURNING, and returns the table name quoted for to_regclass.
func returnableUpdate(stmt []sqlToken) (string, bool) {
	at := func(i int) sqlToken {
		if i < len(stmt) {
			return stmt[i]
		}
		return sqlToken{}
	}
	i := 1
	if at(i).is("ONLY") {
		i++
	}
	var names []string
	for {
		name := identName(at(i))
		if name == "" {
			return "", false
		}
		names = append(names, name)
		i++
		if !at(i).punct(".") {
			break
		}
		i++
	}
	if len(names) > 2 {
		return "", false
	}
	set := false
	for _, t := range stmt[i:] {
		if t.depth == 0 && t.is("RETURNING") {
			return "", false
		}
		set = set || t.depth == 0 && t.is("SET")
	}
	return pgx.Identifier(names).Sanitize(), set
}

// returningPlan records the RETURNING * clauses added to a run.
type returningPlan struct {
	added    []insertion  // in order, at character offsets in the SQL as written
	ordinals map[int]bool // which UPDATE statements got one
}

// planReturning returns sql with RETURNING * added to each UPDATE where it's
// safe (see safeTargets), or sql unchanged.
func planReturning(ctx context.Context, conn *pgx.Conn, sql string) (returningPlan, string) {
	candidates := findReturnable(sql, conn.PgConn().ParameterStatus("standard_conforming_strings") != "on")
	if len(candidates) == 0 {
		return returningPlan{}, sql
	}
	tables := make([]string, len(candidates))
	for i, c := range candidates {
		tables[i] = c.table
	}
	safe, err := safeTargets(ctx, conn, tables)
	if err != nil {
		return returningPlan{}, sql
	}
	return addReturning(sql, candidates, safe)
}

// addReturning adds RETURNING * at the end of each candidate marked safe.
func addReturning(sql string, candidates []returnable, safe []bool) (returningPlan, string) {
	plan := returningPlan{ordinals: map[int]bool{}}
	var b strings.Builder
	last := 0
	for i, c := range candidates {
		if !safe[i] {
			continue
		}
		b.WriteString(sql[last:c.end])
		b.WriteString(returningClause)
		plan.added = append(plan.added, insertion{at: utf8.RuneCountInString(sql[:c.end]), length: len(returningClause)})
		plan.ordinals[c.ordinal] = true
		last = c.end
	}
	if plan.added == nil {
		return returningPlan{}, sql
	}
	b.WriteString(sql[last:])
	return plan, b.String()
}

// originalPosition maps a 1-based error position in the changed SQL back to
// the SQL as written; 0 when it falls inside an added clause.
func (p returningPlan) originalPosition(position int) int {
	shift := 0
	for _, in := range p.added {
		at := in.at + shift
		if position <= at {
			return position - shift
		}
		if position <= at+in.length {
			return 0
		}
		shift += in.length
	}
	return position - shift
}

// UPDATE … RETURNING * on each table can't fail where the UPDATE alone
// works: an ordinary or partitioned table, readable by the current role,
// without row-level security or rules. Missing tables come back NULL.
const returningTargetsQuery = `
SELECT c.relkind IN ('r', 'p') AND NOT c.relrowsecurity AND NOT c.relhasrules
       AND pg_catalog.has_table_privilege(c.oid, 'SELECT')
FROM pg_catalog.unnest($1::text[]) WITH ORDINALITY AS t(name, n)
LEFT JOIN pg_catalog.pg_class c ON c.oid = pg_catalog.to_regclass(t.name)
ORDER BY t.n`

// safeTargets checks tables on the editor's connection, so names resolve as
// the UPDATE's will (search_path, temp tables, SET ROLE). Inside an open
// transaction the lookup runs in a savepoint, so even a failed lookup leaves
// the transaction as it was. A failed transaction is left alone.
func safeTargets(ctx context.Context, conn *pgx.Conn, tables []string) ([]bool, error) {
	inTransaction := false
	switch conn.PgConn().TxStatus() {
	case 'I':
	case 'T':
		inTransaction = true
	default:
		return nil, errors.New("the transaction has failed")
	}
	if inTransaction {
		if _, err := conn.Exec(ctx, "SAVEPOINT spydb_returning", pgx.QueryExecModeSimpleProtocol); err != nil {
			return nil, err
		}
	}
	safe, err := lookupTargets(ctx, conn, tables)
	if inTransaction {
		// Even if the run was cancelled meanwhile, restore the transaction.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err != nil {
			conn.Exec(cleanup, "ROLLBACK TO SAVEPOINT spydb_returning", pgx.QueryExecModeSimpleProtocol)
		}
		if _, releaseErr := conn.Exec(cleanup, "RELEASE SAVEPOINT spydb_returning", pgx.QueryExecModeSimpleProtocol); err == nil {
			err = releaseErr
		}
	}
	return safe, err
}

func lookupTargets(ctx context.Context, conn *pgx.Conn, tables []string) ([]bool, error) {
	rows, err := conn.Query(ctx, returningTargetsQuery, pgx.QueryExecModeExec, tables)
	if err != nil {
		return nil, err
	}
	safe, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (bool, error) {
		var ok *bool
		err := row.Scan(&ok)
		return ok != nil && *ok, err
	})
	if err == nil && len(safe) != len(tables) {
		err = errors.New("unexpected lookup result")
	}
	return safe, err
}
