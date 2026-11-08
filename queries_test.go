package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolate points SpyDB's settings folder at a temporary HOME, so tests never
// touch the real queries.json (macOS uses $HOME/Library/Application Support,
// Linux $XDG_CONFIG_HOME or $HOME/.config).
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
}

func TestSavedQueriesLifecycle(t *testing.T) {
	isolate(t)
	s := &DBService{}

	list, err := s.SavedQueries()
	if err != nil || len(list) != 0 {
		t.Fatalf("empty start: got %v, %v", list, err)
	}

	q, err := s.SaveQuery(SavedQuery{Name: "  Top accounts ", SQL: "SELECT * FROM accounts"})
	if err != nil {
		t.Fatal(err)
	}
	if q.ID == "" || q.Name != "Top accounts" || q.UpdatedAt == 0 || q.Database != "" {
		t.Fatalf("created: %+v", q)
	}

	q.SQL = "SELECT * FROM accounts ORDER BY balance DESC"
	updated, err := s.SaveQuery(q)
	if err != nil || updated.ID != q.ID || updated.SQL != q.SQL {
		t.Fatalf("updated: %+v, %v", updated, err)
	}

	renamed, err := s.RenameQuery(q.ID, "Richest")
	if err != nil || renamed.Name != "Richest" || renamed.SQL != q.SQL || renamed.UpdatedAt != updated.UpdatedAt {
		t.Fatalf("renamed: %+v, %v", renamed, err)
	}

	list, _ = s.SavedQueries()
	if len(list) != 1 || list[0].Name != "Richest" {
		t.Fatalf("after rename: %+v", list)
	}

	if err := s.DeleteQuery(q.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteQuery(q.ID); err != nil { // already gone: fine
		t.Fatal(err)
	}
	if list, _ = s.SavedQueries(); len(list) != 0 {
		t.Fatalf("after delete: %+v", list)
	}
}

func TestSaveQueryTagsTheCurrentDatabase(t *testing.T) {
	isolate(t)
	s := &DBService{info: &ConnectionInfo{Host: "localhost", Database: "cashier"}}
	q, err := s.SaveQuery(SavedQuery{Name: "Debits", SQL: "SELECT 1"})
	if err != nil || q.Database != "cashier" || q.Host != "localhost" {
		t.Fatalf("tag: %+v, %v", q, err)
	}
	// Saved again from another database: the label follows.
	s.info = &ConnectionInfo{Host: "127.0.0.1", Database: "shop"}
	q, _ = s.SaveQuery(q)
	if q.Database != "shop" || q.Host != "127.0.0.1" {
		t.Fatalf("retag: %+v", q)
	}
}

func TestSaveQueryRejects(t *testing.T) {
	isolate(t)
	s := &DBService{}
	cases := map[string]SavedQuery{
		"Give the query a name.":  {Name: "   ", SQL: "SELECT 1"},
		"There's no SQL to save.": {Name: "x", SQL: " \n "},
		"was deleted":             {ID: "missing", Name: "x", SQL: "SELECT 1"},
	}
	for want, q := range cases {
		if _, err := s.SaveQuery(q); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v: got %v, want %q", q, err, want)
		}
	}
	if _, err := s.RenameQuery("missing", "x"); err == nil {
		t.Error("renaming a missing query should fail")
	}
	if _, err := s.RenameQuery("missing", " "); err == nil || !strings.Contains(err.Error(), "name") {
		t.Errorf("empty rename: %v", err)
	}
}

func TestSavedQueriesNewestFirstAndPrivate(t *testing.T) {
	isolate(t)
	s := &DBService{}
	for _, name := range []string{"first", "second", "third"} {
		if _, err := s.SaveQuery(SavedQuery{Name: name, SQL: "SELECT 1"}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(3 * time.Millisecond)
	}
	list, _ := s.SavedQueries()
	if len(list) != 3 || list[0].Name != "third" || list[2].Name != "first" {
		t.Fatalf("order: %+v", list)
	}

	path, _ := queriesFile()
	file, err := os.Stat(path)
	if err != nil || file.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: %v, %v", file.Mode().Perm(), err)
	}
	dir, _ := os.Stat(filepath.Dir(path))
	if dir.Mode().Perm() != 0o700 {
		t.Fatalf("folder mode: %v", dir.Mode().Perm())
	}
	if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*.tmp")); len(leftovers) != 0 {
		t.Fatalf("temporary files left: %v", leftovers)
	}
}

func TestDamagedQueriesFile(t *testing.T) {
	isolate(t)
	path, _ := queriesFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (&DBService{}).SavedQueries(); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Fatalf("got %v", err)
	}
}

func TestConnectionsStillWriteThroughTheSharedHelper(t *testing.T) {
	isolate(t)
	s := &DBService{}
	saved, err := s.SaveConnection(SavedConnection{Name: "Local", Config: DBConfig{Host: "localhost", Port: 5432, User: "u", Database: "d"}})
	if err != nil || saved.ID == "" {
		t.Fatalf("save connection: %+v, %v", saved, err)
	}
	lists, err := s.SavedConnections()
	if err != nil || len(lists.Saved) != 1 || lists.Saved[0].Name != "Local" {
		t.Fatalf("lists: %+v, %v", lists, err)
	}
	path, _ := connectionsFile()
	if file, _ := os.Stat(path); file.Mode().Perm() != 0o600 {
		t.Fatalf("connections.json mode: %v", file.Mode().Perm())
	}
}
