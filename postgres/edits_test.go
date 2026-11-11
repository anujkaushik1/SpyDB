package postgres

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Integration tests for editing rows: every save must change exactly the
// row that was edited, or refuse and change nothing. They run against the
// Postgres named by SPYDB_TEST_DATABASE, in a throwaway schema.

const testSchema = "spydb_edit_test"

func str(v string) *string { return &v }

// editFixture connects and creates a fresh schema, dropped afterwards.
func editFixture(t *testing.T, setup ...string) *DBService {
	t.Helper()
	s := connectForTest(t)
	exec(t, s, "DROP SCHEMA IF EXISTS "+testSchema+" CASCADE", "CREATE SCHEMA "+testSchema)
	// Not t.Context(): Go cancels it before cleanups run, so the DROP would never happen.
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+testSchema+" CASCADE") })
	exec(t, s, setup...)
	return s
}

// exec runs setup SQL on the pool (another session than the editor's).
func exec(t *testing.T, s *DBService, statements ...string) {
	t.Helper()
	for _, sql := range statements {
		if _, err := s.pool.Exec(t.Context(), sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
}

// table returns every row of a query as text, NULL as "NULL".
func table(t *testing.T, s *DBService, sql string) []string {
	t.Helper()
	rows, err := s.pool.Query(t.Context(), sql)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(values))
		for i, v := range values {
			if v == nil {
				parts[i] = "NULL"
			} else {
				parts[i] = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(fmtValue(v), "\n", " "), "  ", " "))
			}
		}
		out = append(out, strings.Join(parts, "|"))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func fmtValue(v any) string { return fmt.Sprint(v) }

// query runs one statement through RunQuery, like the query tab.
func query(t *testing.T, s *DBService, sql string) StatementResult {
	t.Helper()
	result, err := s.RunQuery(t.Context(), "tab", sql)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if len(result.Results) != 1 {
		t.Fatalf("%s: %d results", sql, len(result.Results))
	}
	r := result.Results[0]
	for _, c := range r.Columns {
		if strings.HasPrefix(c.Name, "__spydb") {
			t.Fatalf("%s: hidden column %s is shown", sql, c.Name)
		}
	}
	for _, row := range r.Rows {
		if len(row) != len(r.Columns) {
			t.Fatalf("%s: a row has %d values for %d columns", sql, len(row), len(r.Columns))
		}
	}
	return r
}

// editable asserts r can be edited by kind and returns it.
func editable(t *testing.T, r StatementResult, kind string, key ...string) StatementResult {
	t.Helper()
	if r.Target == nil {
		t.Fatalf("want editable (%s), got read-only: %s", kind, r.ReadOnly)
	}
	if r.Target.Kind != kind || !slices.Equal(r.Target.Key, key) {
		t.Fatalf("target %s %v, want %s %v", r.Target.Kind, r.Target.Key, kind, key)
	}
	if len(r.Keys) != len(r.Rows) {
		t.Fatalf("%d keys for %d rows", len(r.Keys), len(r.Rows))
	}
	return r
}

// rowWhere finds the result row whose column col shows value.
func rowWhere(t *testing.T, r StatementResult, col, value string) int {
	t.Helper()
	c := slices.IndexFunc(r.Columns, func(c Column) bool { return c.Name == col })
	found := -1
	for i, row := range r.Rows {
		if row[c] != nil && *row[c] == value {
			if found >= 0 {
				t.Fatalf("two rows with %s = %s", col, value)
			}
			found = i
		}
	}
	if found < 0 {
		t.Fatalf("no row with %s = %s", col, value)
	}
	return found
}

// keyOf is row i's key as SaveRows takes it.
func keyOf(r StatementResult, i int) map[string]*string {
	key := map[string]*string{}
	for j, name := range r.Target.Key {
		key[name] = r.Keys[i][j]
	}
	return key
}

func save(t *testing.T, s *DBService, target *EditTarget, edits ...RowEdit) []SavedRow {
	t.Helper()
	saved, err := s.SaveRows(t.Context(), *target, edits)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	return saved
}

// refuse asserts the save fails, mentioning want, and changes nothing.
func refuse(t *testing.T, s *DBService, target *EditTarget, check string, want string, edits ...RowEdit) {
	t.Helper()
	before := table(t, s, check)
	_, err := s.SaveRows(t.Context(), *target, edits)
	if err == nil {
		t.Fatalf("save should have been refused (%s)", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q, want it to mention %q", err, want)
	}
	if after := table(t, s, check); !slices.Equal(before, after) {
		t.Fatalf("a refused save changed the table:\nbefore %v\nafter  %v", before, after)
	}
}

func assertRows(t *testing.T, s *DBService, sql string, want ...string) {
	t.Helper()
	if got := table(t, s, sql); !slices.Equal(got, want) {
		t.Fatalf("%s\ngot  %v\nwant %v", sql, got, want)
	}
}

// --- 1. No primary key: rows found by ctid + a hash of the whole row ---

func TestEditNoPrimaryKey(t *testing.T) {
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".logs (status text, n int, note text)",
		"INSERT INTO "+testSchema+".logs VALUES ('a', 1, 'same'), ('a', 1, 'same'), ('b', 2, NULL), ('c', 3, 'x')")
	all := "SELECT status, n, note FROM " + testSchema + ".logs ORDER BY status, n, note"

	// Only status is shown; ctid and the row hash ride along hidden.
	r := editable(t, query(t, s, "SELECT status FROM "+testSchema+".logs"), "ctid", "ctid", "md5")
	if len(r.Columns) != 1 || r.Columns[0].Source != "status" {
		t.Fatalf("columns %+v", r.Columns)
	}

	// Two identical rows: editing the second changes only that one.
	dup := -1
	for i, row := range r.Rows {
		if *row[0] == "a" {
			dup = i // the last 'a'
		}
	}
	saved := save(t, s, r.Target, RowEdit{Key: keyOf(r, dup), Old: map[string]*string{"status": str("a")}, Values: map[string]*string{"status": str("z")}})
	assertRows(t, s, all, "a|1|same", "b|2|NULL", "c|3|x", "z|1|same")
	if *saved[0].Values["status"] != "z" || *saved[0].Key["ctid"] == *r.Keys[dup][0] {
		t.Fatalf("saved %+v: want status z and a new ctid", saved[0])
	}

	// The old key is now stale (the row moved): refused, nothing changes.
	refuse(t, s, r.Target, all, "changed or deleted",
		RowEdit{Key: keyOf(r, dup), Old: map[string]*string{"status": str("z")}, Values: map[string]*string{"status": str("y")}})
	// The key from the save works for the next edit (what the grid does).
	save(t, s, r.Target, RowEdit{Key: saved[0].Key, Old: map[string]*string{"status": str("z")}, Values: map[string]*string{"status": str("y")}})
	assertRows(t, s, all, "a|1|same", "b|2|NULL", "c|3|x", "y|1|same")

	// NULLs: old NULL matches NULL; a wrong old value is refused.
	r = editable(t, query(t, s, "SELECT note FROM "+testSchema+".logs WHERE n = 2"), "ctid", "ctid", "md5")
	refuse(t, s, r.Target, all, "changed by someone else",
		RowEdit{Key: keyOf(r, 0), Old: map[string]*string{"note": str("")}, Values: map[string]*string{"note": str("filled")}})
	save(t, s, r.Target, RowEdit{Key: keyOf(r, 0), Old: map[string]*string{"note": nil}, Values: map[string]*string{"note": str("filled")}})
	assertRows(t, s, all, "a|1|same", "b|2|filled", "c|3|x", "y|1|same")

	// Someone changes a column that isn't even shown: the hash no longer
	// matches, so the save is refused rather than overwriting their change.
	r = editable(t, query(t, s, "SELECT status FROM "+testSchema+".logs WHERE n = 3"), "ctid", "ctid", "md5")
	exec(t, s, "UPDATE "+testSchema+".logs SET note = 'theirs' WHERE n = 3")
	refuse(t, s, r.Target, all, "changed or deleted",
		RowEdit{Key: keyOf(r, 0), Old: map[string]*string{"status": str("c")}, Values: map[string]*string{"status": str("mine")}})

	// Deleted row: refused.
	r = editable(t, query(t, s, "SELECT status FROM "+testSchema+".logs WHERE n = 2"), "ctid", "ctid", "md5")
	exec(t, s, "DELETE FROM "+testSchema+".logs WHERE n = 2")
	refuse(t, s, r.Target, all, "changed or deleted",
		RowEdit{Key: keyOf(r, 0), Old: map[string]*string{"status": str("b")}, Values: map[string]*string{"status": str("gone")}})
}

// A deleted row's address can be reused by a new row after VACUUM. The row
// hash keeps an edit of the old row from landing on the new one.
func TestEditNoPrimaryKeyReusedAddress(t *testing.T) {
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".slots (v text) WITH (autovacuum_enabled = false)",
		"INSERT INTO "+testSchema+".slots VALUES ('one'), ('two'), ('three')")
	r := editable(t, query(t, s, "SELECT v FROM "+testSchema+".slots"), "ctid", "ctid", "md5")
	two := rowWhere(t, r, "v", "two")
	oldCtid := *r.Keys[two][0]

	exec(t, s, "DELETE FROM "+testSchema+".slots WHERE v = 'two'", "VACUUM "+testSchema+".slots",
		"INSERT INTO "+testSchema+".slots VALUES ('intruder')")
	if got := table(t, s, "SELECT ctid::text FROM "+testSchema+".slots WHERE v = 'intruder'"); !slices.Equal(got, []string{oldCtid}) {
		t.Skipf("the new row went to %v, not the reused %s; can't test reuse here", got, oldCtid)
	}
	refuse(t, s, r.Target, "SELECT ctid, v FROM "+testSchema+".slots ORDER BY v", "changed or deleted",
		RowEdit{Key: keyOf(r, two), Old: map[string]*string{"v": str("two")}, Values: map[string]*string{"v": str("TWO")}})
	// Without the Old check (the hash alone) it's refused just the same.
	refuse(t, s, r.Target, "SELECT ctid, v FROM "+testSchema+".slots ORDER BY v", "changed or deleted",
		RowEdit{Key: keyOf(r, two), Values: map[string]*string{"v": str("TWO")}})
	assertRows(t, s, "SELECT v FROM "+testSchema+".slots ORDER BY v", "intruder", "one", "three")
}

// --- 2. Unique keys but no primary key: still matched by ctid + hash ---

func TestEditUniqueKeysNoPrimaryKey(t *testing.T) {
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".people (email text UNIQUE, code int UNIQUE, name text)",
		"INSERT INTO "+testSchema+".people VALUES ('a@x', 1, 'Sam'), ('b@x', 2, 'Sam'), (NULL, 3, 'Kim'), (NULL, 4, 'Kim')")
	all := "SELECT email, code, name FROM " + testSchema + ".people ORDER BY code"

	r := editable(t, query(t, s, "SELECT name, email FROM "+testSchema+".people ORDER BY code"), "ctid", "ctid", "md5")
	// Rows 3 and 4 look the same here (NULL email, Kim): edit only row 4.
	save(t, s, r.Target, RowEdit{Key: keyOf(r, 3), Old: map[string]*string{"name": str("Kim")}, Values: map[string]*string{"name": str("Kim 2")}})
	assertRows(t, s, all, "a@x|1|Sam", "b@x|2|Sam", "NULL|3|Kim", "NULL|4|Kim 2")

	// Editing a unique column to a duplicate fails in Postgres; nothing saved.
	r = editable(t, query(t, s, "SELECT email FROM "+testSchema+".people ORDER BY code"), "ctid", "ctid", "md5")
	refuse(t, s, r.Target, all, "uplicate key",
		RowEdit{Key: keyOf(r, 1), Old: map[string]*string{"email": str("b@x")}, Values: map[string]*string{"email": str("a@x")}})
	// Several rows in one save: all or nothing.
	refuse(t, s, r.Target, all, "uplicate key",
		RowEdit{Key: keyOf(r, 0), Old: map[string]*string{"email": str("a@x")}, Values: map[string]*string{"email": str("new@x")}},
		RowEdit{Key: keyOf(r, 1), Old: map[string]*string{"email": str("b@x")}, Values: map[string]*string{"email": str("new@x")}})
	// Unique columns are checked row by row, so b@x must move away first.
	save(t, s, r.Target,
		RowEdit{Key: keyOf(r, 1), Old: map[string]*string{"email": str("b@x")}, Values: map[string]*string{"email": str("c@x")}},
		RowEdit{Key: keyOf(r, 0), Old: map[string]*string{"email": str("a@x")}, Values: map[string]*string{"email": str("b@x")}})
	assertRows(t, s, all, "b@x|1|Sam", "c@x|2|Sam", "NULL|3|Kim", "NULL|4|Kim 2")
}

// --- 3. One-column primary key, not shown: added as a hidden column ---

func TestEditSinglePrimaryKey(t *testing.T) {
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".users (id int PRIMARY KEY, name text, email text)",
		"INSERT INTO "+testSchema+".users VALUES (1, 'Sam', 's1@x'), (2, 'Sam', 's2@x'), (3, 'Ann', NULL)")
	all := "SELECT id, name, email FROM " + testSchema + ".users ORDER BY id"

	r := editable(t, query(t, s, "SELECT name, email FROM "+testSchema+".users ORDER BY id"), "pk", "id")
	if len(r.Columns) != 2 || r.Columns[0].PrimaryKey {
		t.Fatalf("columns %+v", r.Columns)
	}
	if *r.Keys[1][0] != "2" {
		t.Fatalf("keys %v", r.Keys)
	}
	// Two rows named Sam: the second (id 2) changes, id 1 doesn't.
	save(t, s, r.Target, RowEdit{Key: keyOf(r, 1), Old: map[string]*string{"name": str("Sam")}, Values: map[string]*string{"name": str("Sammy")}})
	assertRows(t, s, all, "1|Sam|s1@x", "2|Sammy|s2@x", "3|Ann|NULL")

	// Someone else changes the same cell: refused, theirs kept.
	r = editable(t, query(t, s, "SELECT name FROM "+testSchema+".users ORDER BY id"), "pk", "id")
	exec(t, s, "UPDATE "+testSchema+".users SET name = 'Theirs' WHERE id = 1")
	refuse(t, s, r.Target, all, "changed by someone else",
		RowEdit{Key: keyOf(r, 0), Old: map[string]*string{"name": str("Sam")}, Values: map[string]*string{"name": str("Mine")}})
	// Someone changes another column: the edited cell is unchanged, so it saves.
	exec(t, s, "UPDATE "+testSchema+".users SET email = 'new@x' WHERE id = 3")
	save(t, s, r.Target, RowEdit{Key: keyOf(r, 2), Old: map[string]*string{"name": str("Ann")}, Values: map[string]*string{"name": str("Anna")}})
	assertRows(t, s, all, "1|Theirs|s1@x", "2|Sammy|s2@x", "3|Anna|new@x")

	// Deleted row: refused.
	exec(t, s, "DELETE FROM "+testSchema+".users WHERE id = 2")
	refuse(t, s, r.Target, all, "wasn't found",
		RowEdit{Key: keyOf(r, 1), Old: map[string]*string{"name": str("Sammy")}, Values: map[string]*string{"name": str("x")}})

	// The primary key shown: nothing hidden is added, keys come from it.
	r = editable(t, query(t, s, "SELECT id, name FROM "+testSchema+".users ORDER BY id"), "pk", "id")
	if !r.Columns[0].PrimaryKey {
		t.Fatal("id should be marked as the primary key")
	}
	// Changing the primary key itself, then editing again with the new key.
	saved := save(t, s, r.Target, RowEdit{Key: keyOf(r, 0), Old: map[string]*string{"id": str("1")}, Values: map[string]*string{"id": str("10")}})
	if *saved[0].Key["id"] != "10" {
		t.Fatalf("saved key %v", saved[0].Key)
	}
	save(t, s, r.Target, RowEdit{Key: saved[0].Key, Old: map[string]*string{"name": str("Theirs")}, Values: map[string]*string{"name": str("Ten")}})
	assertRows(t, s, all, "3|Anna|new@x", "10|Ten|s1@x")
}

// --- 4. Composite primary key (key1 + key2) ---

func TestEditCompositePrimaryKey(t *testing.T) {
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".grid (k1 int, k2 text, v text, PRIMARY KEY (k1, k2))",
		"INSERT INTO "+testSchema+".grid VALUES (1, 'a', 'same'), (1, 'b', 'same'), (2, 'a', 'same')")
	all := "SELECT k1, k2, v FROM " + testSchema + ".grid ORDER BY k1, k2"

	// Neither key column shown: both are added.
	r := editable(t, query(t, s, "SELECT v FROM "+testSchema+".grid ORDER BY k1, k2"), "pk", "k1", "k2")
	save(t, s, r.Target, RowEdit{Key: keyOf(r, 1), Old: map[string]*string{"v": str("same")}, Values: map[string]*string{"v": str("(1,b)")}})
	assertRows(t, s, all, "1|a|same", "1|b|(1,b)", "2|a|same")

	// Only half the key shown (k1 repeats): the whole key is still added.
	r = editable(t, query(t, s, "SELECT k1, v FROM "+testSchema+".grid ORDER BY k1, k2"), "pk", "k1", "k2")
	if *r.Keys[0][1] != "a" || *r.Keys[1][1] != "b" {
		t.Fatalf("keys %v", r.Keys)
	}
	save(t, s, r.Target, RowEdit{Key: keyOf(r, 0), Old: map[string]*string{"v": str("same")}, Values: map[string]*string{"v": str("(1,a)")}})
	assertRows(t, s, all, "1|a|(1,a)", "1|b|(1,b)", "2|a|same")

	// Half a key never matches a row on its own.
	refuse(t, s, r.Target, all, "missing its k2",
		RowEdit{Key: map[string]*string{"k1": str("2")}, Old: map[string]*string{"v": str("same")}, Values: map[string]*string{"v": str("x")}})

	// Editing one half of the key (k1 1 → 2 for (1,b)); the next edit uses the new key.
	r = editable(t, query(t, s, "SELECT * FROM "+testSchema+".grid ORDER BY k1, k2"), "pk", "k1", "k2")
	saved := save(t, s, r.Target, RowEdit{Key: keyOf(r, 1), Old: map[string]*string{"k1": str("1")}, Values: map[string]*string{"k1": str("2")}})
	if *saved[0].Key["k1"] != "2" || *saved[0].Key["k2"] != "b" {
		t.Fatalf("saved key %v", saved[0].Key)
	}
	save(t, s, r.Target, RowEdit{Key: saved[0].Key, Old: map[string]*string{"v": str("(1,b)")}, Values: map[string]*string{"v": str("(2,b)")}})
	assertRows(t, s, all, "1|a|(1,a)", "2|a|same", "2|b|(2,b)")

	// A key swapped meanwhile: (2,a) and (2,b) trade their k2. The edit of
	// what was (2,a) now finds the row that was (2,b), but its v differs, so
	// the old-value check refuses it.
	r = editable(t, query(t, s, "SELECT v FROM "+testSchema+".grid ORDER BY k1, k2"), "pk", "k1", "k2")
	exec(t, s, "UPDATE "+testSchema+".grid SET k2 = CASE k2 WHEN 'a' THEN 'tmp' ELSE k2 END WHERE k1 = 2",
		"UPDATE "+testSchema+".grid SET k2 = 'a' WHERE k1 = 2 AND k2 = 'b'",
		"UPDATE "+testSchema+".grid SET k2 = 'b' WHERE k1 = 2 AND k2 = 'tmp'")
	refuse(t, s, r.Target, all, "changed by someone else",
		RowEdit{Key: keyOf(r, 1), Old: map[string]*string{"v": str("same")}, Values: map[string]*string{"v": str("wrong")}})
}

// --- Results that must stay read-only, with the reason shown ---

func TestEditReadOnlyResults(t *testing.T) {
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".users (id int PRIMARY KEY, name text, parent int)",
		"INSERT INTO "+testSchema+".users VALUES (1, 'a', NULL), (2, 'b', 1), (3, 'c', 1)",
		"CREATE TABLE "+testSchema+".nopk (v text)",
		"INSERT INTO "+testSchema+".nopk VALUES ('x')",
		"CREATE VIEW "+testSchema+".users_v AS SELECT * FROM "+testSchema+".users",
		"CREATE MATERIALIZED VIEW "+testSchema+".users_m AS SELECT * FROM "+testSchema+".users",
		"CREATE TABLE "+testSchema+".parted (id int, v text) PARTITION BY RANGE (id)",
		"CREATE TABLE "+testSchema+".parted_1 PARTITION OF "+testSchema+".parted FOR VALUES FROM (0) TO (100)",
		"CREATE TABLE "+testSchema+".parent (id int PRIMARY KEY, v text)",
		"CREATE TABLE "+testSchema+".child () INHERITS ("+testSchema+".parent)",
	)
	u := testSchema + ".users"
	tests := []struct{ sql, reason string }{
		// The self-join that used to be editable: b.name would have saved into a's row.
		{"SELECT a.id, b.name FROM " + u + " a JOIN " + u + " b ON b.parent = a.id", "a join"},
		{"SELECT a.id, a.name FROM " + u + " a, " + u + " b WHERE b.parent = a.id", "a join"},
		{"SELECT id, name FROM " + u + " GROUP BY id, name", "GROUP BY"},
		{"SELECT DISTINCT id, name FROM " + u, "DISTINCT"},
		{"SELECT id, name FROM " + u + " UNION ALL SELECT id, name FROM " + u, "UNION, INTERSECT or EXCEPT"},
		{"WITH x AS (SELECT * FROM " + u + ") SELECT * FROM x", notPlainSelect},
		{"SELECT * FROM (SELECT * FROM " + u + ") s", "a subquery or function in FROM"},
		{"SELECT count(*) FROM " + u, "no table columns in the result"},
		// A set-returning function repeats rows: each key shows up twice.
		{"SELECT id, unnest(ARRAY[1, 2]) FROM " + u, "rows repeat"},
		{"SELECT name, unnest(ARRAY[1, 2]) FROM " + u, "rows repeat"},
		{"SELECT * FROM " + testSchema + ".users_v", "a view"},
		{"SELECT * FROM " + testSchema + ".users_m", "a materialized view"},
		{"SELECT * FROM " + testSchema + ".parted", "a partitioned table without a primary key"},
		{"SELECT * FROM " + testSchema + ".parent", "a table other tables inherit from"},
		// A set-returning function in ORDER BY repeats rows.
		{"SELECT id, name FROM " + u + " ORDER BY generate_series(1, 2)", "rows repeat"},
		{"SELECT name FROM " + u + " ORDER BY generate_series(1, 2)", "rows repeat"},
		{"SELECT v FROM " + testSchema + ".nopk ORDER BY generate_series(1, 2)", "rows repeat"},
	}
	for _, tc := range tests {
		r := query(t, s, tc.sql)
		if r.Target != nil || r.ReadOnly != tc.reason || len(r.Keys) != 0 {
			t.Errorf("%s: target %v reason %q, want read-only %q", tc.sql, r.Target, r.ReadOnly, tc.reason)
		}
		for _, c := range r.Columns {
			if c.Source != "" {
				t.Errorf("%s: column %s still has a source", tc.sql, c.Name)
			}
		}
	}

	// Expressions next to the columns: the columns stay editable, the
	// expressions don't.
	for _, sql := range []string{"SELECT id, name, upper(name) AS shout FROM " + u + " ORDER BY id", "SELECT upper(name) AS shout, name FROM " + u + " ORDER BY id"} {
		r := editable(t, query(t, s, sql), "pk", "id")
		shout := slices.IndexFunc(r.Columns, func(c Column) bool { return c.Name == "shout" })
		name := slices.IndexFunc(r.Columns, func(c Column) bool { return c.Name == "name" })
		if r.Columns[shout].Source != "" || r.Columns[name].Source != "name" {
			t.Errorf("%s: columns %+v", sql, r.Columns)
		}
		save(t, s, r.Target, RowEdit{Key: keyOf(r, 1), Old: map[string]*string{"name": str("b")}, Values: map[string]*string{"name": str("B")}})
		assertRows(t, s, "SELECT id, name FROM "+u+" ORDER BY id", "1|a", "2|B", "3|c")
		exec(t, s, "UPDATE "+u+" SET name = 'b' WHERE id = 2")
	}

	// Several statements in one run: all read-only.
	result, err := s.RunQuery(t.Context(), "tab", "SELECT * FROM "+u+"; SELECT * FROM "+u)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range result.Results {
		if r.Target != nil || r.ReadOnly != "more than one statement" {
			t.Errorf("multi-statement: target %v reason %q", r.Target, r.ReadOnly)
		}
	}

	// A temporary table: saves go through another session, which can't see it.
	query(t, s, "CREATE TEMP TABLE scratch (id int PRIMARY KEY, v text)")
	if r := query(t, s, "SELECT * FROM scratch"); r.Target != nil || r.ReadOnly != "a temporary table" {
		t.Errorf("temp table: target %v reason %q", r.Target, r.ReadOnly)
	}

	// Inside a transaction nothing is added to the query: only results with
	// the whole primary key stay editable.
	query(t, s, "BEGIN")
	if r := query(t, s, "SELECT name FROM "+u); r.Target != nil || r.ReadOnly != "the primary key isn't shown, inside a transaction" {
		t.Errorf("in transaction, no key: target %v reason %q", r.Target, r.ReadOnly)
	}
	if r := query(t, s, "SELECT v FROM "+testSchema+".nopk"); r.Target != nil || r.ReadOnly != "no primary key, inside a transaction" {
		t.Errorf("in transaction, no primary key: target %v reason %q", r.Target, r.ReadOnly)
	}
	editable(t, query(t, s, "SELECT id, name FROM "+u), "pk", "id")
	query(t, s, "ROLLBACK")

	// A partitioned table with a primary key is editable by it, even when a
	// row moves to another partition.
	exec(t, s, "CREATE TABLE "+testSchema+".orders (id int PRIMARY KEY, v text) PARTITION BY RANGE (id)",
		"CREATE TABLE "+testSchema+".orders_1 PARTITION OF "+testSchema+".orders FOR VALUES FROM (0) TO (10)",
		"CREATE TABLE "+testSchema+".orders_2 PARTITION OF "+testSchema+".orders FOR VALUES FROM (10) TO (20)",
		"INSERT INTO "+testSchema+".orders VALUES (1, 'a'), (11, 'b')")
	r := editable(t, query(t, s, "SELECT v FROM "+testSchema+".orders ORDER BY id"), "pk", "id")
	save(t, s, r.Target, RowEdit{Key: keyOf(r, 0), Old: map[string]*string{"v": str("a")}, Values: map[string]*string{"v": str("A")}})
	r = editable(t, query(t, s, "SELECT id FROM "+testSchema+".orders ORDER BY id"), "pk", "id")
	save(t, s, r.Target, RowEdit{Key: keyOf(r, 0), Old: map[string]*string{"id": str("1")}, Values: map[string]*string{"id": str("15")}})
	assertRows(t, s, "SELECT tableoid::regclass::text, id, v FROM "+testSchema+".orders ORDER BY id",
		testSchema+".orders_2|11|b", testSchema+".orders_2|15|A")
}

// The editor session's time zone and date style: values compare and parse
// as the grid showed them, though the save runs in another session.
func TestEditSessionSettings(t *testing.T) {
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".events (id int PRIMARY KEY, at timestamptz, day date, span interval)",
		"INSERT INTO "+testSchema+".events VALUES (1, '2024-01-01 00:00:00+00', '2024-02-03', '1 day 2 hours')",
		"CREATE TABLE "+testSchema+".events_nopk (at timestamptz, day date)",
		"INSERT INTO "+testSchema+".events_nopk VALUES ('2024-01-01 00:00:00+00', '2024-02-03')")
	query(t, s, "SET TimeZone = 'America/New_York'")
	query(t, s, "SET DateStyle = 'SQL, DMY'")
	query(t, s, "SET IntervalStyle = 'postgres_verbose'")

	r := editable(t, query(t, s, "SELECT at, day, span FROM "+testSchema+".events"), "pk", "id")
	if *r.Rows[0][0] != "31/12/2023 19:00:00 EST" {
		t.Fatalf("shown as %q", *r.Rows[0][0])
	}
	// A typed time without a zone means the editor's zone (New York).
	save(t, s, r.Target, RowEdit{Key: keyOf(r, 0),
		Old:    map[string]*string{"at": r.Rows[0][0], "day": r.Rows[0][1], "span": r.Rows[0][2]},
		Values: map[string]*string{"at": str("01/06/2024 10:00"), "day": str("04/02/2024"), "span": str("@ 3 days")}})
	assertRows(t, s, "SELECT (at AT TIME ZONE 'UTC')::text, day::text, span::text FROM "+testSchema+".events",
		"2024-06-01 14:00:00|2024-02-04|3 days")

	// No primary key: the row hash is computed the same way on both sides.
	r = editable(t, query(t, s, "SELECT day FROM "+testSchema+".events_nopk"), "ctid", "ctid", "md5")
	save(t, s, r.Target, RowEdit{Key: keyOf(r, 0), Old: map[string]*string{"day": r.Rows[0][0]}, Values: map[string]*string{"day": str("05/02/2024")}})
	assertRows(t, s, "SELECT day::text FROM "+testSchema+".events_nopk", "2024-02-05")
}

// Old values and row hashes for many types: saving a value unchanged works,
// a wrong old value is refused.
func TestEditTypes(t *testing.T) {
	columns := `b bool, i inet, c char(5), a int[], j json, jb jsonb, x xml, p point, n numeric(8,2),
		f float8, r real, m money, by bytea, u uuid, iv interval, ts timestamptz, e text`
	values := `true, '192.168.1.5', 'ab', '{1,NULL,3}', '{"a" : 1}', '{"b": [1, 2]}', '<a>x</a>', '(1.5,2)', 1.5,
		'NaN', 0.1, 12.34, '\xdeadbeef', 'A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11', '1 day', '2024-01-02 03:04:05.123456+00', ''`
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".typed (id int PRIMARY KEY, "+columns+")",
		"INSERT INTO "+testSchema+".typed VALUES (1, "+values+")",
		"CREATE TABLE "+testSchema+".typed_nopk ("+columns+")",
		"INSERT INTO "+testSchema+".typed_nopk VALUES ("+values+")")

	for _, name := range []string{"typed", "typed_nopk"} {
		r := query(t, s, "SELECT b, i, c, a, j, jb, x, p, n, f, r, m, by, u, iv, ts, e FROM "+testSchema+"."+name)
		if r.Target == nil {
			t.Fatalf("%s: read-only: %s", name, r.ReadOnly)
		}
		key := keyOf(r, 0)
		for col, c := range r.Columns {
			shown := r.Rows[0][col]
			// Unchanged value with the right old value: saves. For a table
			// without a primary key each save moves the row, so the next uses
			// the returned key.
			saved, err := s.SaveRows(t.Context(), *r.Target, []RowEdit{{Key: key, Old: map[string]*string{c.Name: shown}, Values: map[string]*string{c.Name: shown}}})
			if err != nil {
				t.Errorf("%s.%s = %q: %v", name, c.Name, *shown, err)
				continue
			}
			key = saved[0].Key
			if got := saved[0].Values[c.Name]; *got != *shown {
				t.Errorf("%s.%s: saved %q, shown %q", name, c.Name, *got, *shown)
			}
			// A wrong old value: refused.
			if _, err := s.SaveRows(t.Context(), *r.Target, []RowEdit{{Key: key, Old: map[string]*string{c.Name: str(*shown + "?")}, Values: map[string]*string{c.Name: shown}}}); err == nil {
				t.Errorf("%s.%s: a wrong old value was accepted", name, c.Name)
			}
		}
	}
}

// Table tabs: tables without a primary key are editable there too.
func TestEditTableData(t *testing.T) {
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".nopk (v text, n int)",
		"INSERT INTO "+testSchema+".nopk VALUES ('a', 1), ('a', 1), ('b', 2)",
		"CREATE TABLE "+testSchema+".pk (id int PRIMARY KEY, v text)",
		"INSERT INTO "+testSchema+".pk VALUES (1, 'a')",
		"CREATE VIEW "+testSchema+".v AS SELECT 1 AS one")
	page, err := s.TableData(t.Context(), TableQuery{Schema: testSchema, Table: "nopk", PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if page.Target == nil || page.Target.Kind != "ctid" || len(page.Keys) != 3 || len(page.Columns) != 2 || len(page.Rows[0]) != 2 {
		t.Fatalf("nopk page: target %+v keys %d columns %d", page.Target, len(page.Keys), len(page.Columns))
	}
	key := map[string]*string{"ctid": page.Keys[1][0], "md5": page.Keys[1][1]}
	save(t, s, page.Target, RowEdit{Key: key, Old: map[string]*string{"v": str("a")}, Values: map[string]*string{"v": str("z")}})
	assertRows(t, s, "SELECT v, n FROM "+testSchema+".nopk ORDER BY v", "a|1", "b|2", "z|1")

	page, err = s.TableData(t.Context(), TableQuery{Schema: testSchema, Table: "pk", PageSize: 100})
	if err != nil || page.Target == nil || page.Target.Kind != "pk" || *page.Keys[0][0] != "1" {
		t.Fatalf("pk page: %+v %v", page.Target, err)
	}
	page, err = s.TableData(t.Context(), TableQuery{Schema: testSchema, Table: "v", PageSize: 100})
	if err != nil || page.Target != nil || page.ReadOnly != "a view" {
		t.Fatalf("view page: %+v %q %v", page.Target, page.ReadOnly, err)
	}
	// One extra row is fetched to know there's a next page; keys stay aligned.
	page, err = s.TableData(t.Context(), TableQuery{Schema: testSchema, Table: "nopk", PageSize: 2})
	if err != nil || len(page.Rows) != 2 || len(page.Keys) != 2 || !page.HasMore {
		t.Fatalf("paged: rows %d keys %d more %v %v", len(page.Rows), len(page.Keys), page.HasMore, err)
	}
}

// Later pages carry their keys; exports never include the hidden columns;
// an error's position points into the SQL as written.
func TestEditPagesExportsAndErrors(t *testing.T) {
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".big (v int)",
		"INSERT INTO "+testSchema+".big SELECT g FROM generate_series(1, 1500) g")
	r := editable(t, query(t, s, "SELECT v FROM "+testSchema+".big ORDER BY v"), "ctid", "ctid", "md5")
	result, _ := s.RunQuery(t.Context(), "tab", "SELECT v FROM "+testSchema+".big ORDER BY v")
	page, err := s.FetchRows("tab", result.RunID, 0, 1000)
	if err != nil || len(page.Rows) != 500 || len(page.Keys) != 500 || *page.Rows[0][0] != "1001" {
		t.Fatalf("page 2: rows %d keys %d %v", len(page.Rows), len(page.Keys), err)
	}
	key := map[string]*string{"ctid": page.Keys[233][0], "md5": page.Keys[233][1]} // v = 1234
	save(t, s, r.Target, RowEdit{Key: key, Old: map[string]*string{"v": str("1234")}, Values: map[string]*string{"v": str("-1234")}})
	assertRows(t, s, "SELECT count(*) FILTER (WHERE v < 0), min(v) FROM "+testSchema+".big", "1|-1234")

	path := filepath.Join(t.TempDir(), "out.csv")
	if _, err := s.ExportResult(t.Context(), "tab", result.RunID, 0, path, "csv"); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	header, err := csv.NewReader(f).Read()
	if err != nil || !slices.Equal(header, []string{"v"}) {
		t.Fatalf("export header %v %v", header, err)
	}

	sql := "SELECT v FROM " + testSchema + ".big WHERE v = 'abc'"
	_, err = s.RunQuery(t.Context(), "tab", sql)
	qe, ok := err.(*QueryError)
	if !ok || qe.Position != strings.Index(sql, "'abc'")+1 {
		t.Fatalf("error %v at %d, want position %d", err, qe.Position, strings.Index(sql, "'abc'")+1)
	}
}

// Privileges are checked as the editor's role (after SET ROLE), and saves
// run as that role too. When adding the key columns would make the query
// invalid, it runs exactly as written, read-only.
func TestEditRolesAndFallback(t *testing.T) {
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".secret (v text, hidden text)",
		"INSERT INTO "+testSchema+".secret VALUES ('ok', 'no')",
		"CREATE TABLE "+testSchema+".open (id int PRIMARY KEY, v text)",
		"INSERT INTO "+testSchema+".open VALUES (1, 'a')",
		"DROP ROLE IF EXISTS spydb_edit_limited",
		"CREATE ROLE spydb_edit_limited",
		"GRANT USAGE ON SCHEMA "+testSchema+" TO spydb_edit_limited",
		"GRANT SELECT (v) ON "+testSchema+".secret TO spydb_edit_limited",
		"GRANT SELECT ON "+testSchema+".open TO spydb_edit_limited")
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+testSchema+" CASCADE")
		s.pool.Exec(context.Background(), "DROP ROLE IF EXISTS spydb_edit_limited")
	})
	query(t, s, "SET ROLE spydb_edit_limited")
	if r := query(t, s, "SELECT v FROM "+testSchema+".secret"); r.Target != nil || r.ReadOnly != "no permission to update this table" || *r.Rows[0][0] != "ok" {
		t.Fatalf("no UPDATE: target %v reason %q rows %v", r.Target, r.ReadOnly, r.Rows)
	}
	exec(t, s, "GRANT UPDATE ON "+testSchema+".secret TO spydb_edit_limited")
	if r := query(t, s, "SELECT v FROM "+testSchema+".secret"); r.Target != nil || !strings.HasPrefix(r.ReadOnly, "no permission to read every column") {
		t.Fatalf("SELECT on one column only: target %v reason %q", r.Target, r.ReadOnly)
	}
	// Can read, can't update: read-only before a save is even tried.
	if r := query(t, s, "SELECT id, v FROM "+testSchema+".open"); r.Target != nil || r.ReadOnly != "" && r.ReadOnly != "no permission to update this table" {
		t.Fatalf("open table: target %v reason %q", r.Target, r.ReadOnly)
	}
	// The save runs as the role too: with the PK shown nothing is looked up
	// before the run, so the save itself must be refused.
	query(t, s, "BEGIN")
	r := editable(t, query(t, s, "SELECT id, v FROM "+testSchema+".open"), "pk", "id")
	query(t, s, "ROLLBACK")
	refuse(t, s, r.Target, "SELECT id, v FROM "+testSchema+".open", "ermission denied",
		RowEdit{Key: keyOf(r, 0), Old: map[string]*string{"v": str("a")}, Values: map[string]*string{"v": str("b")}})
	query(t, s, "RESET ROLE")

	// current_user isn't a column: the query is valid, but adding the key
	// next to count(*) isn't. It runs as written, once.
	r = query(t, s, "SELECT current_user, count(*) FROM "+testSchema+".open")
	if r.Target != nil || r.ReadOnly != "SpyDB couldn't add the row's key to this query" || *r.Rows[0][1] != "1" {
		t.Fatalf("fallback: target %v reason %q rows %v", r.Target, r.ReadOnly, r.Rows)
	}
}
