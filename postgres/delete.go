package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// RowDelete is one row to delete: its key, and the values the grid showed.
type RowDelete struct {
	Key map[string]*string `json:"key"` // EditTarget.Key name → this row's value
	Old map[string]*string `json:"old"` // table column → its value when loaded; the delete fails if one changed
}

// DeleteRows deletes rows of target, all in one transaction: either every
// row is deleted or none is. It returns how many rows were deleted.
//
// First it finds each row as the grid showed it, as SaveRows finds one
// (primary key, or ctid plus the whole row's MD5) with every value shown
// still the same, and locks it: a row someone else changed or deleted since
// refuses the whole delete. Then it deletes each by its key. Locked, no
// other session can change those rows any more, so a row that has changed
// or gone by its turn was changed by these deletes themselves: another
// picked row's ON DELETE CASCADE or SET NULL, or a trigger. Rows of other
// tables go or change as their foreign keys say (DeleteEffects lists them
// for the confirmation). A table with DELETE rules is refused: they make a
// DELETE do something else, which can't be checked.
func (s *DBService) DeleteRows(ctx context.Context, target EditTarget, rows []RowDelete) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	seen := map[string]bool{}
	for _, row := range rows {
		id, _ := json.Marshal(row.Key) // map keys come out sorted
		if seen[string(id)] {
			return 0, fmt.Errorf("The same row is in the list twice. %s", nothingDeleted)
		}
		seen[string(id)] = true
	}

	w, err := s.beginRowWrite(ctx, target, nothingDeleted)
	if err != nil {
		return 0, err
	}
	defer w.tx.Rollback(ctx) // no-op after Commit

	var rules []string
	if err := w.tx.QueryRow(ctx, deleteRulesQuery, w.rel.oid).Scan(&rules); err != nil {
		return 0, toQueryError(err)
	}
	if len(rules) > 0 {
		return 0, fmt.Errorf("%s.%s has rules that change what deleting does (%s), so SpyDB doesn't delete its rows. %s",
			w.rel.schema, w.rel.name, strings.Join(rules, ", "), nothingDeleted)
	}

	// 1. Find and lock each row as it was shown.
	matches := make([]rowMatch, len(rows))
	for i, row := range rows {
		edit := RowEdit{Key: row.Key, Old: row.Old}
		m, err := matchRow(w.rel, edit, w.byName, nil)
		if err != nil {
			return 0, fmt.Errorf("%s %s", err.Error(), nothingDeleted)
		}
		matches[i] = m
		n, err := countRows(ctx, w.tx, fmt.Sprintf("SELECT 1 FROM %s AS %s WHERE %s FOR UPDATE",
			w.table, updateAlias, strings.Join(slices.Concat(m.keyCast, m.keyText, m.oldText), " AND ")), m.args)
		switch {
		case err != nil:
			return 0, rowError(w.rel, row.Key, err, nothingDeleted)
		case n == 0:
			return 0, &QueryError{Message: missingRowMessage(ctx, w.tx, w.table, w.rel, edit, w.byName, nothingDeleted)}
		case n > 1:
			// Can't happen (a primary key or ctid names one row); never delete then.
			return 0, fmt.Errorf("%s: %d rows matched instead of one. %s", describeRow(w.rel, row.Key), n, nothingDeleted)
		}
	}

	// 2. Delete each by its key alone.
	keyParams := len(w.rel.keyNames()) // matchRow puts the key's parameters first
	for i, row := range rows {
		m := matches[i]
		key, keyArgs := strings.Join(slices.Concat(m.keyCast, m.keyText), " AND "), m.args[:keyParams]
		tag, err := w.tx.Exec(ctx, fmt.Sprintf("DELETE FROM %s AS %s WHERE %s", w.table, updateAlias, key), keyArgs...)
		if err != nil {
			return 0, rowError(w.rel, row.Key, err, nothingDeleted)
		}
		switch n := tag.RowsAffected(); {
		case n == 1:
			continue
		case n > 1:
			return 0, fmt.Errorf("%s: the delete matched %d rows instead of one. %s", describeRow(w.rel, row.Key), n, nothingDeleted)
		}
		// Its own DELETE removed nothing: already gone, or kept.
		var still bool
		err = w.tx.QueryRow(ctx, fmt.Sprintf("SELECT EXISTS (SELECT 1 FROM %s AS %s WHERE %s)", w.table, updateAlias, key), keyArgs...).Scan(&still)
		switch {
		case err != nil:
			return 0, rowError(w.rel, row.Key, err, nothingDeleted)
		case still:
			return 0, fmt.Errorf("%s wasn't deleted: a trigger, a rule or a row security policy kept it. %s", describeRow(w.rel, row.Key), nothingDeleted)
		case w.rel.keyKind() == "ctid":
			// Without a primary key, a row these deletes changed (SET NULL,
			// a trigger) has a new address: it can't be told from one that's gone.
			return 0, fmt.Errorf("%s changed while the other rows were deleted (a foreign key or a trigger), so SpyDB can't tell whether it's gone. %s Delete it on its own.",
				describeRow(w.rel, row.Key), nothingDeleted)
		}
		// Gone: another picked row's cascade, or a trigger, deleted it.
	}

	// Deferred foreign keys are checked here: a refusal rolls everything back.
	if err := w.tx.Commit(ctx); err != nil {
		return 0, commitError(err)
	}
	return len(rows), nil
}

// commitError explains a failed COMMIT. Postgres refusing it (a deferred
// foreign key), or a failure before anything was sent, means nothing was
// deleted; a connection lost while it ran (or the delete stopped right
// then) leaves it unknown.
func commitError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) || errors.Is(err, pgx.ErrTxCommitRollback) || pgconn.SafeToRetry(err) {
		qe := toQueryError(err)
		qe.Message += " " + nothingDeleted
		return qe
	}
	return &QueryError{Message: "The connection was lost while the delete was finishing, so SpyDB can't tell whether the rows were deleted. Reload the rows to see.",
		Detail: err.Error()}
}

// countRows runs a query and counts the rows it returns.
func countRows(ctx context.Context, tx pgx.Tx, sql string, args []any) (int, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	return n, rows.Err()
}

// The table's rules that act ON DELETE ($1 is its OID).
const deleteRulesQuery = `
SELECT coalesce(pg_catalog.array_agg(r.rulename::text ORDER BY r.rulename), '{}')
FROM pg_catalog.pg_rewrite r WHERE r.ev_class = $1 AND r.ev_type = '4'`

// DeleteEffects is what deleting rows of a table does besides, for the
// confirmation to show before anything is deleted.
type DeleteEffects struct {
	Database string   `json:"database"` // the database it's in, e.g. "cashier"
	Server   string   `json:"server"`   // host:port of the connection
	Role     string   `json:"role"`     // who the delete runs as
	Cascades []string `json:"cascades"` // tables whose rows pointing at the deleted ones are deleted too (ON DELETE CASCADE, also through those)
	SetsNull []string `json:"setsNull"` // tables whose rows pointing at them get NULL or their default (ON DELETE SET NULL / SET DEFAULT)
	Triggers []string `json:"triggers"` // the table's DELETE triggers that fire (enabled, not replica-only)
	Rules    []string `json:"rules"`    // the table's DELETE rules
	Blocked  string   `json:"blocked"`  // why SpyDB won't delete here; "" if it will
}

// Foreign keys with ON DELETE CASCADE, followed from the table: rows they
// delete delete more rows through their own. %[1]s names the table a
// foreign key is on, %[2]s may leave some out: Postgres copies a
// partitioned table's foreign keys onto its partitions (and one pointing at
// a partitioned table onto each of its partitions), so those copies are
// named after the table the key was made on, once (Postgres 12+;
// Postgres 11 only leaves the copies out). %[3]s is the table and, when
// it's partitioned, its partitions, whose own triggers fire too.
const deleteEffectsQuery = `
WITH RECURSIVE reached(relid) AS (
    SELECT $1::oid
  UNION
    SELECT c.conrelid FROM pg_catalog.pg_constraint c JOIN reached r ON c.confrelid = r.relid
    WHERE c.contype = 'f' AND c.confdeltype = 'c' %[2]s
), fks AS (
    SELECT DISTINCT c.confdeltype, pg_catalog.format('%%I.%%I', n.nspname, cl.relname) AS name
    FROM pg_catalog.pg_constraint c JOIN reached r ON c.confrelid = r.relid
    JOIN pg_catalog.pg_class cl ON cl.oid = %[1]s
    JOIN pg_catalog.pg_namespace n ON n.oid = cl.relnamespace
    WHERE c.contype = 'f' AND c.confdeltype IN ('c', 'n', 'd') %[2]s
)
SELECT pg_catalog.current_database(),
       CURRENT_USER::text, -- a keyword, so it can't be schema-qualified
       pg_catalog.has_table_privilege($1::oid, 'DELETE'),
       pg_catalog.has_any_column_privilege($1::oid, 'UPDATE'),
       coalesce((SELECT pg_catalog.array_agg(DISTINCT name ORDER BY name) FROM fks WHERE confdeltype = 'c'), '{}'),
       coalesce((SELECT pg_catalog.array_agg(DISTINCT name ORDER BY name) FROM fks WHERE confdeltype IN ('n', 'd')), '{}'),
       coalesce((SELECT pg_catalog.array_agg(DISTINCT t.tgname::text ORDER BY t.tgname::text) FROM pg_catalog.pg_trigger t
                 WHERE t.tgrelid IN (%[3]s) AND NOT t.tgisinternal AND t.tgenabled IN ('O', 'A') AND t.tgtype & 8 <> 0), '{}')`

// DeleteEffects looks up what deleting rows of target does besides, and
// whether DeleteRows would delete them at all, checked as the role it runs
// as: target's session role (SET ROLE) if it has one, else the login's
// (table tabs).
func (s *DBService) DeleteEffects(ctx context.Context, target EditTarget) (DeleteEffects, error) {
	s.mu.Lock()
	pool := s.pool
	var e DeleteEffects
	if s.info != nil {
		e.Server = fmt.Sprintf("%s:%d", s.info.Host, s.info.Port)
	}
	s.mu.Unlock()
	if pool == nil {
		return DeleteEffects{}, errors.New("Not connected.")
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return DeleteEffects{}, toQueryError(err)
	}
	defer tx.Rollback(ctx)

	rel, err := findRelation(ctx, tx, "n.nspname = $1 AND c.relname = $2", target.Schema, target.Table)
	if err != nil {
		return DeleteEffects{}, toQueryError(err)
	}
	if rel == nil || target.OID != 0 && rel.oid != target.OID {
		return DeleteEffects{}, fmt.Errorf("Table %s.%s isn't the one these rows came from any more. Reload and try again.", target.Schema, target.Table)
	}
	on, copies, tables := "c.conrelid", "", "$1::oid"
	switch {
	case rel.serverVersion >= 120000:
		on = "CASE WHEN c.conparentid = 0 THEN c.conrelid ELSE coalesce(pg_catalog.pg_partition_root(c.conrelid)::oid, c.conrelid) END"
		// pg_partition_tree is empty for a table that isn't partitioned or a partition.
		tables = "SELECT $1::oid UNION SELECT relid FROM pg_catalog.pg_partition_tree($1::oid)"
	case rel.serverVersion >= 110000:
		copies = "AND c.conparentid = 0"
	}
	if err := tx.QueryRow(ctx, deleteRulesQuery, rel.oid).Scan(&e.Rules); err != nil {
		return DeleteEffects{}, toQueryError(err)
	}
	if err := applySession(ctx, tx, target.Session); err != nil {
		return DeleteEffects{}, toQueryError(err)
	}
	var canDelete, canLock bool
	err = tx.QueryRow(ctx, fmt.Sprintf(deleteEffectsQuery, on, copies, tables), rel.oid).Scan(
		&e.Database, &e.Role, &canDelete, &canLock, &e.Cascades, &e.SetsNull, &e.Triggers)
	if err != nil {
		return DeleteEffects{}, toQueryError(err)
	}
	switch {
	case !canDelete:
		e.Blocked = fmt.Sprintf("Role %s isn’t allowed to delete from this table.", e.Role)
	case !canLock:
		// SELECT … FOR UPDATE needs UPDATE on some column.
		e.Blocked = fmt.Sprintf("Role %s may delete here but not update, and SpyDB locks each row (which needs UPDATE) to check it before deleting.", e.Role)
	case len(e.Rules) > 0:
		e.Blocked = fmt.Sprintf("This table has delete rules (%s): they change what deleting does, so SpyDB won’t delete here.", strings.Join(e.Rules, ", "))
	}
	return e, nil
}
