package main

import (
	"context"
	"errors"
)

// SchemaInfo is one schema and the tables and views in it.
type SchemaInfo struct {
	Name   string      `json:"name"`
	Tables []TableInfo `json:"tables"`
}

// TableInfo is a table or view shown in the sidebar.
type TableInfo struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`          // table, partitioned table, foreign table, view, materialized view
	QualifiedName string `json:"qualifiedName"` // schema.name, quoted only where SQL needs it
	EstimatedRows *int64 `json:"estimatedRows"` // from Postgres's statistics; nil for views or if unknown
}

var relkindNames = map[string]string{
	"r": "table",
	"p": "partitioned table",
	"f": "foreign table",
	"v": "view",
	"m": "materialized view",
}

// Lists every schema (empty ones too) with its tables and views. System
// schemas (pg_catalog, information_schema, pg_toast, pg_temp_*) are left out,
// and so are partitions: only their parent table is listed. Row estimates come
// from the statistics collector (n_live_tup, kept current as rows change),
// falling back to the planner's reltuples; no table is scanned.
const schemaQuery = `
SELECT n.nspname,
       c.relname,
       c.relkind::text,
       quote_ident(n.nspname) || '.' || quote_ident(c.relname),
       CASE WHEN c.relkind IN ('r', 'm')
            THEN coalesce(s.n_live_tup, nullif(c.reltuples, -1)::bigint) END
FROM pg_namespace n
LEFT JOIN pg_class c
       ON c.relnamespace = n.oid
      AND c.relkind IN ('r', 'p', 'f', 'v', 'm')
      AND NOT c.relispartition
LEFT JOIN pg_stat_user_tables s ON s.relid = c.oid
WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg\_toast%'
  AND n.nspname NOT LIKE 'pg\_temp\_%'
ORDER BY n.nspname <> 'public', n.nspname, c.relkind IN ('v', 'm'), c.relname`

// ListTables returns the schemas with their tables and views, public first,
// using the open pool.
func (s *DBService) ListTables() ([]SchemaInfo, error) {
	// Hold the lock only to read s.pool; the pool is safe for concurrent use.
	s.mu.Lock()
	pool := s.pool
	s.mu.Unlock()
	if pool == nil {
		return nil, errors.New("Not connected.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()

	rows, err := pool.Query(ctx, schemaQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	schemas := []SchemaInfo{}
	for rows.Next() {
		var schema string
		var name, kind, qualified *string // NULL for a schema with no tables
		var estimate *int64
		if err := rows.Scan(&schema, &name, &kind, &qualified, &estimate); err != nil {
			return nil, err
		}
		// Rows arrive sorted by schema, so a new name starts a new group.
		if len(schemas) == 0 || schemas[len(schemas)-1].Name != schema {
			schemas = append(schemas, SchemaInfo{Name: schema, Tables: []TableInfo{}})
		}
		if name == nil {
			continue
		}
		group := &schemas[len(schemas)-1]
		group.Tables = append(group.Tables, TableInfo{
			Name:          *name,
			Kind:          relkindNames[*kind],
			QualifiedName: *qualified,
			EstimatedRows: estimate,
		})
	}
	return schemas, rows.Err()
}
