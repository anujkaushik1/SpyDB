package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// EditTarget is the table that edited cells are saved to.
type EditTarget struct {
	Schema string `json:"schema"`
	Table  string `json:"table"`
}

// RowEdit is one row's changed cells.
type RowEdit struct {
	Key    map[string]*string `json:"key"`    // primary-key column → its value as shown in the grid
	Values map[string]*string `json:"values"` // column → new value; nil sets NULL
}

// SaveRows applies the grid's edits to target as UPDATEs, matching rows by
// primary key, all in one transaction: either every row saves or none does.
// It uses the pool, not the SQL editor's connection, so an open transaction
// in the editor doesn't swallow or delay the save.
func (s *DBService) SaveRows(ctx context.Context, target EditTarget, edits []RowEdit) (int, error) {
	s.mu.Lock()
	pool := s.pool
	s.mu.Unlock()
	if pool == nil {
		return 0, errors.New("Not connected.")
	}
	if len(edits) == 0 {
		return 0, nil
	}

	columns, err := tableColumns(ctx, pool, target.Schema, target.Table)
	if err != nil {
		return 0, err
	}
	byName := map[string]column{}
	var primaryKey []column
	for _, c := range columns {
		byName[c.Name] = c
		if c.PrimaryKey {
			primaryKey = append(primaryKey, c)
		}
	}
	if len(primaryKey) == 0 {
		return 0, fmt.Errorf("%s.%s has no primary key, so its rows can't be edited.", target.Schema, target.Table)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, toQueryError(err)
	}
	defer tx.Rollback(ctx) // no-op after Commit

	// Fail fast, rather than wait, if another session holds a lock on a row
	// (e.g. an uncommitted UPDATE in the SQL editor).
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '5s'"); err != nil {
		return 0, toQueryError(err)
	}

	table := pgx.Identifier{target.Schema, target.Table}.Sanitize()
	for _, edit := range edits {
		sql, args, err := buildUpdate(table, edit, byName, primaryKey)
		if err != nil {
			return 0, err
		}
		tag, err := tx.Exec(ctx, sql, args...)
		if err != nil {
			qe := toQueryError(err)
			qe.Message = fmt.Sprintf("Row %s: %s Nothing was saved.", describeKey(edit.Key, primaryKey), qe.Message)
			qe.Position = 0
			return 0, qe
		}
		if tag.RowsAffected() != 1 {
			return 0, &QueryError{Message: fmt.Sprintf(
				"Row %s wasn't found; it may have been deleted or its key changed. Nothing was saved. Reload and try again.",
				describeKey(edit.Key, primaryKey))}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, toQueryError(err)
	}
	return len(edits), nil
}

// buildUpdate turns one row edit into UPDATE … SET … WHERE <primary key>.
// Column names must exist in the catalog; values are parameters cast to the
// column's type, so Postgres reports bad input (e.g. "abc" for an int).
func buildUpdate(table string, edit RowEdit, byName map[string]column, primaryKey []column) (string, []any, error) {
	if len(edit.Values) == 0 {
		return "", nil, errors.New("A row edit has no changed cells.")
	}
	names := make([]string, 0, len(edit.Values))
	for name := range edit.Values {
		names = append(names, name)
	}
	sort.Strings(names) // stable SQL for the same edit

	var set, where []string
	var args []any
	for _, name := range names {
		c, ok := byName[name]
		if !ok {
			return "", nil, fmt.Errorf("Unknown column %q.", name)
		}
		col := pgx.Identifier{name}.Sanitize()
		if v := edit.Values[name]; v == nil {
			set = append(set, col+" = NULL")
		} else {
			args = append(args, *v)
			set = append(set, fmt.Sprintf("%s = CAST($%d::text AS %s)", col, len(args), c.sqlType))
		}
	}
	for _, c := range primaryKey {
		v, ok := edit.Key[c.Name]
		if !ok || v == nil {
			return "", nil, fmt.Errorf("A row edit is missing its %s value.", c.Name)
		}
		args = append(args, *v)
		where = append(where, fmt.Sprintf("%s = CAST($%d::text AS %s)", pgx.Identifier{c.Name}.Sanitize(), len(args), c.sqlType))
	}
	return fmt.Sprintf("UPDATE %s SET %s WHERE %s", table, strings.Join(set, ", "), strings.Join(where, " AND ")), args, nil
}

// describeKey formats a row's key for messages, e.g. "id = 5".
func describeKey(key map[string]*string, primaryKey []column) string {
	parts := make([]string, 0, len(primaryKey))
	for _, c := range primaryKey {
		value := "NULL"
		if v := key[c.Name]; v != nil {
			value = *v
		}
		parts = append(parts, c.Name+" = "+value)
	}
	return strings.Join(parts, ", ")
}

// Where a query's result columns come from. Postgres reports, per result
// column, the table OID and column number when the column is a plain table
// column (0 for expressions).
const sourceTableQuery = `
SELECT n.nspname, c.relname, c.relkind::text
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.oid = $1`

const sourceColumnsQuery = `
SELECT a.attnum, a.attname, coalesce(a.attnum = ANY (i.indkey), false)
FROM pg_attribute a
LEFT JOIN pg_index i ON i.indrelid = a.attrelid AND i.indisprimary
WHERE a.attrelid = $1 AND a.attnum > 0 AND NOT a.attisdropped`

// editTarget decides whether a result's cells can be edited: every table
// column in it must come from one table (or partitioned table), and all of
// that table's primary-key columns must be in the result. It fills in each
// column's Source and PrimaryKey. Any lookup problem just means read-only.
func editTarget(ctx context.Context, q querier, st *statementData) *EditTarget {
	var oid uint32
	for _, o := range st.tableOIDs {
		switch {
		case o == 0:
		case oid == 0:
			oid = o
		case o != oid:
			return nil // columns from more than one table (a join)
		}
	}
	if oid == 0 {
		return nil
	}

	var target EditTarget
	var relkind string
	if err := q.QueryRow(ctx, sourceTableQuery, oid).Scan(&target.Schema, &target.Table, &relkind); err != nil {
		return nil
	}
	if relkind != "r" && relkind != "p" {
		return nil // views and other relations aren't edited
	}

	rows, err := q.Query(ctx, sourceColumnsQuery, oid)
	if err != nil {
		return nil
	}
	type attr struct {
		name string
		pk   bool
	}
	attrs := map[int16]attr{}
	pkCount := 0
	for rows.Next() {
		var num int16
		var a attr
		if err := rows.Scan(&num, &a.name, &a.pk); err != nil {
			rows.Close()
			return nil
		}
		attrs[num] = a
		if a.pk {
			pkCount++
		}
	}
	rows.Close()
	if rows.Err() != nil || pkCount == 0 {
		return nil
	}

	seenKeys := map[string]bool{}
	for i := range st.columns {
		if st.tableOIDs[i] != oid {
			continue
		}
		a, ok := attrs[int16(st.attNums[i])]
		if !ok {
			continue
		}
		st.columns[i].Source = a.name
		st.columns[i].PrimaryKey = a.pk
		if a.pk {
			seenKeys[a.name] = true
		}
	}
	if len(seenKeys) != pkCount {
		// Without the whole primary key we can't tell which row to update.
		for i := range st.columns {
			st.columns[i].Source = ""
			st.columns[i].PrimaryKey = false
		}
		return nil
	}
	return &target
}
