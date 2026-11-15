package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
)

func TestDescribeTable(t *testing.T) {
	s := connectForTest(t)
	schema := fmt.Sprintf("spydb_desc_%d", os.Getpid())
	if _, err := s.pool.Exec(t.Context(), fmt.Sprintf(`CREATE SCHEMA %[1]s;
		CREATE TABLE %[1]s.parents (id int PRIMARY KEY, name text NOT NULL DEFAULT 'x');
		CREATE TABLE %[1]s.kids (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, parent_id int REFERENCES %[1]s.parents(id), amount numeric(12,2), note varchar(80));
		CREATE INDEX kids_amount ON %[1]s.kids (amount) WHERE amount > 0;
		COMMENT ON TABLE %[1]s.kids IS 'the kids'; COMMENT ON COLUMN %[1]s.kids.note IS 'free text';
		CREATE VIEW %[1]s.v AS SELECT id FROM %[1]s.kids;
		INSERT INTO %[1]s.parents VALUES (1, 'p'); INSERT INTO %[1]s.kids (parent_id, amount) VALUES (1, 5); ANALYZE %[1]s.kids`, schema)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })

	d, err := s.DescribeTable(t.Context(), schema, "kids")
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != "table" || d.Comment != "the kids" || d.Owner == "" || d.EstimatedRows == nil || *d.EstimatedRows != 1 || d.SizeBytes == nil || *d.SizeBytes <= 0 {
		t.Fatalf("facts: %+v", d)
	}
	want := []ColumnDescription{
		{Name: "id", Type: "bigint", Nullable: false, PrimaryKey: true, Identity: "always"},
		{Name: "parent_id", Type: "integer", Nullable: true},
		{Name: "amount", Type: "numeric(12,2)", Nullable: true},
		{Name: "note", Type: "character varying(80)", Nullable: true, Comment: "free text"},
	}
	if len(d.Columns) != len(want) {
		t.Fatalf("columns: %+v", d.Columns)
	}
	for i, w := range want {
		if d.Columns[i] != w {
			t.Errorf("column %d: %+v; want %+v", i, d.Columns[i], w)
		}
	}
	if len(d.Indexes) != 2 || !d.Indexes[0].Primary || d.Indexes[0].Columns[0] != "id" || d.Indexes[1].Name != "kids_amount" || d.Indexes[1].Method != "btree" || d.Indexes[1].Unique {
		t.Fatalf("indexes: %+v", d.Indexes)
	}
	if len(d.ForeignKeys) != 1 || d.ForeignKeys[0].RefTable != "parents" {
		t.Fatalf("foreign keys: %+v", d.ForeignKeys)
	}
	v, err := s.DescribeTable(t.Context(), schema, "v")
	if err != nil || v.Kind != "view" || v.EstimatedRows != nil || len(v.Columns) != 1 {
		t.Fatalf("view: %+v, %v", v, err)
	}
	if _, err := s.DescribeTable(t.Context(), schema, "nope"); err == nil {
		t.Fatal("a missing table should be an error")
	}
}
