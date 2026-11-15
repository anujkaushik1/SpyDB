package postgres

import (
	"context"
	"errors"
)

// TableDescription is what the sidebar's "Table details" shows for a
// Postgres table or view: the facts Postgres keeps about it, its columns,
// indexes and foreign keys. Read from the catalogs: nothing in the table
// itself is read, so it's instant on any size of table.
type TableDescription struct {
	Schema        string              `json:"schema"`
	Name          string              `json:"name"`
	Kind          string              `json:"kind"` // table, partitioned table, foreign table, view, materialized view
	Owner         string              `json:"owner"`
	Comment       string              `json:"comment"`       // COMMENT ON TABLE, "" if none
	EstimatedRows *int64              `json:"estimatedRows"` // from the planner's statistics (ANALYZE); nil for views
	SizeBytes     *int64              `json:"sizeBytes"`     // table + indexes + TOAST on disk; nil for views
	Columns       []ColumnDescription `json:"columns"`
	Indexes       []IndexDescription  `json:"indexes"`
	ForeignKeys   []ForeignKey        `json:"foreignKeys"`
}

// ColumnDescription is one column as Postgres defines it.
type ColumnDescription struct {
	Name       string `json:"name"`
	Type       string `json:"type"` // as SQL writes it: "character varying(80)", "numeric(12,2)", "timestamp with time zone"
	Nullable   bool   `json:"nullable"`
	Default    string `json:"default"` // the DEFAULT expression, "" if none
	PrimaryKey bool   `json:"primaryKey"`
	Identity   string `json:"identity"` // "always" or "by default" for GENERATED … AS IDENTITY; "" otherwise
	Comment    string `json:"comment"`
}

// IndexDescription is one index of the table.
type IndexDescription struct {
	Name       string   `json:"name"`
	Columns    []string `json:"columns"` // the indexed columns (or expressions)
	Unique     bool     `json:"unique"`
	Primary    bool     `json:"primary"`
	Method     string   `json:"method"`     // btree, gin, gist, hash…
	Definition string   `json:"definition"` // the CREATE INDEX statement
	SizeBytes  int64    `json:"sizeBytes"`
}

const describeTableQuery = `
SELECT c.relkind::text, pg_catalog.pg_get_userbyid(c.relowner), COALESCE(pg_catalog.obj_description(c.oid, 'pg_class'), ''),
       CASE WHEN c.relkind IN ('r', 'p', 'f', 'm') THEN GREATEST(c.reltuples, 0)::bigint END,
       CASE WHEN c.relkind IN ('r', 'p', 'f', 'm') THEN pg_catalog.pg_total_relation_size(c.oid) END
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2`

const describeColumnsQuery = `
SELECT a.attname, pg_catalog.format_type(a.atttypid, a.atttypmod), NOT a.attnotnull,
       COALESCE(pg_catalog.pg_get_expr(d.adbin, d.adrelid), ''),
       EXISTS (SELECT 1 FROM pg_catalog.pg_index i WHERE i.indrelid = a.attrelid AND i.indisprimary AND a.attnum = ANY (i.indkey)),
       CASE a.attidentity WHEN 'a' THEN 'always' WHEN 'd' THEN 'by default' ELSE '' END,
       COALESCE(pg_catalog.col_description(a.attrelid, a.attnum), '')
FROM pg_catalog.pg_attribute a
JOIN pg_catalog.pg_class c ON c.oid = a.attrelid
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE n.nspname = $1 AND c.relname = $2 AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY a.attnum`

const describeIndexesQuery = `
SELECT ic.relname, i.indisunique, i.indisprimary, am.amname, pg_catalog.pg_get_indexdef(i.indexrelid),
       pg_catalog.pg_relation_size(i.indexrelid),
       ARRAY(SELECT pg_catalog.pg_get_indexdef(i.indexrelid, k.n, true) FROM generate_series(1, i.indnkeyatts) k(n))
FROM pg_catalog.pg_index i
JOIN pg_catalog.pg_class c ON c.oid = i.indrelid
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
JOIN pg_catalog.pg_class ic ON ic.oid = i.indexrelid
JOIN pg_catalog.pg_am am ON am.oid = ic.relam
WHERE n.nspname = $1 AND c.relname = $2
ORDER BY i.indisprimary DESC, ic.relname`

// DescribeTable reads a table's facts, columns, indexes and foreign keys
// from Postgres's catalogs (three small queries on the pool).
func (s *DBService) DescribeTable(ctx context.Context, schema, table string) (TableDescription, error) {
	s.mu.Lock()
	pool := s.pool
	s.mu.Unlock()
	if pool == nil {
		return TableDescription{}, errors.New("Not connected.")
	}
	d := TableDescription{Schema: schema, Name: table, Columns: []ColumnDescription{}, Indexes: []IndexDescription{}, ForeignKeys: []ForeignKey{}}
	var kind string
	if err := pool.QueryRow(ctx, describeTableQuery, schema, table).Scan(&kind, &d.Owner, &d.Comment, &d.EstimatedRows, &d.SizeBytes); err != nil {
		return TableDescription{}, toQueryError(err)
	}
	d.Kind = kindName(kind)

	rows, err := pool.Query(ctx, describeColumnsQuery, schema, table)
	if err != nil {
		return TableDescription{}, toQueryError(err)
	}
	for rows.Next() {
		var c ColumnDescription
		if err := rows.Scan(&c.Name, &c.Type, &c.Nullable, &c.Default, &c.PrimaryKey, &c.Identity, &c.Comment); err != nil {
			rows.Close()
			return TableDescription{}, err
		}
		d.Columns = append(d.Columns, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return TableDescription{}, err
	}

	rows, err = pool.Query(ctx, describeIndexesQuery, schema, table)
	if err != nil {
		return TableDescription{}, toQueryError(err)
	}
	for rows.Next() {
		var ix IndexDescription
		if err := rows.Scan(&ix.Name, &ix.Unique, &ix.Primary, &ix.Method, &ix.Definition, &ix.SizeBytes, &ix.Columns); err != nil {
			rows.Close()
			return TableDescription{}, err
		}
		d.Indexes = append(d.Indexes, ix)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return TableDescription{}, err
	}

	if d.ForeignKeys, err = s.ForeignKeys(ctx, schema, table); err != nil {
		return TableDescription{}, err
	}
	return d, nil
}

// kindName is relkind in words, as TableInfo.Kind says it.
func kindName(relkind string) string {
	switch relkind {
	case "r":
		return "table"
	case "p":
		return "partitioned table"
	case "f":
		return "foreign table"
	case "v":
		return "view"
	case "m":
		return "materialized view"
	}
	return relkind
}
