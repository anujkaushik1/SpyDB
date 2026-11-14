package postgres

import (
	"strings"
	"testing"
)

// A table tab's saves and deletes run with the search_path SET in the query
// editor, as a trigger written for it needs. Against SPYDB_TEST_DATABASE.

func editorRun(t *testing.T, s *DBService, sql string) {
	t.Helper()
	if _, err := runIn(t, s, sql); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// setCell is row i of p with column changed to value, as the grid saves it.
func setCell(p TablePage, i int, column, value string) RowEdit {
	row := sent(p, i)
	return RowEdit{Key: row.Key, Old: map[string]*string{column: row.Old[column]}, Values: map[string]*string{column: str(value)}}
}

func TestTableTabUsesEditorSearchPath(t *testing.T) {
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".outbox (op text, id int)",
		"CREATE TABLE "+testSchema+".kyc (id int PRIMARY KEY, v text)",
		"INSERT INTO "+testSchema+".kyc VALUES (1, 'a'), (2, 'b'), (3, 'c')",
		// outbox without its schema: found only through search_path.
		`CREATE FUNCTION `+testSchema+`.to_outbox() RETURNS trigger LANGUAGE plpgsql AS $$
		 BEGIN INSERT INTO outbox VALUES (TG_OP, coalesce(NEW.id, OLD.id)); RETURN NULL; END $$`,
		"CREATE TRIGGER to_outbox AFTER UPDATE OR DELETE ON "+testSchema+".kyc FOR EACH ROW EXECUTE FUNCTION "+testSchema+".to_outbox()")
	kyc := "SELECT id, v FROM " + testSchema + ".kyc ORDER BY id"
	outbox := "SELECT op, id FROM " + testSchema + ".outbox ORDER BY op, id"
	p := tabPage(t, s, "kyc")
	row := func(id string) int { return pageRow(t, p, "id", id) }
	missing := `"outbox" does not exist`

	// As before: the pool's own search_path doesn't have outbox's schema.
	refuse(t, s, p.Target, kyc, missing, setCell(p, row("1"), "v", "x"))

	// SET in the editor: the same save, from the same page, now works.
	editorRun(t, s, "SET search_path TO "+testSchema+", public")
	save(t, s, p.Target, setCell(p, row("1"), "v", "x"))
	if n, err := s.DeleteRows(t.Context(), *p.Target, []RowDelete{sent(p, row("2"))}); n != 1 || err != nil {
		t.Fatalf("delete: %d, %v", n, err)
	}
	assertRows(t, s, kyc, "1|x", "3|c")
	assertRows(t, s, outbox, "DELETE|2", "UPDATE|1")

	// Inside a transaction a new SET isn't taken up: it may be rolled back.
	editorRun(t, s, "BEGIN")
	editorRun(t, s, "RESET search_path")
	save(t, s, p.Target, setCell(p, row("3"), "v", "y"))
	editorRun(t, s, "ROLLBACK")
	p = tabPage(t, s, "kyc")
	save(t, s, p.Target, setCell(p, row("3"), "v", "y2"))

	// RESET in the editor: back to the pool's own. set_config counts as SET.
	editorRun(t, s, "RESET search_path")
	p = tabPage(t, s, "kyc")
	refuse(t, s, p.Target, kyc, missing, setCell(p, row("1"), "v", "x2"))
	editorRun(t, s, "SELECT pg_catalog.set_config('search_path', '"+testSchema+"', false)")
	save(t, s, p.Target, setCell(p, row("1"), "v", "x2"))

	// The editor's connection lost: the new one hasn't the SET, and saves don't either.
	cutEditor(t, s)
	if _, err := runIn(t, s, "SELECT 1"); err == nil || !strings.Contains(err.Error(), "is gone") {
		t.Fatalf("run after the cut: %v", err)
	}
	p = tabPage(t, s, "kyc")
	refuse(t, s, p.Target, kyc, missing, setCell(p, row("1"), "v", "x3"))
	assertRows(t, s, kyc, "1|x2", "3|y2")
}

// A regclass key shows relative to search_path: read without the editor's
// and saved with it, "pg_type" would find another table's row. Refused.
func TestTableTabEditorSearchPathRegclass(t *testing.T) {
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".pg_type (x int)",
		"CREATE TABLE "+testSchema+".tblcfg (tbl regclass PRIMARY KEY, note text)",
		"INSERT INTO "+testSchema+".tblcfg VALUES ('"+testSchema+".pg_type', 'same'), ('pg_catalog.pg_type', 'same')",
		// Inside an array, not the key: still refused (an old value would compare wrongly).
		"CREATE TABLE "+testSchema+".typed (id int PRIMARY KEY, types regtype[])",
		"INSERT INTO "+testSchema+".typed VALUES (1, '{int4}')")
	all := "SELECT tbl::oid::regclass::text, note FROM " + testSchema + ".tblcfg ORDER BY 1"
	p := tabPage(t, s, "tblcfg")
	i := pageRow(t, p, "tbl", "pg_type") // pg_catalog.pg_type, as the pool shows it
	edit := setCell(p, i, "note", "edited")

	editorRun(t, s, "SET search_path TO "+testSchema+", pg_catalog")
	refuse(t, s, p.Target, all, "a type like regclass", edit)
	refuseDelete(t, s, p.Target, all, "a type like regclass", sent(p, i))
	typed := tabPage(t, s, "typed")
	refuse(t, s, typed.Target, "SELECT * FROM "+testSchema+".typed", "a type like regclass",
		setCell(typed, pageRow(t, typed, "id", "1"), "types", "{text}"))

	// Without a SET, saves read the key as it was shown.
	editorRun(t, s, "RESET search_path")
	save(t, s, p.Target, edit)
	assertRows(t, s, all, "pg_type|edited", testSchema+".pg_type|same")
}
