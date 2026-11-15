package postgres

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
)

// connectForTest connects to the Postgres named by SPYDB_TEST_DATABASE (and
// optionally SPYDB_TEST_USER / SPYDB_TEST_HOST), or skips the test.
func connectForTest(t *testing.T) *DBService {
	t.Helper()
	database := os.Getenv("SPYDB_TEST_DATABASE")
	if database == "" {
		t.Skip("set SPYDB_TEST_DATABASE to run tests against Postgres")
	}
	isolate(t) // Connect records Recent; keep the real connections.json out of it
	host := os.Getenv("SPYDB_TEST_HOST")
	if host == "" {
		host = "localhost"
	}
	s := &DBService{}
	if _, err := s.Connect(DBConfig{Host: host, Port: 5432, User: os.Getenv("SPYDB_TEST_USER"), Database: database}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Disconnect)
	return s
}

func TestListColumns(t *testing.T) {
	s := connectForTest(t)
	// A table of its own (the test database may have none), and a temporary one.
	schema := fmt.Sprintf("spydb_cols_%d", os.Getpid())
	if _, err := s.pool.Exec(t.Context(), fmt.Sprintf(`CREATE SCHEMA %s; CREATE TABLE %s.listed (id int, "Name" text)`, schema, schema)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	if _, err := s.pool.Exec(t.Context(), `CREATE TEMP TABLE spydb_cols_probe (id int, "Name" text, gone int)`); err != nil {
		t.Fatal(err)
	}
	tables, err := s.ListColumns()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(tables, func(tc TableColumns) bool {
		return tc.Schema == schema && tc.Table == "listed" && slices.Equal(tc.Columns, []string{"id", "Name"})
	}) {
		t.Fatalf("%s.listed (id, Name) isn't listed: %v", schema, tables)
	}
	for _, tc := range tables {
		if tc.Table == "spydb_cols_probe" {
			t.Fatal("temporary tables shouldn't be listed (pg_temp schemas are skipped)")
		}
		if len(tc.Columns) == 0 {
			t.Errorf("%s.%s has no columns", tc.Schema, tc.Table)
		}
	}
	// Same relations as the sidebar's ListTables.
	schemas, err := s.ListTables()
	if err != nil {
		t.Fatal(err)
	}
	listed := 0
	for _, sc := range schemas {
		for _, tb := range sc.Tables {
			listed++
			if !slices.ContainsFunc(tables, func(tc TableColumns) bool { return tc.Schema == tb.Schema && tc.Table == tb.Name }) {
				t.Errorf("%s.%s is in ListTables but not ListColumns", tb.Schema, tb.Name)
			}
		}
	}
	if listed != len(tables) {
		t.Errorf("ListTables has %d relations, ListColumns %d", listed, len(tables))
	}
}

func TestListColumnsNotConnected(t *testing.T) {
	if _, err := (&DBService{}).ListColumns(); err == nil {
		t.Fatal("expected an error when not connected")
	}
}
