package main

import (
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
	if _, err := s.pool.Exec(t.Context(), `CREATE TEMP TABLE spydb_cols_probe (id int, "Name" text, gone int)`); err != nil {
		t.Fatal(err)
	}
	tables, err := s.ListColumns()
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) == 0 {
		t.Fatal("no tables")
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
