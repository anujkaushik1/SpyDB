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
