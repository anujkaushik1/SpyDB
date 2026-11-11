package postgres

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// The edit matrix: every way rows reach the grid (a table tab's pages, sorts
// and filters; query results with the key shown, hidden, renamed, swapped,
// paged or returned by an UPDATE) × every kind of edit (one cell, several
// cells, several rows, NULLs, the key itself, the same row again, rows
// someone else changed or deleted meanwhile, values Postgres rejects), on:
//
//   - members: primary key user_id, and a column named id that repeats
//   - memberships: primary key (org, user_id), each part repeating on its own
//   - logs and big_nopk: no primary key, with exact duplicate rows and NULLs
//   - big_pk: 2,500 rows, for later pages
//
// Edits are built the way the grid builds them (DataGrid.tsx): the row's key
// as loaded (or as the last save returned it, in query results), each
// edited cell's value as shown, and the new values. After every save the
// whole of both schemas is compared with what it was: exactly the edited
// rows may change, only in the edited cells, to exactly what was typed. A
// save that's refused must change nothing.

const mxSchema, mxDecoy = "spydb_matrix_test", "spydb_matrix_decoy"

// Every table the snapshot compares, and its columns.
var mxTables = []string{
	mxSchema + ".members", mxSchema + ".memberships", mxSchema + ".logs",
	mxSchema + ".big_pk", mxSchema + ".big_nopk", mxSchema + ".swaps", mxDecoy + ".members",
}

var mxColumns = map[string][]string{
	"members":     {"id", "user_id", "name", "status", "score", "joined", "note"},
	"memberships": {"org", "user_id", "role", "since"},
	"logs":        {"id", "user_id", "level", "msg", "n"},
	"big_pk":      {"row_no", "id", "val"},
	"big_nopk":    {"id", "val", "dup"},
	"swaps":       {"code", "label"},
}

// SQL types, for another session's changes; text when not listed.
var mxTypes = map[string]string{
	"id": "int", "user_id": "int", "row_no": "int", "n": "int", "dup": "int",
	"score": "numeric", "joined": "date", "since": "date",
}

const mxCreate = `
DROP SCHEMA IF EXISTS spydb_matrix_test CASCADE;
DROP SCHEMA IF EXISTS spydb_matrix_decoy CASCADE;
CREATE SCHEMA spydb_matrix_test;
CREATE SCHEMA spydb_matrix_decoy;
CREATE TABLE spydb_matrix_test.members (id int NOT NULL, user_id int PRIMARY KEY, name text NOT NULL,
  status text, score numeric(8,2), joined date, note text);
CREATE TABLE spydb_matrix_decoy.members (LIKE spydb_matrix_test.members INCLUDING ALL);
CREATE TABLE spydb_matrix_test.memberships (org text, user_id int, role text, since date, PRIMARY KEY (org, user_id));
CREATE TABLE spydb_matrix_test.logs (id int, user_id int, level text, msg text, n int);
CREATE TABLE spydb_matrix_test.big_pk (row_no int PRIMARY KEY, id int, val text);
CREATE TABLE spydb_matrix_test.big_nopk (id int, val text, dup int);
CREATE TABLE spydb_matrix_test.swaps (code text PRIMARY KEY DEFERRABLE INITIALLY DEFERRED, label text);
CREATE VIEW spydb_matrix_test.members_view AS SELECT * FROM spydb_matrix_test.members;`

// Fresh rows for every case. Rows go in shuffled, so the physical order is
// neither the key order nor the order shown.
const mxData = `
TRUNCATE spydb_matrix_test.members, spydb_matrix_decoy.members, spydb_matrix_test.memberships, spydb_matrix_test.logs,
  spydb_matrix_test.big_pk, spydb_matrix_test.big_nopk, spydb_matrix_test.swaps;
INSERT INTO spydb_matrix_test.members
  SELECT g % 5, 100 + g, 'name' || lpad(g::text, 2, '0'), CASE WHEN g % 2 = 0 THEN 'active' ELSE 'paused' END,
         g * 1.5, date '2026-01-01' + g, CASE WHEN g % 3 = 0 THEN NULL ELSE 'note ' || g END
  FROM generate_series(1, 30) g ORDER BY (g * 7) % 31;
INSERT INTO spydb_matrix_decoy.members
  SELECT id, user_id, 'decoy ' || name, status, score, joined, note FROM spydb_matrix_test.members;
INSERT INTO spydb_matrix_test.memberships
  SELECT o, 100 + g, CASE WHEN g % 2 = 0 THEN 'admin' ELSE 'member' END, date '2026-02-01' + g
  FROM unnest(ARRAY['beta', 'acme']) o, generate_series(1, 10) g ORDER BY (g * 3) % 11, o DESC;
INSERT INTO spydb_matrix_test.logs VALUES
  (7, 101, 'info', 'dup', 1), (7, 101, 'info', 'dup', 1), (7, 101, 'info', 'dup', 1), (7, 101, 'info', 'dup', 2),
  (7, 102, 'warn', 'disk', NULL), (8, NULL, 'warn', NULL, NULL),
  (NULL, NULL, NULL, NULL, NULL), (NULL, NULL, NULL, NULL, NULL), (NULL, 101, NULL, 'only msg', NULL);
INSERT INTO spydb_matrix_test.logs
  SELECT g % 4, 100 + g % 6, CASE WHEN g % 3 = 0 THEN 'warn' ELSE 'info' END, 'msg ' || (g % 9), g
  FROM generate_series(1, 30) g;
INSERT INTO spydb_matrix_test.big_pk
  SELECT g, g % 10, 'v' || lpad(g::text, 4, '0') FROM generate_series(1, 2500) g ORDER BY (g * 7919) % 2503;
INSERT INTO spydb_matrix_test.big_nopk SELECT g % 10, 'v' || (g % 1000), 0 FROM generate_series(1, 2500) g;
INSERT INTO spydb_matrix_test.swaps VALUES ('A', 'label of A'), ('B', 'label of B'), ('C', 'label of C');`

// mxRow is one table row: column → value as text, nil for NULL.
type mxRow map[string]*string

// mxView is rows as the grid has them, plus the whole table row each one
// really is (its truth), found independently of SpyDB's keys.
type mxView struct {
	table     string // schema.table the rows come from
	target    *EditTarget
	cols      []string // each shown column's table column (Source); "" for an expression
	rows      [][]*string
	keys      [][]*string
	truth     []mxRow
	keepSaved bool                       // query results keep a save's new key and values (keepSavedValues)
	savedKey  map[int]map[string]*string // row → key the last save returned
	savedVal  map[int]map[string]*string // row → values the last save returned
}

// mxSpec is one way of getting rows into the grid.
type mxSpec struct {
	name   string
	before []string // run in the query tab's session first (SET …, BEGIN)
	load   func(t *testing.T, s *DBService) *mxView
	kind   string   // the target it must get: "pk" or "ctid"
	edit   []string // shown, editable, nullable columns that aren't the key
	key    string   // a key column that's shown, "" if none
	bad    string   // a shown column and a value Postgres rejects, as "column=value" ("column=" for NULL)
}

func TestEditMatrix(t *testing.T) {
	s := connectForTest(t)
	mxExec(t, s, mxCreate)
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+mxSchema+" CASCADE; DROP SCHEMA IF EXISTS "+mxDecoy+" CASCADE")
	})
	for _, spec := range mxSpecs() {
		for _, kind := range mxKinds {
			t.Run(spec.name+"/"+kind.name, func(t *testing.T) {
				v := mxOpen(t, s, spec)
				kind.run(t, s, spec, v)
			})
		}
	}
}

// mxSpecs lists every way rows reach the grid.
func mxSpecs() []mxSpec {
	m, ms, lg := mxSchema+".members", mxSchema+".memberships", mxSchema+".logs"
	tab := func(table string, q TableQuery) func(*testing.T, *DBService) *mxView {
		return func(t *testing.T, s *DBService) *mxView { return mxTableTab(t, s, table, q) }
	}
	sql := func(query, truth string, offset int) func(*testing.T, *DBService) *mxView {
		return func(t *testing.T, s *DBService) *mxView { return mxQuery(t, s, query, truth, offset) }
	}
	members := []string{"status", "note"}
	return []mxSpec{
		// members: primary key user_id; id repeats (1 to 4 and 0, six rows each).
		{name: "members table tab", load: tab(m, TableQuery{}), kind: "pk", edit: members, key: "user_id", bad: "name="},
		{name: "members table tab sorted by the repeating id", load: tab(m, TableQuery{SortBy: "id", SortDesc: true}), kind: "pk", edit: members, key: "user_id", bad: "name="},
		{name: "members table tab filtered on id = 3", load: tab(m, TableQuery{Filters: []TableFilter{{Column: "id", Operator: "eq", Value: "3"}}}), kind: "pk", edit: members, key: "user_id", bad: "score=abc"},
		{name: "members table tab page 3 of 5", load: tab(m, TableQuery{Page: 2, PageSize: 7, SortBy: "name"}), kind: "pk", edit: members, key: "user_id", bad: "joined=not a date"},
		{name: "members SELECT *", load: sql(`SELECT * FROM `+m+` ORDER BY user_id`, `SELECT * FROM `+m+` ORDER BY user_id`, 0), kind: "pk", edit: members, key: "user_id", bad: "name="},
		{name: "members key hidden", load: sql(`SELECT name, status, note FROM `+m+` ORDER BY joined DESC`, `SELECT * FROM `+m+` ORDER BY joined DESC`, 0), kind: "pk", edit: members, bad: "name="},
		{name: "members one row, key hidden", load: sql(`SELECT status, note FROM `+m+` WHERE user_id = 117`, `SELECT * FROM `+m+` WHERE user_id = 117`, 0), kind: "pk", edit: members},
		{name: "members where the repeating id = 3, key hidden", load: sql(`SELECT note, status FROM `+m+` WHERE id = 3 ORDER BY name`, `SELECT * FROM `+m+` WHERE id = 3 ORDER BY name`, 0), kind: "pk", edit: members},
		{name: "members names swapped by aliases", load: sql(`SELECT name AS user_id, user_id AS name, status, note FROM `+m+` ORDER BY members.score`, `SELECT * FROM `+m+` ORDER BY score`, 0), kind: "pk", edit: members, key: "user_id", bad: "name="},
		{name: "members key renamed by an alias", load: sql(`SELECT user_id AS uid, status, note FROM `+m+` ORDER BY uid`, `SELECT * FROM `+m+` ORDER BY user_id`, 0), kind: "pk", edit: members, key: "user_id", bad: "user_id=abc"},
		{name: "members key renamed to id, another column's name", load: sql(`SELECT user_id AS id, status, note FROM `+m+` ORDER BY id DESC`, `SELECT * FROM `+m+` ORDER BY user_id DESC`, 0), kind: "pk", edit: members, key: "user_id", bad: "user_id=abc"},
		{name: "members repeating id renamed to user_id, key hidden", load: sql(`SELECT id AS user_id, status, note FROM `+m+` ORDER BY members.joined`, `SELECT * FROM `+m+` ORDER BY joined`, 0), kind: "pk", edit: members, bad: "id=abc"},
		{name: "members key shown twice under two names", load: sql(`SELECT user_id AS a, status, user_id AS b, note FROM `+m+` ORDER BY b DESC`, `SELECT * FROM `+m+` ORDER BY user_id DESC`, 0), kind: "pk", edit: members, key: "user_id", bad: "user_id=abc"},
		{name: "members key renamed with quotes and spaces, table alias", load: sql(`SELECT mm.user_id AS "User ID", mm.status AS "Status!", mm.note FROM `+m+` AS mm ORDER BY "User ID"`, `SELECT * FROM `+m+` ORDER BY user_id`, 0), kind: "pk", edit: members, key: "user_id", bad: "user_id=abc"},
		{name: "members key renamed, an expression takes its name", load: sql(`SELECT user_id AS uid, user_id * 0 AS user_id, status, note FROM `+m+` ORDER BY uid`, `SELECT * FROM `+m+` ORDER BY user_id`, 0), kind: "pk", edit: members, key: "user_id", bad: "user_id=abc"},
		{name: "members expression named like the key", load: sql(`SELECT user_id + 0 AS user_id, status, note FROM `+m+` ORDER BY 1 DESC`, `SELECT * FROM `+m+` ORDER BY user_id DESC`, 0), kind: "pk", edit: members},
		{name: "members table alias, LIMIT and OFFSET", load: sql(`SELECT mm.status, mm.note FROM `+m+` AS mm WHERE mm.user_id > 105 ORDER BY mm.user_id DESC LIMIT 6 OFFSET 2`, `SELECT * FROM `+m+` WHERE user_id > 105 ORDER BY user_id DESC LIMIT 6 OFFSET 2`, 0), kind: "pk", edit: members},
		{name: "members quoted names, comments and strings", load: sql(`SELECT "status", 'FROM x' AS f, "note" /* FROM spydb_matrix_decoy.members */ FROM "spydb_matrix_test"."members" WHERE name <> 'join' ORDER BY "user_id"`, `SELECT * FROM `+m+` ORDER BY user_id`, 0), kind: "pk", edit: members},
		{name: "members subquery in WHERE and FOR UPDATE", load: sql(`SELECT status, note FROM `+m+` WHERE user_id IN (SELECT user_id FROM `+ms+` WHERE org = 'acme') ORDER BY user_id FOR UPDATE`, `SELECT * FROM `+m+` WHERE user_id IN (SELECT user_id FROM `+ms+` WHERE org = 'acme') ORDER BY user_id`, 0), kind: "pk", edit: members},
		{name: "members search_path picks the decoy schema", before: []string{`SET search_path = ` + mxDecoy}, load: sql(`SELECT status, note FROM members ORDER BY user_id`, `SELECT * FROM `+mxDecoy+`.members ORDER BY user_id`, 0), kind: "pk", edit: members},
		{name: "members inside a transaction, key shown", before: []string{`BEGIN`}, load: sql(`SELECT * FROM `+m+` ORDER BY user_id`, `SELECT * FROM `+m+` ORDER BY user_id`, 0), kind: "pk", edit: members, key: "user_id", bad: "name="},
		{name: "members rows an UPDATE returned", load: mxReturned(`UPDATE ` + m + ` SET note = 'bulk ' || user_id WHERE id = 2`), kind: "pk", edit: members, key: "user_id", bad: "name="},

		// memberships: primary key (org, user_id); user_id repeats across orgs.
		{name: "memberships table tab", load: tab(ms, TableQuery{}), kind: "pk", edit: []string{"role", "since"}, key: "user_id", bad: "since=not a date"},
		{name: "memberships one user in two orgs, key hidden", load: sql(`SELECT role, since FROM `+ms+` WHERE user_id = 104 ORDER BY org`, `SELECT * FROM `+ms+` WHERE user_id = 104 ORDER BY org`, 0), kind: "pk", edit: []string{"role", "since"}, bad: "since=not a date"},
		{name: "memberships key partly shown", load: sql(`SELECT user_id, role FROM `+ms+` ORDER BY user_id, org`, `SELECT * FROM `+ms+` ORDER BY user_id, org`, 0), kind: "pk", edit: []string{"role"}, key: "user_id", bad: "user_id=abc"},
		{name: "memberships whole key shown", load: sql(`SELECT org, user_id, role FROM `+ms+` ORDER BY org, user_id`, `SELECT * FROM `+ms+` ORDER BY org, user_id`, 0), kind: "pk", edit: []string{"role"}, key: "org", bad: "user_id=abc"},

		// logs: no primary key; three identical rows, two rows of NULLs, a repeating id.
		{name: "logs table tab", load: tab(lg, TableQuery{}), kind: "ctid", edit: []string{"msg", "level", "n"}, bad: "n=abc"},
		{name: "logs table tab sorted by msg, filtered on level", load: tab(lg, TableQuery{SortBy: "msg", Filters: []TableFilter{{Column: "level", Operator: "eq", Value: "info"}}}), kind: "ctid", edit: []string{"msg", "n"}, bad: "n=abc"},
		{name: "logs SELECT *", load: sql(`SELECT * FROM `+lg+` ORDER BY ctid`, `SELECT * FROM `+lg+` ORDER BY ctid`, 0), kind: "ctid", edit: []string{"msg", "level"}, bad: "n=abc"},
		{name: "logs identical rows", load: sql(`SELECT msg, n FROM `+lg+` WHERE msg = 'dup' ORDER BY ctid`, `SELECT * FROM `+lg+` WHERE msg = 'dup' ORDER BY ctid`, 0), kind: "ctid", edit: []string{"msg", "n"}, bad: "n=abc"},
		{name: "logs rows of NULLs", load: sql(`SELECT level, msg FROM `+lg+` WHERE id IS NULL ORDER BY ctid`, `SELECT * FROM `+lg+` WHERE id IS NULL ORDER BY ctid`, 0), kind: "ctid", edit: []string{"level", "msg"}},
		{name: "logs repeating id, table alias", load: sql(`SELECT l.msg, l.level FROM `+lg+` l WHERE l.id = 7 ORDER BY l.ctid`, `SELECT * FROM `+lg+` WHERE id = 7 ORDER BY ctid`, 0), kind: "ctid", edit: []string{"msg", "level"}},
		{name: "logs one column", load: sql(`SELECT n FROM `+lg+` WHERE user_id = 101 ORDER BY ctid`, `SELECT * FROM `+lg+` WHERE user_id = 101 ORDER BY ctid`, 0), kind: "ctid", edit: []string{"n"}, bad: "n=abc"},

		// Later pages: a table tab's 14th page, a query's second and third 1,000 rows.
		{name: "big_pk table tab page 14", load: tab(mxSchema+".big_pk", TableQuery{Page: 13, SortBy: "val", SortDesc: true}), kind: "pk", edit: []string{"val"}, key: "row_no", bad: "id=abc"},
		{name: "big_pk query rows 1001-2000, key hidden", load: sql(`SELECT val, id FROM `+mxSchema+`.big_pk ORDER BY val`, `SELECT * FROM `+mxSchema+`.big_pk ORDER BY val`, 1000), kind: "pk", edit: []string{"val", "id"}, bad: "id=abc"},
		{name: "big_nopk query rows 2001-2500, triplicates", load: sql(`SELECT val, dup FROM `+mxSchema+`.big_nopk ORDER BY ctid`, `SELECT * FROM `+mxSchema+`.big_nopk ORDER BY ctid`, 2000), kind: "ctid", edit: []string{"val", "dup"}, bad: "dup=abc"},
		{name: "big_nopk table tab page 21", load: tab(mxSchema+".big_nopk", TableQuery{Page: 20}), kind: "ctid", edit: []string{"val", "dup"}, bad: "dup=abc"},
	}
}

// mxKinds lists every kind of edit; each runs on every spec it applies to.
var mxKinds = []struct {
	name string
	run  func(t *testing.T, s *DBService, spec mxSpec, v *mxView)
}{
	{"one cell", func(t *testing.T, s *DBService, spec mxSpec, v *mxView) {
		v.save(t, s, mxEdit{0: {spec.edit[0]: mxValue(spec.edit[0], 1)}})
	}},
	{"two cells of a row, one to or from NULL", func(t *testing.T, s *DBService, spec mxSpec, v *mxView) {
		if len(spec.edit) < 2 {
			t.Skip("one editable column")
		}
		r, second := len(v.rows)/2, spec.edit[1]
		var to *string // a value becomes NULL, NULL becomes a value
		if v.shown(r, second) == nil {
			to = mxValue(second, 2)
		}
		v.save(t, s, mxEdit{r: {spec.edit[0]: mxValue(spec.edit[0], 1), second: to}})
	}},
	{"three rows in one save", func(t *testing.T, s *DBService, spec mxSpec, v *mxView) {
		if len(v.rows) < 3 {
			t.Skip("fewer than 3 rows")
		}
		c := spec.edit[0]
		v.save(t, s, mxEdit{0: {c: mxValue(c, 1)}, len(v.rows) / 2: {c: mxValue(c, 2)}, len(v.rows) - 1: {c: mxValue(c, 3)}})
	}},
	{"NULL to a value and a value to NULL", func(t *testing.T, s *DBService, spec mxSpec, v *mxView) {
		for _, c := range spec.edit {
			isNull := slices.IndexFunc(v.rows, func(row []*string) bool { return row[slices.Index(v.cols, c)] == nil })
			notNull := slices.IndexFunc(v.rows, func(row []*string) bool { return row[slices.Index(v.cols, c)] != nil })
			if isNull >= 0 && notNull >= 0 {
				v.save(t, s, mxEdit{isNull: {c: mxValue(c, 1)}, notNull: {c: nil}})
				return
			}
		}
		t.Skip("no column with both NULLs and values")
	}},
	{"the key itself, then that row again", func(t *testing.T, s *DBService, spec mxSpec, v *mxView) {
		if spec.key == "" {
			t.Skip("the key isn't shown")
		}
		r := len(v.rows) - 1
		v.save(t, s, mxEdit{r: {spec.key: mxValue(spec.key, 1)}})
		v.again(t, s, spec, r, mxEdit{r: {spec.edit[0]: mxValue(spec.edit[0], 2)}})
	}},
	{"the same cell twice", func(t *testing.T, s *DBService, spec mxSpec, v *mxView) {
		c := spec.edit[0]
		v.save(t, s, mxEdit{0: {c: mxValue(c, 1)}})
		v.again(t, s, spec, 0, mxEdit{0: {c: mxValue(c, 2)}})
	}},
	{"someone changed that cell", func(t *testing.T, s *DBService, spec mxSpec, v *mxView) {
		c := spec.edit[0]
		v.other(t, s, 0, "UPDATE %s SET "+c+" = $1::text::"+mxType(c)+" WHERE %s", *mxValue(c, 90))
		err := v.refused(t, s, mxEdit{0: {c: mxValue(c, 1)}})
		if !strings.Contains(err.Error(), "changed") {
			t.Errorf("message: %v", err)
		}
	}},
	{"someone changed another row", func(t *testing.T, s *DBService, spec mxSpec, v *mxView) {
		if len(v.rows) < 2 {
			t.Skip("one row")
		}
		c, last := spec.edit[0], len(v.rows)-1
		v.other(t, s, last, "UPDATE %s SET "+c+" = $1::text::"+mxType(c)+" WHERE %s", *mxValue(c, 90))
		v.save(t, s, mxEdit{0: {c: mxValue(c, 1)}})
	}},
	{"someone changed another cell of the row", func(t *testing.T, s *DBService, spec mxSpec, v *mxView) {
		other := mxOtherColumn(v.table, spec)
		if other == "" {
			t.Skip("every column is the key or edited")
		}
		theirs := mxValue(other, 90)
		v.other(t, s, 0, "UPDATE %s SET "+other+" = $1::text::"+mxType(other)+" WHERE %s", *theirs)
		edit := mxEdit{0: {spec.edit[0]: mxValue(spec.edit[0], 1)}}
		if v.target.Kind == "ctid" {
			v.refused(t, s, edit) // the row's fingerprint changed: it can't be told from a new row
			return
		}
		v.truth[0][other] = theirs // a primary key still names it; their change stays
		v.save(t, s, edit)
	}},
	{"someone deleted the row", func(t *testing.T, s *DBService, spec mxSpec, v *mxView) {
		v.other(t, s, 0, "DELETE FROM %s WHERE %s")
		v.refused(t, s, mxEdit{0: {spec.edit[0]: mxValue(spec.edit[0], 1)}})
	}},
	{"deleted and inserted again with the same key", func(t *testing.T, s *DBService, spec mxSpec, v *mxView) {
		if v.target.Kind != "pk" {
			t.Skip("no primary key: a new row is never the same row")
		}
		c := spec.edit[0]
		// The new row holds something else in the edited cell: refused.
		v.other(t, s, 0, "WITH gone AS (DELETE FROM %s WHERE %s RETURNING *) INSERT INTO "+v.table+" SELECT * FROM gone")
		v.other(t, s, 0, "UPDATE %s SET "+c+" = $1::text::"+mxType(c)+" WHERE %s", *mxValue(c, 90))
		v.refused(t, s, mxEdit{0: {c: mxValue(c, 1)}})
	}},
	{"deleted, vacuumed, and a different row put in its place", func(t *testing.T, s *DBService, spec mxSpec, v *mxView) {
		if v.target.Kind != "ctid" {
			t.Skip("a primary key never names a different row")
		}
		// The new row matches the edited cell's old value but differs in
		// another column: only the row's fingerprint can tell them apart.
		c := spec.edit[0]
		address := *v.key(0)[ctidKey]
		v.other(t, s, 0, "DELETE FROM %s WHERE %s")
		mxExec(t, s, "VACUUM "+v.table)
		cols := mxColumns[v.table[strings.IndexByte(v.table, '.')+1:]]
		row := maps.Clone(v.truth[0])
		changed := cols[slices.IndexFunc(cols, func(name string) bool { return name != c })]
		row[changed] = mxValue(changed, 90)
		var values []string
		var args []any
		for _, name := range cols {
			args = append(args, row[name])
			values = append(values, fmt.Sprintf("$%d::text::%s", len(args), mxType(name)))
		}
		var at string
		if err := s.pool.QueryRow(t.Context(), fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING ctid::text",
			v.table, strings.Join(cols, ", "), strings.Join(values, ", ")), args...).Scan(&at); err != nil {
			t.Fatal(err)
		}
		if at != address {
			t.Skipf("the new row went to %s, not the freed %s", at, address)
		}
		v.refused(t, s, mxEdit{0: {c: mxValue(c, 1)}})
	}},
	{"a value Postgres rejects, next to a good edit", func(t *testing.T, s *DBService, spec mxSpec, v *mxView) {
		if spec.bad == "" || len(v.rows) < 2 {
			t.Skip("no rejectable column shown, or one row")
		}
		col, value, _ := strings.Cut(spec.bad, "=")
		var bad *string
		if value != "" {
			bad = &value
		}
		// The good edit comes first: the bad one must undo it.
		err := v.refused(t, s, mxEdit{0: {spec.edit[0]: mxValue(spec.edit[0], 1)}, len(v.rows) - 1: {col: bad}})
		if !strings.Contains(err.Error(), "Nothing was saved") {
			t.Errorf("message: %v", err)
		}
	}},
}

// mxEdit is one save: row in the view → column → new value (nil for NULL).
type mxEdit map[int]map[string]*string

// mxOpen gives the case fresh rows, runs the session SQL and loads the view.
func mxOpen(t *testing.T, s *DBService, spec mxSpec) *mxView {
	t.Helper()
	mxExec(t, s, mxData)
	t.Cleanup(func() {
		for _, sql := range []string{"ROLLBACK", "RESET ALL"} {
			if _, err := s.RunQuery(context.Background(), "mx-session", sql); err != nil {
				t.Errorf("%s: %v", sql, err)
			}
		}
	})
	for _, sql := range spec.before {
		if _, err := s.RunQuery(t.Context(), "mx-session", sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	v := spec.load(t, s)
	if v.target == nil {
		t.Fatalf("read-only, want editable by %s", spec.kind)
	}
	if v.target.Kind != spec.kind {
		t.Fatalf("rows found by %s, want %s", v.target.Kind, spec.kind)
	}
	if len(v.rows) == 0 || len(v.keys) != len(v.rows) || len(v.truth) != len(v.rows) {
		t.Fatalf("%d rows, %d keys, %d truth rows", len(v.rows), len(v.keys), len(v.truth))
	}
	return v
}

// mxTableTab loads a table tab's page like TableTab.tsx (100 rows a page).
func mxTableTab(t *testing.T, s *DBService, table string, q TableQuery) *mxView {
	t.Helper()
	q.Schema, q.Table, _ = strings.Cut(table, ".")
	if q.PageSize == 0 {
		q.PageSize = 100
	}
	page, err := s.TableData(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	v := &mxView{table: table, target: page.Target, rows: page.Rows, keys: page.Keys}
	for _, c := range page.Columns {
		v.cols = append(v.cols, c.Name)
	}
	for _, row := range page.Rows { // every column is shown: the row is its own truth
		v.truth = append(v.truth, mxRowOf(v.cols, row))
	}
	return v
}

// mxQuery runs sql like the query tab, from offset on (FetchRows), and
// checks each shown value against truth: the whole rows, in the same order,
// read by the test itself.
func mxQuery(t *testing.T, s *DBService, sql, truth string, offset int) *mxView {
	t.Helper()
	return mxQueryChecked(t, s, sql, truth, offset, true)
}

// mxQueryChecked is mxQuery; check false skips comparing shown values with
// the truth's (for a session that prints values another way).
func mxQueryChecked(t *testing.T, s *DBService, sql, truth string, offset int, check bool) *mxView {
	t.Helper()
	result, err := s.RunQuery(t.Context(), "mx", sql)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	r := result.Results[len(result.Results)-1]
	v := &mxView{target: r.Target, rows: r.Rows, keys: r.Keys, keepSaved: true}
	if offset > 0 {
		page, err := s.FetchRows("mx", result.RunID, len(result.Results)-1, offset)
		if err != nil {
			t.Fatal(err)
		}
		v.rows, v.keys = page.Rows, page.Keys
	}
	for _, c := range r.Columns {
		if strings.HasPrefix(c.Name, "__spydb") {
			t.Fatalf("hidden column %s is shown", c.Name)
		}
		v.cols = append(v.cols, c.Source)
	}
	if v.target == nil {
		return v
	}
	v.table = v.target.Schema + "." + v.target.Table
	all := mxRows(t, s, truth)
	if offset > 0 {
		all = all[offset:min(offset+pageSize, len(all))]
	}
	if len(all) != len(v.rows) {
		t.Fatalf("%d rows shown, %d in the table", len(v.rows), len(all))
	}
	v.truth = all
	for i, row := range v.rows {
		if !check {
			break
		}
		for j, col := range v.cols {
			if col != "" && !mxSame(row[j], all[i][col]) {
				t.Fatalf("row %d shows %s = %s, the table has %s: the test's truth is off", i, col, mxShow(row[j]), mxShow(all[i][col]))
			}
		}
	}
	return v
}

// mxReturned runs an UPDATE (SpyDB adds RETURNING *) and edits the rows it returned.
func mxReturned(sql string) func(*testing.T, *DBService) *mxView {
	return func(t *testing.T, s *DBService) *mxView {
		t.Helper()
		result, err := s.RunQuery(t.Context(), "mx", sql)
		if err != nil {
			t.Fatal(err)
		}
		r := result.Results[0]
		if !strings.Contains(r.Note, "RETURNING") {
			t.Fatalf("no RETURNING added: %q", r.Note)
		}
		v := &mxView{target: r.Target, rows: r.Rows, keys: r.Keys, keepSaved: true}
		for _, c := range r.Columns {
			v.cols = append(v.cols, c.Source)
		}
		if v.target != nil {
			v.table = v.target.Schema + "." + v.target.Table
		}
		for _, row := range r.Rows { // RETURNING * shows every column
			v.truth = append(v.truth, mxRowOf(v.cols, row))
		}
		return v
	}
}

// shown is a cell as the grid shows it: as the last save returned it, or as loaded.
func (v *mxView) shown(r int, col string) *string {
	if value, ok := v.savedVal[r][col]; ok {
		return value
	}
	return v.rows[r][slices.Index(v.cols, col)]
}

// key is row r's key as the grid sends it: as the last save returned it, or as loaded.
func (v *mxView) key(r int) map[string]*string {
	if k, ok := v.savedKey[r]; ok {
		return k
	}
	key := map[string]*string{}
	for j, name := range v.target.Key {
		key[name] = v.keys[r][j]
	}
	return key
}

// rowEdits builds what DataGrid sends for e, rows in order.
func (v *mxView) rowEdits(t *testing.T, e mxEdit) ([]int, []RowEdit) {
	t.Helper()
	order := slices.Sorted(maps.Keys(e))
	edits := make([]RowEdit, 0, len(order))
	for _, r := range order {
		old := map[string]*string{}
		for col := range e[r] {
			if col == "" || !slices.Contains(v.cols, col) {
				t.Fatalf("column %q isn't shown, so it can't be edited", col)
			}
			old[col] = v.shown(r, col)
		}
		edits = append(edits, RowEdit{Key: v.key(r), Old: old, Values: e[r]})
	}
	return order, edits
}

// save saves e and checks the whole of both schemas: exactly the edited rows
// changed, from what they really were to that with the typed values.
func (v *mxView) save(t *testing.T, s *DBService, e mxEdit) {
	t.Helper()
	before := mxSnapshot(t, s)
	order, edits := v.rowEdits(t, e)
	saved, err := s.SaveRows(t.Context(), *v.target, edits)
	if err != nil {
		t.Fatalf("save refused: %v", err)
	}
	if len(saved) != len(edits) {
		t.Fatalf("%d rows saved for %d edits", len(saved), len(edits))
	}
	var wantFrom, wantTo []string
	next := make([]mxRow, len(order))
	for k, r := range order {
		row := maps.Clone(v.truth[r])
		wantFrom = append(wantFrom, mxCanon(v.table, row))
		maps.Copy(row, e[r])
		wantTo = append(wantTo, mxCanon(v.table, row))
		next[k] = row
		for col, value := range e[r] {
			if !mxSame(saved[k].Values[col], value) {
				t.Errorf("row %d: the save returned %s = %s, typed %s", r, col, mxShow(saved[k].Values[col]), mxShow(value))
			}
		}
	}
	from, to := mxDiff(before, mxSnapshot(t, s))
	slices.Sort(wantFrom)
	slices.Sort(wantTo)
	if !slices.Equal(from, wantFrom) || !slices.Equal(to, wantTo) {
		t.Fatalf("the save changed the wrong rows\nchanged:\n  %s\nwant changed:\n  %s\ninto:\n  %s\nwant into:\n  %s",
			strings.Join(from, "\n  "), strings.Join(wantFrom, "\n  "), strings.Join(to, "\n  "), strings.Join(wantTo, "\n  "))
	}
	for k, r := range order {
		v.truth[r] = next[k]
		if v.keepSaved {
			if v.savedKey == nil {
				v.savedKey, v.savedVal = map[int]map[string]*string{}, map[int]map[string]*string{}
			}
			v.savedKey[r] = saved[k].Key
			if v.savedVal[r] == nil {
				v.savedVal[r] = map[string]*string{}
			}
			maps.Copy(v.savedVal[r], saved[k].Values)
		}
	}
}

// again edits row r a second time after a save. Query results keep the key
// the save returned; a table tab reloads, so the row is found again by what
// it now holds (any of identical rows will do).
func (v *mxView) again(t *testing.T, s *DBService, spec mxSpec, r int, e mxEdit) {
	t.Helper()
	if v.keepSaved {
		v.save(t, s, e)
		return
	}
	want := mxCanon(v.table, v.truth[r])
	reloaded := spec.load(t, s)
	at := slices.IndexFunc(reloaded.truth, func(row mxRow) bool { return mxCanon(v.table, row) == want })
	if at < 0 {
		t.Skip("the saved row moved to another page")
	}
	reloaded.save(t, s, mxEdit{at: e[r]})
}

// refused saves e, which must fail and change nothing; it returns the error.
func (v *mxView) refused(t *testing.T, s *DBService, e mxEdit) error {
	t.Helper()
	before := mxSnapshot(t, s)
	_, edits := v.rowEdits(t, e)
	_, err := s.SaveRows(t.Context(), *v.target, edits)
	if err == nil {
		from, to := mxDiff(before, mxSnapshot(t, s))
		t.Fatalf("saved, want refused\nchanged:\n  %s\ninto:\n  %s", strings.Join(from, "\n  "), strings.Join(to, "\n  "))
	}
	if after := mxSnapshot(t, s); !slices.Equal(before, after) {
		from, to := mxDiff(before, after)
		t.Fatalf("a refused save changed rows (%v)\nchanged:\n  %s\ninto:\n  %s", err, strings.Join(from, "\n  "), strings.Join(to, "\n  "))
	}
	return err
}

// other runs sql in another session on row r of the view, found by its
// primary key as the test knows it (or by its address): format has %s for
// the table and %s for the condition; $1 is free for a value.
func (v *mxView) other(t *testing.T, s *DBService, r int, format string, args ...any) {
	t.Helper()
	var where []string
	if v.target.Kind == "ctid" {
		args = append(args, *v.key(r)[ctidKey])
		where = append(where, fmt.Sprintf("ctid = $%d::tid", len(args)))
	} else {
		for _, k := range v.target.Key {
			args = append(args, *v.truth[r][k])
			where = append(where, fmt.Sprintf("%s::text = $%d", pgx.Identifier{k}.Sanitize(), len(args)))
		}
	}
	sql := fmt.Sprintf(format, v.table, strings.Join(where, " AND "))
	tag, err := s.pool.Exec(t.Context(), sql, args...)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("%s: %d rows", sql, tag.RowsAffected())
	}
}

// mxValue is a new value for col, unlike anything in the rows, written the
// way Postgres prints it back.
func mxValue(col string, round int) *string {
	var v string
	switch col {
	case "user_id", "row_no":
		v = fmt.Sprint(9000 + round)
	case "id":
		v = fmt.Sprint(70 + round)
	case "n", "dup":
		v = fmt.Sprint(7000 + round)
	case "score":
		v = fmt.Sprintf("%d.25", 700+round)
	case "joined", "since":
		v = fmt.Sprintf("2031-01-%02d", round%28+1)
	case "org", "code":
		v = fmt.Sprintf("org%d", round)
	default:
		v = fmt.Sprintf("%s edited %d", col, round)
	}
	return &v
}

func mxType(col string) string {
	if t, ok := mxTypes[col]; ok {
		return t
	}
	return "text"
}

// mxOtherColumn is a column of the table that's neither edited nor part of
// a key, or "" when there's none.
func mxOtherColumn(table string, spec mxSpec) string {
	for _, c := range slices.Backward(mxColumns[table[strings.IndexByte(table, '.')+1:]]) {
		if !slices.Contains(spec.edit, c) && c != spec.key && !(c == "user_id" || c == "org" || c == "row_no") {
			return c
		}
	}
	return ""
}

func mxRowOf(cols []string, values []*string) mxRow {
	row := mxRow{}
	for i, c := range cols {
		row[c] = values[i]
	}
	return row
}

// mxRows reads whole rows with the pool, as text.
func mxRows(t *testing.T, s *DBService, sql string) []mxRow {
	t.Helper()
	rows, err := s.pool.Query(t.Context(), sql, pgx.QueryResultFormats{pgx.TextFormatCode})
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	defer rows.Close()
	var out []mxRow
	for rows.Next() {
		row := mxRow{}
		for i, f := range rows.FieldDescriptions() {
			if raw := rows.RawValues()[i]; raw != nil {
				text := string(raw)
				row[f.Name] = &text
			} else {
				row[f.Name] = nil
			}
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// mxSnapshot is every row of every table, as sorted text: a multiset.
func mxSnapshot(t *testing.T, s *DBService) []string {
	t.Helper()
	var out []string
	for _, table := range mxTables {
		cols := mxColumns[table[strings.IndexByte(table, '.')+1:]]
		for _, row := range mxRows(t, s, fmt.Sprintf("SELECT %s FROM ONLY %s", strings.Join(cols, ", "), table)) {
			out = append(out, mxCanon(table, row))
		}
	}
	slices.Sort(out)
	return out
}

// mxCanon writes a row as one line: table, then each column in order.
func mxCanon(table string, row mxRow) string {
	var b strings.Builder
	b.WriteString(table)
	for _, c := range mxColumns[table[strings.IndexByte(table, '.')+1:]] {
		v, ok := row[c]
		switch {
		case !ok:
			fmt.Fprintf(&b, " %s=<missing>", c)
		case v == nil:
			fmt.Fprintf(&b, " %s=NULL", c)
		default:
			fmt.Fprintf(&b, " %s=%q", c, *v)
		}
	}
	return b.String()
}

// mxDiff returns the rows only in before and the rows only in after (as multisets).
func mxDiff(before, after []string) (gone, added []string) {
	count := map[string]int{}
	for _, r := range before {
		count[r]++
	}
	for _, r := range after {
		count[r]--
	}
	for _, r := range slices.Sorted(maps.Keys(count)) {
		for ; count[r] > 0; count[r]-- {
			gone = append(gone, r)
		}
		for ; count[r] < 0; count[r]++ {
			added = append(added, r)
		}
	}
	return gone, added
}

func mxSame(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

func mxShow(v *string) string {
	if v == nil {
		return "NULL"
	}
	return fmt.Sprintf("%q", *v)
}

func mxExec(t *testing.T, s *DBService, sql string) {
	t.Helper()
	if _, err := s.pool.Exec(t.Context(), sql); err != nil {
		t.Fatalf("%v", err)
	}
}

// Shapes whose rows aren't each one table row, or can't be found again, are
// never editable: nothing to save, so nothing to save wrongly.
func TestEditMatrixReadOnly(t *testing.T) {
	s := connectForTest(t)
	mxExec(t, s, mxCreate)
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+mxSchema+" CASCADE; DROP SCHEMA IF EXISTS "+mxDecoy+" CASCADE")
	})
	mxExec(t, s, mxData)
	m, ms, lg := mxSchema+".members", mxSchema+".memberships", mxSchema+".logs"
	for _, c := range []struct{ before, sql string }{
		{"", `SELECT DISTINCT status FROM ` + m},
		{"", `SELECT mm.status FROM ` + m + ` mm JOIN ` + ms + ` USING (user_id)`},
		{"", `SELECT mm.status FROM ` + m + ` mm, ` + lg},
		{"", `SELECT mm.* FROM ` + m + ` mm, LATERAL (SELECT 1) x`},
		{"", `SELECT status, count(*) FROM ` + m + ` GROUP BY status`},
		{"", `WITH x AS (SELECT * FROM ` + m + `) SELECT * FROM x`},
		{"", `SELECT * FROM (SELECT * FROM ` + m + `) x`},
		{"", `SELECT status FROM ` + m + ` UNION ALL SELECT status FROM ` + m},
		{"", `SELECT status, generate_series(1, 2) FROM ` + m},
		{"", `SELECT * FROM ` + m + ` TABLESAMPLE SYSTEM (100)`},
		{"", `TABLE ` + m},
		{"", `SELECT * FROM ` + mxSchema + `.members_view`},
		{"", `UPDATE ` + lg + ` SET n = n WHERE id = 7`},
		{"BEGIN", `SELECT status FROM ` + m},
		{"BEGIN", `SELECT * FROM ` + lg},
	} {
		t.Run(c.sql, func(t *testing.T) {
			if c.before != "" {
				query(t, s, c.before)
				defer query(t, s, "ROLLBACK")
			}
			r := query(t, s, c.sql)
			if r.Target != nil || len(r.Keys) != 0 || r.ReadOnly == "" {
				t.Fatalf("editable (%+v, %d keys), want read-only", r.Target, len(r.Keys))
			}
			if len(r.Rows) == 0 {
				t.Fatal("no rows: nothing was tested")
			}
		})
	}
}

// Primary keys swapped or rotated in one save, in either order: each edit
// finds its row by the key it had, never a row this save already renamed to
// that key (the key is deferrable, so the duplicate exists until commit).
func TestEditMatrixKeySwap(t *testing.T) {
	s := connectForTest(t)
	mxExec(t, s, mxCreate)
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+mxSchema+" CASCADE; DROP SCHEMA IF EXISTS "+mxDecoy+" CASCADE")
	})
	sw := mxSchema + ".swaps"
	for _, c := range []struct {
		name  string
		moves [][2]string // from code → to code, in the order sent
	}{
		{"swap A and B", [][2]string{{"A", "B"}, {"B", "A"}}},
		{"swap B and A", [][2]string{{"B", "A"}, {"A", "B"}}},
		{"rotate A to B to C to A", [][2]string{{"A", "B"}, {"B", "C"}, {"C", "A"}}},
		{"rotate the other way round", [][2]string{{"C", "A"}, {"B", "C"}, {"A", "B"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			mxExec(t, s, mxData)
			v := mxQuery(t, s, `SELECT code, label FROM `+sw+` ORDER BY code`, `SELECT * FROM `+sw+` ORDER BY code`, 0)
			before := mxSnapshot(t, s)
			var edits []RowEdit
			var wantFrom, wantTo []string
			for _, move := range c.moves {
				r := slices.IndexFunc(v.rows, func(row []*string) bool { return *row[0] == move[0] })
				to := move[1]
				edits = append(edits, RowEdit{Key: v.key(r), Old: map[string]*string{"code": v.rows[r][0]}, Values: map[string]*string{"code": &to}})
				row := maps.Clone(v.truth[r])
				wantFrom = append(wantFrom, mxCanon(sw, row))
				row["code"] = &to
				wantTo = append(wantTo, mxCanon(sw, row))
			}
			if _, err := s.SaveRows(t.Context(), *v.target, edits); err != nil {
				t.Fatalf("save refused: %v", err)
			}
			from, to := mxDiff(before, mxSnapshot(t, s))
			slices.Sort(wantFrom)
			slices.Sort(wantTo)
			if !slices.Equal(from, wantFrom) || !slices.Equal(to, wantTo) {
				t.Fatalf("changed:\n  %s\ninto:\n  %s\nwant into:\n  %s", strings.Join(from, "\n  "), strings.Join(to, "\n  "), strings.Join(wantTo, "\n  "))
			}
		})
	}
}

// Dates shown as German DMY (11.01.2026): an edit typed the same way is
// stored as that date, on that row only.
func TestEditMatrixDateStyle(t *testing.T) {
	s := connectForTest(t)
	mxExec(t, s, mxCreate)
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+mxSchema+" CASCADE; DROP SCHEMA IF EXISTS "+mxDecoy+" CASCADE")
	})
	mxExec(t, s, mxData)
	m := mxSchema + ".members"
	query(t, s, "SET DateStyle = 'German, DMY'")
	defer query(t, s, "RESET DateStyle")
	v := mxQueryChecked(t, s, `SELECT joined, note FROM `+m+` WHERE id = 1 ORDER BY user_id`, `SELECT * FROM `+m+` WHERE id = 1 ORDER BY user_id`, 0, false)
	if got := *v.rows[2][0]; got != "12.01.2026" { // user 111 joined on 12 January
		t.Fatalf("row 3 shows %s, want 12.01.2026 (German)", got)
	}
	before := mxSnapshot(t, s)
	typed := "02.03.2027" // 2 March 2027
	_, edits := v.rowEdits(t, mxEdit{2: {"joined": &typed}})
	if _, err := s.SaveRows(t.Context(), *v.target, edits); err != nil {
		t.Fatalf("save refused: %v", err)
	}
	row := maps.Clone(v.truth[2])
	wantFrom := mxCanon(m, row)
	iso := "2027-03-02"
	row["joined"] = &iso
	from, to := mxDiff(before, mxSnapshot(t, s))
	if !slices.Equal(from, []string{wantFrom}) || !slices.Equal(to, []string{mxCanon(m, row)}) {
		t.Fatalf("changed %v into %v", from, to)
	}
}

// Keys renamed in the editor's open transaction, not committed: the grid
// would show user 107 as 105, while the save runs on another connection,
// where 105 is still someone else (and after a rollback, for good). Rows read
// in a transaction that has written are read-only, so it can't get that far.
func TestEditMatrixUncommittedKeys(t *testing.T) {
	s := connectForTest(t)
	mxExec(t, s, mxCreate)
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+mxSchema+" CASCADE; DROP SCHEMA IF EXISTS "+mxDecoy+" CASCADE")
	})
	m := mxSchema + ".members"
	mxExec(t, s, mxData)
	query(t, s, "BEGIN")
	defer query(t, s, "ROLLBACK")
	if _, err := s.RunQuery(t.Context(), "mx", `UPDATE `+m+` SET user_id = 9999 WHERE user_id = 105; UPDATE `+m+` SET user_id = 105 WHERE user_id = 107`); err != nil {
		t.Fatal(err)
	}
	r := query(t, s, `SELECT * FROM `+m+` WHERE user_id = 105`)
	if name := *r.Rows[0][2]; name != "name07" {
		t.Fatalf("the transaction shows %s as 105", name)
	}
	if r.Target != nil || r.ReadOnly != transactionWrote {
		t.Fatalf("rows the open transaction renamed are editable (%q)", r.ReadOnly)
	}
}

// The trap the other way round: the repeating id renamed user_id, the real
// key not selected. The column labelled user_id is id: editing it changes
// that row's id and nothing else, and the hidden key still finds the row.
func TestEditMatrixAliasTrap(t *testing.T) {
	s := connectForTest(t)
	mxExec(t, s, mxCreate)
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+mxSchema+" CASCADE; DROP SCHEMA IF EXISTS "+mxDecoy+" CASCADE")
	})
	mxExec(t, s, mxData)
	m := mxSchema + ".members"
	v := mxQuery(t, s, `SELECT id AS user_id, user_id AS id, name FROM `+m+` WHERE id = 3 ORDER BY name`, `SELECT * FROM `+m+` WHERE id = 3 ORDER BY name`, 0)
	if !slices.Equal(v.cols, []string{"id", "user_id", "name"}) {
		t.Fatalf("columns are really %v, want id, user_id, name", v.cols)
	}
	// Row 4 shows user_id = 3 (really its id) and id = its user_id.
	if *v.rows[3][0] != "3" || *v.rows[3][1] != *v.truth[3]["user_id"] {
		t.Fatalf("row 4 shows %s, %s", mxShow(v.rows[3][0]), mxShow(v.rows[3][1]))
	}
	v.save(t, s, mxEdit{3: {"id": mxValue("id", 1)}})           // the column labelled user_id
	v.save(t, s, mxEdit{3: {"user_id": mxValue("user_id", 2)}}) // the column labelled id: the real key
	v.save(t, s, mxEdit{3: {"name": mxValue("name", 3)}})       // and the same row once more, by its new key
}
