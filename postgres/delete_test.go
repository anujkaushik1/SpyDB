package postgres

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// Integration tests for deleting rows: every delete must remove exactly the
// rows picked, or refuse and remove nothing. Against SPYDB_TEST_DATABASE, in
// the throwaway schema editFixture makes.

// rowDelete is row i of r as the grid sends it: its key, and every shown
// value that comes from the table.
func rowDelete(r StatementResult, i int) RowDelete {
	old := map[string]*string{}
	for j, c := range r.Columns {
		if c.Source != "" {
			old[c.Source] = r.Rows[i][j]
		}
	}
	return RowDelete{Key: keyOf(r, i), Old: old}
}

// refuseDelete asserts the delete fails, mentioning want, and changes nothing.
func refuseDelete(t *testing.T, s *DBService, target *EditTarget, check, want string, rows ...RowDelete) {
	t.Helper()
	before := table(t, s, check)
	n, err := s.DeleteRows(t.Context(), *target, rows)
	if err == nil {
		t.Fatalf("delete should have been refused (%s); deleted %d", want, n)
	}
	if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), nothingDeleted) {
		t.Fatalf("error %q, want it to mention %q and %q", err, want, nothingDeleted)
	}
	if after := table(t, s, check); !slices.Equal(before, after) {
		t.Fatalf("a refused delete changed the table:\nbefore %v\nafter  %v", before, after)
	}
}

func TestDeleteRows(t *testing.T) {
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".people (id int PRIMARY KEY, name text, note text)",
		"INSERT INTO "+testSchema+".people VALUES (1, 'Asha', 'a'), (2, 'Ravi', NULL), (3, 'Meera', 'c'), (4, 'Kabir', 'd')")
	all := "SELECT id, name, note FROM " + testSchema + ".people ORDER BY id"
	r := editable(t, query(t, s, "SELECT id, name, note FROM "+testSchema+".people ORDER BY id"), "pk", "id")

	n, err := s.DeleteRows(t.Context(), *r.Target, []RowDelete{rowDelete(r, 1), rowDelete(r, 2)})
	if err != nil || n != 2 {
		t.Fatalf("deleted %d, %v; want 2, nil", n, err)
	}
	assertRows(t, s, all, "1|Asha|a", "4|Kabir|d")

	// Already deleted: refused, and the other row in the same delete stays.
	refuseDelete(t, s, r.Target, all, "wasn't found", rowDelete(r, 0), rowDelete(r, 1))

	// Changed by someone else since it was loaded: refused, naming the column.
	exec(t, s, "UPDATE "+testSchema+".people SET note = 'changed' WHERE id = 4")
	refuseDelete(t, s, r.Target, all, "changed by someone else since it was loaded (note)", rowDelete(r, 0), rowDelete(r, 3))

	// The same row twice; rows read before a reconnect; an empty list.
	refuseDelete(t, s, r.Target, all, "twice", rowDelete(r, 0), rowDelete(r, 0))
	stale := *r.Target
	stale.Conn++
	refuseDelete(t, s, &stale, all, "earlier connection", rowDelete(r, 0))
	if n, err := s.DeleteRows(t.Context(), *r.Target, nil); n != 0 || err != nil {
		t.Fatalf("nothing to delete: %d, %v", n, err)
	}

	// The table dropped and created again under the same name: another table.
	exec(t, s, "DROP TABLE "+testSchema+".people",
		"CREATE TABLE "+testSchema+".people (id int PRIMARY KEY, name text, note text)",
		"INSERT INTO "+testSchema+".people VALUES (1, 'Asha', 'a')")
	refuseDelete(t, s, r.Target, all, "dropped and created again", rowDelete(r, 0))
}

// Without a primary key a row is found by its address and a hash of the
// whole row: of two identical rows, only the one picked goes.
func TestDeleteNoPrimaryKey(t *testing.T) {
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".logs (status text, n int)",
		"INSERT INTO "+testSchema+".logs VALUES ('a', 1), ('a', 1), ('b', 2)")
	all := "SELECT status, n FROM " + testSchema + ".logs ORDER BY status, n"
	r := editable(t, query(t, s, "SELECT status FROM "+testSchema+".logs ORDER BY status"), "ctid", "ctid", "md5")

	if n, err := s.DeleteRows(t.Context(), *r.Target, []RowDelete{rowDelete(r, 1)}); err != nil || n != 1 {
		t.Fatalf("deleted %d, %v", n, err)
	}
	assertRows(t, s, all, "a|1", "b|2")

	// A column not shown changed: the hash no longer matches.
	exec(t, s, "UPDATE "+testSchema+".logs SET n = 9 WHERE status = 'b'")
	refuseDelete(t, s, r.Target, all, "changed or deleted", rowDelete(r, 2))
}

// Foreign keys: a row others still point at is refused (at once, or at
// commit when deferred); ON DELETE CASCADE / SET NULL act, and
// DeleteEffects lists them, with the table's DELETE triggers, beforehand.
func TestDeleteForeignKeys(t *testing.T) {
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".users (id int PRIMARY KEY, name text)",
		"INSERT INTO "+testSchema+".users VALUES (1, 'a'), (2, 'b'), (3, 'c'), (4, 'd')",
		// Refuses: points at user 1.
		"CREATE TABLE "+testSchema+".invoices (id int PRIMARY KEY, user_id int REFERENCES "+testSchema+".users)",
		"INSERT INTO "+testSchema+".invoices VALUES (10, 1)",
		// Refuses at commit: points at user 2.
		"CREATE TABLE "+testSchema+".audits (id int PRIMARY KEY, user_id int REFERENCES "+testSchema+".users DEFERRABLE INITIALLY DEFERRED)",
		"INSERT INTO "+testSchema+".audits VALUES (20, 2)",
		// Cascades from user 3, and on to its lines.
		"CREATE TABLE "+testSchema+".orders (id int PRIMARY KEY, user_id int REFERENCES "+testSchema+".users ON DELETE CASCADE)",
		"CREATE TABLE "+testSchema+".order_lines (id int PRIMARY KEY, order_id int REFERENCES "+testSchema+".orders ON DELETE CASCADE)",
		"INSERT INTO "+testSchema+".orders VALUES (30, 3)",
		"INSERT INTO "+testSchema+".order_lines VALUES (31, 30), (32, 30)",
		// A partitioned table: named once, not once per partition.
		"CREATE TABLE "+testSchema+".events (id int, user_id int REFERENCES "+testSchema+".users ON DELETE CASCADE) PARTITION BY RANGE (id)",
		"CREATE TABLE "+testSchema+".events_1 PARTITION OF "+testSchema+".events FOR VALUES FROM (0) TO (100)",
		// Set to NULL when user 4 goes.
		"CREATE TABLE "+testSchema+".notes (id int PRIMARY KEY, user_id int REFERENCES "+testSchema+".users ON DELETE SET NULL)",
		"INSERT INTO "+testSchema+".notes VALUES (40, 4)",
		// Triggers: only enabled DELETE ones are listed.
		"CREATE FUNCTION "+testSchema+".noop() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RETURN OLD; END'",
		"CREATE TRIGGER users_deleted BEFORE DELETE ON "+testSchema+".users FOR EACH ROW EXECUTE FUNCTION "+testSchema+".noop()",
		"CREATE TRIGGER users_updated BEFORE UPDATE ON "+testSchema+".users FOR EACH ROW EXECUTE FUNCTION "+testSchema+".noop()",
		"CREATE TRIGGER users_off BEFORE DELETE ON "+testSchema+".users FOR EACH ROW EXECUTE FUNCTION "+testSchema+".noop()",
		"ALTER TABLE "+testSchema+".users DISABLE TRIGGER users_off")
	users := "SELECT id, name FROM " + testSchema + ".users ORDER BY id"
	r := editable(t, query(t, s, users), "pk", "id")

	e, err := s.DeleteEffects(t.Context(), *r.Target)
	if err != nil {
		t.Fatal(err)
	}
	q := func(name string) string { return testSchema + "." + name }
	if e.Blocked != "" || e.Database == "" || e.Role == "" || e.Server != "localhost:5432" && e.Server != "127.0.0.1:5432" ||
		!slices.Equal(e.Cascades, []string{q("events"), q("order_lines"), q("orders")}) ||
		!slices.Equal(e.SetsNull, []string{q("notes")}) ||
		!slices.Equal(e.Triggers, []string{"users_deleted"}) {
		t.Fatalf("effects %+v", e)
	}

	refuseDelete(t, s, r.Target, users, "foreign key", rowDelete(r, 0))
	refuseDelete(t, s, r.Target, users, "foreign key", rowDelete(r, 1)) // at commit
	// All or nothing: user 3 would go, but user 1 can't.
	refuseDelete(t, s, r.Target, users, "foreign key", rowDelete(r, 2), rowDelete(r, 0))
	assertRows(t, s, "SELECT id FROM "+q("order_lines")+" ORDER BY id", "31", "32")

	if n, err := s.DeleteRows(t.Context(), *r.Target, []RowDelete{rowDelete(r, 2), rowDelete(r, 3)}); err != nil || n != 2 {
		t.Fatalf("deleted %d, %v", n, err)
	}
	assertRows(t, s, users, "1|a", "2|b")
	assertRows(t, s, "SELECT count(*) FROM "+q("orders")+", "+q("order_lines"), "0")
	assertRows(t, s, "SELECT id, user_id FROM "+q("notes"), "40|NULL")
}

// Deletes run as the editor's role (SET ROLE): DeleteEffects says it can't,
// and the delete itself is refused by Postgres.
func TestDeleteAsEditorRole(t *testing.T) {
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".open (id int PRIMARY KEY, v text)",
		"INSERT INTO "+testSchema+".open VALUES (1, 'a')",
		"DROP ROLE IF EXISTS spydb_delete_limited",
		"CREATE ROLE spydb_delete_limited",
		"GRANT USAGE ON SCHEMA "+testSchema+" TO spydb_delete_limited",
		"GRANT SELECT, UPDATE ON "+testSchema+".open TO spydb_delete_limited")
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+testSchema+" CASCADE")
		s.pool.Exec(context.Background(), "DROP ROLE IF EXISTS spydb_delete_limited")
	})
	query(t, s, "SET ROLE spydb_delete_limited")
	r := editable(t, query(t, s, "SELECT id, v FROM "+testSchema+".open"), "pk", "id")
	query(t, s, "RESET ROLE")

	if e, err := s.DeleteEffects(t.Context(), *r.Target); err != nil || e.Role != "spydb_delete_limited" || !strings.Contains(e.Blocked, "allowed to delete") {
		t.Fatalf("effects %+v, %v; want it blocked for spydb_delete_limited", e, err)
	}
	refuseDelete(t, s, r.Target, "SELECT id, v FROM "+testSchema+".open", "ermission denied", rowDelete(r, 0))
}

// Several rows saved or deleted at once touch exactly those rows: not their
// neighbours, not the rest of the table, not another table. Checked by
// comparing every row of both tables before and after, as a person would
// fear "I picked 4 and everything went".
func TestOnlyThoseRowsChange(t *testing.T) {
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".orders (id int PRIMARY KEY, city text, amount numeric(10,2), paid boolean)",
		"INSERT INTO "+testSchema+".orders SELECT i, (ARRAY['Delhi','Mumbai','Pune'])[i % 3 + 1], i * 10.5, i % 2 = 0 FROM generate_series(1, 30) i",
		"CREATE TABLE "+testSchema+".bystander (id int PRIMARY KEY, city text)",
		"INSERT INTO "+testSchema+".bystander SELECT i, 'Delhi' FROM generate_series(1, 10) i",
		"CREATE TABLE "+testSchema+".nokey (status text, n int)",
		"INSERT INTO "+testSchema+".nokey VALUES ('a', 1), ('a', 1), ('a', 1), ('b', 2), ('b', 2), ('c', 3)")
	orders := "SELECT id, city, amount, paid FROM " + testSchema + ".orders ORDER BY id"
	bystander := "SELECT id, city FROM " + testSchema + ".bystander ORDER BY id"
	nokey := "SELECT status, n FROM " + testSchema + ".nokey ORDER BY status, n"
	r := editable(t, query(t, s, orders), "pk", "id")
	col := func(name string) int {
		for j, c := range r.Columns {
			if c.Name == name {
				return j
			}
		}
		t.Fatalf("no column %s", name)
		return -1
	}
	text := func(v string) *string { return &v }
	// Rows 5, 12, 19 and 26 (ids 6, 13, 20, 27): a different column each, in one save.
	before, beforeOther := table(t, s, orders), table(t, s, bystander)
	edits := []RowEdit{
		{Key: keyOf(r, 5), Old: map[string]*string{"city": r.Rows[5][col("city")]}, Values: map[string]*string{"city": text("Goa")}},
		{Key: keyOf(r, 12), Old: map[string]*string{"amount": r.Rows[12][col("amount")]}, Values: map[string]*string{"amount": text("999.00")}},
		{Key: keyOf(r, 19), Old: map[string]*string{"paid": r.Rows[19][col("paid")]}, Values: map[string]*string{"paid": text("f")}}, // id 20 is paid: unpaid now
		{Key: keyOf(r, 26), Old: map[string]*string{"city": r.Rows[26][col("city")]}, Values: map[string]*string{"city": nil}},
	}
	if saved := save(t, s, r.Target, edits...); len(saved) != 4 {
		t.Fatalf("saved %d rows", len(saved))
	}
	after := table(t, s, orders)
	changed := diffRows(before, after)
	if want := []int{5, 12, 19, 26}; !slices.Equal(changed, want) {
		t.Fatalf("rows changed by the save: %v (0-based); want %v\nbefore %v\nafter  %v", changed, want, before, after)
	}
	if !strings.HasPrefix(after[5], "6|Goa|") || !strings.Contains(after[12], "|Mumbai|{99900 ") || !strings.HasSuffix(after[19], "|false") || !strings.HasPrefix(after[26], "27|NULL|") {
		t.Fatalf("the saved rows: %q %q %q %q", after[5], after[12], after[19], after[26])
	}
	if other := table(t, s, bystander); !slices.Equal(other, beforeOther) {
		t.Fatalf("the other table changed: %v", other)
	}

	// Delete 4 rows at once (ids 3, 4, 17, 30): exactly those go.
	r = editable(t, query(t, s, orders), "pk", "id")
	before = table(t, s, orders)
	n, err := s.DeleteRows(t.Context(), *r.Target, []RowDelete{rowDelete(r, 2), rowDelete(r, 3), rowDelete(r, 16), rowDelete(r, 29)})
	if err != nil || n != 4 {
		t.Fatalf("deleted %d, %v", n, err)
	}
	after = table(t, s, orders)
	if len(after) != 26 {
		t.Fatalf("%d rows left; want 26", len(after))
	}
	gone := map[string]bool{}
	for _, row := range before {
		gone[row] = true
	}
	for _, row := range after {
		if !gone[row] {
			t.Fatalf("a row that stayed is not as it was: %q", row)
		}
		delete(gone, row)
	}
	var goneIDs []string
	for row := range gone {
		goneIDs = append(goneIDs, strings.SplitN(row, "|", 2)[0])
	}
	slices.Sort(goneIDs)
	if !slices.Equal(goneIDs, []string{"17", "3", "30", "4"}) {
		t.Fatalf("rows gone: %v; want 3, 4, 17, 30", goneIDs)
	}
	if other := table(t, s, bystander); !slices.Equal(other, beforeOther) {
		t.Fatalf("the other table changed: %v", other)
	}

	// No primary key, identical rows: delete 2 of the 3 'a' rows and 1 of
	// the 2 'b' rows picked by position; the third 'a', the other 'b' and 'c' stay.
	rn := editable(t, query(t, s, nokey), "ctid", "ctid", "md5")
	n, err = s.DeleteRows(t.Context(), *rn.Target, []RowDelete{rowDelete(rn, 0), rowDelete(rn, 2), rowDelete(rn, 4)})
	if err != nil || n != 3 {
		t.Fatalf("no-key delete: %d, %v", n, err)
	}
	assertRows(t, s, nokey, "a|1", "b|2", "c|3")
	// And an edit of 2 of 3 identical rows changes 2, not 3.
	exec(t, s, "INSERT INTO "+testSchema+".nokey VALUES ('a', 1), ('a', 1)")
	rn = editable(t, query(t, s, "SELECT status, n FROM "+testSchema+".nokey WHERE status = 'a'"), "ctid", "ctid", "md5")
	save(t, s, rn.Target,
		RowEdit{Key: keyOf(rn, 0), Old: map[string]*string{"n": text("1")}, Values: map[string]*string{"n": text("7")}},
		RowEdit{Key: keyOf(rn, 2), Old: map[string]*string{"n": text("1")}, Values: map[string]*string{"n": text("7")}})
	assertRows(t, s, nokey, "a|1", "a|7", "a|7", "b|2", "c|3")
}

// diffRows is the indexes of rows that differ between two listings of the
// same length.
func diffRows(before, after []string) []int {
	var out []int
	for i := range before {
		if i >= len(after) || before[i] != after[i] {
			out = append(out, i)
		}
	}
	return out
}
