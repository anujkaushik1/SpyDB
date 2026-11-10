package main

import (
	"context"
	"errors"
)

// ForeignKey is one FOREIGN KEY constraint of a table: its Columns point at
// RefColumns of RefSchema.RefTable, pair by pair.
type ForeignKey struct {
	Name       string   `json:"name"`
	Columns    []string `json:"columns"`
	RefSchema  string   `json:"refSchema"`
	RefTable   string   `json:"refTable"`
	RefColumns []string `json:"refColumns"`
	// RefSchema.RefTable quoted only where SQL needs it, like TableInfo.QualifiedName
	RefQualifiedName string `json:"refQualifiedName"`
}

// A foreign key to a partitioned table gets a copy for each of its
// partitions, with conparentid set and the same conrelid; those copies are
// left out. A partition's own copy of its parent's key (another conrelid)
// stays: it's that partition's foreign key.
const foreignKeysQuery = `
SELECT con.conname,
       ARRAY(SELECT a.attname::text FROM unnest(con.conkey) WITH ORDINALITY k(num, ord)
             JOIN pg_catalog.pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.num ORDER BY k.ord),
       rn.nspname, rc.relname,
       ARRAY(SELECT a.attname::text FROM unnest(con.confkey) WITH ORDINALITY k(num, ord)
             JOIN pg_catalog.pg_attribute a ON a.attrelid = con.confrelid AND a.attnum = k.num ORDER BY k.ord),
       quote_ident(rn.nspname) || '.' || quote_ident(rc.relname)
FROM pg_catalog.pg_constraint con
JOIN pg_catalog.pg_class c ON c.oid = con.conrelid
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
JOIN pg_catalog.pg_class rc ON rc.oid = con.confrelid
JOIN pg_catalog.pg_namespace rn ON rn.oid = rc.relnamespace
WHERE con.contype = 'f' AND n.nspname = $1 AND c.relname = $2
  AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint p WHERE p.oid = con.conparentid AND p.conrelid = con.conrelid)
ORDER BY con.conname`

// ForeignKeys lists a table's foreign keys, so the grid can mark those
// columns and link each value to the row it points at. One catalog query on
// the pool, once per table tab (not per page).
func (s *DBService) ForeignKeys(ctx context.Context, schema, table string) ([]ForeignKey, error) {
	s.mu.Lock()
	pool := s.pool
	s.mu.Unlock()
	if pool == nil {
		return nil, errors.New("Not connected.")
	}

	rows, err := pool.Query(ctx, foreignKeysQuery, schema, table)
	if err != nil {
		return nil, toQueryError(err)
	}
	defer rows.Close()

	keys := []ForeignKey{}
	for rows.Next() {
		var k ForeignKey
		if err := rows.Scan(&k.Name, &k.Columns, &k.RefSchema, &k.RefTable, &k.RefColumns, &k.RefQualifiedName); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}
