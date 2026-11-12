package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Edge cases for deleting rows from a table tab, run as the tab does it:
// a page from TableData, each picked row sent with its key and every value
// shown. A delete must remove exactly the rows picked (plus what the
// database's own foreign keys and triggers do), or refuse and change
// nothing at all, in any table.

// tabPage loads a table tab's page of 100 rows, as TableTab does.
func tabPage(t *testing.T, s *DBService, table string, change ...func(*TableQuery)) TablePage {
	t.Helper()
	q := TableQuery{Schema: testSchema, Table: table, PageSize: 100}
	for _, c := range change {
		c(&q)
	}
	p, err := s.TableData(t.Context(), q)
	if err != nil {
		t.Fatalf("%s: %v", table, err)
	}
	if p.Target == nil {
		t.Fatalf("%s is read-only: %s", table, p.ReadOnly)
	}
	return p
}

// sent is row i of p as the grid sends it for Delete.
func sent(p TablePage, i int) RowDelete {
	key := map[string]*string{}
	for j, name := range p.Target.Key {
		key[name] = p.Keys[i][j]
	}
	old := map[string]*string{}
	for j, c := range p.Columns {
		old[c.Name] = p.Rows[i][j]
	}
	return RowDelete{Key: key, Old: old}
}

// pick is rows of p as the grid sends them: top to bottom.
func pick(p TablePage, rows ...int) []RowDelete {
	rows = slices.Sorted(slices.Values(rows))
	out := make([]RowDelete, len(rows))
	for k, i := range rows {
		out[k] = sent(p, i)
	}
	return out
}

// pageRow is the index of the row of p whose column shows value.
func pageRow(t *testing.T, p TablePage, column, value string) int {
	t.Helper()
	c := slices.IndexFunc(p.Columns, func(c TableColumn) bool { return c.Name == column })
	if c < 0 {
		t.Fatalf("no column %s", column)
	}
	found := -1
	for i, row := range p.Rows {
		if row[c] != nil && *row[c] == value {
			if found >= 0 {
				t.Fatalf("two rows with %s = %s", column, value)
			}
			found = i
		}
	}
	if found < 0 {
		t.Fatalf("no row with %s = %q", column, value)
	}
	return found
}

// everything is every row of every table in schema, as text: a refused
// delete must leave all of it as it was.
func everything(t *testing.T, s *DBService, schema string) []string {
	t.Helper()
	names := table(t, s, fmt.Sprintf(`SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = '%s' AND c.relkind IN ('r', 'p') ORDER BY 1`, strings.ReplaceAll(schema, "'", "''")))
	var all []string
	for _, name := range names {
		q := pgx.Identifier{schema, name}.Sanitize()
		for _, row := range table(t, s, "SELECT t::text FROM "+q+" t ORDER BY 1") {
			all = append(all, name+": "+row)
		}
	}
	return all
}

// refuseAll asserts the delete is refused, saying want and that nothing was
// deleted, and that no table in the schema changed.
func refuseAll(t *testing.T, s *DBService, target *EditTarget, want string, rows ...RowDelete) {
	t.Helper()
	refuseAllIn(t, s, testSchema, target, want, rows...)
}

func refuseAllIn(t *testing.T, s *DBService, schema string, target *EditTarget, want string, rows ...RowDelete) {
	t.Helper()
	before := everything(t, s, schema)
	n, err := s.DeleteRows(t.Context(), *target, rows)
	if err == nil {
		t.Fatalf("delete should have been refused (%s); it deleted %d rows", want, n)
	}
	if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), nothingDeleted) {
		t.Fatalf("error %q\nwant it to mention %q and %q", err, want, nothingDeleted)
	}
	if after := everything(t, s, schema); !slices.Equal(before, after) {
		t.Fatalf("a refused delete changed something:\nbefore %v\nafter  %v", before, after)
	}
}

// deleteAll deletes rows and asserts it said so.
func deleteAll(t *testing.T, s *DBService, target *EditTarget, rows ...RowDelete) {
	t.Helper()
	n, err := s.DeleteRows(t.Context(), *target, rows)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n != len(rows) {
		t.Fatalf("deleted %d, want %d", n, len(rows))
	}
}

// --- Many rows: a whole page, then a few from a sorted, filtered page ---

func TestDeleteEdgeWholePage(t *testing.T) {
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".items (id int PRIMARY KEY, label text, n int)",
		"INSERT INTO "+testSchema+".items SELECT i, 'item ' || (i % 7), i % 10 FROM generate_series(1, 250) i")
	ids := "SELECT id FROM " + testSchema + ".items ORDER BY id"

	// Select all on page 1 (ids 1–100): only those go; the other 150 stay.
	p := tabPage(t, s, "items")
	all := make([]int, len(p.Rows))
	for i := range all {
		all[i] = i
	}
	deleteAll(t, s, p.Target, pick(p, all...)...)
	var want []string
	for i := 101; i <= 250; i++ {
		want = append(want, fmt.Sprint(i))
	}
	assertRows(t, s, ids, want...)

	// Page 2 of a filtered page sorted by label descending: the rows picked,
	// by their place on that page, are the ones that go.
	sortedFiltered := func(q *TableQuery) {
		q.Page, q.PageSize, q.SortBy, q.SortDesc = 1, 20, "label", true
		q.Filters = []TableFilter{{Column: "n", Operator: "gt", Value: "3"}}
	}
	p = tabPage(t, s, "items", sortedFiltered)
	if len(p.Rows) != 20 {
		t.Fatalf("page has %d rows", len(p.Rows))
	}
	idCol := slices.IndexFunc(p.Columns, func(c TableColumn) bool { return c.Name == "id" })
	gone := map[string]bool{}
	for _, i := range []int{2, 5, 19} {
		gone[*p.Rows[i][idCol]] = true
	}
	deleteAll(t, s, p.Target, pick(p, 2, 5, 19)...)
	var left []string
	for _, id := range want {
		if !gone[id] {
			left = append(left, id)
		}
	}
	assertRows(t, s, ids, left...)
}

// --- Primary keys of many types, with values that look alike ---

func TestDeleteEdgeKeyTypes(t *testing.T) {
	s := editFixture(t,
		"CREATE TYPE "+testSchema+".mood AS ENUM ('sad', 'ok', 'happy')",
		"CREATE DOMAIN "+testSchema+".code AS text CHECK (VALUE <> '')",
		"CREATE TYPE "+testSchema+".pair AS (a int, b text)")
	cases := []struct {
		name, typ string
		values    []string // SQL literals; the middle one is deleted
	}{
		{"text", "text", []string{"'a'", "'a '", "'A'"}},
		{"char3", "char(3)", []string{"'a'", "'ab'", "'abc'"}},
		{"varchar", "varchar(5)", []string{"''", "' '", "'  '"}},
		{"numeric", "numeric", []string{"1", "1.5", "10.000"}},
		{"float8", "float8", []string{"0.1", "0.30000000000000004", "'NaN'"}},
		{"float8_big", "float8", []string{"1e-300", "'Infinity'", "'-Infinity'"}},
		{"float4", "float4", []string{"0.1", "3.4e38", "-0.5"}},
		{"timestamptz", "timestamptz", []string{"'2024-03-10 01:59:59.999999+00'", "'2024-11-03 06:30:00+00'", "'infinity'"}},
		{"timestamp", "timestamp", []string{"'2024-01-01 00:00:00'", "'2024-01-01 00:00:00.000001'", "'-infinity'"}},
		{"date", "date", []string{"'2000-02-29'", "'0044-03-15 BC'", "'infinity'"}},
		{"interval", "interval", []string{"'1 day'", "'1 mon'", "'-00:00:01'"}},
		{"uuid", "uuid", []string{"'00000000-0000-0000-0000-000000000000'", "'c937321d-ffac-4811-9474-f50d466f7cfc'", "'ffffffff-ffff-ffff-ffff-ffffffffffff'"}},
		{"bytea", "bytea", []string{"'\\x'", "'\\x00'", "'\\x0000'"}},
		{"jsonb", "jsonb", []string{`'{"a": 1}'`, `'{"a": "1"}'`, `'[1, 2]'`}},
		{"intarray", "int[]", []string{"'{}'", "'{1,2}'", "'{1,2,NULL}'"}},
		{"textarray", "text[]", []string{`'{"a,b"}'`, `'{a,b}'`, `'{"a\"b"}'`}},
		{"range", "int4range", []string{"'empty'", "'[1,5)'", "'[1,6)'"}},
		{"inet", "inet", []string{"'10.0.0.1'", "'10.0.0.0/8'", "'::1'"}},
		{"bool", "bool", []string{"false", "true"}},
		{"enum", testSchema + ".mood", []string{"'sad'", "'ok'", "'happy'"}},
		{"domain", testSchema + ".code", []string{"'x'", "'x y'", "'x,y'"}},
		{"bits", "varbit", []string{"B'0'", "B'00'", "B'1'"}},
		{"money", "money", []string{"0", "1.5", "-2"}},
		{"composite", testSchema + ".pair", []string{"ROW(1, 'x')", "ROW(1, 'x y')", "ROW(2, '')"}},
		{"emoji", "text", []string{"'😀'", "E'tab\\there'", "E'new\\nline'"}},
	}
	for _, c := range cases {
		exec(t, s, fmt.Sprintf("CREATE TABLE %s.%s (k %s PRIMARY KEY, other int)", testSchema, c.name, c.typ))
		for i, v := range c.values {
			exec(t, s, fmt.Sprintf("INSERT INTO %s.%s VALUES (%s, %d)", testSchema, c.name, v, i))
		}
		p := tabPage(t, s, c.name)
		victim := len(c.values) / 2
		i := pageRow(t, p, "other", fmt.Sprint(victim))
		deleteAll(t, s, p.Target, sent(p, i))
		var want []string
		for k := range c.values {
			if k != victim {
				want = append(want, fmt.Sprint(k))
			}
		}
		assertRows(t, s, fmt.Sprintf("SELECT other FROM %s.%s ORDER BY other", testSchema, c.name), want...)
	}

	// Composite primary key whose parts run together alike: (1,'23') and (12,'3').
	exec(t, s, "CREATE TABLE "+testSchema+".pairs (a int, b text, other int, PRIMARY KEY (a, b))",
		"INSERT INTO "+testSchema+".pairs VALUES (1, '23', 0), (12, '3', 1), (1, '2,3', 2), (1, '2', 3)")
	p := tabPage(t, s, "pairs")
	deleteAll(t, s, p.Target, sent(p, pageRow(t, p, "other", "0")), sent(p, pageRow(t, p, "other", "2")))
	assertRows(t, s, "SELECT a, b FROM "+testSchema+".pairs ORDER BY other", "12|3", "1|2")

	// A composite key value with a NULL field: Postgres compares stored
	// composite values with NULL fields as equal, so it's found; only it goes.
	exec(t, s, "CREATE TABLE "+testSchema+".nullpair (k "+testSchema+".pair PRIMARY KEY, other int)",
		"INSERT INTO "+testSchema+".nullpair VALUES (ROW(1, NULL), 0), (ROW(1, 'a'), 1), (ROW(NULL, NULL), 2)")
	p = tabPage(t, s, "nullpair")
	deleteAll(t, s, p.Target, sent(p, pageRow(t, p, "other", "0")), sent(p, pageRow(t, p, "other", "2")))
	assertRows(t, s, "SELECT k::text, other FROM "+testSchema+".nullpair", "(1,a)|1")

	// Case-insensitive (nondeterministic ICU) collation and citext keys.
	exec(t, s, "CREATE COLLATION "+testSchema+".ci (provider = icu, locale = 'und-u-ks-level2', deterministic = false)",
		"CREATE TABLE "+testSchema+".ci_keys (k text COLLATE "+testSchema+".ci PRIMARY KEY, other int)",
		"INSERT INTO "+testSchema+".ci_keys VALUES ('Apple', 0), ('banana', 1), ('Cherry', 2)",
		"CREATE EXTENSION IF NOT EXISTS citext SCHEMA "+testSchema,
		"CREATE TABLE "+testSchema+".citext_keys (k "+testSchema+".citext PRIMARY KEY, other int)",
		"INSERT INTO "+testSchema+".citext_keys VALUES ('Apple', 0), ('banana', 1), ('Cherry', 2)")
	for _, name := range []string{"ci_keys", "citext_keys"} {
		p := tabPage(t, s, name)
		deleteAll(t, s, p.Target, sent(p, pageRow(t, p, "other", "1")))
		assertRows(t, s, "SELECT k FROM "+testSchema+"."+name+" ORDER BY other", "Apple", "Cherry")
	}
}

// --- Values of every kind: comparing what was shown must never refuse
// a row that didn't change ---

func TestDeleteEdgeValueTypes(t *testing.T) {
	s := editFixture(t,
		"CREATE EXTENSION IF NOT EXISTS hstore SCHEMA "+testSchema,
		"CREATE TYPE "+testSchema+".pair AS (a int, b text)",
		`CREATE TABLE `+testSchema+`.things (
			id int GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			j json, jb jsonb, x xml, pt point, poly polygon, ts tsvector, m money, iv interval,
			tz timestamptz, f8 float8, f4 float4, num numeric(20, 6), b bytea, t text, empty text, nothing text,
			arr text[], nested int[][], comp `+testSchema+`.pair, rng tstzrange, h `+testSchema+`.hstore,
			vb varbit, mac macaddr8, lsn pg_lsn, u uuid, big text, bigb bytea,
			twice int GENERATED ALWAYS AS (id * 2) STORED)`,
		`INSERT INTO `+testSchema+`.things (j, jb, x, pt, poly, ts, m, iv, tz, f8, f4, num, b, t, empty, nothing,
			arr, nested, comp, rng, h, vb, mac, lsn, u, big, bigb) VALUES
		('{ "b": 1,  "a": [1, 2], "a": 3 }', '{"z": 1, "a": {"k": [true, null]}}', '<a b="1">x &amp; y</a>',
		 '(1.5,-2)', '((0,0),(1,1),(1,0))', 'the quick brown fox', 12.34, '1 year 2 mons 3 days 04:05:06.789',
		 '2024-06-01 12:34:56.789+05:30', 0.1 + 0.2, 0.1, 123.456789, '\x00ff10', E'quotes '' " \\ tab\t nl\n emoji 😀',
		 '', NULL, '{"a,b", "c\"d", NULL, ""}', '{{1,2},{3,4}}', ROW(1, 'x y'), '[2024-01-01, 2024-02-01)',
		 'a=>1, "b c"=>NULL', B'10101', '08:00:2b:01:02:03:04:05', '16/B374D848', gen_random_uuid(),
		 repeat('long text ', 200000), decode(repeat('ab', 500000), 'hex')),
		(NULL, 'null', NULL, NULL, NULL, '', -0.0, '-1 day', '-infinity', '-Infinity', 'NaN', -0.000001, '\x',
		 'NULL', '', NULL, '{}', '{}', ROW(NULL, NULL), 'empty', '', B'', NULL, '0/0', NULL, '', '\x')`)

	p := tabPage(t, s, "things")
	if len(p.Rows) != 2 {
		t.Fatalf("%d rows", len(p.Rows))
	}
	big := slices.IndexFunc(p.Columns, func(c TableColumn) bool { return c.Name == "big" })
	if v := p.Rows[0][big]; v == nil || len(*v) != 2_000_000 {
		t.Fatalf("the 2 MB value reached the grid cut short")
	}
	deleteAll(t, s, p.Target, pick(p, 0, 1)...)
	assertRows(t, s, "SELECT count(*) FROM "+testSchema+".things", "0")
}

// --- No primary key: rows found by address and a hash of the whole row ---

func TestDeleteEdgeNoPrimaryKey(t *testing.T) {
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".dupes (v text, n int, gone int)",
		"ALTER TABLE "+testSchema+".dupes DROP COLUMN gone", // a dropped column must not disturb the hash
		"INSERT INTO "+testSchema+".dupes VALUES ('same', 1), ('same', 1), ('same', 1), ('other', 2), (NULL, NULL)")
	all := "SELECT v, n FROM " + testSchema + ".dupes ORDER BY v NULLS FIRST, n"

	// Of three identical rows, the two picked go and the third stays.
	p := tabPage(t, s, "dupes")
	var same []int
	for i, row := range p.Rows {
		if row[0] != nil && *row[0] == "same" {
			same = append(same, i)
		}
	}
	deleteAll(t, s, p.Target, pick(p, same[0], same[2])...)
	assertRows(t, s, all, "NULL|NULL", "other|2", "same|1")

	// The all-NULL row goes too.
	p = tabPage(t, s, "dupes")
	deleteAll(t, s, p.Target, sent(p, slices.IndexFunc(p.Rows, func(r []*string) bool { return r[0] == nil })))
	assertRows(t, s, all, "other|2", "same|1")

	// Updated to the very same values: the row moved, so its address no longer matches.
	p = tabPage(t, s, "dupes")
	exec(t, s, "UPDATE "+testSchema+".dupes SET v = v WHERE v = 'other'")
	refuseAll(t, s, p.Target, "changed or deleted", sent(p, pageRow(t, p, "v", "other")))
}

// --- Other sessions: changes, deletes and locks after the page loaded ---

func TestDeleteEdgeOtherSessions(t *testing.T) {
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".accounts (id int PRIMARY KEY, owner text, balance numeric(12, 2), note text)",
		"INSERT INTO "+testSchema+".accounts SELECT i, 'owner ' || i, i * 100, NULL FROM generate_series(1, 6) i")
	p := tabPage(t, s, "accounts")
	row := func(id string) RowDelete { return sent(p, pageRow(t, p, "id", id)) }

	// Changed meanwhile: a value, a NULL that got a value, the key itself.
	exec(t, s, "UPDATE "+testSchema+".accounts SET balance = balance + 0.01 WHERE id = 1")
	refuseAll(t, s, p.Target, "changed by someone else since it was loaded (balance)", row("2"), row("1"))
	exec(t, s, "UPDATE "+testSchema+".accounts SET note = '' WHERE id = 2")
	refuseAll(t, s, p.Target, "(note)", row("3"), row("2"))
	exec(t, s, "UPDATE "+testSchema+".accounts SET id = 30 WHERE id = 3")
	refuseAll(t, s, p.Target, "wasn't found", row("4"), row("3"))

	// Deleted meanwhile, then another row put in under the same key with other values.
	exec(t, s, "DELETE FROM "+testSchema+".accounts WHERE id = 4",
		"INSERT INTO "+testSchema+".accounts VALUES (4, 'someone new', 1, NULL)")
	refuseAll(t, s, p.Target, "changed by someone else", row("5"), row("4"))

	// Another session holds the row (an uncommitted UPDATE): refused after
	// lock_timeout, saying who to look for, and nothing deleted.
	ctx := context.Background()
	other, err := s.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Release()
	tx, err := other.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "UPDATE "+testSchema+".accounts SET note = note WHERE id = 6"); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	refuseAll(t, s, p.Target, "Another session is using", row("5"), row("6"))
	if took := time.Since(started); took > 8*time.Second {
		t.Errorf("waited %v for the lock", took)
	}

	// Cancelled while waiting for that lock: nothing deleted, and the next delete works.
	cctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	if _, err := s.DeleteRows(cctx, *p.Target, []RowDelete{row("5"), row("6")}); err == nil {
		t.Fatal("a cancelled delete reported success")
	}
	cancel()
	tx.Rollback(ctx)
	assertRows(t, s, "SELECT id FROM "+testSchema+".accounts ORDER BY id", "1", "2", "4", "5", "6", "30")
	deleteAll(t, s, p.Target, row("5"), row("6"))
	assertRows(t, s, "SELECT id FROM "+testSchema+".accounts ORDER BY id", "1", "2", "4", "30")
}

// --- Foreign keys and the rows the database itself removes or changes ---

func TestDeleteEdgeForeignKeys(t *testing.T) {
	s := editFixture(t,
		// A tree: deleting a node deletes its children (ON DELETE CASCADE).
		"CREATE TABLE "+testSchema+".tree (id int PRIMARY KEY, parent int REFERENCES "+testSchema+".tree ON DELETE CASCADE, name text)",
		"INSERT INTO "+testSchema+".tree VALUES (1, NULL, 'root'), (2, 1, 'child'), (3, 2, 'grandchild'), (4, NULL, 'other root'), (5, 4, 'other child')",
		// Staff: deleting a manager clears their reports' manager (ON DELETE SET NULL).
		"CREATE TABLE "+testSchema+".staff (id int PRIMARY KEY, manager int REFERENCES "+testSchema+".staff ON DELETE SET NULL, name text)",
		"INSERT INTO "+testSchema+".staff VALUES (1, NULL, 'boss'), (2, 1, 'report'), (3, 1, 'another report'), (4, NULL, 'solo')",
		// A cascade that ends at a row something else still needs (RESTRICT).
		"CREATE TABLE "+testSchema+".customers (id int PRIMARY KEY)",
		"CREATE TABLE "+testSchema+".orders (id int PRIMARY KEY, customer int REFERENCES "+testSchema+".customers ON DELETE CASCADE)",
		"CREATE TABLE "+testSchema+".invoices (id int PRIMARY KEY, order_id int REFERENCES "+testSchema+".orders ON DELETE RESTRICT)",
		"INSERT INTO "+testSchema+".customers VALUES (1), (2)",
		"INSERT INTO "+testSchema+".orders VALUES (10, 1), (20, 2)",
		"INSERT INTO "+testSchema+".invoices VALUES (100, 10)")
	tree := "SELECT id FROM " + testSchema + ".tree ORDER BY id"

	// Parent and child picked together, whichever comes first on the page:
	// the parent's cascade takes the child, and that's no reason to refuse.
	for _, desc := range []bool{false, true} {
		exec(t, s, "TRUNCATE "+testSchema+".tree",
			"INSERT INTO "+testSchema+".tree VALUES (1, NULL, 'root'), (2, 1, 'child'), (3, 2, 'grandchild'), (4, NULL, 'other root'), (5, 4, 'other child')")
		p := tabPage(t, s, "tree", func(q *TableQuery) { q.SortBy, q.SortDesc = "id", desc })
		deleteAll(t, s, p.Target, pick(p, pageRow(t, p, "id", "1"), pageRow(t, p, "id", "2"))...)
		assertRows(t, s, tree, "4", "5")
	}

	// Manager and report picked together: the manager's SET NULL changes
	// the report, and that's no reason to refuse either.
	for _, desc := range []bool{false, true} {
		exec(t, s, "TRUNCATE "+testSchema+".staff",
			"INSERT INTO "+testSchema+".staff VALUES (1, NULL, 'boss'), (2, 1, 'report'), (3, 1, 'another report'), (4, NULL, 'solo')")
		p := tabPage(t, s, "staff", func(q *TableQuery) { q.SortBy, q.SortDesc = "id", desc })
		deleteAll(t, s, p.Target, pick(p, pageRow(t, p, "id", "1"), pageRow(t, p, "id", "2"))...)
		assertRows(t, s, "SELECT id, manager FROM "+testSchema+".staff ORDER BY id", "3|NULL", "4|NULL")
	}

	// The cascade reaches an invoice that RESTRICTs: refused, every level intact.
	p := tabPage(t, s, "customers")
	refuseAll(t, s, p.Target, "foreign key", sent(p, pageRow(t, p, "id", "2")), sent(p, pageRow(t, p, "id", "1")))
	deleteAll(t, s, p.Target, sent(p, pageRow(t, p, "id", "2")))
	assertRows(t, s, "SELECT id FROM "+testSchema+".orders", "10")

	// The confirmation names the self-reference too (its own other rows go).
	e, err := s.DeleteEffects(t.Context(), *tabPage(t, s, "tree").Target)
	if err != nil || !slices.Equal(e.Cascades, []string{testSchema + ".tree"}) {
		t.Fatalf("tree effects %+v, %v", e, err)
	}
	e, err = s.DeleteEffects(t.Context(), *tabPage(t, s, "staff").Target)
	if err != nil || !slices.Equal(e.SetsNull, []string{testSchema + ".staff"}) || len(e.Cascades) != 0 {
		t.Fatalf("staff effects %+v, %v", e, err)
	}
}

// A partition opened as a table of its own: the foreign keys that point at
// its partitioned table act on its rows too, and the confirmation says so.
func TestDeleteEdgePartitions(t *testing.T) {
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".events (id int, day date, PRIMARY KEY (id, day)) PARTITION BY RANGE (day)",
		"CREATE TABLE "+testSchema+".events_2024 PARTITION OF "+testSchema+".events FOR VALUES FROM ('2024-01-01') TO ('2025-01-01')",
		"CREATE TABLE "+testSchema+".events_2025 PARTITION OF "+testSchema+".events FOR VALUES FROM ('2025-01-01') TO ('2026-01-01')",
		"CREATE TABLE "+testSchema+".attendees (id int PRIMARY KEY, event_id int, event_day date, FOREIGN KEY (event_id, event_day) REFERENCES "+testSchema+".events ON DELETE CASCADE)",
		"INSERT INTO "+testSchema+".events VALUES (1, '2024-05-01'), (1, '2025-05-01'), (2, '2025-06-01')",
		// A trigger on the partitioned table (copied onto each partition), and one on a partition only.
		"CREATE FUNCTION "+testSchema+".noop() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RETURN OLD; END'",
		"CREATE TRIGGER on_all AFTER DELETE ON "+testSchema+".events FOR EACH ROW EXECUTE FUNCTION "+testSchema+".noop()",
		"CREATE TRIGGER on_2025 AFTER DELETE ON "+testSchema+".events_2025 FOR EACH ROW EXECUTE FUNCTION "+testSchema+".noop()",
		"INSERT INTO "+testSchema+".attendees VALUES (10, 1, '2024-05-01'), (11, 1, '2025-05-01'), (12, 2, '2025-06-01')")
	want := []string{testSchema + ".attendees"}

	for _, table := range []string{"events", "events_2025"} {
		e, err := s.DeleteEffects(t.Context(), *tabPage(t, s, table).Target)
		if err != nil || !slices.Equal(e.Cascades, want) || !slices.Equal(e.Triggers, []string{"on_2025", "on_all"}) {
			t.Fatalf("%s: effects %+v, %v; want cascades %v and triggers on_2025, on_all", table, e, err, want)
		}
	}
	if e, err := s.DeleteEffects(t.Context(), *tabPage(t, s, "events_2024").Target); err != nil || !slices.Equal(e.Triggers, []string{"on_all"}) {
		t.Fatalf("events_2024: effects %+v, %v", e, err)
	}

	// From the partitioned table: (1, 2025-05-01) only, not (1, 2024-05-01).
	p := tabPage(t, s, "events")
	deleteAll(t, s, p.Target, sent(p, pageRow(t, p, "day", "2025-05-01")))
	assertRows(t, s, "SELECT id, day::text FROM "+testSchema+".events ORDER BY day", "1|2024-05-01", "2|2025-06-01")
	assertRows(t, s, "SELECT id FROM "+testSchema+".attendees ORDER BY id", "10", "12")

	// From the partition itself.
	p = tabPage(t, s, "events_2025")
	deleteAll(t, s, p.Target, sent(p, 0))
	assertRows(t, s, "SELECT id FROM "+testSchema+".attendees ORDER BY id", "10")

	// A row moved to another partition since: refused.
	p = tabPage(t, s, "events")
	exec(t, s, "DELETE FROM "+testSchema+".attendees", "UPDATE "+testSchema+".events SET day = '2025-05-01' WHERE id = 1")
	refuseAll(t, s, p.Target, "wasn't found", sent(p, 0))
}

// --- Triggers and rules that stop, refuse or add to a delete ---

func TestDeleteEdgeTriggersAndRules(t *testing.T) {
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".log (msg text)",
		"CREATE TABLE "+testSchema+".mandates (id int PRIMARY KEY, status text)",
		"INSERT INTO "+testSchema+".mandates VALUES (1, 'draft'), (2, 'locked'), (3, 'active'), (4, 'draft'), (5, 'draft')",
		// Locked mandates are silently kept (the trigger returns NULL).
		`CREATE FUNCTION `+testSchema+`.keep_locked() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN IF OLD.status = 'locked' THEN RETURN NULL; END IF; RETURN OLD; END $$`,
		"CREATE TRIGGER keep_locked BEFORE DELETE ON "+testSchema+".mandates FOR EACH ROW EXECUTE FUNCTION "+testSchema+".keep_locked()",
		// Active mandates are refused with a message.
		`CREATE FUNCTION `+testSchema+`.refuse_active() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN IF OLD.status = 'active' THEN RAISE EXCEPTION 'mandate % is active', OLD.id; END IF; RETURN OLD; END $$`,
		"CREATE TRIGGER refuse_active BEFORE DELETE ON "+testSchema+".mandates FOR EACH ROW EXECUTE FUNCTION "+testSchema+".refuse_active()",
		// Every delete is logged; deleting 4 also deletes 5.
		`CREATE FUNCTION `+testSchema+`.after_delete() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN INSERT INTO `+testSchema+`.log VALUES ('deleted ' || OLD.id);
			IF OLD.id = 4 THEN DELETE FROM `+testSchema+`.mandates WHERE id = 5; END IF; RETURN OLD; END $$`,
		"CREATE TRIGGER after_delete AFTER DELETE ON "+testSchema+".mandates FOR EACH ROW EXECUTE FUNCTION "+testSchema+".after_delete()",
		// Not listed: disabled, or firing only on replicas.
		"CREATE TRIGGER off BEFORE DELETE ON "+testSchema+".mandates FOR EACH ROW EXECUTE FUNCTION "+testSchema+".keep_locked()",
		"ALTER TABLE "+testSchema+".mandates DISABLE TRIGGER off",
		"CREATE TRIGGER replica_only BEFORE DELETE ON "+testSchema+".mandates FOR EACH ROW EXECUTE FUNCTION "+testSchema+".keep_locked()",
		"ALTER TABLE "+testSchema+".mandates ENABLE REPLICA TRIGGER replica_only",
		// A soft-delete table: a rule turns DELETE into an UPDATE.
		"CREATE TABLE "+testSchema+".soft (id int PRIMARY KEY, deleted bool NOT NULL DEFAULT false)",
		"INSERT INTO "+testSchema+".soft VALUES (1), (2)",
		"CREATE RULE soft_delete AS ON DELETE TO "+testSchema+".soft DO INSTEAD UPDATE "+testSchema+".soft SET deleted = true WHERE id = OLD.id",
		// A table whose deletes do nothing at all.
		"CREATE TABLE "+testSchema+".keep (id int PRIMARY KEY)",
		"INSERT INTO "+testSchema+".keep VALUES (1)",
		"CREATE RULE keep_all AS ON DELETE TO "+testSchema+".keep DO INSTEAD NOTHING")

	p := tabPage(t, s, "mandates")
	row := func(id string) RowDelete { return sent(p, pageRow(t, p, "id", id)) }

	e, err := s.DeleteEffects(t.Context(), *p.Target)
	if err != nil || !slices.Equal(e.Triggers, []string{"after_delete", "keep_locked", "refuse_active"}) {
		t.Fatalf("effects %+v, %v", e, err)
	}

	refuseAll(t, s, p.Target, "a trigger, a rule or a row security policy kept it", row("1"), row("2"))
	refuseAll(t, s, p.Target, "Mandate 3 is active", row("1"), row("3"))

	// 4's trigger deletes 5, which was picked too: both go, each logged once.
	deleteAll(t, s, p.Target, row("1"), row("4"), row("5"))
	assertRows(t, s, "SELECT id FROM "+testSchema+".mandates ORDER BY id", "2", "3")
	assertRows(t, s, "SELECT msg FROM "+testSchema+".log ORDER BY msg", "deleted 1", "deleted 4", "deleted 5")

	// Rules: listed, and a delete they turn into something else (or nothing) is refused.
	for _, c := range []struct{ table, rule string }{{"soft", "soft_delete"}, {"keep", "keep_all"}} {
		p := tabPage(t, s, c.table)
		e, err := s.DeleteEffects(t.Context(), *p.Target)
		if err != nil || !slices.Equal(e.Rules, []string{c.rule}) || !strings.Contains(e.Blocked, "delete rules ("+c.rule+")") {
			t.Fatalf("%s: effects %+v, %v", c.table, e, err)
		}
		refuseAll(t, s, p.Target, "rules that change what deleting does ("+c.rule+")", sent(p, 0))
	}
}

// A row security policy that hides a row from DELETE (not from SELECT).
func TestDeleteEdgeRowSecurity(t *testing.T) {
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".secured (id int PRIMARY KEY, v text)",
		"INSERT INTO "+testSchema+".secured VALUES (1, 'a'), (2, 'b')",
		"DROP ROLE IF EXISTS spydb_delete_rls",
		"CREATE ROLE spydb_delete_rls",
		"GRANT USAGE ON SCHEMA "+testSchema+" TO spydb_delete_rls",
		"GRANT SELECT, UPDATE, DELETE ON "+testSchema+".secured TO spydb_delete_rls",
		"ALTER TABLE "+testSchema+".secured ENABLE ROW LEVEL SECURITY",
		"CREATE POLICY see_all ON "+testSchema+".secured FOR SELECT USING (true)",
		"CREATE POLICY update_all ON "+testSchema+".secured FOR UPDATE USING (true)",
		"CREATE POLICY delete_one ON "+testSchema+".secured FOR DELETE USING (id = 1)")
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+testSchema+" CASCADE")
		s.pool.Exec(context.Background(), "DROP ROLE IF EXISTS spydb_delete_rls")
	})
	p := tabPage(t, s, "secured")
	target := *p.Target
	target.Session = map[string]string{"role": "spydb_delete_rls"}
	refuseAll(t, s, &target, "row security policy", sent(p, 0), sent(p, 1))
	deleteAll(t, s, &target, sent(p, pageRow(t, p, "id", "1")))
	assertRows(t, s, "SELECT id FROM "+testSchema+".secured", "2")
}

// --- Names that need quoting, including the delete's own alias ---

func TestDeleteEdgeNames(t *testing.T) {
	const schema = `Spydb "Weird" Schema`
	s := connectForTest(t)
	q := pgx.Identifier{schema}.Sanitize()
	exec(t, s, "DROP SCHEMA IF EXISTS "+q+" CASCADE", "CREATE SCHEMA "+q,
		`CREATE TABLE `+q+`."Ta""ble.x" ("select" int PRIMARY KEY, "Col With Space" text, "ÜmlautCol" text, "spydb_row" text, "a""quote" text)`,
		`INSERT INTO `+q+`."Ta""ble.x" VALUES (1, 'a', 'ü', 'r1', 'q'), (2, 'b', 'ö', 'r2', NULL), (3, 'c', 'ä', 'r3', '"')`,
		`CREATE TABLE `+q+`."child ""rows""" (id int PRIMARY KEY, ref int REFERENCES `+q+`."Ta""ble.x" ON DELETE CASCADE)`,
		`INSERT INTO `+q+`."child ""rows""" VALUES (1, 2)`)
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+q+" CASCADE") })

	p, err := s.TableData(t.Context(), TableQuery{Schema: schema, Table: `Ta"ble.x`, PageSize: 100})
	if err != nil || p.Target == nil {
		t.Fatalf("page: %v (%s)", err, p.ReadOnly)
	}
	e, err := s.DeleteEffects(t.Context(), *p.Target)
	if err != nil || !slices.Equal(e.Cascades, []string{`"Spydb ""Weird"" Schema"."child ""rows"""`}) {
		t.Fatalf("effects %+v, %v", e, err)
	}
	deleteAll(t, s, p.Target, sent(p, pageRow(t, p, "select", "2")), sent(p, pageRow(t, p, "select", "3")))
	assertRows(t, s, `SELECT "select" FROM `+q+`."Ta""ble.x"`, "1")
	assertRows(t, s, `SELECT count(*) FROM `+q+`."child ""rows"""`, "0")
}

// --- Requests the grid should never send: still never a wrong delete ---

func TestDeleteEdgeBadRequests(t *testing.T) {
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".people (id int PRIMARY KEY, name text)",
		"INSERT INTO "+testSchema+".people VALUES (1, 'Asha'), (2, 'Ravi'), (3, 'Asha')",
		"CREATE VIEW "+testSchema+".people_view AS SELECT * FROM "+testSchema+".people")
	p := tabPage(t, s, "people")
	one, two, three := sent(p, pageRow(t, p, "id", "1")), sent(p, pageRow(t, p, "id", "2")), sent(p, pageRow(t, p, "id", "3"))

	// Row 1's key with row 3's values (same name, other id): never row 3, never row 1.
	mixed := RowDelete{Key: one.Key, Old: three.Old}
	refuseAll(t, s, p.Target, "", mixed)
	// Values without the key that should come with them.
	refuseAll(t, s, p.Target, "missing its id", RowDelete{Key: map[string]*string{}, Old: one.Old})
	refuseAll(t, s, p.Target, "missing its id", RowDelete{Key: map[string]*string{"id": nil}, Old: one.Old})
	// A column the table doesn't have.
	bad := RowDelete{Key: two.Key, Old: map[string]*string{"name": str("Ravi"), "nope": str("x")}}
	refuseAll(t, s, p.Target, "Unknown column", bad)
	// A NULL shown where there's a value.
	refuseAll(t, s, p.Target, "changed by someone else", RowDelete{Key: two.Key, Old: map[string]*string{"id": str("2"), "name": nil}})

	// The target says the wrong key, or names another kind of relation.
	wrongKind := *p.Target
	wrongKind.Kind, wrongKind.Key = "ctid", []string{ctidKey, md5Key}
	refuseAll(t, s, &wrongKind, "has a primary key now", two)
	wrongKey := *p.Target
	wrongKey.Key = []string{"name"}
	refuseAll(t, s, &wrongKey, "primary key changed", two)
	view := *p.Target
	view.Table, view.OID = "people_view", 0
	refuseAll(t, s, &view, "a view", two)

	assertRows(t, s, "SELECT id FROM "+testSchema+".people ORDER BY id", "1", "2", "3")
}

// A role that may delete but not update: SpyDB can't lock the rows to check
// them, so the confirmation says it won't delete (and DeleteRows refuses).
func TestDeleteEdgeDeleteOnlyRole(t *testing.T) {
	s := editFixture(t,
		"CREATE TABLE "+testSchema+".logs (id int PRIMARY KEY, v text)",
		"INSERT INTO "+testSchema+".logs VALUES (1, 'a')",
		"DROP ROLE IF EXISTS spydb_delete_only",
		"CREATE ROLE spydb_delete_only",
		"GRANT USAGE ON SCHEMA "+testSchema+" TO spydb_delete_only",
		"GRANT SELECT, DELETE ON "+testSchema+".logs TO spydb_delete_only")
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+testSchema+" CASCADE")
		s.pool.Exec(context.Background(), "DROP ROLE IF EXISTS spydb_delete_only")
	})
	p := tabPage(t, s, "logs")
	target := *p.Target
	target.Session = map[string]string{"role": "spydb_delete_only"}
	e, err := s.DeleteEffects(t.Context(), target)
	if err != nil || !strings.Contains(e.Blocked, "may delete here but not update") {
		t.Fatalf("effects %+v, %v", e, err)
	}
	refuseAll(t, s, &target, "ermission denied", sent(p, 0))
}

// Under a case-insensitive collation 'abc' equals 'ABC'; a value changed
// only in case since it was loaded must still count as changed.
func TestDeleteEdgeCaseOnlyChange(t *testing.T) {
	s := editFixture(t,
		"CREATE COLLATION "+testSchema+".ci (provider = icu, locale = 'und-u-ks-level2', deterministic = false)",
		"CREATE TABLE "+testSchema+".names (id int PRIMARY KEY, name text COLLATE "+testSchema+".ci)",
		"INSERT INTO "+testSchema+".names VALUES (1, 'abc'), (2, 'x')")
	p := tabPage(t, s, "names")
	exec(t, s, "UPDATE "+testSchema+".names SET name = 'ABC' WHERE id = 1")
	refuseAll(t, s, p.Target, "changed by someone else since it was loaded (name)", sent(p, pageRow(t, p, "id", "1")))
	deleteAll(t, s, p.Target, sent(p, pageRow(t, p, "id", "2")))
}

// A failed COMMIT: refused by Postgres means nothing was deleted; a lost
// connection means nobody can tell.
func TestDeleteCommitError(t *testing.T) {
	refused := commitError(&pgconn.PgError{Code: "23503", Message: "update or delete on table violates foreign key constraint"})
	if !strings.Contains(refused.Error(), nothingDeleted) {
		t.Errorf("refused by Postgres: %v", refused)
	}
	lost := commitError(errors.New("read tcp 10.0.0.1:5432: connection reset by peer"))
	if strings.Contains(lost.Error(), nothingDeleted) || !strings.Contains(lost.Error(), "can't tell whether the rows were deleted") {
		t.Errorf("connection lost: %v", lost)
	}
	if stopped := commitError(context.Canceled); strings.Contains(stopped.Error(), nothingDeleted) {
		t.Errorf("stopped while committing: %v", stopped)
	}
}

// Disconnect closes the editor's own connection instead of handing it back
// to the pool, where a save or delete could run with what the editor SET.
func TestDisconnectClosesEditorSession(t *testing.T) {
	s := connectForTest(t)
	if _, err := s.RunQuery(t.Context(), "tab", "SET statement_timeout = '1s'"); err != nil {
		t.Fatal(err)
	}
	pid := s.editor.Conn().PgConn().PID()
	watcher := connectForTest(t)
	s.Disconnect()
	for i := 0; ; i++ {
		var alive bool
		if err := watcher.pool.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid = $1)", int(pid)).Scan(&alive); err != nil {
			t.Fatal(err)
		}
		if !alive {
			break
		}
		if i == 50 {
			t.Fatal("the editor's session is still open after Disconnect")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Identical rows share one fingerprint; only the address tells them apart.
// When addresses change after the page loaded, a picked row is deleted only
// if the row now at its address has its fingerprint, i.e. is an identical
// copy (same data, so the table ends up as asked); otherwise it's refused.
func TestDeleteEdgeIdenticalRowsMoved(t *testing.T) {
	s := editFixture(t, "CREATE TABLE "+testSchema+".twins (name text, friend text, n int)")
	twins := testSchema + ".twins"
	all := "SELECT name, friend, n FROM " + twins + " ORDER BY friend, n"
	layout := "SELECT ctid::text || ' ' || friend FROM " + twins + " ORDER BY ctid"
	at := func(p TablePage, ctid string) int { // the page's row loaded at that address
		t.Helper()
		for i, k := range p.Keys {
			if *k[0] == ctid {
				return i
			}
		}
		t.Fatalf("no row at %s", ctid)
		return -1
	}

	// 1. One of two identical copies updated to the very same values: it
	// moved to a new address, so deleting it as loaded is refused, and the
	// untouched copy can still be deleted.
	exec(t, s, "INSERT INTO "+twins+" VALUES ('anuj', 'vishal', 8), ('anuj', 'ashish', 18), ('anuj', 'ashish', 18)")
	p := tabPage(t, s, "twins")
	assertRows(t, s, layout, "(0,1) vishal", "(0,2) ashish", "(0,3) ashish")
	exec(t, s, "UPDATE "+twins+" SET n = n WHERE ctid = '(0,3)'")
	assertRows(t, s, layout, "(0,1) vishal", "(0,2) ashish", "(0,4) ashish")
	refuseAll(t, s, p.Target, "changed or deleted", sent(p, at(p, "(0,3)")))
	deleteAll(t, s, p.Target, sent(p, at(p, "(0,2)")))
	assertRows(t, s, all, "anuj|ashish|18", "anuj|vishal|8")

	// 2. VACUUM FULL packs the table again, shifting every address.
	exec(t, s, "TRUNCATE "+twins,
		"INSERT INTO "+twins+" VALUES ('anuj', 'gone', 0), ('anuj', 'ashish', 18), ('anuj', 'ashish', 18), ('anuj', 'vishal', 8)")
	p = tabPage(t, s, "twins")
	assertRows(t, s, layout, "(0,1) gone", "(0,2) ashish", "(0,3) ashish", "(0,4) vishal")
	exec(t, s, "DELETE FROM "+twins+" WHERE friend = 'gone'", "VACUUM FULL "+twins)
	assertRows(t, s, layout, "(0,1) ashish", "(0,2) ashish", "(0,3) vishal")

	// A copy loaded at (0,3): vishal sits there now, another fingerprint. Refused.
	refuseAll(t, s, p.Target, "changed or deleted", sent(p, at(p, "(0,3)")))
	// vishal loaded at (0,4): nothing there now. Refused.
	refuseAll(t, s, p.Target, "changed or deleted", sent(p, at(p, "(0,4)")))
	// Both copies at once, (0,3) among them: refused as a whole, nothing deleted.
	refuseAll(t, s, p.Target, "changed or deleted", pick(p, at(p, "(0,2)"), at(p, "(0,3)"))...)
	// A copy loaded at (0,2): an identical copy sits there now, same
	// fingerprint, so it goes. Same data: one ashish is left, as asked.
	deleteAll(t, s, p.Target, sent(p, at(p, "(0,2)")))
	assertRows(t, s, all, "anuj|ashish|18", "anuj|vishal|8")
}
