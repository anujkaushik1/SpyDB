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
