package main

import (
	"slices"
	"strings"
	"testing"
)

// Ways a save once could land on a row other than the one on screen, found
// by an adversarial review of the edit matrix. Each test reproduces one and
// checks it's now refused (read-only, or the save fails and writes nothing).

// UPDATE … FROM with RETURNING * returns the FROM tables' columns too, after
// the updated table's; with the table joined to itself they looked like the
// updated row's own, so editing the boss's dept saved it on the employee.
// Every column still shows, but only the updated row's own are editable.
func TestWrongRowUpdateFromJoin(t *testing.T) {
	s := editFixture(t,
		`CREATE TABLE `+testSchema+`.emp (id int PRIMARY KEY, name text, boss int, gone int, dept text)`,
		`ALTER TABLE `+testSchema+`.emp DROP COLUMN gone`,
		`INSERT INTO `+testSchema+`.emp VALUES (1, 'Alice', NULL, 'eng'), (2, 'Bob', 1, 'sales'), (3, 'Carol', 1, 'ops')`,
		`CREATE TABLE `+testSchema+`.depts (code text, title text)`,
		`INSERT INTO `+testSchema+`.depts VALUES ('ops', 'Operations')`)
	e := testSchema + ".emp"
	own := []string{"id", "name", "boss", "dept"}
	for _, c := range []struct {
		sql   string
		other int // the FROM tables' columns after the updated row's
	}{
		{`UPDATE ` + e + ` e SET dept = b.dept FROM ` + e + ` b WHERE e.boss = b.id AND e.id = 3`, 4},
		{`UPDATE ` + e + ` AS e SET name = 'Bobby' FROM (SELECT * FROM ` + e + `) b WHERE e.boss = b.id AND e.id = 2`, 4},
		{`UPDATE ` + e + ` SET boss = 2 FROM ` + testSchema + `.depts d, ` + e + ` b2 WHERE ` + e + `.dept = d.code AND b2.id = 2`, 6},
	} {
		r := editable(t, query(t, s, c.sql), "pk", "id")
		var sources []string
		for _, col := range r.Columns {
			sources = append(sources, col.Source)
		}
		if want := append(slices.Clone(own), make([]string, c.other)...); !slices.Equal(sources, want) {
			t.Fatalf("%s: editable columns %q, want only the updated row's %q", c.sql, sources, want)
		}
		// Saving the updated row's own cell changes that row only.
		before := table(t, s, `SELECT id, name, boss, dept FROM `+e+` ORDER BY id`)
		updated := *r.Rows[0][0]
		v := "edited"
		if _, err := s.SaveRows(t.Context(), *r.Target, []RowEdit{{Key: keyOf(r, 0), Old: map[string]*string{"dept": r.Rows[0][3]}, Values: map[string]*string{"dept": &v}}}); err != nil {
			t.Fatal(err)
		}
		after := table(t, s, `SELECT id, name, boss, dept FROM `+e+` ORDER BY id`)
		for i := range before {
			if strings.HasPrefix(after[i], updated+"|") != (after[i] != before[i]) {
				t.Fatalf("%s: rows went from %v to %v; only %s may change", c.sql, before, after, updated)
			}
		}
		exec(t, s, `UPDATE `+e+` SET dept = 'ops' WHERE id = `+updated)
	}
}

// Rows an UPDATE returned in a run that rolled it back (ROLLBACK, ROLLBACK
// TO SAVEPOINT) show values that never existed: their keys can name other
// rows. Here pos 1 is shown as Blog, but after the rollback it's Home.
func TestWrongRowRolledBackReturning(t *testing.T) {
	s := editFixture(t,
		`CREATE TABLE `+testSchema+`.menu (pos int PRIMARY KEY, name text, visible bool)`,
		`INSERT INTO `+testSchema+`.menu VALUES (1, 'Home', true), (2, 'Blog', true)`)
	m := testSchema + ".menu"
	swap := `UPDATE ` + m + ` SET pos = -1 WHERE pos = 1; UPDATE ` + m + ` SET pos = 1 WHERE pos = 2; UPDATE ` + m + ` SET pos = 2 WHERE pos = -1;`
	for _, sql := range []string{
		`BEGIN; ` + swap + ` ROLLBACK`,
		`BEGIN; SAVEPOINT s; ` + swap + ` ROLLBACK TO s; COMMIT`,
		`BEGIN; ` + swap + ` ABORT`,
	} {
		result, err := s.RunQuery(t.Context(), "tab", sql)
		if err != nil {
			t.Fatal(err)
		}
		for i, r := range result.Results {
			if r.Target != nil {
				t.Errorf("%s: result %d (%s) is editable after a rollback", sql, i+1, r.Command)
			}
		}
	}
	// Committed in the same run: still editable.
	result, err := s.RunQuery(t.Context(), "tab", `BEGIN; UPDATE `+m+` SET visible = false WHERE pos = 2; COMMIT`)
	if err != nil {
		t.Fatal(err)
	}
	editable(t, result.Results[1], "pk", "pos")
}

// A SELECT inside a transaction that has written shows its uncommitted rows;
// after a rollback (another tab, a timeout) their keys can name other rows.
// Before it has written anything, what it shows is committed: still editable.
func TestWrongRowReadInWritingTransaction(t *testing.T) {
	s := editFixture(t,
		`CREATE TABLE `+testSchema+`.menu (pos int PRIMARY KEY, name text, visible bool)`,
		`INSERT INTO `+testSchema+`.menu VALUES (1, 'Home', true), (2, 'Blog', true)`)
	m := testSchema + ".menu"
	query(t, s, "BEGIN")
	clean := editable(t, query(t, s, `SELECT * FROM `+m+` ORDER BY pos`), "pk", "pos")
	if _, err := s.RunQuery(t.Context(), "tab", `UPDATE `+m+` SET pos = -1 WHERE pos = 1; UPDATE `+m+` SET pos = 1 WHERE pos = 2; UPDATE `+m+` SET pos = 2 WHERE pos = -1`); err != nil {
		t.Fatal(err)
	}
	if r := query(t, s, `SELECT * FROM `+m+` ORDER BY pos`); r.Target != nil {
		t.Errorf("rows read after the transaction wrote are editable (pos 1 shows %s)", *r.Rows[0][1])
	}
	query(t, s, "ROLLBACK")
	// The rows read before the writes were committed rows: they save right.
	hidden := "f"
	if _, err := s.SaveRows(t.Context(), *clean.Target, []RowEdit{{Key: keyOf(clean, 1), Old: map[string]*string{"visible": clean.Rows[1][2]}, Values: map[string]*string{"visible": &hidden}}}); err != nil {
		t.Fatal(err)
	}
	if got := table(t, s, `SELECT pos, name, visible FROM `+m+` ORDER BY pos`); strings.Join(got, ";") != "1|Home|true;2|Blog|false" {
		t.Fatalf("got %v", got)
	}
}

// Keys whose text isn't exact: two rows can print the same, and the save
// then finds the other one. Floats rounded by extra_float_digits (also behind
// a domain or in an array), and timestamptz named by an ambiguous zone
// abbreviation (Moscow's 2014 change: 21:30Z and 22:30Z both read 01:30 MSK).
func TestWrongRowInexactKeys(t *testing.T) {
	s := editFixture(t,
		`CREATE DOMAIN `+testSchema+`.f AS float8`,
		`CREATE TABLE `+testSchema+`.fd (id `+testSchema+`.f PRIMARY KEY, label text, status text)`,
		`INSERT INTO `+testSchema+`.fd VALUES (1.1, 'a', 'x'), (1.1000000000000003, 'b', 'x')`,
		`CREATE TABLE `+testSchema+`.fa (id float8[] PRIMARY KEY, label text, status text)`,
		`INSERT INTO `+testSchema+`.fa VALUES ('{1.1}', 'a', 'x'), ('{1.1000000000000003}', 'b', 'x')`,
		`CREATE TABLE `+testSchema+`.ts (at timestamptz PRIMARY KEY, label text, status text)`,
		`INSERT INTO `+testSchema+`.ts VALUES ('2014-10-25 21:30:00+00', 'a', 'x'), ('2014-10-25 22:30:00+00', 'b', 'x')`)
	query(t, s, "SET extra_float_digits = 0")
	for _, table := range []string{"fd", "fa"} {
		if r := query(t, s, `SELECT id, status FROM `+testSchema+`.`+table+` WHERE label = 'b'`); r.Target != nil {
			t.Errorf("%s: a float key shown rounded (%s) is editable", table, *r.Rows[0][0])
		}
	}
	query(t, s, "RESET extra_float_digits")
	for _, table := range []string{"fd", "fa"} {
		editable(t, query(t, s, `SELECT id, status FROM `+testSchema+`.`+table+` WHERE label = 'b'`), "pk", "id")
	}

	query(t, s, "SET TimeZone = 'Europe/Moscow'")
	query(t, s, "SET DateStyle = 'SQL, DMY'")
	if r := query(t, s, `SELECT at, status FROM `+testSchema+`.ts WHERE label = 'b'`); r.Target != nil {
		t.Errorf("a timestamptz key shown with a zone name (%s) is editable", *r.Rows[0][0])
	}
	query(t, s, "SET DateStyle = 'ISO, DMY'")
	editable(t, query(t, s, `SELECT at, status FROM `+testSchema+`.ts WHERE label = 'b'`), "pk", "at")
	query(t, s, "RESET ALL")
}

// Table tabs read with the pool's own settings, which can round floats too
// (extra_float_digits = 0 is the default before Postgres 12, or set for a
// role or database).
func TestWrongRowInexactKeysTableTab(t *testing.T) {
	t.Setenv("PGOPTIONS", "-c extra_float_digits=0")
	s := editFixture(t,
		`CREATE TABLE `+testSchema+`.fk (id float8 PRIMARY KEY, label text, status text)`,
		`INSERT INTO `+testSchema+`.fk VALUES (1.1, 'a', 'x'), (1.1000000000000003, 'b', 'x')`)
	page, err := s.TableData(t.Context(), TableQuery{Schema: testSchema, Table: "fk", PageSize: 100,
		Filters: []TableFilter{{Column: "label", Operator: "eq", Value: "b"}}})
	if err != nil {
		t.Fatal(err)
	}
	if page.Target != nil {
		t.Errorf("a float key shown rounded (%s) is editable in a table tab", *page.Rows[0][0])
	}
}
