package postgres

// Adversarial integration tests for editing query results (editable.go,
// edits.go, RunQuery/FetchRows, TableData, ExportResult). Every test works in
// its own schema, spydb_adv, dropped at the start and in cleanup; roles it
// creates are named spydb_adv_*.
//
// Run: SPYDB_TEST_DATABASE=anuj go test -run TestAdv -count=1 .
//
// Tests whose comment starts with "BUG:" fail on purpose: they demonstrate a
// confirmed problem. Everything else is a regression test that passes.

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ---------------------------------------------------------------- helpers

func advSetup(t *testing.T) *DBService {
	t.Helper()
	s := connectForTest(t)
	advExec(t, s, `DROP SCHEMA IF EXISTS spydb_adv CASCADE`)
	advExec(t, s, `CREATE SCHEMA spydb_adv`)
	t.Cleanup(func() {
		s.mu.Lock()
		pool := s.pool
		s.mu.Unlock()
		if pool == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS spydb_adv CASCADE`); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})
	return s
}

// advRole creates a NOLOGIN role (spydb_adv_ prefix) and drops it in cleanup.
// Register it after advSetup, so it is dropped before the schema.
func advRole(t *testing.T, s *DBService, name string) {
	t.Helper()
	if !strings.HasPrefix(name, "spydb_adv_") {
		t.Fatalf("role %q must start with spydb_adv_", name)
	}
	drop := fmt.Sprintf(`DO $$ BEGIN IF EXISTS (SELECT FROM pg_roles WHERE rolname = '%[1]s') THEN
		EXECUTE 'DROP OWNED BY %[1]s'; EXECUTE 'DROP ROLE %[1]s'; END IF; END $$`, name)
	advExec(t, s, drop)
	advExec(t, s, fmt.Sprintf(`CREATE ROLE %s NOLOGIN`, name))
	t.Cleanup(func() {
		s.mu.Lock()
		pool := s.pool
		s.mu.Unlock()
		if pool == nil {
			return
		}
		if _, err := pool.Exec(context.Background(), drop); err != nil {
			t.Logf("cleanup role %s: %v", name, err)
		}
	})
}

func advExec(t *testing.T, s *DBService, sql string, args ...any) {
	t.Helper()
	if _, err := s.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// advText returns the single value sql returns, as text (nil for NULL).
func advText(t *testing.T, s *DBService, sql string, args ...any) *string {
	t.Helper()
	rows, err := s.pool.Query(context.Background(), sql, append([]any{pgx.QueryResultFormats{pgx.TextFormatCode}}, args...)...)
	if err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	defer rows.Close()
	var out *string
	n := 0
	for rows.Next() {
		n++
		if v := rows.RawValues()[0]; v != nil {
			text := string(v)
			out = &text
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	if n != 1 {
		t.Fatalf("query %q returned %d rows, want 1", sql, n)
	}
	return out
}

func advStr(t *testing.T, s *DBService, sql string, args ...any) string {
	t.Helper()
	v := advText(t, s, sql, args...)
	if v == nil {
		return "<NULL>"
	}
	return *v
}

func p(v string) *string { return &v }

func advShow(v *string) string {
	if v == nil {
		return "NULL"
	}
	return fmt.Sprintf("%q", *v)
}

func advEq(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

// advSQL runs statements (SET, BEGIN, ...) in the editor session.
func advSQL(t *testing.T, s *DBService, sql string) {
	t.Helper()
	if _, err := s.RunQuery(context.Background(), "adv", sql); err != nil {
		t.Fatalf("RunQuery %q: %v", sql, err)
	}
}

// advRun runs a query in the editor and checks the result's shape.
func advRun(t *testing.T, s *DBService, sql string) StatementResult {
	t.Helper()
	res, err := s.RunQuery(context.Background(), "adv", sql)
	if err != nil {
		t.Fatalf("RunQuery %q: %v", sql, err)
	}
	if len(res.Results) == 0 {
		t.Fatalf("RunQuery %q: no results", sql)
	}
	r := res.Results[len(res.Results)-1]
	advCheckResult(t, sql, r, true)
	return r
}

// advCheckResult checks hidden key columns never show and keys line up.
func advCheckResult(t *testing.T, sql string, r StatementResult, noSpydbNames bool) {
	t.Helper()
	if noSpydbNames {
		for _, c := range r.Columns {
			if strings.HasPrefix(c.Name, "__spydb") {
				t.Errorf("%q: hidden column %q is shown", sql, c.Name)
			}
		}
	}
	for i, row := range r.Rows {
		if len(row) != len(r.Columns) {
			t.Errorf("%q: row %d has %d values for %d columns", sql, i, len(row), len(r.Columns))
		}
	}
	if r.Target == nil {
		if len(r.Keys) != 0 {
			t.Errorf("%q: read-only result has %d keys", sql, len(r.Keys))
		}
		for _, c := range r.Columns {
			if c.Source != "" {
				t.Errorf("%q: read-only result has column %q with Source %q", sql, c.Name, c.Source)
			}
		}
		return
	}
	if r.ReadOnly != "" {
		t.Errorf("%q: has a target and readOnly %q", sql, r.ReadOnly)
	}
	if len(r.Keys) != len(r.Rows) {
		t.Errorf("%q: %d keys for %d rows", sql, len(r.Keys), len(r.Rows))
	}
	for i, k := range r.Keys {
		if len(k) != len(r.Target.Key) {
			t.Errorf("%q: key %d has %d values, target key %v", sql, i, len(k), r.Target.Key)
		}
	}
}

func advKey(r StatementResult, row int) map[string]*string {
	m := map[string]*string{}
	for j, name := range r.Target.Key {
		m[name] = r.Keys[row][j]
	}
	return m
}

func advCol(t *testing.T, r StatementResult, source string) int {
	t.Helper()
	for i, c := range r.Columns {
		if c.Source == source {
			return i
		}
	}
	t.Fatalf("no column with source %q in %v", source, r.Columns)
	return -1
}

// advRowWhere returns the first row whose column `source` shows value.
func advRowWhere(t *testing.T, r StatementResult, source, value string) int {
	t.Helper()
	c := advCol(t, r, source)
	for i, row := range r.Rows {
		if row[c] != nil && *row[c] == value {
			return i
		}
	}
	t.Fatalf("no row with %s = %q", source, value)
	return -1
}

// advEdit is what the grid sends for one row: its key, and for each edited
// column the value shown (old) and the new value.
func advEdit(t *testing.T, r StatementResult, row int, values map[string]*string) RowEdit {
	t.Helper()
	old := map[string]*string{}
	for c := range values {
		old[c] = r.Rows[row][advCol(t, r, c)]
	}
	return RowEdit{Key: advKey(r, row), Old: old, Values: values}
}

func advSave(t *testing.T, s *DBService, r StatementResult, edits ...RowEdit) ([]SavedRow, error) {
	t.Helper()
	if r.Target == nil {
		t.Fatalf("result is read-only (%s); can't save", r.ReadOnly)
	}
	return s.SaveRows(context.Background(), *r.Target, edits)
}

// advVerify is an independent oracle: for every editable row, its key must
// find exactly one table row (compared as text in the target's session, not
// by casting), and that row must hold every value shown in a Source column.
func advVerify(t *testing.T, s *DBService, label string, r StatementResult) {
	t.Helper()
	if r.Target == nil {
		return
	}
	ctx := context.Background()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	for name, v := range r.Target.Session {
		if _, err := tx.Exec(ctx, "SELECT pg_catalog.set_config($1, $2, true)", name, v); err != nil {
			t.Fatal(err)
		}
	}
	table := pgx.Identifier{r.Target.Schema, r.Target.Table}.Sanitize()
	var where []string
	if r.Target.Kind == "ctid" {
		table = "ONLY " + table
		where = []string{"x.ctid = $1::text::tid", rowHash("x") + " = $2::text"}
	} else {
		for j, k := range r.Target.Key {
			where = append(where, fmt.Sprintf("pg_catalog.format('%%s', x.%s) = $%d::text", pgx.Identifier{k}.Sanitize(), j+1))
		}
	}
	var cols []int
	var exprs []string
	for i, c := range r.Columns {
		if c.Source == "" {
			continue
		}
		cols = append(cols, i)
		q := "x." + pgx.Identifier{c.Source}.Sanitize()
		exprs = append(exprs, fmt.Sprintf("CASE WHEN pg_catalog.num_nulls(%s) = 1 THEN NULL ELSE pg_catalog.format('%%s', %s) END", q, q))
	}
	if len(exprs) == 0 {
		t.Errorf("%s: editable, but no column has a Source", label)
		return
	}
	sql := fmt.Sprintf("SELECT %s FROM %s AS x WHERE %s", strings.Join(exprs, ", "), table, strings.Join(where, " AND "))
	for i, key := range r.Keys {
		args := []any{pgx.QueryResultFormats{pgx.TextFormatCode}}
		for _, v := range key {
			args = append(args, *v)
		}
		rows, err := tx.Query(ctx, sql, args...)
		if err != nil {
			t.Errorf("%s: oracle query failed: %v", label, err)
			return
		}
		n := 0
		for rows.Next() {
			n++
			vals := rows.RawValues()
			for j, ci := range cols {
				var got *string
				if vals[j] != nil {
					text := string(vals[j])
					got = &text
				}
				if !advEq(got, r.Rows[i][ci]) {
					t.Errorf("%s: row %d column %s shows %s, but the table row its key names holds %s",
						label, i, r.Columns[ci].Source, advShow(r.Rows[i][ci]), advShow(got))
				}
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Errorf("%s: oracle: %v", label, err)
			return
		}
		if n != 1 {
			t.Errorf("%s: row %d's key matches %d table rows, want exactly 1", label, i, n)
		}
	}
}

// ---------------------------------------------------- wrong-row: no PK

// Identical rows in a table without a primary key: each gets its own key,
// and saving one changes exactly that physical row.
func TestAdvDuplicateRowsNoPK(t *testing.T) {
	s := advSetup(t)
	advExec(t, s, `CREATE TABLE spydb_adv.dup (a int, b text);
		INSERT INTO spydb_adv.dup VALUES (1,'x'),(1,'x'),(1,'x'),(2,'y')`)
	r := advRun(t, s, `SELECT a, b FROM spydb_adv.dup ORDER BY ctid`)
	if r.Target == nil || r.Target.Kind != "ctid" {
		t.Fatalf("want a ctid target, got %+v (%s)", r.Target, r.ReadOnly)
	}
	advVerify(t, s, "dup", r)
	saved, err := advSave(t, s, r, advEdit(t, r, 1, map[string]*string{"b": p("changed")}))
	if err != nil {
		t.Fatal(err)
	}
	if got := advStr(t, s, `SELECT count(*) FROM spydb_adv.dup WHERE b = 'changed'`); got != "1" {
		t.Fatalf("%s rows changed, want 1", got)
	}
	for _, i := range []int{0, 2} {
		if got := advStr(t, s, `SELECT b FROM spydb_adv.dup WHERE ctid = $1::text::tid`, *r.Keys[i][0]); got != "x" {
			t.Errorf("row %d (untouched duplicate) now %q", i, got)
		}
	}
	if got := advStr(t, s, `SELECT count(*) FROM spydb_adv.dup WHERE ctid = $1::text::tid`, *r.Keys[1][0]); got != "0" {
		t.Errorf("the edited row's old address still holds a live row")
	}
	if got := advStr(t, s, `SELECT b FROM spydb_adv.dup WHERE ctid = $1::text::tid`, *saved[0].Key["ctid"]); got != "changed" {
		t.Errorf("SavedRow.Key doesn't point at the saved row: %q", got)
	}
}

// A row deleted, VACUUMed, and its address reused by a new row: different
// content must be refused; identical content is indistinguishable by design.
func TestAdvCtidReuseAfterVacuum(t *testing.T) {
	for _, tc := range []struct {
		name, intruder string
		wantSaved      bool
	}{
		{"different content", "intruder", false},
		{"identical content", "target", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := advSetup(t)
			other := connectForTest(t)
			advExec(t, s, `CREATE TABLE spydb_adv.reuse (v text) WITH (autovacuum_enabled = off);
				INSERT INTO spydb_adv.reuse VALUES ('keep'), ('target'), ('other')`)
			r := advRun(t, s, `SELECT v FROM spydb_adv.reuse ORDER BY ctid`)
			row := advRowWhere(t, r, "v", "target")
			addr := *r.Keys[row][0]

			advExec(t, other, `DELETE FROM spydb_adv.reuse WHERE v = 'target'`)
			advExec(t, other, `VACUUM spydb_adv.reuse`)
			advExec(t, other, `INSERT INTO spydb_adv.reuse VALUES ($1)`, tc.intruder)
			at := advStr(t, other, `SELECT ctid::text FROM spydb_adv.reuse WHERE v = $1 ORDER BY ctid DESC LIMIT 1`, tc.intruder)
			if at != addr {
				t.Skipf("the new row landed at %s, not the freed %s (another session may hold back VACUUM)", at, addr)
			}

			_, err := advSave(t, s, r, advEdit(t, r, row, map[string]*string{"v": p("edited")}))
			if tc.wantSaved {
				if err != nil {
					t.Logf("identical content at the reused address: refused (%v)", err)
				} else {
					t.Logf("identical content at the reused address: saved to the new identical row (by design: identical rows are indistinguishable)")
				}
				return
			}
			if err == nil {
				t.Fatalf("saved onto a different row that reused the address %s", addr)
			}
			if got := advStr(t, s, `SELECT count(*) FROM spydb_adv.reuse WHERE v = 'intruder'`); got != "1" {
				t.Errorf("intruder row changed")
			}
		})
	}
}

// VACUUM FULL moves rows: another row may now sit at the loaded address.
func TestAdvVacuumFullMovesRows(t *testing.T) {
	s := advSetup(t)
	other := connectForTest(t)
	advExec(t, s, `CREATE TABLE spydb_adv.vf (v text, n int);
		INSERT INTO spydb_adv.vf VALUES ('v1',1),('v2',2),('v3',3),('v4',4),('v5',5)`)
	r := advRun(t, s, `SELECT v, n FROM spydb_adv.vf ORDER BY n`)
	row := advRowWhere(t, r, "v", "v3")
	addr := *r.Keys[row][0]
	advExec(t, other, `DELETE FROM spydb_adv.vf WHERE n <= 2`)
	advExec(t, other, `VACUUM FULL spydb_adv.vf`)
	t.Logf("after VACUUM FULL, %s holds %s", addr, advStr(t, other, `SELECT coalesce((SELECT v FROM spydb_adv.vf WHERE ctid = $1::text::tid), '<nothing>')`, addr))
	if _, err := advSave(t, s, r, advEdit(t, r, row, map[string]*string{"n": p("33")})); err == nil {
		t.Fatal("saved after VACUUM FULL moved rows")
	}
	if got := advStr(t, s, `SELECT string_agg(v || '=' || n, ',' ORDER BY n) FROM spydb_adv.vf`); got != "v3=3,v4=4,v5=5" {
		t.Errorf("table changed: %s", got)
	}
	// After a reload, saving works again.
	r = advRun(t, s, `SELECT v, n FROM spydb_adv.vf ORDER BY n`)
	if _, err := advSave(t, s, r, advEdit(t, r, advRowWhere(t, r, "v", "v3"), map[string]*string{"n": p("33")})); err != nil {
		t.Fatal(err)
	}
	if got := advStr(t, s, `SELECT string_agg(v || '=' || n, ',' ORDER BY v) FROM spydb_adv.vf`); got != "v3=33,v4=4,v5=5" {
		t.Errorf("after reload+save: %s", got)
	}
}

// --------------------------------------------- concurrent changes, PK + ctid

func TestAdvConcurrentChanges(t *testing.T) {
	s := advSetup(t)
	other := connectForTest(t)
	advExec(t, s, `CREATE TABLE spydb_adv.cpk (id int PRIMARY KEY, a text, b text);
		CREATE TABLE spydb_adv.cno (id int, a text, b text);
		INSERT INTO spydb_adv.cpk VALUES (1,'a1','b1'),(2,'a2','b2'),(3,'a3','b3');
		INSERT INTO spydb_adv.cno SELECT * FROM spydb_adv.cpk`)
	rp := advRun(t, s, `SELECT id, a FROM spydb_adv.cpk ORDER BY id`)
	rn := advRun(t, s, `SELECT id, a FROM spydb_adv.cno ORDER BY id`)
	if rp.Target == nil || rp.Target.Kind != "pk" || rn.Target == nil || rn.Target.Kind != "ctid" {
		t.Fatalf("targets: %+v %+v", rp.Target, rn.Target)
	}

	// A column that isn't shown changes: ctid refuses, pk saves.
	advExec(t, other, `UPDATE spydb_adv.cpk SET b = 'B1' WHERE id = 1; UPDATE spydb_adv.cno SET b = 'B1' WHERE id = 1`)
	if _, err := advSave(t, s, rp, advEdit(t, rp, 0, map[string]*string{"a": p("A1")})); err != nil {
		t.Errorf("pk: a change to a column not shown must not block the save: %v", err)
	}
	if _, err := advSave(t, s, rn, advEdit(t, rn, 0, map[string]*string{"a": p("A1")})); err == nil {
		t.Errorf("ctid: saved although the row changed since it was loaded")
	}

	// The edited column changes: both refuse.
	advExec(t, other, `UPDATE spydb_adv.cpk SET a = 'X2' WHERE id = 2; UPDATE spydb_adv.cno SET a = 'X2' WHERE id = 2`)
	for _, r := range []StatementResult{rp, rn} {
		if _, err := advSave(t, s, r, advEdit(t, r, 1, map[string]*string{"a": p("A2")})); err == nil {
			t.Errorf("%s: saved over someone else's change to the edited cell", r.Target.Kind)
		}
	}

	// The row is deleted: both refuse.
	advExec(t, other, `DELETE FROM spydb_adv.cpk WHERE id = 3; DELETE FROM spydb_adv.cno WHERE id = 3`)
	for _, r := range []StatementResult{rp, rn} {
		if _, err := advSave(t, s, r, advEdit(t, r, 2, map[string]*string{"a": p("A3")})); err == nil {
			t.Errorf("%s: saved a deleted row", r.Target.Kind)
		}
	}
	// A batch where one row fails saves nothing.
	if _, err := advSave(t, s, rp,
		advEdit(t, rp, 0, map[string]*string{"a": p("batch")}),
		advEdit(t, rp, 1, map[string]*string{"a": p("batch")})); err == nil {
		t.Errorf("batch with a stale row saved")
	}
	if got := advStr(t, s, `SELECT string_agg(a, ',' ORDER BY id) FROM spydb_adv.cpk`); got != "A1,X2" {
		t.Errorf("cpk now %s, want A1,X2", got)
	}
	if got := advStr(t, s, `SELECT string_agg(a || b, ',' ORDER BY id) FROM spydb_adv.cno`); got != "a1B1,X2b2" {
		t.Errorf("cno now %s", got)
	}
}

// PK values swapped between two rows between load and save. A pk key names
// whichever row now holds that key: the edited cell's old value is the only
// guard. Documented, not a failure (identity is the primary key).
func TestAdvPKSwap(t *testing.T) {
	s := advSetup(t)
	other := connectForTest(t)
	advExec(t, s, `CREATE TABLE spydb_adv.swap (id int PRIMARY KEY, name text, v text);
		INSERT INTO spydb_adv.swap VALUES (1,'one','same'),(2,'two','same')`)
	r := advRun(t, s, `SELECT id, name, v FROM spydb_adv.swap ORDER BY id`)
	advExec(t, other, `UPDATE spydb_adv.swap SET id = -1 WHERE id = 1; UPDATE spydb_adv.swap SET id = 1 WHERE id = 2; UPDATE spydb_adv.swap SET id = 2 WHERE id = -1`)
	// Editing name (which differs between the rows) is refused.
	if _, err := advSave(t, s, r, advEdit(t, r, 0, map[string]*string{"name": p("uno")})); err == nil {
		t.Errorf("saved name onto the row that took id 1")
	}
	// Editing v (same in both rows) lands on the row now holding id 1.
	if _, err := advSave(t, s, r, advEdit(t, r, 0, map[string]*string{"v": p("new")})); err != nil {
		t.Logf("v edit refused: %v", err)
	}
	t.Logf("after the swap, editing the row loaded as (1,'one') changed: %s",
		advStr(t, s, `SELECT string_agg(id || ':' || name || ':' || v, ' ' ORDER BY id) FROM spydb_adv.swap`))
}

// ------------------------------------------------------- composite PKs etc.

func TestAdvCompositePKPartlyShown(t *testing.T) {
	s := advSetup(t)
	advExec(t, s, `CREATE TABLE spydb_adv.cp (b int, a int, v text, PRIMARY KEY (a, b));
		INSERT INTO spydb_adv.cp VALUES (1,1,'x'),(2,1,'y'),(1,2,'z')`)
	for _, sql := range []string{
		`SELECT a, v FROM spydb_adv.cp ORDER BY a, b`,
		`SELECT b, v FROM spydb_adv.cp WHERE a = 1 ORDER BY b`,
		`SELECT v FROM spydb_adv.cp ORDER BY v`,
		`SELECT v, a AS b, b AS a FROM spydb_adv.cp ORDER BY v`,
	} {
		r := advRun(t, s, sql)
		if r.Target == nil || r.Target.Kind != "pk" {
			t.Fatalf("%s: %+v %s", sql, r.Target, r.ReadOnly)
		}
		advVerify(t, s, sql, r)
	}
	r := advRun(t, s, `SELECT v, a AS b, b AS a FROM spydb_adv.cp ORDER BY v`)
	row := advRowWhere(t, r, "v", "y") // a=1, b=2
	if _, err := advSave(t, s, r, advEdit(t, r, row, map[string]*string{"v": p("Y")})); err != nil {
		t.Fatal(err)
	}
	if got := advStr(t, s, `SELECT string_agg(a || '/' || b || '=' || v, ',' ORDER BY a, b) FROM spydb_adv.cp`); got != "1/1=x,1/2=Y,2/1=z" {
		t.Errorf("got %s", got)
	}
}

// Editing a visible PK cell, then the same row again with SavedRow.Key;
// and the same for a ctid row (whose address changes on every save).
func TestAdvEditTwiceWithSavedKey(t *testing.T) {
	s := advSetup(t)
	advExec(t, s, `CREATE TABLE spydb_adv.t2 (id int PRIMARY KEY, v text);
		CREATE TABLE spydb_adv.n2 (v text, w text);
		INSERT INTO spydb_adv.t2 VALUES (1,'a'),(2,'b');
		INSERT INTO spydb_adv.n2 VALUES ('a','w'),('a','w')`)
	r := advRun(t, s, `SELECT id, v FROM spydb_adv.t2 ORDER BY id`)
	saved, err := advSave(t, s, r, advEdit(t, r, 0, map[string]*string{"id": p("100"), "v": p("a2")}))
	if err != nil {
		t.Fatal(err)
	}
	if got := saved[0].Key["id"]; got == nil || *got != "100" {
		t.Fatalf("SavedRow.Key = %v", saved[0].Key)
	}
	second := RowEdit{Key: saved[0].Key, Old: map[string]*string{"v": saved[0].Values["v"]}, Values: map[string]*string{"v": p("a3")}}
	if _, err := advSave(t, s, r, second); err != nil {
		t.Fatalf("second edit with SavedRow.Key: %v", err)
	}
	// The original key no longer names a row.
	stale := advEdit(t, r, 0, map[string]*string{"v": p("zzz")})
	stale.Old["v"] = p("a3")
	if _, err := advSave(t, s, r, stale); err == nil {
		t.Errorf("the stale key id=1 saved")
	}
	if got := advStr(t, s, `SELECT string_agg(id || v, ',' ORDER BY id) FROM spydb_adv.t2`); got != "2b,100a3" {
		t.Errorf("got %s", got)
	}

	rn := advRun(t, s, `SELECT v FROM spydb_adv.n2 ORDER BY ctid`)
	saved, err = advSave(t, s, rn, advEdit(t, rn, 1, map[string]*string{"v": p("b")}))
	if err != nil {
		t.Fatal(err)
	}
	second = RowEdit{Key: saved[0].Key, Old: map[string]*string{"v": saved[0].Values["v"]}, Values: map[string]*string{"v": p("c")}}
	if _, err := advSave(t, s, rn, second); err != nil {
		t.Fatalf("second ctid edit with SavedRow.Key: %v", err)
	}
	stale = advEdit(t, rn, 1, map[string]*string{"v": p("zzz")})
	if _, err := advSave(t, s, rn, stale); err == nil {
		t.Errorf("a stale ctid key saved")
	}
	if got := advStr(t, s, `SELECT string_agg(v, ',' ORDER BY v) FROM spydb_adv.n2`); got != "a,c" {
		t.Errorf("got %s", got)
	}
}

func TestAdvPartitioned(t *testing.T) {
	s := advSetup(t)
	advExec(t, s, `CREATE TABLE spydb_adv.part (id int, region text, v text, PRIMARY KEY (id, region)) PARTITION BY LIST (region);
		CREATE TABLE spydb_adv.part_eu PARTITION OF spydb_adv.part FOR VALUES IN ('eu');
		CREATE TABLE spydb_adv.part_us PARTITION OF spydb_adv.part FOR VALUES IN ('us');
		INSERT INTO spydb_adv.part VALUES (1,'eu','a'),(1,'us','b'),(2,'eu','c');
		CREATE TABLE spydb_adv.partnopk (id int, region text) PARTITION BY LIST (region);
		CREATE TABLE spydb_adv.partnopk_eu PARTITION OF spydb_adv.partnopk FOR VALUES IN ('eu');
		INSERT INTO spydb_adv.partnopk VALUES (1,'eu'),(2,'eu')`)
	r := advRun(t, s, `SELECT v FROM spydb_adv.part ORDER BY v`)
	if r.Target == nil || r.Target.Kind != "pk" || r.Target.Table != "part" {
		t.Fatalf("got %+v %s", r.Target, r.ReadOnly)
	}
	advVerify(t, s, "part", r)
	// Moving (1,eu) onto (1,us) collides: refused.
	if _, err := advSave(t, s, r, RowEdit{Key: advKey(r, 0), Old: map[string]*string{}, Values: map[string]*string{"region": p("us")}}); err == nil {
		t.Errorf("moved a row onto an existing key")
	}
	// Moving (2,eu) to us, then editing it again by its new key.
	row := advRowWhere(t, r, "v", "c")
	saved, err := advSave(t, s, r, RowEdit{Key: advKey(r, row), Old: map[string]*string{}, Values: map[string]*string{"region": p("us")}})
	if err != nil {
		t.Fatal(err)
	}
	if *saved[0].Key["region"] != "us" || *saved[0].Key["id"] != "2" {
		t.Fatalf("saved key %v", saved[0].Key)
	}
	if _, err := advSave(t, s, r, RowEdit{Key: saved[0].Key, Old: map[string]*string{"v": p("c")}, Values: map[string]*string{"v": p("C")}}); err != nil {
		t.Fatal(err)
	}
	if got := advStr(t, s, `SELECT string_agg(tableoid::regclass::text || ':' || id || v, ',' ORDER BY id, region) FROM spydb_adv.part`); got != "spydb_adv.part_eu:1a,spydb_adv.part_us:1b,spydb_adv.part_us:2C" {
		t.Errorf("got %s", got)
	}
	if r := advRun(t, s, `SELECT * FROM spydb_adv.partnopk`); r.Target != nil {
		t.Errorf("partitioned table without PK is editable: %+v", r.Target)
	}
	// A partition by itself: editable by its own PK; moving out is refused.
	r = advRun(t, s, `SELECT * FROM spydb_adv.part_eu`)
	if r.Target == nil {
		t.Fatalf("partition read-only: %s", r.ReadOnly)
	}
	if _, err := advSave(t, s, r, RowEdit{Key: advKey(r, 0), Old: map[string]*string{}, Values: map[string]*string{"region": p("us")}}); err == nil {
		t.Errorf("moved a row out of the partition through the partition")
	}
	// A partition without a PK: ctid within that partition.
	r = advRun(t, s, `SELECT id FROM spydb_adv.partnopk_eu ORDER BY id`)
	if r.Target == nil || r.Target.Kind != "ctid" {
		t.Fatalf("partnopk_eu: %+v %s", r.Target, r.ReadOnly)
	}
	if _, err := advSave(t, s, r, advEdit(t, r, 1, map[string]*string{"id": p("20")})); err != nil {
		t.Fatal(err)
	}
	if got := advStr(t, s, `SELECT string_agg(id::text, ',' ORDER BY id) FROM spydb_adv.partnopk`); got != "1,20" {
		t.Errorf("got %s", got)
	}
}

func TestAdvInheritance(t *testing.T) {
	s := advSetup(t)
	other := connectForTest(t)
	advExec(t, s, `CREATE TABLE spydb_adv.ip (id int PRIMARY KEY, v text);
		CREATE TABLE spydb_adv.ic (extra text) INHERITS (spydb_adv.ip);
		INSERT INTO spydb_adv.ip VALUES (1,'p'); INSERT INTO spydb_adv.ic VALUES (1,'c','e');
		CREATE TABLE spydb_adv.lone (v text); INSERT INTO spydb_adv.lone VALUES ('x');
		CREATE TABLE spydb_adv.lonepk (id int PRIMARY KEY, v text); INSERT INTO spydb_adv.lonepk VALUES (1,'x')`)
	for _, sql := range []string{`SELECT * FROM spydb_adv.ip`, `SELECT id, v FROM spydb_adv.ip`, `SELECT * FROM spydb_adv.ip*`, `SELECT * FROM ONLY spydb_adv.ip`} {
		if r := advRun(t, s, sql); r.Target != nil {
			t.Errorf("%s: parent with children is editable", sql)
		}
	}
	r := advRun(t, s, `SELECT v FROM spydb_adv.ic`)
	if r.Target == nil || r.Target.Kind != "ctid" {
		t.Fatalf("child: %+v %s", r.Target, r.ReadOnly)
	}
	if _, err := advSave(t, s, r, advEdit(t, r, 0, map[string]*string{"v": p("C")})); err != nil {
		t.Fatal(err)
	}
	if got := advStr(t, s, `SELECT string_agg(tableoid::regclass::text || v, ',' ORDER BY v) FROM spydb_adv.ip`); got != "spydb_adv.icC,spydb_adv.ipp" {
		t.Errorf("got %s", got)
	}
	// A child created after loading: the save is refused.
	rl := advRun(t, s, `SELECT v FROM spydb_adv.lone`)
	rk := advRun(t, s, `SELECT id, v FROM spydb_adv.lonepk`)
	advExec(t, other, `CREATE TABLE spydb_adv.lone_child () INHERITS (spydb_adv.lone); CREATE TABLE spydb_adv.lonepk_child () INHERITS (spydb_adv.lonepk);
		INSERT INTO spydb_adv.lonepk_child VALUES (1, 'x')`)
	if _, err := advSave(t, s, rl, advEdit(t, rl, 0, map[string]*string{"v": p("y")})); err == nil {
		t.Errorf("saved to a table that gained a child")
	}
	if _, err := advSave(t, s, rk, advEdit(t, rk, 0, map[string]*string{"v": p("y")})); err == nil {
		t.Errorf("saved to a pk table that gained a child (with the same id)")
	}
}

// ------------------------------------------------- names, aliases, paths

func TestAdvNamesAndAliases(t *testing.T) {
	s := advSetup(t)
	advExec(t, s, `CREATE TABLE spydb_adv."MixedCase" ("Id" int PRIMARY KEY, "Val" text);
		INSERT INTO spydb_adv."MixedCase" VALUES (1,'a'),(2,'b');
		CREATE TABLE spydb_adv.status (status text, n int);
		INSERT INTO spydb_adv.status VALUES ('s1',1),('s2',2);
		CREATE TABLE spydb_adv."we.ird ""t""" ("a b" text, "c.d" int);
		INSERT INTO spydb_adv."we.ird ""t""" VALUES ('x',1),('y',2);
		CREATE TABLE spydb_adv.spydb_row (spydb_row text, n int);
		INSERT INTO spydb_adv.spydb_row VALUES ('r1',1),('r2',2);
		CREATE TABLE spydb_adv.statpk (status int PRIMARY KEY, v text);
		INSERT INTO spydb_adv.statpk VALUES (1,'a'),(2,'b')`)
	cases := []struct{ sql, source, from, to string }{
		{`SELECT "Val" FROM Spydb_Adv."MixedCase" AS "Id" ORDER BY "Val"`, "Val", "b", "B"},
		{`SELECT "Val" FROM SPYDB_ADV."MixedCase" "Val" WHERE "Val"."Id" > 0`, "Val", "a", "A"},
		{`SELECT status FROM spydb_adv.status ORDER BY n`, "status", "s1", "S1"},
		{`SELECT n FROM spydb_adv.status status ORDER BY n`, "n", "2", "22"},
		{`SELECT n FROM spydb_adv.status AS n ORDER BY n`, "n", "1", "11"},
		{`SELECT status.status FROM spydb_adv.status WHERE status.n > 0`, "status", "s2", "S2"},
		{`SELECT "a b" FROM spydb_adv."we.ird ""t""" ORDER BY 1`, "a b", "y", "Y"},
		{`SELECT "c.d" FROM spydb_adv."we.ird ""t""" AS w ORDER BY 1`, "c.d", "1", "10"},
		{`SELECT spydb_row FROM spydb_adv.spydb_row ORDER BY n`, "spydb_row", "r1", "R1"},
		{`SELECT n, spydb_row FROM spydb_adv.spydb_row AS spydb_row ORDER BY n`, "n", "2", "20"},
		{`SELECT v FROM spydb_adv.statpk AS status ORDER BY v`, "v", "a", "A"},
		{`SELECT v FROM spydb_adv.statpk AS v ORDER BY v`, "v", "b", "B"},
	}
	for _, tc := range cases {
		r := advRun(t, s, tc.sql)
		if r.Target == nil {
			t.Errorf("%s: read-only (%s)", tc.sql, r.ReadOnly)
			continue
		}
		advVerify(t, s, tc.sql, r)
		row := advRowWhere(t, r, tc.source, tc.from)
		if _, err := advSave(t, s, r, advEdit(t, r, row, map[string]*string{tc.source: p(tc.to)})); err != nil {
			t.Errorf("%s: %v", tc.sql, err)
		}
	}
	for sql, want := range map[string]string{
		`SELECT string_agg("Id" || "Val", ',' ORDER BY "Id") FROM spydb_adv."MixedCase"`:  "1A,2B",
		`SELECT string_agg(status || n, ',' ORDER BY status) FROM spydb_adv.status`:         "S111,S222",
		`SELECT string_agg("a b" || "c.d", ',' ORDER BY "a b") FROM spydb_adv."we.ird ""t"""`: "x10,Y2",
		`SELECT string_agg(spydb_row || n, ',' ORDER BY n) FROM spydb_adv.spydb_row`:        "R11,r220",
		`SELECT string_agg(status || v, ',' ORDER BY status) FROM spydb_adv.statpk`:         "1A,2B",
	} {
		if got := advStr(t, s, sql); got != want {
			t.Errorf("%s = %s, want %s", sql, got, want)
		}
	}
}

// The editor's search_path picks a same-named table the pool wouldn't (here
// spydb_adv.pg_am, ahead of pg_catalog.pg_am); a temp table shadows a real
// one. Saves must go to the table the editor showed, never another.
func TestAdvSearchPathAndTempShadow(t *testing.T) {
	s := advSetup(t)
	advExec(t, s, `CREATE TABLE spydb_adv.pg_am (id int PRIMARY KEY, amname text);
		INSERT INTO spydb_adv.pg_am VALUES (1,'mine');
		CREATE TABLE spydb_adv.pg_amop (amopmethod text); INSERT INTO spydb_adv.pg_amop VALUES ('mine');
		CREATE TABLE spydb_adv.sp (id int PRIMARY KEY, v text); INSERT INTO spydb_adv.sp VALUES (1,'real')`)
	advSQL(t, s, `SET search_path = spydb_adv, pg_catalog`)
	r := advRun(t, s, `SELECT amname FROM pg_am`)
	if r.Target == nil || r.Target.Schema != "spydb_adv" || r.Target.Table != "pg_am" {
		t.Fatalf("got %+v %s", r.Target, r.ReadOnly)
	}
	advVerify(t, s, "pg_am", r)
	if _, err := advSave(t, s, r, advEdit(t, r, 0, map[string]*string{"amname": p("edited")})); err != nil {
		t.Fatal(err)
	}
	r = advRun(t, s, `SELECT amopmethod FROM pg_amop`)
	if r.Target == nil || r.Target.Schema != "spydb_adv" {
		t.Fatalf("got %+v %s", r.Target, r.ReadOnly)
	}
	if _, err := advSave(t, s, r, advEdit(t, r, 0, map[string]*string{"amopmethod": p("edited")})); err != nil {
		t.Fatal(err)
	}
	if got := advStr(t, s, `SELECT amname || (SELECT amopmethod FROM spydb_adv.pg_amop) FROM spydb_adv.pg_am`); got != "editededited" {
		t.Errorf("got %s", got)
	}

	advSQL(t, s, `CREATE TEMP TABLE sp (id int PRIMARY KEY, v text)`)
	advSQL(t, s, `INSERT INTO sp VALUES (1, 'temp')`)
	advSQL(t, s, `SET search_path = spydb_adv`)
	if r := advRun(t, s, `SELECT * FROM sp`); r.Target != nil || len(r.Rows) != 1 || *r.Rows[0][1] != "temp" {
		t.Errorf("temp table: target %+v rows %v", r.Target, r.Rows)
	}
	r = advRun(t, s, `SELECT * FROM spydb_adv.sp`)
	if r.Target == nil {
		t.Fatalf("real table read-only: %s", r.ReadOnly)
	}
	if _, err := advSave(t, s, r, advEdit(t, r, 0, map[string]*string{"v": p("REAL")})); err != nil {
		t.Fatal(err)
	}
	if got := advStr(t, s, `SELECT v FROM spydb_adv.sp`); got != "REAL" {
		t.Errorf("got %s", got)
	}
}

// ---------------------------------------------------- shape-check bypasses

// Every query here must either come back read-only, or every row's key must
// name exactly the table row whose values it shows.
func TestAdvShapeBypasses(t *testing.T) {
	s := advSetup(t)
	advExec(t, s, `CREATE TABLE spydb_adv.sb (id int PRIMARY KEY, v text, n int);
		INSERT INTO spydb_adv.sb SELECT g, 'v' || g, g % 3 FROM generate_series(1, 6) g;
		CREATE TABLE spydb_adv.sb2 (id int PRIMARY KEY, v text);
		INSERT INTO spydb_adv.sb2 VALUES (100, 'other'), (101, 'other2');
		CREATE TABLE spydb_adv.sbn (v text, n int);
		INSERT INTO spydb_adv.sbn VALUES ('a',1),('a',1),('b',2),('c',3);
		CREATE TABLE spydb_adv.sbu ("user" text PRIMARY KEY, v text);
		INSERT INTO spydb_adv.sbu VALUES ('u1','x'),('u2','y');
		CREATE TABLE spydb_adv.sbe (); INSERT INTO spydb_adv.sbe DEFAULT VALUES; INSERT INTO spydb_adv.sbe DEFAULT VALUES`)

	type want int
	const (
		any_     want = iota // read-only or verified
		readOnly             // must be read-only
		editable             // must be editable (and verified)
	)
	cases := []struct {
		sql  string
		want want
	}{
		{`SELECT v FROM spydb_adv.sb /* UNION SELECT v FROM spydb_adv.sb2 */ WHERE id < 4 -- JOIN x`, editable},
		{`SELECT v FROM spydb_adv.sb WHERE v <> 'x UNION SELECT 1' AND v <> $$ GROUP BY $$ AND v <> $a$ ' $a$ AND v <> E'\' UNION'`, editable},
		// An alias named distinct hides the real FROM from the scanner; the
		// next top-level FROM is in the UNION's second branch.
		{`SELECT id, v AS distinct FROM spydb_adv.sb UNION ALL SELECT id, v FROM spydb_adv.sb2`, readOnly},
		{`SELECT n, v AS distinct FROM spydb_adv.sb UNION ALL SELECT n, v FROM spydb_adv.sbn`, readOnly},
		{`SELECT v AS distinct, id FROM spydb_adv.sb WHERE id = 1 UNION ALL SELECT v, id FROM spydb_adv.sb2`, readOnly},
		{`SELECT n AS distinct FROM spydb_adv.sbn`, any_},
		{`SELECT v AS from FROM spydb_adv.sb`, any_},
		{`SELECT id AS from, v FROM spydb_adv.sb`, any_},
		{`SELECT v AS "distinct" FROM spydb_adv.sbn`, editable},
		{`SELECT v AS "FROM" FROM spydb_adv.sbn`, editable},
		// Set-returning functions in the select list or ORDER BY repeat rows.
		{`SELECT id, generate_series(1, 2) AS g FROM spydb_adv.sb`, readOnly},
		{`SELECT v, generate_series(1, 2) AS g FROM spydb_adv.sbn`, readOnly},
		{`SELECT n, v, generate_series(1, n) FROM spydb_adv.sbn`, readOnly},
		{`SELECT id, v, unnest(ARRAY[n]) AS u FROM spydb_adv.sb`, editable},
		{`SELECT v, jsonb_array_elements('[1]') FROM spydb_adv.sbn`, editable},
		{`SELECT v, generate_series(1, 0) FROM spydb_adv.sbn`, any_},
		{`SELECT v FROM spydb_adv.sb ORDER BY generate_series(1, 2)`, readOnly},
		{`SELECT v FROM spydb_adv.sbn ORDER BY generate_series(1, 2), v LIMIT 1`, any_},
		{`SELECT v, unnest(ARRAY[1, 2]) FROM spydb_adv.sbn LIMIT 1`, any_},
		{`SELECT id, v FROM spydb_adv.sb ORDER BY n FETCH FIRST 2 ROWS WITH TIES`, editable},
		{`SELECT v FROM spydb_adv.sbn FOR UPDATE`, editable},
		{`SELECT v FROM spydb_adv.sbn WHERE n > 0 FOR UPDATE SKIP LOCKED`, editable},
		{`SELECT current_user, count(*) FROM spydb_adv.sb`, readOnly},
		{`SELECT current_user, count(*) FROM spydb_adv.sbn`, readOnly},
		{`SELECT user, v FROM spydb_adv.sbu`, any_}, // user is current_user, not the column
		{`SELECT "user", v FROM spydb_adv.sbu`, editable},
		{`SELECT sb, id FROM spydb_adv.sb sb`, editable},
		{`SELECT (sb).v, id FROM spydb_adv.sb`, editable},
		{`SELECT (v), n FROM spydb_adv.sbn`, editable},
		{`SELECT id, (SELECT x.v FROM spydb_adv.sb x WHERE x.id = sb.id + 1) AS v FROM spydb_adv.sb`, any_},
		{`SELECT ctid, v FROM spydb_adv.sbn`, editable},
		{`SELECT * FROM spydb_adv.sbe`, readOnly},
		{`SELECT *, 1 AS one FROM spydb_adv.sbe`, readOnly},
		{`SELECT * FROM spydb_adv.sb*`, readOnly},
		{`SELECT v, count(*) OVER () FROM spydb_adv.sbn`, editable},
		{`SELECT max(n) OVER (), v FROM spydb_adv.sbn`, editable},
		{`SELECT v, row_number() OVER w FROM spydb_adv.sbn WINDOW w AS ()`, readOnly},
		{`SELECT v FROM spydb_adv.sb WHERE id IN (SELECT id FROM spydb_adv.sb JOIN spydb_adv.sb2 USING (id) GROUP BY id UNION SELECT 1)`, editable},
		{`SELECT sb.v FROM spydb_adv.sb WHERE sb.n IS DISTINCT FROM 3`, editable},
		{`SELECT v IS DISTINCT FROM 'a' FROM spydb_adv.sbn`, readOnly},
		{`SELECT v, n IS NOT DISTINCT FROM 1 AS d FROM spydb_adv.sbn`, editable},
		{`SELECT v, n IS DISTINCT FROM 1 FROM spydb_adv.sbn`, editable},
		{`SELECT U&"v" FROM spydb_adv.sb`, readOnly},
		{`SELECT v FROM U&"spydb_adv".U&"sb"`, readOnly},
		{`SELECT id, v FROM U&"spydb_adv".sb`, editable},
		{`SELECT v, substring(v FROM 1 FOR 1) s, extract(epoch FROM now()) e, trim(BOTH FROM v) tr, overlay(v PLACING 'z' FROM 1) o FROM spydb_adv.sbn`, editable},
		{`SELECT CASE WHEN n > 2 THEN v END AS c, v FROM spydb_adv.sbn`, editable},
		{`SELECT v, ARRAY[n, n], ARRAY(SELECT 1 FROM spydb_adv.sb) FROM spydb_adv.sbn`, editable},
		{`SELECT v FROM spydb_adv.sbn LIMIT (SELECT count(*) FROM spydb_adv.sb)`, editable},
		{`SELECT v, (SELECT 1 UNION SELECT 2 LIMIT 1) FROM spydb_adv.sbn`, editable},
		{`SELECT v FROM spydb_adv.sbn TABLESAMPLE SYSTEM (100)`, readOnly},
		{`SELECT v FROM ONLY spydb_adv.sbn`, readOnly},
		{`select V from SPYDB_ADV.SBN`, editable},
		{`SELECT v FROM spydb_adv.sbn AS "group" WHERE "group".n > 0`, editable},
		{`SELECT v, n FROM spydb_adv.sbn ORDER BY 1, 2`, editable},
		{`SELECT v , n FROM spydb_adv.sbn WHERE v = 'a' OR v IN ('b', 'c''d')`, editable},
		{`SELECT v FROM spydb_adv.sbn WHERE v = ANY ($$ {a,b} $$::text[])`, editable},
		{`SELECT v, pg_sleep(0) FROM spydb_adv.sbn`, editable},
		{`SELECT v FROM spydb_adv.sbn;;`, editable},
		{`SELECT v FROM spydb_adv.sbn; SELECT 1`, readOnly},
		{`SELECT v, n FROM spydb_adv.sbn WHERE (n, v) IN (SELECT n, v FROM spydb_adv.sbn)`, editable},
		{"SELECT v -- comment FROM\nFROM spydb_adv.sbn", editable},
		{"SELECT v -- comment FROM\rFROM spydb_adv.sbn", editable},
		{`SELECT v /* FROM x JOIN y /* nested */ GROUP BY */ FROM spydb_adv.sbn`, editable},
		{`SELECT v FROM spydb_adv.sbn WHERE n = 1 ORDER BY v LIMIT 1 OFFSET 0 ROWS`, editable},
		{`SELECT v, (SELECT max(n) FROM spydb_adv.sbn) FROM spydb_adv.sbn`, editable},
		{`SELECT v, n::text AS n FROM spydb_adv.sbn`, editable},
		{`SELECT v COLLATE "C", n FROM spydb_adv.sbn`, editable},
		{`SELECT *, v || '!' AS bang FROM spydb_adv.sb`, editable},
		{`SELECT t.* FROM spydb_adv.sb t WHERE t.n = ANY (SELECT n FROM spydb_adv.sbn)`, editable},
		{`SELECT sb.*, sb2 FROM spydb_adv.sb, spydb_adv.sb2 sb2`, readOnly},
		{`SELECT v FROM spydb_adv.sbn x, LATERAL (SELECT 1) y`, readOnly},
		{`SELECT sbn.v FROM spydb_adv.sbn CROSS JOIN spydb_adv.sb`, readOnly},
		{`SELECT v FROM spydb_adv.sbn WHERE n > 0 GROUP BY v`, readOnly},
		{`SELECT v FROM spydb_adv.sbn WHERE true UNION ALL SELECT v FROM spydb_adv.sbn`, readOnly},
		{`SELECT v FROM spydb_adv.sbn INTO TEMP xx`, any_},
		{`SELECT v INTO TEMP xx FROM spydb_adv.sbn`, readOnly},
		{`SELECT a.v FROM spydb_adv.sbn a WHERE EXISTS (SELECT FROM spydb_adv.sbn b WHERE b.v = a.v AND b.ctid <> a.ctid)`, editable},
	}
	for _, tc := range cases {
		res, err := s.RunQuery(context.Background(), "adv", tc.sql)
		if err != nil {
			if tc.want == editable || tc.want == readOnly {
				t.Errorf("%s: unexpected error %v", tc.sql, err)
			}
			continue
		}
		r := res.Results[len(res.Results)-1]
		advCheckResult(t, tc.sql, r, true)
		switch {
		case tc.want == readOnly && r.Target != nil:
			t.Errorf("%s: editable (%+v), want read-only", tc.sql, r.Target)
		case tc.want == editable && r.Target == nil:
			t.Errorf("%s: read-only (%s), want editable", tc.sql, r.ReadOnly)
		}
		if r.Target != nil && len(res.Results) > 1 {
			t.Errorf("%s: a multi-statement run is editable", tc.sql)
		}
		advVerify(t, s, tc.sql, r)
		// A scalar subquery's column must never get a Source.
		for _, c := range r.Columns {
			if c.Name == "v" && strings.Contains(tc.sql, "x.id = sb.id + 1") && c.Source != "" {
				t.Errorf("%s: subquery column v has Source %q", tc.sql, c.Source)
			}
			if c.Name == "ctid" && c.Source != "" {
				t.Errorf("%s: ctid column has Source %q", tc.sql, c.Source)
			}
		}
	}
}

// Was a bug, now fixed (regression test): Hidden key columns change what ORDER BY <position> means. Postgres
// rejects "ORDER BY 2" when the select list has one column (42P10), but with
// the key columns appended SpyDB's rewritten query is valid, so the user's
// broken query silently runs, sorted by the hidden ctid / primary key.
func TestAdvOrderByOrdinalSeesHiddenColumns(t *testing.T) {
	s := advSetup(t)
	advExec(t, s, `CREATE TABLE spydb_adv.oo (v text); INSERT INTO spydb_adv.oo VALUES ('b'),('a');
		CREATE TABLE spydb_adv.oopk (id int PRIMARY KEY, v text); INSERT INTO spydb_adv.oopk VALUES (2,'a'),(1,'b')`)
	for _, sql := range []string{
		`SELECT v FROM spydb_adv.oo ORDER BY 2`,
		`SELECT v FROM spydb_adv.oo ORDER BY 3`,
		`SELECT v FROM spydb_adv.oopk ORDER BY 2`,
		`SELECT v FROM spydb_adv.oo ORDER BY __spydb_key_2`,
	} {
		if _, err := s.pool.Exec(context.Background(), sql); err == nil {
			t.Fatalf("%s: Postgres accepted it; test assumption wrong", sql)
		}
		res, err := s.RunQuery(context.Background(), "adv", sql)
		if err == nil {
			t.Errorf("BUG: %s: Postgres rejects this query, but SpyDB ran it (sorted by a hidden key column) and returned %d rows",
				sql, len(res.Results[0].Rows))
		}
	}
}

// With standard_conforming_strings off in the editor, \' is an escaped quote.
func TestAdvStandardConformingStringsOff(t *testing.T) {
	s := advSetup(t)
	advExec(t, s, `CREATE TABLE spydb_adv.scs (v text, n int); INSERT INTO spydb_adv.scs VALUES ('a',1),('b',2)`)
	advSQL(t, s, `SET standard_conforming_strings = off`)
	advSQL(t, s, `SET escape_string_warning = off`)
	r := advRun(t, s, `SELECT v FROM spydb_adv.scs WHERE v <> 'x\' UNION ALL SELECT v FROM spydb_adv.scs --'`)
	if len(r.Rows) != 2 {
		t.Fatalf("got %d rows", len(r.Rows))
	}
	advVerify(t, s, "scs off, string", r)
	r = advRun(t, s, `SELECT v FROM spydb_adv.scs WHERE v <> 'x\\' UNION ALL SELECT v FROM spydb_adv.scs --'`)
	if r.Target != nil || len(r.Rows) != 4 {
		t.Errorf("real UNION with scs off: target %+v, %d rows", r.Target, len(r.Rows))
	}
	advSQL(t, s, `SET standard_conforming_strings = on`)
	r = advRun(t, s, `SELECT v FROM spydb_adv.scs WHERE v <> 'x\' UNION ALL SELECT v FROM spydb_adv.scs --'`)
	if r.Target != nil || len(r.Rows) != 4 {
		t.Errorf("real UNION with scs on: target %+v, %d rows", r.Target, len(r.Rows))
	}
	r = advRun(t, s, `SELECT v FROM spydb_adv.scs WHERE v <> E'x\' UNION ALL SELECT v FROM spydb_adv.scs --'`)
	if len(r.Rows) != 2 {
		t.Errorf("E'' string: %d rows", len(r.Rows))
	}
	advVerify(t, s, "E string", r)
}

// ------------------------------------------------------------------ types

// For every type and several editor session settings: saving a cell's
// unchanged value must succeed and leave the stored value as it was; a wrong
// old value must be refused.
//
// BUG (kolkata_sql_dmy): with TimeZone Asia/Kolkata and DateStyle SQL/Postgres/
// German, timestamptz shows as "... IST"; Postgres reads IST back as Israel
// time (+02), so an unchanged save moves the stored instant by 3h30.
// BUG (german_mdy): DateStyle 'German, MDY' shows 2026-03-04 as 04.03.2026 and
// reads it back as 2026-04-03: an unchanged save rewrites the date.
// BUG (bytea_escape): bytea_output isn't carried to the save session, so the
// old bytea value (and, without a PK, the whole-row hash) never matches: every
// save is refused. (extra_float_digits = 0 passes here only because these
// floats print the same either way; see TestAdvFloatKeyWrongRow.)
func TestAdvTypesRoundTrip(t *testing.T) {
	s := advSetup(t)
	advExec(t, s, `CREATE TYPE spydb_adv.mood AS ENUM ('sad', 'happy');
		CREATE TABLE spydb_adv.ty (j json, jb jsonb, x xml, pt point, bc char(5), ip inet, cn cidr, bo bool,
			nu numeric(8,3), f4 real, f8 double precision, fnan double precision, fneg double precision, finf real,
			mo money, by bytea, arr text[], u uuid, iv interval, tz timestamptz, d date, ts timestamp, tm time, ttz timetz,
			e spydb_adv.mood, nl text, vc varchar(10), ia int[]);
		INSERT INTO spydb_adv.ty VALUES ('{"b": 1,  "a":[1, 2]}', '{"b": 1, "a":[1, 2]}', '<a b="1">t &amp; u</a>', '(1.5,-2)', 'ab',
			'10.0.0.1/24', '10.0.0.0/8', true, 1.5, 0.1, 0.1, 'NaN', '-0', '-Infinity', 12.34, '\x00ff275c',
			'{"a b","c,d",NULL,"\""}', 'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11', '1 year 2 mons -3 days 04:05:06.789',
			'2026-03-04 10:11:12.345+05:30', '2026-03-04', '2026-03-04 10:11:12', '10:11:12', '10:11:12+05:30', 'happy', NULL,
			'tail  ', '[0:1]={1,2}');
		CREATE TABLE spydb_adv.ty_orig AS SELECT * FROM spydb_adv.ty;
		CREATE TABLE spydb_adv.typk (id int PRIMARY KEY, LIKE spydb_adv.ty);
		INSERT INTO spydb_adv.typk SELECT 1, * FROM spydb_adv.ty`)
	var columns []string
	rows, err := s.pool.Query(context.Background(), `SELECT attname FROM pg_attribute WHERE attrelid = 'spydb_adv.ty'::regclass AND attnum > 0 ORDER BY attnum`)
	if err != nil {
		t.Fatal(err)
	}
	columns, err = pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	restore := func() {
		advExec(t, s, `TRUNCATE spydb_adv.ty, spydb_adv.typk;
			INSERT INTO spydb_adv.ty SELECT * FROM spydb_adv.ty_orig;
			INSERT INTO spydb_adv.typk SELECT 1, * FROM spydb_adv.ty_orig`)
	}
	canonical := func(table, col string) string {
		return advStr(t, s, fmt.Sprintf(`SELECT CASE WHEN num_nulls(%[1]s) = 1 THEN '<NULL>' ELSE format('%%s', %[1]s) END FROM spydb_adv.%[2]s`,
			pgx.Identifier{col}.Sanitize(), table))
	}

	sessions := []struct {
		name string
		set  []string
	}{
		{"default", nil},
		{"new_york_sql_dmy_sql_standard", []string{"SET TimeZone = 'America/New_York'", "SET DateStyle = 'SQL, DMY'", "SET IntervalStyle = 'sql_standard'"}},
		{"utc_postgres_verbose", []string{"SET TimeZone = 'UTC'", "SET DateStyle = 'Postgres, MDY'", "SET IntervalStyle = 'postgres_verbose'"}},
		{"berlin_german_iso8601", []string{"SET TimeZone = 'Europe/Berlin'", "SET DateStyle = 'German'", "SET IntervalStyle = 'iso_8601'"}},
		{"kolkata_sql_dmy", []string{"SET TimeZone = 'Asia/Kolkata'", "SET DateStyle = 'SQL, DMY'"}},
		{"german_mdy", []string{"SET DateStyle = 'German, MDY'"}},
		{"extra_float_digits_0", []string{"SET extra_float_digits = 0"}},
		{"bytea_escape", []string{"SET bytea_output = 'escape'"}},
	}
	for _, sess := range sessions {
		t.Run(sess.name, func(t *testing.T) {
			advSQL(t, s, "RESET ALL")
			for _, set := range sess.set {
				advSQL(t, s, set)
			}
			for _, table := range []string{"ty", "typk"} {
				var refused, changed, wrongOld []string
				for _, col := range columns {
					r := advRun(t, s, "SELECT * FROM spydb_adv."+table)
					if r.Target == nil {
						t.Fatalf("%s read-only: %s", table, r.ReadOnly)
					}
					shown := r.Rows[0][advCol(t, r, col)]
					before := canonical(table, col)
					// A date/time shown in a form Postgres reads back differently
					// (IST, German + MDY) is refused on purpose, with the reason.
					if _, err := advSave(t, s, r, RowEdit{Key: advKey(r, 0), Old: map[string]*string{col: shown}, Values: map[string]*string{col: shown}}); err != nil &&
						!strings.Contains(err.Error(), "reads back as a different value") {
						refused = append(refused, fmt.Sprintf("%s (shown %s): %v", col, advShow(shown), err))
					}
					if after := canonical(table, col); after != before {
						changed = append(changed, fmt.Sprintf("%s: %s -> %s (shown %s)", col, before, after, advShow(shown)))
						restore()
					}
					r = advRun(t, s, "SELECT * FROM spydb_adv."+table)
					wrong := p("zz")
					if shown != nil {
						wrong = p(*shown + "0")
					}
					if _, err := advSave(t, s, r, RowEdit{Key: advKey(r, 0), Old: map[string]*string{col: wrong}, Values: map[string]*string{col: shown}}); err == nil {
						wrongOld = append(wrongOld, col)
					}
				}
				for _, m := range changed {
					t.Errorf("BUG: %s: saving an unchanged value changed what is stored: %s", table, m)
				}
				for _, m := range wrongOld {
					t.Errorf("%s: a wrong old value for %s was saved", table, m)
				}
				if len(refused) > 0 {
					t.Errorf("BUG: %s: saving an unchanged value was refused for %d of %d columns, e.g. %s", table, len(refused), len(columns), refused[0])
				}
			}
		})
	}
	advSQL(t, s, "RESET ALL")
}

// ------------------------------------- session-dependent keys: wrong row

// Was a bug, now fixed (regression test): Keys are read in the editor's session and parsed back with CAST in the
// save session. With TimeZone Asia/Kolkata (this machine's default) and
// DateStyle 'SQL, DMY', timestamptz prints "01/01/2026 10:00:00 IST"; the save
// reads IST as Israel time (+02), i.e. 13:30 IST, so a composite key
// (sensor, ts) names the NEXT reading and the edit lands on the wrong row.
func TestAdvTimestamptzKeyWrongRow(t *testing.T) {
	s := advSetup(t)
	advExec(t, s, `CREATE TABLE spydb_adv.readings (sensor int, ts timestamptz, status text, PRIMARY KEY (sensor, ts));
		INSERT INTO spydb_adv.readings VALUES (1, '2026-01-01 10:00+05:30', 'ok'), (1, '2026-01-01 13:30+05:30', 'ok')`)
	advSQL(t, s, `SET TimeZone = 'Asia/Kolkata'`)
	advSQL(t, s, `SET DateStyle = 'SQL, DMY'`)
	r := advRun(t, s, `SELECT sensor, ts, status FROM spydb_adv.readings ORDER BY ts`)
	// Fixed twice over: a timestamptz key shown with a zone name is now
	// read-only before any edit (zone names can repeat: see inexactKey).
	if r.Target == nil && strings.Contains(r.ReadOnly, "DateStyle") {
		return
	}
	if r.Target == nil {
		t.Fatalf("read-only: %s", r.ReadOnly)
	}
	advVerify(t, s, "readings", r) // the keys are fine as text
	t.Logf("row 0 key: %v", advShow(r.Keys[0][1]))
	_, err := advSave(t, s, r, advEdit(t, r, 0, map[string]*string{"status": p("bad")}))
	got := advStr(t, s, `SELECT string_agg(to_char(ts AT TIME ZONE 'Asia/Kolkata', 'HH24:MI') || '=' || status, ',' ORDER BY ts) FROM spydb_adv.readings`)
	// Fixed: the key is also compared as text, so the save is refused (and
	// says why) rather than landing on the 13:30 row.
	if err != nil && strings.Contains(err.Error(), "reads back as a different value") && got == "10:00=ok,13:30=ok" {
		return
	}
	if got != "10:00=bad,13:30=ok" {
		t.Errorf("BUG: edited the 10:00 reading (save err %v); table is now %s, want 10:00=bad,13:30=ok", err, got)
	}
}

// Was a bug, now fixed (regression test): DateStyle 'German, MDY' prints date 2026-03-04 as "04.03.2026" but
// parses "04.03.2026" as April 3: an edit of the March 4 row by its date key
// lands on the April 3 row.
func TestAdvDateKeyGermanMDYWrongRow(t *testing.T) {
	s := advSetup(t)
	advExec(t, s, `CREATE TABLE spydb_adv.days (d date PRIMARY KEY, note text);
		INSERT INTO spydb_adv.days VALUES ('2026-03-04', 'same'), ('2026-04-03', 'same')`)
	advSQL(t, s, `SET DateStyle = 'German, MDY'`)
	r := advRun(t, s, `SELECT d, note FROM spydb_adv.days ORDER BY d`)
	if r.Target == nil {
		t.Fatalf("read-only: %s", r.ReadOnly)
	}
	_, err := advSave(t, s, r, advEdit(t, r, 0, map[string]*string{"note": p("edited")}))
	got := advStr(t, s, `SELECT string_agg(to_char(d, 'YYYY-MM-DD') || '=' || note, ',' ORDER BY d) FROM spydb_adv.days`)
	// Fixed: refused with the reason instead of editing April 3.
	if err != nil && strings.Contains(err.Error(), "reads back as a different value") && got == "2026-03-04=same,2026-04-03=same" {
		return
	}
	if got != "2026-03-04=edited,2026-04-03=same" {
		t.Errorf("BUG: edited the row shown as %s (save err %v); table is now %s", advShow(r.Keys[0][0]), err, got)
	}
}

// Was a bug, now fixed (regression test): extra_float_digits isn't part of EditTarget.Session. With it lowered
// in the editor, float8 key 1.1000000000000003 shows (and is keyed) as "1.1";
// the save parses "1.1" exactly and updates the row whose id is 1.1.
func TestAdvFloatKeyWrongRow(t *testing.T) {
	s := advSetup(t)
	advExec(t, s, `CREATE TABLE spydb_adv.fk (id float8 PRIMARY KEY, status text);
		INSERT INTO spydb_adv.fk VALUES (1.1, 'ok'), (1.1000000000000003, 'ok')`)
	advSQL(t, s, `SET extra_float_digits = 0`)
	if r := advRun(t, s, `SELECT id, status FROM spydb_adv.fk`); r.Target != nil {
		t.Errorf("both keys print as 1.1, so this must be read-only (rows repeat)")
	}
	r := advRun(t, s, `SELECT id, status FROM spydb_adv.fk WHERE id > 1.1`)
	// Fixed: with floats printed rounded, float keys are read-only.
	if r.Target == nil && strings.Contains(r.ReadOnly, "floating-point key shown rounded") {
		return
	}
	if r.Target == nil || len(r.Rows) != 1 {
		t.Fatalf("got %+v %d rows %s", r.Target, len(r.Rows), r.ReadOnly)
	}
	_, err := advSave(t, s, r, advEdit(t, r, 0, map[string]*string{"status": p("bad")}))
	got := advStr(t, s, `SELECT string_agg(id::text || '=' || status, ',' ORDER BY id) FROM spydb_adv.fk`)
	if got != "1.1=ok,1.1000000000000003=bad" {
		t.Errorf("BUG: the shown row was 1.1000000000000003 (save err %v); table is now %s", err, got)
	}
}

// Was a bug, now fixed (regression test): search_path isn't part of EditTarget.Session. A regclass key prints
// relative to the editor's search_path ("pg_type" for spydb_adv.pg_type) and
// is parsed with the pool's (where "pg_type" is pg_catalog.pg_type).
func TestAdvRegclassKeyWrongRow(t *testing.T) {
	s := advSetup(t)
	advExec(t, s, `CREATE TABLE spydb_adv.pg_type (x int);
		CREATE TABLE spydb_adv.tblcfg (tbl regclass PRIMARY KEY, note text);
		INSERT INTO spydb_adv.tblcfg VALUES ('spydb_adv.pg_type', 'same'), ('pg_catalog.pg_type', 'same')`)
	advSQL(t, s, `SET search_path = spydb_adv, pg_catalog`)
	r := advRun(t, s, `SELECT tbl, note FROM tblcfg`)
	if r.Target == nil {
		t.Fatalf("read-only: %s", r.ReadOnly)
	}
	row := advRowWhere(t, r, "tbl", "pg_type") // spydb_adv.pg_type, as the editor shows it
	_, err := advSave(t, s, r, advEdit(t, r, row, map[string]*string{"note": p("edited")}))
	got := advStr(t, s, `SELECT string_agg(tbl::oid::regclass::text || '=' || note, ',' ORDER BY tbl::oid::regclass::text) FROM spydb_adv.tblcfg`)
	if got != "pg_type=same,spydb_adv.pg_type=edited" {
		t.Errorf("BUG: edited the row shown as pg_type (spydb_adv.pg_type) (save err %v); table is now %s", err, got)
	}
}

// ------------------------------------------------- pages, export, errors

func TestAdvFetchRowsAndExport(t *testing.T) {
	s := advSetup(t)
	advExec(t, s, `CREATE TABLE spydb_adv.big (n int, v text);
		INSERT INTO spydb_adv.big SELECT g, 'r' || g FROM generate_series(1, 2500) g;
		INSERT INTO spydb_adv.big VALUES (2501, 'r1')`)
	ctx := context.Background()
	res, err := s.RunQuery(ctx, "adv", `SELECT v, n FROM spydb_adv.big ORDER BY n`)
	if err != nil {
		t.Fatal(err)
	}
	r := res.Results[0]
	advCheckResult(t, "big", r, true)
	if len(r.Rows) != pageSize || r.RowCount != 2501 || r.Target == nil {
		t.Fatalf("rows %d count %d target %+v", len(r.Rows), r.RowCount, r.Target)
	}
	all := StatementResult{Columns: r.Columns, Target: r.Target, Rows: r.Rows, Keys: r.Keys}
	for off := pageSize; off < 2501; off += pageSize {
		pg, err := s.FetchRows("adv", res.RunID, 0, off)
		if err != nil {
			t.Fatal(err)
		}
		if len(pg.Rows) != len(pg.Keys) {
			t.Fatalf("page at %d: %d rows, %d keys", off, len(pg.Rows), len(pg.Keys))
		}
		for _, row := range pg.Rows {
			if len(row) != len(r.Columns) {
				t.Fatalf("page row has %d values", len(row))
			}
		}
		all.Rows = append(all.Rows, pg.Rows...)
		all.Keys = append(all.Keys, pg.Keys...)
	}
	if len(all.Rows) != 2501 {
		t.Fatalf("%d rows over all pages", len(all.Rows))
	}
	advVerify(t, s, "all pages", all)
	if _, err := advSave(t, s, all, advEdit(t, all, 2400, map[string]*string{"v": p("edited")})); err != nil {
		t.Fatal(err)
	}
	if got := advStr(t, s, `SELECT string_agg(n::text, ',') FROM spydb_adv.big WHERE v = 'edited'`); got != "2401" {
		t.Errorf("edited n = %s, want 2401", got)
	}

	path := filepath.Join(t.TempDir(), "out.csv")
	n, err := s.ExportResult(ctx, "adv", res.RunID, 0, path, "csv")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if n != 2501 || len(records) != 2502 || !slices.Equal(records[0], []string{"v", "n"}) {
		t.Errorf("export: n=%d records=%d header=%v", n, len(records), records[0])
	}
	for i, rec := range records {
		if len(rec) != 2 {
			t.Fatalf("export record %d has %d fields", i, len(rec))
		}
	}
	xlsx := filepath.Join(t.TempDir(), "out.xlsx")
	if _, err := s.ExportResult(ctx, "adv", res.RunID, 0, xlsx, "xlsx"); err != nil {
		t.Errorf("xlsx export: %v", err)
	}
}

// Error positions point into the SQL as written, not the rewritten SQL.
func TestAdvErrorPosition(t *testing.T) {
	s := advSetup(t)
	advExec(t, s, `CREATE TABLE spydb_adv.ep (n int, v text); INSERT INTO spydb_adv.ep VALUES (1, 'a');
		CREATE TABLE spydb_adv.eppk (id int PRIMARY KEY, n int); INSERT INTO spydb_adv.eppk VALUES (1, 1)`)
	for _, sql := range []string{
		`SELECT v AS "ñandú", n FROM spydb_adv.ep WHERE n = 'abc'`,
		`SELECT v AS "ñandú", n FROM spydb_adv.ep WHERE n = 1 ORDER BY n + 'é'::int`,
		`SELECT n + 'x'::int AS "日本", v FROM spydb_adv.ep`,
		`SELECT "日本" FROM spydb_adv.ep`,
		`SELECT n FROM spydb_adv.eppk WHERE n = 'abc'`,
		`SELECT n FROM spydb_adv.ep WHERE n = 1 AND nosuchcol = 2`,
		`SELECT n FROM spydb_adv.ep WHERE n = 1 ORDER BY 1 LIMIT 'x'`,
	} {
		_, err := s.RunQuery(context.Background(), "adv", sql)
		var qe *QueryError
		if !errors.As(err, &qe) {
			t.Errorf("%s: want a QueryError, got %v", sql, err)
			continue
		}
		_, perr := s.pool.Exec(context.Background(), sql)
		var pe *pgconn.PgError
		if !errors.As(perr, &pe) {
			t.Fatalf("%s: pool error %v", sql, perr)
		}
		if qe.Position != int(pe.Position) || qe.Code != pe.Code {
			t.Errorf("%s: SpyDB says %s at %d, Postgres says %s at %d", sql, qe.Code, qe.Position, pe.Code, pe.Position)
		}
	}
}

// Was a bug, now fixed (regression test): When the rewritten query fails with SQLSTATE class 42/0A/54, RunQuery
// runs the user's SQL again, even when that error came from the user's own
// query at run time, after it had already done work. Here a plpgsql function
// hits a dropped table (42P01) on the 3rd row: the query runs twice, so its
// non-transactional side effects (nextval) happen twice and it takes twice as long.
func TestAdvFailingQueryRunsTwice(t *testing.T) {
	s := advSetup(t)
	advExec(t, s, `CREATE TABLE spydb_adv.rt (n int); INSERT INTO spydb_adv.rt VALUES (1),(2),(3);
		CREATE SEQUENCE spydb_adv.rt_seq;
		CREATE FUNCTION spydb_adv.audit(n int) RETURNS int LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM nextval('spydb_adv.rt_seq');
			IF n = 3 THEN PERFORM 1 FROM spydb_adv.dropped_table; END IF;
			RETURN n;
		END $$`)
	_, err := s.RunQuery(context.Background(), "adv", `SELECT n, spydb_adv.audit(n) FROM spydb_adv.rt`)
	var qe *QueryError
	if !errors.As(err, &qe) || qe.Code != "42P01" {
		t.Fatalf("want 42P01, got %v", err)
	}
	calls := advStr(t, s, `SELECT last_value FROM spydb_adv.rt_seq`)
	if calls != "3" {
		t.Errorf("BUG: the failing query ran more than once: audit() was called %s times for 3 rows", calls)
	}
}

// User columns literally named like the hidden ones stay, the hidden ones go.
func TestAdvUserColumnsNamedLikeHidden(t *testing.T) {
	s := advSetup(t)
	advExec(t, s, `CREATE TABLE spydb_adv.hid (__spydb_key_1 text, __spydb_key_2 text, v text);
		INSERT INTO spydb_adv.hid VALUES ('k1','k2','a'),('k1','k2','a');
		CREATE TABLE spydb_adv.hidpk (id int PRIMARY KEY, __spydb_key_1 text);
		INSERT INTO spydb_adv.hidpk VALUES (1, 'x'), (2, 'y')`)
	ctx := context.Background()
	for _, tc := range []struct {
		sql   string
		names []string
	}{
		{`SELECT * FROM spydb_adv.hid`, []string{"__spydb_key_1", "__spydb_key_2", "v"}},
		{`SELECT v, __spydb_key_2 FROM spydb_adv.hid`, []string{"v", "__spydb_key_2"}},
		{`SELECT __spydb_key_1 FROM spydb_adv.hidpk ORDER BY 1`, []string{"__spydb_key_1"}},
		{`SELECT __spydb_key_1 AS __spydb_key_2 FROM spydb_adv.hidpk ORDER BY 1`, []string{"__spydb_key_2"}},
		{`SELECT __spydb_key_1, id AS __spydb_key_1 FROM spydb_adv.hidpk ORDER BY 1`, []string{"__spydb_key_1", "__spydb_key_1"}},
	} {
		res, err := s.RunQuery(ctx, "adv", tc.sql)
		if err != nil {
			t.Errorf("%s: %v", tc.sql, err)
			continue
		}
		r := res.Results[0]
		advCheckResult(t, tc.sql, r, false)
		var names []string
		for _, c := range r.Columns {
			names = append(names, c.Name)
		}
		if !slices.Equal(names, tc.names) {
			t.Errorf("%s: columns %v, want %v", tc.sql, names, tc.names)
		}
		if r.Target == nil {
			t.Errorf("%s: read-only (%s)", tc.sql, r.ReadOnly)
			continue
		}
		advVerify(t, s, tc.sql, r)
	}
	res, _ := s.RunQuery(ctx, "adv", `SELECT * FROM spydb_adv.hid`)
	r := res.Results[0]
	if _, err := advSave(t, s, r, advEdit(t, r, 1, map[string]*string{"__spydb_key_1": p("K")})); err != nil {
		t.Fatal(err)
	}
	if got := advStr(t, s, `SELECT string_agg(__spydb_key_1, ',' ORDER BY __spydb_key_1) FROM spydb_adv.hid`); got != "K,k1" {
		t.Errorf("got %s", got)
	}
}

// ------------------------------------------------- privileges and roles

// Column privileges that don't cover the key (or the whole row, for the
// hash) make the rewritten query fail; the original runs read-only.
func TestAdvColumnPrivilegesFallBack(t *testing.T) {
	s := advSetup(t)
	advRole(t, s, "spydb_adv_cols")
	advExec(t, s, `CREATE TABLE spydb_adv.cpn (v text, secret text); INSERT INTO spydb_adv.cpn VALUES ('a','s');
		CREATE TABLE spydb_adv.cpp (id int PRIMARY KEY, v text); INSERT INTO spydb_adv.cpp VALUES (1,'a');
		GRANT USAGE ON SCHEMA spydb_adv TO spydb_adv_cols;
		GRANT SELECT (v) ON spydb_adv.cpn TO spydb_adv_cols;
		GRANT SELECT (v) ON spydb_adv.cpp TO spydb_adv_cols`)
	advSQL(t, s, `SET ROLE spydb_adv_cols`)
	defer advSQL(t, s, `RESET ROLE`)
	for _, sql := range []string{`SELECT v FROM spydb_adv.cpn`, `SELECT v FROM spydb_adv.cpp`} {
		r := advRun(t, s, sql)
		if r.Target != nil || len(r.Rows) != 1 || *r.Rows[0][0] != "a" {
			t.Errorf("%s: target %+v rows %v (%s)", sql, r.Target, r.Rows, r.ReadOnly)
		}
	}
}

// Was a bug, now fixed (regression test): Saves run on the pool as the login role, not as the editor session's
// current role. After SET ROLE to a role that may only SELECT (or whose RLS
// policies allow no UPDATE), the grid stays editable and saves succeed with
// the login role's (here superuser) rights.
func TestAdvSetRoleBypassedBySave(t *testing.T) {
	s := advSetup(t)
	advRole(t, s, "spydb_adv_reader")
	advExec(t, s, `CREATE TABLE spydb_adv.ro (id int PRIMARY KEY, v text); INSERT INTO spydb_adv.ro VALUES (1, 'a');
		CREATE TABLE spydb_adv.rl (id int PRIMARY KEY, tenant int, v text); INSERT INTO spydb_adv.rl VALUES (1, 1, 'mine'), (2, 2, 'theirs');
		ALTER TABLE spydb_adv.rl ENABLE ROW LEVEL SECURITY;
		CREATE POLICY see_all ON spydb_adv.rl FOR SELECT TO spydb_adv_reader USING (true);
		GRANT USAGE ON SCHEMA spydb_adv TO spydb_adv_reader;
		GRANT SELECT ON spydb_adv.ro TO spydb_adv_reader;
		GRANT SELECT, UPDATE ON spydb_adv.rl TO spydb_adv_reader`)
	advSQL(t, s, `SET ROLE spydb_adv_reader`)
	defer advSQL(t, s, `RESET ROLE`)
	if _, err := s.RunQuery(context.Background(), "adv", `UPDATE spydb_adv.ro SET v = 'x'`); err == nil {
		t.Fatal("the reader role can update ro; test assumption wrong")
	}
	if res, err := s.RunQuery(context.Background(), "adv", `UPDATE spydb_adv.rl SET v = 'x' WHERE id = 2`); err != nil || res.Results[0].Command != "UPDATE 0" {
		t.Fatalf("the reader role can update rl row 2 (%v); test assumption wrong", err)
	}
	r := advRun(t, s, `SELECT id, v FROM spydb_adv.ro`)
	if r.Target != nil {
		if _, err := advSave(t, s, r, advEdit(t, r, 0, map[string]*string{"v": p("hacked")})); err == nil {
			t.Errorf("BUG: as spydb_adv_reader (SELECT only) the editor can't UPDATE ro, but SaveRows saved it")
		}
	}
	r = advRun(t, s, `SELECT id, tenant, v FROM spydb_adv.rl ORDER BY id`)
	if r.Target != nil {
		if _, err := advSave(t, s, r, advEdit(t, r, 1, map[string]*string{"v": p("hacked")})); err == nil {
			t.Errorf("BUG: RLS lets spydb_adv_reader update no rl rows, but SaveRows saved row 2")
		}
	}
}

// ------------------------------------------------ triggers, generated, casts

func TestAdvTriggersAndGenerated(t *testing.T) {
	s := advSetup(t)
	advExec(t, s, `CREATE TABLE spydb_adv.trg (v text, touched int DEFAULT 0, g int GENERATED ALWAYS AS (length(v)) STORED);
		INSERT INTO spydb_adv.trg (v) VALUES ('a'), ('a');
		CREATE FUNCTION spydb_adv.touch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.touched := OLD.touched + 1; RETURN NEW; END $$;
		CREATE TRIGGER touch BEFORE UPDATE ON spydb_adv.trg FOR EACH ROW EXECUTE FUNCTION spydb_adv.touch();
		CREATE TABLE spydb_adv.trgpk (id int PRIMARY KEY, v text); INSERT INTO spydb_adv.trgpk VALUES (1, 'a');
		CREATE FUNCTION spydb_adv.rekey() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.id := NEW.id + 1000; RETURN NEW; END $$;
		CREATE TRIGGER rekey BEFORE UPDATE ON spydb_adv.trgpk FOR EACH ROW EXECUTE FUNCTION spydb_adv.rekey()`)
	r := advRun(t, s, `SELECT v FROM spydb_adv.trg ORDER BY ctid`)
	saved, err := advSave(t, s, r, advEdit(t, r, 0, map[string]*string{"v": p("abc")}))
	if err != nil {
		t.Fatal(err)
	}
	second := RowEdit{Key: saved[0].Key, Old: map[string]*string{"v": saved[0].Values["v"]}, Values: map[string]*string{"v": p("abcd")}}
	if _, err := advSave(t, s, r, second); err != nil {
		t.Errorf("second edit after a trigger changed other columns: %v", err)
	}
	if got := advStr(t, s, `SELECT string_agg(v || ':' || touched || ':' || g, ',' ORDER BY v) FROM spydb_adv.trg`); got != "a:0:1,abcd:2:4" {
		t.Errorf("got %s", got)
	}
	r = advRun(t, s, `SELECT * FROM spydb_adv.trg ORDER BY v`)
	if _, err := advSave(t, s, r, advEdit(t, r, 0, map[string]*string{"g": p("9")})); err == nil {
		t.Errorf("saved a generated column")
	}
	r = advRun(t, s, `SELECT id, v FROM spydb_adv.trgpk`)
	saved, err = advSave(t, s, r, advEdit(t, r, 0, map[string]*string{"v": p("b")}))
	if err != nil {
		t.Fatal(err)
	}
	if *saved[0].Key["id"] != "1001" {
		t.Errorf("SavedRow.Key after a trigger changed the PK: %v", *saved[0].Key["id"])
	}
	if _, err := advSave(t, s, r, RowEdit{Key: saved[0].Key, Old: map[string]*string{"v": p("b")}, Values: map[string]*string{"v": p("c")}}); err != nil {
		t.Errorf("second edit by the trigger-changed key: %v", err)
	}
}

// Was a bug, now fixed (regression test): New values are written with an explicit CAST(... AS varchar(3)), and
// explicit casts to varchar(n)/char(n)/bit(n) silently truncate. Typing a
// value that's too long saves a cut-off value instead of failing with
// "value too long for type character varying(3)".
func TestAdvExplicitCastTruncates(t *testing.T) {
	s := advSetup(t)
	advExec(t, s, `CREATE TABLE spydb_adv.trunc (id int PRIMARY KEY, code varchar(3), c char(2), b bit(3));
		INSERT INTO spydb_adv.trunc VALUES (1, 'abc', 'ab', '101')`)
	if _, err := s.pool.Exec(context.Background(), `UPDATE spydb_adv.trunc SET code = 'ABCDEF'`); err == nil {
		t.Fatal("plain UPDATE accepted a too-long value; test assumption wrong")
	}
	for col, long := range map[string]string{"code": "ABCDEF", "c": "XYZ", "b": "11111"} {
		r := advRun(t, s, `SELECT * FROM spydb_adv.trunc`)
		saved, err := advSave(t, s, r, advEdit(t, r, 0, map[string]*string{col: p(long)}))
		if err == nil {
			t.Errorf("BUG: %s = %q (too long) was saved as %s instead of refused", col, long, advShow(saved[0].Values[col]))
		}
	}
}

// Was a bug, now fixed (regression test): relationQuery takes every column of the primary-key index, including
// INCLUDE columns, as the primary key. A NULL in an INCLUDE column then makes
// the whole result read-only ("rows repeat"), in query tabs and table tabs,
// and the INCLUDE column is flagged as part of the primary key.
func TestAdvPrimaryKeyInclude(t *testing.T) {
	s := advSetup(t)
	advExec(t, s, `CREATE TABLE spydb_adv.inc (id int, extra text, v text, PRIMARY KEY (id) INCLUDE (extra));
		INSERT INTO spydb_adv.inc VALUES (1, NULL, 'a'), (2, 'x', 'b')`)
	r := advRun(t, s, `SELECT * FROM spydb_adv.inc ORDER BY id`)
	if r.Target == nil || !slices.Equal(r.Target.Key, []string{"id"}) {
		t.Errorf("BUG: query: target %+v, read-only %q; want key [id]", r.Target, r.ReadOnly)
	}
	page, err := s.TableData(context.Background(), TableQuery{Schema: "spydb_adv", Table: "inc", PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Target == nil || !slices.Equal(page.Target.Key, []string{"id"}) {
		t.Errorf("BUG: table tab: target %+v, read-only %q; want key [id]", page.Target, page.ReadOnly)
	}
	for _, c := range page.Columns {
		if c.Name == "extra" && c.PrimaryKey {
			t.Errorf("BUG: INCLUDE column extra is flagged as primary key")
		}
	}
}

// ------------------------------------------------- transactions and locks

func TestAdvInTransactionAndLocks(t *testing.T) {
	s := advSetup(t)
	advExec(t, s, `CREATE TABLE spydb_adv.tx (id int PRIMARY KEY, v text); INSERT INTO spydb_adv.tx VALUES (1, 'a'), (2, 'b');
		CREATE TABLE spydb_adv.txn (v text); INSERT INTO spydb_adv.txn VALUES ('a')`)
	advSQL(t, s, `BEGIN`)
	defer func() { s.RunQuery(context.Background(), "adv", "ROLLBACK") }()
	if r := advRun(t, s, `SELECT v FROM spydb_adv.tx`); r.Target != nil {
		t.Errorf("PK not shown inside a transaction, but editable")
	}
	if r := advRun(t, s, `SELECT v FROM spydb_adv.txn`); r.Target != nil {
		t.Errorf("no PK inside a transaction, but editable")
	}
	// Read before the transaction wrote anything: committed rows, editable.
	r := advRun(t, s, `SELECT id, v FROM spydb_adv.tx ORDER BY id`)
	if r.Target == nil {
		t.Fatalf("PK shown inside a transaction: read-only %s", r.ReadOnly)
	}
	// Once it has written, rows read in it may be its own uncommitted ones
	// (here row 9): read-only, since a rollback would leave their keys
	// naming other rows.
	advSQL(t, s, `INSERT INTO spydb_adv.tx VALUES (9, 'uncommitted')`)
	if after := advRun(t, s, `SELECT id, v FROM spydb_adv.tx ORDER BY id`); after.Target != nil {
		t.Errorf("rows read after the transaction wrote (row 9 is uncommitted) are editable")
	}
	// A row the editor's transaction has locked: the save gives up after lock_timeout.
	advSQL(t, s, `UPDATE spydb_adv.tx SET v = v WHERE id = 1`)
	started := time.Now()
	_, err := advSave(t, s, r, advEdit(t, r, advRowWhere(t, r, "v", "a"), map[string]*string{"v": p("x")}))
	took := time.Since(started)
	if err == nil || took > 9*time.Second {
		t.Errorf("save on a locked row: err %v after %s", err, took)
	}
	advSQL(t, s, `ROLLBACK`)
	if got := advStr(t, s, `SELECT string_agg(id || v, ',' ORDER BY id) FROM spydb_adv.tx`); got != "1a,2b" {
		t.Errorf("got %s", got)
	}
	// A failed transaction: the next query just fails, nothing breaks.
	advSQL(t, s, `BEGIN`)
	s.RunQuery(context.Background(), "adv", `SELECT 1/0`)
	if _, err := s.RunQuery(context.Background(), "adv", `SELECT id, v FROM spydb_adv.tx`); err == nil {
		t.Errorf("query in an aborted transaction succeeded")
	}
	advSQL(t, s, `ROLLBACK`)
	advRun(t, s, `SELECT id, v FROM spydb_adv.tx`)
}

// ------------------------------------------- ctid: a batch reusing a slot

// Was a bug, now fixed (regression test): Inside one SaveRows batch, an earlier UPDATE's new row version can be
// placed in a slot freed by a concurrent DELETE + VACUUM. If that new version
// is identical to the deleted row, a later edit in the batch whose key is the
// deleted row's (ctid, md5) matches it and updates the row just saved: the
// batch "succeeds", writing both edits into one row, instead of refusing
// because a loaded row is gone.
func TestAdvCtidBatchSlotReuse(t *testing.T) {
	s := advSetup(t)
	other := connectForTest(t)
	advExec(t, s, `CREATE TABLE spydb_adv.slot (v text) WITH (autovacuum_enabled = off);
		INSERT INTO spydb_adv.slot VALUES ('b'), ('x'), ('c')`)
	r := advRun(t, s, `SELECT v FROM spydb_adv.slot ORDER BY ctid`)
	rowB, rowX := advRowWhere(t, r, "v", "b"), advRowWhere(t, r, "v", "x")
	addrX := *r.Keys[rowX][0]
	advExec(t, other, `DELETE FROM spydb_adv.slot WHERE v = 'x'`)
	advExec(t, other, `VACUUM spydb_adv.slot`)
	saved, err := advSave(t, s, r,
		advEdit(t, r, rowB, map[string]*string{"v": p("x")}), // b -> x: identical to the deleted row
		advEdit(t, r, rowX, map[string]*string{"v": p("z")})) // the deleted row
	state := advStr(t, s, `SELECT string_agg(v, ',' ORDER BY v) FROM spydb_adv.slot`)
	if err == nil {
		t.Errorf("BUG: the batch saved although row %s was deleted; first edit's new address %s, table now %s (the 'b' row got both edits)",
			addrX, advShow(saved[0].Key["ctid"]), state)
	} else if state != "b,c" {
		t.Errorf("refused, but the table changed: %s", state)
	}
}

// BUG (minor): an old-value check on a composite column uses
// "col IS NOT NULL", which is false when any field is NULL, so a composite
// value like (1,) can never be edited: always "changed by someone else".
func TestAdvCompositeWithNullField(t *testing.T) {
	s := advSetup(t)
	advExec(t, s, `CREATE TYPE spydb_adv.pair AS (a int, b text);
		CREATE TABLE spydb_adv.cmp (id int PRIMARY KEY, p spydb_adv.pair);
		INSERT INTO spydb_adv.cmp VALUES (1, ROW(1, NULL)), (2, ROW(NULL, NULL)), (3, ROW(1, 'x'))`)
	r := advRun(t, s, `SELECT id, p FROM spydb_adv.cmp ORDER BY id`)
	for row, newValue := range []string{"(2,)", "(5,y)", "(4,z)"} {
		if _, err := advSave(t, s, r, advEdit(t, r, row, map[string]*string{"p": p(newValue)})); err != nil {
			t.Errorf("BUG: editing p = %s on id %d refused: %v", advShow(r.Rows[row][1]), row+1, err)
		}
	}
}

// ------------------------------------------------------------- table tabs

func TestAdvTableData(t *testing.T) {
	s := advSetup(t)
	advExec(t, s, `CREATE TABLE spydb_adv.status (status text, n int);
		INSERT INTO spydb_adv.status VALUES ('a',1),('a',1),('b',2),('c',3);
		CREATE TABLE spydb_adv."q""t.x" ("q""c" text); INSERT INTO spydb_adv."q""t.x" VALUES ('v');
		CREATE VIEW spydb_adv.vw AS SELECT * FROM spydb_adv.status;
		CREATE TABLE spydb_adv.par (id int PRIMARY KEY); CREATE TABLE spydb_adv.chi () INHERITS (spydb_adv.par)`)
	ctx := context.Background()
	asResult := func(page TablePage) StatementResult {
		var cols []Column
		for _, c := range page.Columns {
			cols = append(cols, Column{Name: c.Name, Source: c.Name})
		}
		return StatementResult{Columns: cols, Target: page.Target, Rows: page.Rows, Keys: page.Keys}
	}
	for _, q := range []TableQuery{
		{Schema: "spydb_adv", Table: "status", PageSize: 10},
		{Schema: "spydb_adv", Table: "status", PageSize: 2, Page: 1, SortBy: "status"},
		{Schema: "spydb_adv", Table: "status", PageSize: 10, Filters: []TableFilter{{Column: "status", Operator: "eq", Value: "a"}}},
		{Schema: "spydb_adv", Table: `q"t.x`, PageSize: 10},
	} {
		page, err := s.TableData(ctx, q)
		if err != nil {
			t.Fatalf("%+v: %v", q, err)
		}
		if page.Target == nil || page.Target.Kind != "ctid" {
			t.Fatalf("%+v: target %+v %s", q, page.Target, page.ReadOnly)
		}
		r := asResult(page)
		advCheckResult(t, q.Table, r, true)
		advVerify(t, s, q.Table, r)
	}
	page, _ := s.TableData(ctx, TableQuery{Schema: "spydb_adv", Table: "status", PageSize: 10, SortBy: "n"})
	r := asResult(page)
	if _, err := advSave(t, s, r, advEdit(t, r, 1, map[string]*string{"status": p("A")})); err != nil {
		t.Fatal(err)
	}
	if got := advStr(t, s, `SELECT string_agg(status, ',' ORDER BY status COLLATE "C") FROM spydb_adv.status`); got != "A,a,b,c" {
		t.Errorf("got %s", got)
	}
	for _, table := range []string{"vw", "par"} {
		page, err := s.TableData(ctx, TableQuery{Schema: "spydb_adv", Table: table, PageSize: 10})
		if err != nil {
			t.Fatal(err)
		}
		if page.Target != nil {
			t.Errorf("%s is editable in a table tab", table)
		}
	}
}

// -------------------------------------------------------- export re-run

// BUG (export, pre-existing): a result bigger than maxStoredRows is exported by
// re-running the SQL on a pool connection, which has the pool's search_path,
// not the editor's. After SET search_path in the editor, the export silently
// writes a different table's rows (here pg_catalog.pg_am instead of the
// spydb_adv.pg_am the editor showed).
func TestAdvExportRerunUsesPoolSearchPath(t *testing.T) {
	s := advSetup(t)
	advExec(t, s, `CREATE TABLE spydb_adv.pg_am (n int);
		INSERT INTO spydb_adv.pg_am SELECT g FROM generate_series(1, 100001) g`)
	advSQL(t, s, `SET search_path = spydb_adv, pg_catalog`)
	ctx := context.Background()
	res, err := s.RunQuery(ctx, "adv", `SELECT * FROM pg_am`)
	if err != nil {
		t.Fatal(err)
	}
	r := res.Results[0]
	advCheckResult(t, "pg_am", r, true)
	if r.RowCount != 100001 || r.StoredRows != maxStoredRows {
		t.Fatalf("count %d stored %d", r.RowCount, r.StoredRows)
	}
	path := filepath.Join(t.TempDir(), "big.csv")
	n, err := s.ExportResult(ctx, "adv", res.RunID, 0, path, "csv")
	if err != nil || n != 100001 {
		head := ""
		if b, rerr := os.ReadFile(path); rerr == nil {
			head = strings.SplitN(string(b), "\n", 3)[1]
		}
		t.Errorf("BUG: export wrote %d rows (err %v), want the 100001 rows shown; first data line: %q", n, err, head)
	}
}

// ---------------------------------------------------------- suspicious

// System catalogs have primary keys since PG 14, so they come back editable.
// Logged only; nothing is saved.
func TestAdvCatalogsEditable(t *testing.T) {
	s := advSetup(t)
	for _, sql := range []string{`SELECT oid, relname FROM pg_catalog.pg_class LIMIT 1`, `SELECT * FROM pg_catalog.pg_namespace LIMIT 1`} {
		if r := advRun(t, s, sql); r.Target != nil {
			t.Logf("SUSPICIOUS: %s is editable (target %s.%s by %v)", sql, r.Target.Schema, r.Target.Table, r.Target.Key)
		}
	}
}

// Heavy (set SPYDB_ADV_HEAVY=1): a table without a PK holding a ~540 MB bytea.
// The user's query doesn't select the blob, but the injected row hash formats
// the whole row; byteaout's hex text would exceed 1 GB.
func TestAdvHugeRowBreaksQuery(t *testing.T) {
	if os.Getenv("SPYDB_ADV_HEAVY") == "" {
		t.Skip("set SPYDB_ADV_HEAVY=1 (writes a ~540 MB value)")
	}
	s := advSetup(t)
	advExec(t, s, `CREATE TABLE spydb_adv.blobs (id int, name text, data bytea)`)
	advExec(t, s, `INSERT INTO spydb_adv.blobs SELECT 1, 'big', convert_to(repeat('x', 270000000), 'UTF8') || convert_to(repeat('y', 270000000), 'UTF8')`)
	sql := `SELECT id, name FROM spydb_adv.blobs`
	if _, err := s.pool.Exec(context.Background(), sql); err != nil {
		t.Fatalf("plain query fails too: %v", err)
	}
	started := time.Now()
	_, err := s.RunQuery(context.Background(), "adv", sql)
	if err != nil {
		t.Errorf("BUG: %s works in Postgres but fails in SpyDB (after %s): %v", sql, time.Since(started), err)
	} else {
		t.Logf("took %s", time.Since(started))
	}
}
