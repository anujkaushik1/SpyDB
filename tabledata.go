package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// TableQuery asks for one page of a table tab.
type TableQuery struct {
	Schema   string        `json:"schema"`
	Table    string        `json:"table"`
	Page     int           `json:"page"` // 0-based
	PageSize int           `json:"pageSize"`
	SortBy   string        `json:"sortBy"` // column name; "" sorts by primary key
	SortDesc bool          `json:"sortDesc"`
	Filters  []TableFilter `json:"filters"` // combined with AND
}

// TableFilter is one row of the filter bar.
type TableFilter struct {
	Column   string `json:"column"`
	Operator string `json:"operator"` // a key of filterOperators
	Value    string `json:"value"`
}

// TableColumn describes a column for the grid header.
type TableColumn struct {
	Name       string `json:"name"`
	Type       string `json:"type"`     // short type, e.g. int4, numeric(12,2), timestamptz
	Category   string `json:"category"` // number, bool, time, json, text or other (for colouring)
	PrimaryKey bool   `json:"primaryKey"`
}

// TablePage is one page of rows.
type TablePage struct {
	Columns    []TableColumn `json:"columns"`
	Rows       [][]*string   `json:"rows"`     // values as text; nil is SQL NULL
	Target     *EditTarget   `json:"target"`   // where edited cells save to; nil means read-only
	ReadOnly   string        `json:"readOnly"` // why it's read-only, e.g. "a view"
	Keys       [][]*string   `json:"keys"`     // per row, its key (Target.Key); empty when read-only
	HasMore    bool          `json:"hasMore"`  // another page follows
	DurationMs int           `json:"durationMs"`
}

// Filter SQL per operator; %s is the quoted column, $%d the value parameter.
// Comparisons cast the value to the column's own type, so `id = 5` works on
// an int column and bad input ("abc" for an int) gets Postgres's own error.
var filterOperators = map[string]string{
	"eq":          "%s = CAST($%d::text AS %s)",
	"neq":         "%s <> CAST($%d::text AS %s)",
	"gt":          "%s > CAST($%d::text AS %s)",
	"gte":         "%s >= CAST($%d::text AS %s)",
	"lt":          "%s < CAST($%d::text AS %s)",
	"lte":         "%s <= CAST($%d::text AS %s)",
	"contains":    "%s::text ILIKE $%d",
	"starts":      "%s::text ILIKE $%d",
	"is_null":     "%s IS NULL",
	"is_not_null": "%s IS NOT NULL",
}

// The cast type is the column's type without its length or precision,
// under its own schema and internal name (pg_catalog."varchar", not
// character varying(3)), and for a domain its base type: an explicit cast to
// varchar(3) or a domain over it silently cuts 'ABCDEF' to 'ABC', where
// storing an unlimited varchar into the column raises "value too long".
const columnsQuery = `
SELECT a.attname,
       pg_catalog.format_type(a.atttypid, a.atttypmod),
       t.typname,
       t.typcategory::text,
       coalesce(a.attnum = ANY ((i.indkey::int2[])[0:i.indnkeyatts - 1]), false),
       (WITH RECURSIVE base(oid, depth) AS (
            SELECT a.atttypid, 0
            UNION ALL
            SELECT bt.typbasetype, base.depth + 1 FROM base JOIN pg_catalog.pg_type bt ON bt.oid = base.oid WHERE bt.typtype = 'd')
        SELECT pg_catalog.format('%I.%I', bn.nspname, bt.typname)
        FROM base JOIN pg_catalog.pg_type bt ON bt.oid = base.oid JOIN pg_catalog.pg_namespace bn ON bn.oid = bt.typnamespace
        ORDER BY base.depth DESC LIMIT 1)
FROM pg_catalog.pg_attribute a
JOIN pg_catalog.pg_class c ON c.oid = a.attrelid
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
JOIN pg_catalog.pg_type t ON t.oid = a.atttypid
LEFT JOIN pg_catalog.pg_index i ON i.indrelid = c.oid AND i.indisprimary
WHERE n.nspname = $1 AND c.relname = $2 AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY a.attnum`

// column is a TableColumn plus its SQL types.
type column struct {
	TableColumn
	sqlType     string // from format_type, e.g. "numeric(12,2)"
	castType    string // what edited values are cast to, e.g. pg_catalog."numeric"
	typCategory string // pg_type.typcategory, e.g. D for dates and times
}

// TableData returns one page of a table or view, sorted and filtered, using
// the open pool. Wails cancels ctx when the frontend cancels the call.
func (s *DBService) TableData(ctx context.Context, q TableQuery) (TablePage, error) {
	s.mu.Lock()
	pool := s.pool
	s.mu.Unlock()
	if pool == nil {
		return TablePage{}, errors.New("Not connected.")
	}
	if q.PageSize < 1 || q.PageSize > maxStoredRows || q.Page < 0 {
		return TablePage{}, errors.New("Invalid page.")
	}

	started := time.Now()
	columns, err := tableColumns(ctx, pool, q.Schema, q.Table)
	if err != nil {
		return TablePage{}, err
	}

	// Rows save back by primary key; a table without one gets each row's
	// ctid and hash as extra columns, kept out of the grid.
	page := TablePage{Rows: [][]*string{}, Keys: [][]*string{}}
	rel, err := findRelation(ctx, pool, "n.nspname = $1 AND c.relname = $2", q.Schema, q.Table)
	if err != nil {
		return TablePage{}, toQueryError(err)
	}
	var extra []string
	var keyAt []int // positions of the key values in each row
	switch {
	case rel == nil:
		page.ReadOnly = "table not found"
	case rel.readOnly() != "":
		page.ReadOnly = rel.readOnly()
	case rel.keyKind() == "ctid":
		extra = rel.keyColumns(pgx.Identifier{q.Table}.Sanitize())
		keyAt = []int{len(columns), len(columns) + 1}
	default:
		for _, name := range rel.primaryKey {
			keyAt = append(keyAt, slices.IndexFunc(columns, func(c column) bool { return c.Name == name }))
		}
	}
	if keyAt != nil && !slices.Contains(keyAt, -1) {
		page.Target = &EditTarget{OID: rel.oid, Schema: rel.schema, Table: rel.name, Kind: rel.keyKind(), Key: rel.keyNames()}
	} else if keyAt != nil {
		page.ReadOnly, keyAt, extra = "SpyDB couldn't find the primary key", nil, nil
	}

	sql, args, err := buildTableQuery(q, columns, extra)
	if err != nil {
		return TablePage{}, err
	}

	// Text results, like the query editor: every value arrives as a string.
	rows, err := pool.Query(ctx, sql, append([]any{pgx.QueryResultFormats{pgx.TextFormatCode}}, args...)...)
	if err != nil {
		return TablePage{}, toQueryError(err)
	}
	defer rows.Close()

	for _, c := range columns {
		page.Columns = append(page.Columns, c.TableColumn)
	}
	for rows.Next() {
		values := rows.RawValues() // reused by the next row, so copy each value
		row := make([]*string, len(values))
		for i, v := range values {
			if v != nil {
				text := string(v)
				row[i] = &text
			}
		}
		if page.Target != nil {
			key := make([]*string, len(keyAt))
			for j, at := range keyAt {
				key[j] = row[at]
			}
			page.Keys = append(page.Keys, key)
		}
		page.Rows = append(page.Rows, row[:len(columns)])
	}
	if err := rows.Err(); err != nil {
		return TablePage{}, toQueryError(err)
	}

	// One extra row was asked for, to know whether another page exists.
	if len(page.Rows) > q.PageSize {
		page.Rows = page.Rows[:q.PageSize]
		page.HasMore = true
		if page.Target != nil {
			page.Keys = page.Keys[:q.PageSize]
		}
	}
	if page.Target != nil && !distinctKeys(page.Keys) {
		page.Target, page.Keys, page.ReadOnly = nil, [][]*string{}, "rows repeat"
	}
	page.DurationMs = int(time.Since(started).Milliseconds())
	return page, nil
}

func tableColumns(ctx context.Context, q querier, schema, table string) ([]column, error) {
	rows, err := q.Query(ctx, columnsQuery, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []column
	for rows.Next() {
		var c column
		var typname, category string
		if err := rows.Scan(&c.Name, &c.sqlType, &typname, &category, &c.PrimaryKey, &c.castType); err != nil {
			return nil, err
		}
		c.typCategory = category
		c.Type = shortType(c.sqlType, typname, category)
		c.Category = valueCategory(typname, category)
		columns = append(columns, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("Table %s.%s was not found. Refresh the sidebar.", schema, table)
	}
	return columns, nil
}

// buildTableQuery turns q into SQL plus parameters. Column names are only
// ever taken from the catalog and quoted; typed values travel as parameters.
// extra expressions are selected after the columns.
func buildTableQuery(q TableQuery, columns []column, extra []string) (string, []any, error) {
	byName := map[string]column{}
	names := make([]string, len(columns))
	var primaryKey []string
	for i, c := range columns {
		byName[c.Name] = c
		names[i] = pgx.Identifier{c.Name}.Sanitize()
		if c.PrimaryKey {
			primaryKey = append(primaryKey, names[i])
		}
	}

	var where []string
	var args []any
	for _, f := range q.Filters {
		c, ok := byName[f.Column]
		if !ok {
			return "", nil, fmt.Errorf("Unknown column %q in filter.", f.Column)
		}
		pattern, ok := filterOperators[f.Operator]
		if !ok {
			return "", nil, fmt.Errorf("Unknown filter operator %q.", f.Operator)
		}
		col := pgx.Identifier{c.Name}.Sanitize()
		switch f.Operator {
		case "is_null", "is_not_null":
			where = append(where, fmt.Sprintf(pattern, col))
		case "contains", "starts":
			value := escapeLike(f.Value) + "%"
			if f.Operator == "contains" {
				value = "%" + value
			}
			args = append(args, value)
			where = append(where, fmt.Sprintf(pattern, col, len(args)))
		default:
			args = append(args, f.Value)
			where = append(where, fmt.Sprintf(pattern, col, len(args), c.sqlType))
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "SELECT %s FROM %s", strings.Join(append(names, extra...), ", "), pgx.Identifier{q.Schema, q.Table}.Sanitize())
	if len(where) > 0 {
		sb.WriteString(" WHERE " + strings.Join(where, " AND "))
	}

	// Paging needs a stable order: the chosen column, else the primary key.
	switch {
	case q.SortBy != "":
		if _, ok := byName[q.SortBy]; !ok {
			return "", nil, fmt.Errorf("Unknown sort column %q.", q.SortBy)
		}
		direction := "ASC"
		if q.SortDesc {
			direction = "DESC"
		}
		fmt.Fprintf(&sb, " ORDER BY %s %s", pgx.Identifier{q.SortBy}.Sanitize(), direction)
	case len(primaryKey) > 0:
		sb.WriteString(" ORDER BY " + strings.Join(primaryKey, ", "))
	}

	// PageSize 0 means every row (exports); otherwise one page plus one extra
	// row, to know whether another page exists.
	if q.PageSize > 0 {
		fmt.Fprintf(&sb, " LIMIT %d OFFSET %d", q.PageSize+1, q.Page*q.PageSize)
	}
	return sb.String(), args, nil
}

// escapeLike makes % and _ in a user's value match literally in ILIKE.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// shortType turns format_type output into a compact label: "numeric(12,2)"
// stays, "timestamp with time zone" becomes "timestamptz", arrays keep "[]".
func shortType(formatted, typname, category string) string {
	if category == "A" {
		return formatted
	}
	if open := strings.IndexByte(formatted, '('); open >= 0 {
		if end := strings.IndexByte(formatted[open:], ')'); end >= 0 {
			return typname + formatted[open:open+end+1]
		}
	}
	return typname
}

// valueCategory groups types for colouring in the grid.
func valueCategory(typname, category string) string {
	switch {
	case category == "N":
		return "number"
	case category == "B":
		return "bool"
	case category == "D":
		return "time"
	case typname == "json" || typname == "jsonb":
		return "json"
	case category == "S" || category == "E":
		return "text"
	}
	return "other"
}
