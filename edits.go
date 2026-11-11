package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// EditTarget is the table that edited cells are saved to, and how each row
// is found again.
type EditTarget struct {
	OID    uint32 `json:"oid"` // the table's OID: a table dropped and re-created under the same name is another table
	Schema string `json:"schema"`
	Table  string `json:"table"`
	// Kind is "pk" when rows are found by primary key, or "ctid" for a table
	// without one: by the row's physical address plus an MD5 of the whole
	// row, so a row that changed (or a new row in a reused address) never matches.
	Kind string `json:"kind"`
	// Key names the values in each row's key, in order: the primary-key
	// columns, or ["ctid", "md5"].
	Key []string `json:"key"`
	// Session holds settings of the session the rows were read in (role,
	// search_path, date/time and number formats); a save uses the same, so
	// it runs with the same privileges and values compare and parse alike.
	Session map[string]string `json:"session"`
	// Conn is the connection the rows were read over (DBService.generation).
	// After a reconnect, maybe to another server through the same tunnel port
	// with the same table OIDs, their keys mean nothing: saves are refused.
	Conn uint64 `json:"conn"`
}

// RowEdit is one row's changed cells.
type RowEdit struct {
	Key    map[string]*string `json:"key"`    // EditTarget.Key name → this row's value
	Old    map[string]*string `json:"old"`    // column → its value when loaded; the save fails if it changed since
	Values map[string]*string `json:"values"` // column → new value; nil sets NULL
}

// SavedRow is one saved row as Postgres now holds it.
type SavedRow struct {
	Key    map[string]*string `json:"key"`    // the row's key after the save (a ctid changes on every update)
	Values map[string]*string `json:"values"` // each saved column, as Postgres shows it now (1.5 may read 1.50)
}

const ctidKey, md5Key = "ctid", "md5"

// Session settings a save copies from the editor's session: who runs it
// (SET ROLE), how names resolve, and how values are written and read as
// text. role is "none" unless SET ROLE was used.
var sessionSettings = []string{"role", "search_path", "TimeZone", "DateStyle", "IntervalStyle",
	"extra_float_digits", "bytea_output", "lc_monetary", "timezone_abbreviations"}

// sessionOf reads conn's sessionSettings; nil if it can't (e.g. a failed
// transaction, where nothing can be edited anyway).
func sessionOf(ctx context.Context, conn *pgx.Conn) map[string]string {
	if conn.PgConn().TxStatus() == 'E' {
		return nil
	}
	exprs := make([]string, len(sessionSettings))
	for i, name := range sessionSettings {
		exprs[i] = fmt.Sprintf("pg_catalog.current_setting('%s')", name)
	}
	values := make([]string, len(sessionSettings))
	dest := make([]any, len(values))
	for i := range values {
		dest[i] = &values[i]
	}
	if err := conn.QueryRow(ctx, "SELECT "+strings.Join(exprs, ", "), pgx.QueryExecModeExec).Scan(dest...); err != nil {
		return nil
	}
	settings := map[string]string{}
	for i, name := range sessionSettings {
		settings[name] = values[i]
	}
	return settings
}

// applySession copies settings into tx (SET LOCAL), in sessionSettings order.
func applySession(ctx context.Context, tx pgx.Tx, settings map[string]string) error {
	for _, name := range sessionSettings {
		v, ok := settings[name]
		if !ok || name == "role" && v == "none" {
			continue
		}
		if _, err := tx.Exec(ctx, "SELECT pg_catalog.set_config($1, $2, true)", name, v); err != nil {
			return err
		}
	}
	return nil
}

// rowHash is an MD5 of a whole row, as SQL; q names the row's table. It
// hashes the binary form (record_send), which no session setting changes
// and which stays half the size of text for bytea.
func rowHash(q string) string { return "pg_catalog.md5(pg_catalog.record_send(" + q + ".*))" }

// relation is what editing needs to know about a table.
type relation struct {
	oid         uint32
	schema      string
	name        string
	kind        string // pg_class.relkind: r table, p partitioned table, v view, …
	persistence string // t for a temporary table
	hasChildren bool   // other tables inherit from it (or it's partitioned)
	primaryKey  []string
	pkAttnums   []int16 // primaryKey's column numbers, same order
	columns     int     // how many columns it has
	sendable    bool    // every column type has a binary form, so rowHash works
	// Privileges of the role that looked it up (the editor's, before a run).
	canSelect    bool // SELECT on the whole table (needed for rowHash)
	canSelectKey bool // SELECT on the primary-key columns
	canUpdate    bool // UPDATE on the table or some of its columns
	// pg_catalog types the primary key is made of, through domains, arrays,
	// ranges and composite types (see inexactKey).
	pkBaseTypes []string
	// The server's version, and the settings of the session that looked it up.
	serverVersion int // server_version_num, e.g. 150015
	floatDigits   string
	dateStyle     string
}

// The primary key's columns: only the key part of the index, not INCLUDE columns.
const relationQuery = `
SELECT c.oid, n.nspname, c.relname, c.relkind::text, c.relpersistence::text,
       EXISTS (SELECT 1 FROM pg_catalog.pg_inherits h WHERE h.inhparent = c.oid),
       coalesce(k.names, '{}'), coalesce(k.nums, '{}'),
       (SELECT pg_catalog.count(*) FROM pg_catalog.pg_attribute a WHERE a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped)::int,
       coalesce((WITH RECURSIVE types(oid) AS (
           SELECT a.atttypid FROM pg_catalog.pg_attribute a WHERE a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
           UNION
           SELECT inner_type.oid FROM types JOIN pg_catalog.pg_type t ON t.oid = types.oid, LATERAL (
               SELECT t.typelem WHERE t.typcategory = 'A' AND t.typelem <> 0
               UNION ALL SELECT t.typbasetype WHERE t.typtype = 'd'
               UNION ALL SELECT r.rngsubtype FROM pg_catalog.pg_range r WHERE r.rngtypid = t.oid
               UNION ALL SELECT a.atttypid FROM pg_catalog.pg_attribute a
                         WHERE t.typtype = 'c' AND a.attrelid = t.typrelid AND a.attnum > 0 AND NOT a.attisdropped
           ) inner_type(oid))
         SELECT pg_catalog.bool_and(t.typsend <> 0) FROM types JOIN pg_catalog.pg_type t ON t.oid = types.oid), true),
       pg_catalog.has_table_privilege(c.oid, 'SELECT'),
       coalesce(k.readable, true),
       pg_catalog.has_any_column_privilege(c.oid, 'UPDATE'),
       coalesce((WITH RECURSIVE types(oid) AS (
           SELECT a.atttypid FROM pg_catalog.pg_index i
           JOIN pg_catalog.pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY ((i.indkey::int2[])[0:i.indnkeyatts - 1])
           WHERE i.indrelid = c.oid AND i.indisprimary
           UNION
           SELECT inner_type.oid FROM types JOIN pg_catalog.pg_type t ON t.oid = types.oid, LATERAL (
               SELECT t.typelem WHERE t.typcategory = 'A' AND t.typelem <> 0
               UNION ALL SELECT t.typbasetype WHERE t.typtype = 'd'
               UNION ALL SELECT r.rngsubtype FROM pg_catalog.pg_range r WHERE r.rngtypid = t.oid
               UNION ALL SELECT a.atttypid FROM pg_catalog.pg_attribute a
                         WHERE t.typtype = 'c' AND a.attrelid = t.typrelid AND a.attnum > 0 AND NOT a.attisdropped
           ) inner_type(oid))
         SELECT pg_catalog.array_agg(DISTINCT t.typname::text) FROM types
         JOIN pg_catalog.pg_type t ON t.oid = types.oid
         JOIN pg_catalog.pg_namespace tn ON tn.oid = t.typnamespace AND tn.nspname = 'pg_catalog'), '{}'),
       pg_catalog.current_setting('server_version_num')::int,
       pg_catalog.current_setting('extra_float_digits'),
       pg_catalog.current_setting('DateStyle')
FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN LATERAL (
    SELECT pg_catalog.array_agg(a.attname::text ORDER BY a.attnum) AS names, pg_catalog.array_agg(a.attnum ORDER BY a.attnum) AS nums,
           pg_catalog.bool_and(pg_catalog.has_column_privilege(c.oid, a.attnum, 'SELECT')) AS readable
    FROM pg_catalog.pg_index i
    JOIN pg_catalog.pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY ((i.indkey::int2[])[0:i.indnkeyatts - 1])
    WHERE i.indrelid = c.oid AND i.indisprimary) k ON true
WHERE `

// findRelation looks a relation up; nil (and no error) when there's none.
func findRelation(ctx context.Context, q querier, where string, args ...any) (*relation, error) {
	var r relation
	err := q.QueryRow(ctx, relationQuery+where, args...).Scan(
		&r.oid, &r.schema, &r.name, &r.kind, &r.persistence, &r.hasChildren, &r.primaryKey, &r.pkAttnums,
		&r.columns, &r.sendable, &r.canSelect, &r.canSelectKey, &r.canUpdate,
		&r.pkBaseTypes, &r.serverVersion, &r.floatDigits, &r.dateStyle)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// readOnly says why rows of r can't be edited, or "".
func (r *relation) readOnly() string {
	switch r.kind {
	case "r", "p":
	case "v":
		return "a view"
	case "m":
		return "a materialized view"
	case "f":
		return "a foreign table"
	default:
		return "not a table"
	}
	switch {
	case r.schema == "pg_catalog" || r.schema == "information_schema" || strings.HasPrefix(r.schema, "pg_toast"):
		return "a system catalog"
	case r.persistence == "t":
		return "a temporary table" // saves use another session, which can't see it
	case r.kind == "r" && r.hasChildren:
		return "a table other tables inherit from" // its primary key doesn't cover their rows
	case r.kind == "p" && len(r.primaryKey) == 0:
		return "a partitioned table without a primary key"
	case len(r.primaryKey) == 0 && !r.sendable:
		return "no primary key, and a column type SpyDB can't fingerprint"
	}
	return ""
}

// privilegeProblem says why the role that looked r up can't edit it, or "".
func (r *relation) privilegeProblem() string {
	switch {
	case !r.canUpdate:
		return "no permission to update this table"
	case r.keyKind() == "pk" && !r.canSelectKey:
		return "no permission to read the primary key"
	case r.keyKind() == "ctid" && !r.canSelect:
		return "no permission to read every column, which rows without a primary key need"
	}
	return ""
}

// inexactKey says why r's primary key, printed with these session settings
// (extra_float_digits, DateStyle), may not name one row, or "". A save finds
// a row by its key as shown, read back: if two rows print the same, it can
// find the other one. Floats need enough digits (Postgres 12 prints the
// shortest exact form from extra_float_digits 1; older servers need 3), and
// timestamptz needs ISO, whose numeric offsets don't repeat: zone names do
// (Moscow's 01:30 MSK meant 21:30 UTC, then an hour later 22:30 UTC).
func (r *relation) inexactKey(settings map[string]string) string {
	for _, t := range r.pkBaseTypes {
		switch t {
		case "float4", "float8":
			need := 1
			if r.serverVersion < 120000 {
				need = 3
			}
			if digits, err := strconv.Atoi(settings["extra_float_digits"]); err != nil || digits < need {
				return "a floating-point key shown rounded (extra_float_digits)"
			}
		case "timestamptz":
			if !strings.HasPrefix(settings["DateStyle"], "ISO") {
				return "a timestamptz key shown with a zone name (DateStyle isn't ISO)"
			}
		}
	}
	return ""
}

// keyKind is how rows of r are found again: "pk" or "ctid".
func (r *relation) keyKind() string {
	if len(r.primaryKey) > 0 {
		return "pk"
	}
	return "ctid"
}

// keyColumns are the SQL expressions for a row's key, with q naming the table.
func (r *relation) keyColumns(q string) []string {
	if r.keyKind() == "ctid" {
		return []string{q + ".ctid", rowHash(q)}
	}
	exprs := make([]string, len(r.primaryKey))
	for i, c := range r.primaryKey {
		exprs[i] = q + "." + pgx.Identifier{c}.Sanitize()
	}
	return exprs
}

func (r *relation) keyNames() []string {
	if r.keyKind() == "ctid" {
		return []string{ctidKey, md5Key}
	}
	return r.primaryKey
}

// editPlan is RunQuery's decision, made before running, about editing the
// result: whether the SQL has the right shape, and which key columns it adds.
type editPlan struct {
	reason        string // why the result is read-only; "" if it may be editable
	inTransaction bool   // the editor was inside a transaction, so nothing was added
	returning     bool   // the rows came from UPDATE … RETURNING * (planReturning)
	rel           *relation
	added         []string // aliases of the key columns added at the end; nil if none
	insertion     insertion
}

// addedAliases names n added key columns with prefix; the result is checked
// to really have them.
func addedAliases(prefix string, n int) []string {
	aliases := make([]string, n)
	for i := range aliases {
		aliases[i] = fmt.Sprintf("%s_%d", prefix, i+1)
	}
	return aliases
}

// planEdit decides how to run sql so its result can be edited, returning
// the SQL to run (sql itself, or sql with key columns added). Lookups run on
// the editor's connection, so the table resolves as the query will (same
// search_path), but only outside a transaction: a failed lookup inside one
// would abort the user's transaction. Inside one, results stay editable
// only when they show the whole primary key.
func planEdit(ctx context.Context, conn *pgx.Conn, sql string) (editPlan, string) {
	pg := conn.PgConn()
	shape, reason := analyzeSelect(sql, pg.ParameterStatus("standard_conforming_strings") != "on")
	if reason != "" {
		return editPlan{reason: reason}, sql
	}
	if pg.TxStatus() != 'I' {
		return editPlan{inTransaction: true}, sql
	}
	name := strings.Join(shape.table, ".")
	rel, err := findRelation(ctx, conn, "c.oid = pg_catalog.to_regclass($1)", pgx.QueryExecModeExec, name)
	if err != nil || rel == nil {
		return editPlan{}, sql // the query will say what's wrong, or the result is checked afterwards
	}
	if reason := rel.readOnly(); reason != "" {
		return editPlan{reason: reason}, sql
	}
	if reason := rel.privilegeProblem(); reason != "" {
		return editPlan{reason: reason}, sql
	}
	if rel.keyKind() == "pk" && shape.shows(rel.primaryKey) {
		return editPlan{rel: rel}, sql
	}
	// Added columns mustn't change what the query means: ORDER BY 3 on a
	// two-column result has to stay an error, and the SQL must not be able to
	// name them, so their names appear nowhere in it.
	if shape.maxOrdinal > shape.width(rel.columns) {
		return editPlan{rel: rel}, sql
	}
	prefix := "__spydb_key"
	for strings.Contains(strings.ToLower(sql), prefix) {
		prefix += "_x"
	}
	exprs := rel.keyColumns(shape.qualifier)
	plan := editPlan{rel: rel, added: addedAliases(prefix, len(exprs))}
	changed, in := shape.addColumns(sql, exprs, plan.added)
	plan.insertion = in
	// Parse and analyze the changed SQL without running it. If it fails
	// (say an aggregate makes the key columns invalid), the query runs as
	// written instead: it never runs twice.
	if _, err := pg.Prepare(ctx, "", changed, nil); err != nil {
		return editPlan{reason: "SpyDB couldn't add the row's key to this query"}, sql
	}
	return plan, changed
}

// resolve works out whether st, the statement's result, can be edited, after
// it ran. It removes the key columns the plan added (whatever happens, so
// they're never shown), fills in each column's Source and PrimaryKey, and
// returns the target with every stored row's key, or why it's read-only.
func (p editPlan) resolve(ctx context.Context, q querier, session map[string]string, st *statementData) (*EditTarget, [][]*string, string) {
	added := len(p.added)
	var addedNames []string
	var addedOIDs []uint32
	var addedAttnums []uint16
	var addedValues [][]*string
	if added > 0 {
		n := len(st.columns) - added
		if n < 0 {
			return nil, nil, "SpyDB couldn't match result rows to table rows"
		}
		for _, c := range st.columns[n:] {
			addedNames = append(addedNames, c.Name)
		}
		addedOIDs, addedAttnums = st.tableOIDs[n:], st.attNums[n:]
		st.columns, st.tableOIDs, st.attNums = st.columns[:n], st.tableOIDs[:n], st.attNums[:n]
		addedValues = make([][]*string, len(st.rows))
		for i, row := range st.rows {
			addedValues[i] = row[n:]
			st.rows[i] = row[:n]
		}
	}
	if p.reason != "" {
		return nil, nil, p.reason
	}

	// Rows an UPDATE returned (RETURNING *) list the updated table's columns
	// first, then any FROM tables' (UPDATE … FROM): other rows, maybe of the
	// same table. Only a result of just the table's columns is editable.
	var oid uint32
	if p.returning {
		if len(st.tableOIDs) > 0 {
			oid = st.tableOIDs[0]
		}
	} else {
		for _, o := range st.tableOIDs {
			switch {
			case o == 0:
			case oid == 0:
				oid = o
			case o != oid:
				return nil, nil, "columns from more than one table"
			}
		}
	}
	if oid == 0 {
		return nil, nil, "no table columns in the result"
	}

	// The table as it is now, by OID, so search_path can't matter.
	rel, err := findRelation(ctx, q, "c.oid = $1", oid)
	if err != nil || rel == nil {
		return nil, nil, "SpyDB couldn't look up the table"
	}
	if reason := rel.readOnly(); reason != "" {
		return nil, nil, reason
	}
	attrs, err := sourceColumns(ctx, q, oid)
	if err != nil {
		return nil, nil, "SpyDB couldn't look up the table"
	}
	if p.returning {
		// Exactly the table's columns, in order.
		nums := slices.Sorted(maps.Keys(attrs))
		if len(st.columns) != len(nums) {
			return nil, nil, updateFromReadOnly
		}
		for i, num := range nums {
			if st.tableOIDs[i] != oid || int16(st.attNums[i]) != num {
				return nil, nil, "SpyDB couldn't match result rows to table rows"
			}
		}
	}
	for i := range st.columns {
		if a, ok := attrs[int16(st.attNums[i])]; ok && st.tableOIDs[i] == oid {
			st.columns[i].Source = a
			st.columns[i].PrimaryKey = slices.Contains(rel.primaryKey, a)
		}
	}
	clearSources := func() {
		for i := range st.columns {
			st.columns[i].Source = ""
			st.columns[i].PrimaryKey = false
		}
	}

	var keys [][]*string
	if added > 0 {
		// The added columns must be what was asked for: this table's primary
		// key, or its ctid (system column -1) and the row hash.
		ok := rel.oid == p.rel.oid && rel.keyKind() == p.rel.keyKind() && slices.Equal(addedNames, p.added)
		if ok && rel.keyKind() == "pk" {
			ok = slices.Equal(rel.pkAttnums, p.rel.pkAttnums)
			for j, num := range rel.pkAttnums {
				ok = ok && addedOIDs[j] == oid && int16(addedAttnums[j]) == num
			}
		} else if ok {
			ok = addedOIDs[0] == oid && int16(addedAttnums[0]) == -1 && addedOIDs[1] == 0
		}
		if !ok {
			clearSources()
			return nil, nil, "SpyDB couldn't match result rows to table rows"
		}
		keys = addedValues
	} else {
		// Nothing added: the result must show the whole primary key.
		if len(rel.primaryKey) == 0 {
			clearSources()
			if p.returning {
				return nil, nil, "the table has no primary key"
			}
			if p.inTransaction {
				return nil, nil, "no primary key, inside a transaction"
			}
			return nil, nil, "SpyDB couldn't add the row's address to this query"
		}
		at := make([]int, len(rel.pkAttnums))
		for j, num := range rel.pkAttnums {
			at[j] = -1
			for i := range st.columns {
				if st.tableOIDs[i] == oid && int16(st.attNums[i]) == num {
					at[j] = i
					break
				}
			}
			if at[j] < 0 {
				clearSources()
				if p.inTransaction {
					return nil, nil, "the primary key isn't shown, inside a transaction"
				}
				return nil, nil, "the primary key isn't in the result"
			}
		}
		keys = make([][]*string, len(st.rows))
		for i, row := range st.rows {
			key := make([]*string, len(at))
			for j, col := range at {
				key[j] = row[col]
			}
			keys[i] = key
		}
	}

	// Every row needs its own key, or an edit could land on the wrong row.
	if !distinctKeys(keys) {
		clearSources()
		return nil, nil, "rows repeat"
	}
	// Keys shown inexactly (rounded floats, zone names) could name another row.
	if reason := rel.inexactKey(session); reason != "" {
		clearSources()
		return nil, nil, reason
	}
	return &EditTarget{OID: rel.oid, Schema: rel.schema, Table: rel.name, Kind: rel.keyKind(), Key: rel.keyNames(), Session: session}, keys, ""
}

// distinctKeys reports whether every key is complete (no NULLs) and unique.
func distinctKeys(keys [][]*string) bool {
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		var b strings.Builder
		for _, v := range key {
			if v == nil {
				return false
			}
			b.WriteString(*v)
			b.WriteByte(0)
		}
		if seen[b.String()] {
			return false
		}
		seen[b.String()] = true
	}
	return true
}

const sourceColumnsQuery = `
SELECT a.attnum, a.attname FROM pg_attribute a
WHERE a.attrelid = $1 AND a.attnum > 0 AND NOT a.attisdropped`

// sourceColumns maps a table's column numbers to names.
func sourceColumns(ctx context.Context, q querier, oid uint32) (map[int16]string, error) {
	rows, err := q.Query(ctx, sourceColumnsQuery, oid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := map[int16]string{}
	for rows.Next() {
		var num int16
		var name string
		if err := rows.Scan(&num, &name); err != nil {
			return nil, err
		}
		names[num] = name
	}
	return names, rows.Err()
}

// SaveRows applies the grid's edits to target as UPDATEs, all in one
// transaction: either every row saves or none does. Each UPDATE must match
// exactly one row: by primary key, or by ctid plus the whole row's MD5; and
// every edited cell must still hold the value it had when loaded, so a row
// someone else changed meanwhile isn't overwritten. It uses the pool, not
// the SQL editor's connection, so an open transaction in the editor doesn't
// swallow or delay the save. It returns each row as saved.
func (s *DBService) SaveRows(ctx context.Context, target EditTarget, edits []RowEdit) ([]SavedRow, error) {
	s.mu.Lock()
	pool, generation := s.pool, s.generation
	s.mu.Unlock()
	if pool == nil {
		return nil, errors.New("Not connected.")
	}
	if target.Conn != generation {
		return nil, errors.New("These rows were loaded over an earlier connection (you've reconnected since). Nothing was saved. Reload and edit them again.")
	}
	if len(edits) == 0 {
		return []SavedRow{}, nil
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, toQueryError(err)
	}
	defer tx.Rollback(ctx) // no-op after Commit

	// Fail fast, rather than wait, if another session holds a lock on a row
	// (e.g. an uncommitted UPDATE in the SQL editor).
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '5s'"); err != nil {
		return nil, toQueryError(err)
	}
	where := fmt.Sprintf("%s.%s", target.Schema, target.Table)
	rel, err := findRelation(ctx, tx, "n.nspname = $1 AND c.relname = $2", target.Schema, target.Table)
	if err != nil {
		return nil, toQueryError(err)
	}
	if rel == nil {
		return nil, fmt.Errorf("Table %s was not found. Refresh the sidebar.", where)
	}
	if target.OID != 0 && rel.oid != target.OID {
		return nil, fmt.Errorf("%s was dropped and created again since these rows were loaded. Nothing was saved. Reload and try again.", where)
	}
	if reason := rel.readOnly(); reason != "" {
		return nil, fmt.Errorf("%s is %s, so its rows can't be edited.", where, reason)
	}
	switch target.Kind {
	case "pk":
		if !sameSet(target.Key, rel.primaryKey) {
			return nil, fmt.Errorf("%s's primary key changed since these rows were loaded. Nothing was saved. Reload and try again.", where)
		}
	case "ctid":
		if rel.kind != "r" || !slices.Equal(target.Key, []string{ctidKey, md5Key}) {
			return nil, fmt.Errorf("%s's rows can't be found by address. Nothing was saved. Reload and try again.", where)
		}
	default:
		return nil, errors.New("SpyDB doesn't know how to find these rows. Nothing was saved. Reload and try again.")
	}

	columns, err := tableColumns(ctx, tx, target.Schema, target.Table)
	if err != nil {
		return nil, err
	}
	byName := map[string]column{}
	for _, c := range columns {
		byName[c.Name] = c
	}

	// From here on, run as the editor's role (SET ROLE), with its
	// search_path and formats. (The lookups above ran before, so a user
	// table named like a catalog can't get in their way.)
	if err := applySession(ctx, tx, target.Session); err != nil {
		return nil, toQueryError(err)
	}

	table := pgx.Identifier{target.Schema, target.Table}.Sanitize()
	if rel.kind == "r" {
		table = "ONLY " + table // never a row of a table created to inherit from it since
	}
	saved := make([]SavedRow, 0, len(edits))
	for _, edit := range edits {
		u, err := buildUpdate(table, rel, edit, byName)
		if err != nil {
			return nil, err
		}
		row, found, err := runUpdate(ctx, tx, u)
		if err != nil {
			qe := toQueryError(err)
			qe.Message = fmt.Sprintf("%s: %s Nothing was saved.", describeRow(rel, edit.Key), qe.Message)
			qe.Position = 0
			return nil, qe
		}
		if !found {
			return nil, &QueryError{Message: missingRowMessage(ctx, tx, table, rel, edit, byName)}
		}
		saved = append(saved, row)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, toQueryError(err)
	}
	return saved, nil
}

// update is one row's UPDATE … RETURNING, and what its returned values are.
type update struct {
	sql      string
	args     []any
	keyNames []string // the first returned values: the row's new key
	columns  []string // then these columns' new values
}

// The UPDATE's alias for the table, used for its row's ctid, hash and columns.
const updateAlias = "spydb_row"

// buildUpdate turns one row edit into UPDATE … SET … WHERE <the row, as it
// was> RETURNING <its new key and values>. Column names must exist in the
// catalog. Values are parameters cast to the column's type without its
// length (varchar, not varchar(3)), so storing them checks the length and
// Postgres reports "value too long" or bad input instead of cutting it short.
// Keys match with = (using the index) and also as text: a key that doesn't
// read back as the same value (IST is Israel to Postgres, 04.03 is April
// under German, MDY) then matches nothing instead of another row. Old values
// compare as text in the column's own output format (what the grid showed),
// which works for every type, even ones without = (json, point, xml). A row
// this save already wrote (its xmin is this transaction) never matches again.
func buildUpdate(table string, rel *relation, edit RowEdit, byName map[string]column) (update, error) {
	if len(edit.Values) == 0 {
		return update{}, errors.New("A row edit has no changed cells.")
	}
	var set []string
	var args []any
	edited := sortedKeys(edit.Values)
	for _, name := range edited {
		c, ok := byName[name]
		if !ok {
			return update{}, fmt.Errorf("Unknown column %q.", name)
		}
		if v := edit.Values[name]; v == nil {
			set = append(set, pgx.Identifier{name}.Sanitize()+" = NULL")
		} else {
			args = append(args, *v)
			set = append(set, fmt.Sprintf("%s = CAST($%d::text AS %s)", pgx.Identifier{name}.Sanitize(), len(args), c.castType))
		}
	}
	m, err := matchRow(rel, edit, byName, args)
	if err != nil {
		return update{}, err
	}
	where := slices.Concat(m.keyCast, m.keyText, m.oldText, m.readBack,
		[]string{fmt.Sprintf("NOT %s.xmin = (pg_catalog.txid_current() %% 4294967296)::text::xid", updateAlias)})

	returning := rel.keyColumns(updateAlias)
	for _, name := range edited {
		returning = append(returning, updateAlias+"."+pgx.Identifier{name}.Sanitize())
	}
	return update{
		sql: fmt.Sprintf("UPDATE %s AS %s SET %s WHERE %s RETURNING %s",
			table, updateAlias, strings.Join(set, ", "), strings.Join(where, " AND "), strings.Join(returning, ", ")),
		args:     m.args,
		keyNames: rel.keyNames(),
		columns:  edited,
	}, nil
}

// rowMatch is the WHERE conditions that find an edited row as it was
// loaded, on the alias updateAlias.
type rowMatch struct {
	keyCast  []string // primary key: each column = its value (uses the index)
	keyText  []string // primary key: each column shows its value as text; or ctid and row hash
	oldText  []string // each edited cell still shows its old value
	readBack []string // each edited date/time cell's old value, as shown, reads back as what's stored
	args     []any
}

// matchRow builds the rowMatch for edit; its parameters follow args.
func matchRow(rel *relation, edit RowEdit, byName map[string]column, args []any) (rowMatch, error) {
	m := rowMatch{args: args}
	param := func(v string) string {
		m.args = append(m.args, v)
		return fmt.Sprintf("$%d", len(m.args))
	}
	col := func(name string) string { return updateAlias + "." + pgx.Identifier{name}.Sanitize() }
	keyValue := func(name string) (string, error) {
		v, ok := edit.Key[name]
		if !ok || v == nil {
			return "", fmt.Errorf("A row edit is missing its %s value.", name)
		}
		return *v, nil
	}

	if rel.keyKind() == "ctid" {
		ctid, err := keyValue(ctidKey)
		if err != nil {
			return m, err
		}
		hash, err := keyValue(md5Key)
		if err != nil {
			return m, err
		}
		m.keyText = append(m.keyText,
			fmt.Sprintf("%s.ctid = CAST(%s::text AS tid)", updateAlias, param(ctid)),
			fmt.Sprintf("%s = %s::text", rowHash(updateAlias), param(hash)))
	} else {
		for _, name := range rel.primaryKey {
			v, err := keyValue(name)
			if err != nil {
				return m, err
			}
			p := param(v)
			m.keyCast = append(m.keyCast, fmt.Sprintf("%s = CAST(%s::text AS %s)", col(name), p, byName[name].castType))
			m.keyText = append(m.keyText, fmt.Sprintf("pg_catalog.format('%%s', %s) = %s::text", col(name), p))
		}
	}
	// num_nulls, not IS NULL: a composite value with NULL fields, (1,), is
	// neither IS NULL nor IS NOT NULL.
	for _, name := range sortedKeys(edit.Old) {
		c, ok := byName[name]
		if !ok {
			return m, fmt.Errorf("Unknown column %q.", name)
		}
		v := edit.Old[name]
		if v == nil {
			m.oldText = append(m.oldText, fmt.Sprintf("pg_catalog.num_nulls(%s) = 1", col(name)))
			continue
		}
		p := param(*v)
		m.oldText = append(m.oldText, fmt.Sprintf("pg_catalog.num_nulls(%s) = 0 AND pg_catalog.format('%%s', %s) = %s::text", col(name), col(name), p))
		// Dates and times may print in a form Postgres reads differently
		// (IST is Israel time to it; under German, MDY 04.03 is April 3), so
		// what's typed in the same form would be stored wrong.
		if (c.typCategory == "D" || c.typCategory == "T") && edit.Values[name] != nil {
			m.readBack = append(m.readBack, fmt.Sprintf("CAST(%s::text AS %s) = %s", p, c.castType, col(name)))
		}
	}
	return m, nil
}

// runUpdate runs u, which must change exactly one row, and returns it as
// text (the grid's format). found is false when no row matched.
func runUpdate(ctx context.Context, tx pgx.Tx, u update) (SavedRow, bool, error) {
	rows, err := tx.Query(ctx, u.sql, append([]any{pgx.QueryResultFormats{pgx.TextFormatCode}}, u.args...)...)
	if err != nil {
		return SavedRow{}, false, err
	}
	defer rows.Close()
	var saved SavedRow
	count := 0
	for rows.Next() {
		count++
		values := rows.RawValues()
		text := func(i int) *string {
			if values[i] == nil {
				return nil
			}
			s := string(values[i])
			return &s
		}
		saved = SavedRow{Key: map[string]*string{}, Values: map[string]*string{}}
		for i, name := range u.keyNames {
			saved.Key[name] = text(i)
		}
		for i, name := range u.columns {
			saved.Values[name] = text(len(u.keyNames) + i)
		}
	}
	if err := rows.Err(); err != nil {
		return SavedRow{}, false, err
	}
	if count > 1 {
		// Can't happen (a primary key or ctid names one row); never commit it.
		return SavedRow{}, false, fmt.Errorf("The update matched %d rows instead of one.", count)
	}
	return saved, count == 1, nil
}

// missingRowMessage explains an UPDATE that matched no row: gone, changed
// since it was loaded, or shown in a form Postgres reads back differently.
func missingRowMessage(ctx context.Context, tx pgx.Tx, table string, rel *relation, edit RowEdit, byName map[string]column) string {
	row := describeRow(rel, edit.Key)
	const reload = "Nothing was saved. Reload and try again."
	const isoHint = "Nothing was saved. Run SET DateStyle = ISO, then the query again."
	m, err := matchRow(rel, edit, byName, nil)
	if err != nil {
		return fmt.Sprintf("%s was changed or deleted since it was loaded. %s", row, reload)
	}
	all := func(conditions []string) string {
		if len(conditions) == 0 {
			return "true"
		}
		return strings.Join(conditions, " AND ")
	}
	var oldOK, readBackOK bool
	err = tx.QueryRow(ctx, fmt.Sprintf("SELECT %s, %s FROM %s AS %s WHERE %s",
		all(m.oldText), all(m.readBack), table, updateAlias, all(slices.Concat(m.keyCast, m.keyText))), m.args...).Scan(&oldOK, &readBackOK)
	switch {
	case errors.Is(err, pgx.ErrNoRows) && rel.keyKind() == "pk":
		var cast bool
		// Only the key's parameters (they come first): Postgres rejects unused ones.
		keyArgs := m.args[:len(rel.primaryKey)]
		if tx.QueryRow(ctx, fmt.Sprintf("SELECT EXISTS (SELECT 1 FROM %s AS %s WHERE %s)", table, updateAlias, all(m.keyCast)), keyArgs...).Scan(&cast) == nil && cast {
			return fmt.Sprintf("%s: the key as shown reads back as a different value in Postgres (it reads IST as Israel time, and dates by DateStyle), so SpyDB can't tell which row it is. %s", row, isoHint)
		}
		return fmt.Sprintf("%s wasn't found; it may have been deleted or its key changed. %s", row, reload)
	case err != nil:
		return fmt.Sprintf("%s was changed or deleted since it was loaded. %s", row, reload)
	case !oldOK:
		return fmt.Sprintf("%s was changed by someone else since it was loaded (%s). Nothing was saved. Reload to see the new values.",
			row, strings.Join(sortedKeys(edit.Old), ", "))
	case !readBackOK:
		return fmt.Sprintf("%s: a date or time as shown reads back as a different value in Postgres (it reads IST as Israel time, and dates by DateStyle), so the edit could store the wrong one. %s", row, isoHint)
	}
	return fmt.Sprintf("%s was changed or deleted since it was loaded. %s", row, reload)
}

// describeRow names a row in messages: "Row id = 5", or by address.
func describeRow(rel *relation, key map[string]*string) string {
	if rel.keyKind() == "ctid" {
		address := "?"
		if v := key[ctidKey]; v != nil {
			address = *v
		}
		return "The row at " + address
	}
	parts := make([]string, 0, len(rel.primaryKey))
	for _, name := range rel.primaryKey {
		value := "NULL"
		if v := key[name]; v != nil {
			value = *v
		}
		parts = append(parts, name+" = "+value)
	}
	return "Row " + strings.Join(parts, ", ")
}

func sortedKeys(m map[string]*string) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names) // stable SQL for the same edit
	return names
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}
