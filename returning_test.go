package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFindReturnable(t *testing.T) {
	cases := []struct {
		sql  string
		want []string // table@ordinal
	}{
		{"update accounts set owner = 'x' where id = 1;", []string{`"accounts"@0`}},
		{"UPDATE ONLY public.accounts AS a SET owner = 'x'", []string{`"public"."accounts"@0`}},
		{`update "Weird ""Name""" set a = 1`, []string{`"Weird ""Name"""@0`}},
		{"update Accounts set a = 1", []string{`"accounts"@0`}},
		{"update accounts set owner = 'x' returning id", nil},
		{"update accounts set owner = 'returning' where id = 1", []string{`"accounts"@0`}},
		{"-- returning\nupdate accounts set a = 1 /* returning */", []string{`"accounts"@0`}},
		{"update a set x = (select y from b where b.id = a.id)", []string{`"a"@0`}},
		{"begin; update a set x = 1; update b set y = 2 returning *; update c set z = 3; commit;",
			[]string{`"a"@0`, `"c"@2`}},
		// WITH … UPDATE also says "UPDATE n", so it counts, but it's left alone.
		{"with t as (select 1) update a set x = 1; update b set y = 2", []string{`"b"@1`}},
		{"select 1; insert into a values (1); delete from b; update c set z = 1", []string{`"c"@0`}},
		// Anything that could change what a name means stops the search.
		{"set search_path = x; update a set x = 1", nil},
		{"update a set x = 1; set role r; update b set y = 2", []string{`"a"@0`}},
		{"select set_config('search_path', 'x', false); update a set x = 1", nil},
		{"create table n (id int); update n set id = 1", nil},
		{"update a.b.c set x = 1", nil},
		{"update a", nil},
		{"delete from a", nil},
		{"insert into a values (1) on conflict (id) do update set x = 1", nil},
		{"update a set x = 'unterminated", nil},
	}
	for _, c := range cases {
		var got []string
		for _, r := range findReturnable(c.sql, false) {
			got = append(got, fmt.Sprintf("%s@%d", r.table, r.ordinal))
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%q: got %v, want %v", c.sql, got, c.want)
		}
	}
}

func TestAddReturning(t *testing.T) {
	cases := []struct {
		sql  string
		safe []bool
		want string
	}{
		{"update a set x = 1;", []bool{true}, "update a set x = 1 RETURNING *;"},
		{"update a set x = 1 -- note", []bool{true}, "update a set x = 1 RETURNING * -- note"},
		{"update a set x = 1; update b set y = 2", []bool{true, true}, "update a set x = 1 RETURNING *; update b set y = 2 RETURNING *"},
		{"update a set x = 1; update b set y = 2", []bool{false, true}, "update a set x = 1; update b set y = 2 RETURNING *"},
		{"update a set x = 1", []bool{false}, "update a set x = 1"},
	}
	for _, c := range cases {
		plan, got := addReturning(c.sql, findReturnable(c.sql, false), c.safe)
		if got != c.want {
			t.Errorf("%q: got %q, want %q", c.sql, got, c.want)
		}
		if strings.Count(got, returningClause) != len(plan.added) {
			t.Errorf("%q: %d insertions recorded", c.sql, len(plan.added))
		}
	}
}

func TestReturningOriginalPosition(t *testing.T) {
	sql := "update a set x = 'é' where id = 1; update b set y = 2; select nope from b"
	plan, changed := addReturning(sql, findReturnable(sql, false), []bool{true, true})
	at := func(s, word string) int { return utf8.RuneCountInString(s[:strings.Index(s, word)]) + 1 }
	for _, word := range []string{"where", "update b", "nope"} {
		if got, want := plan.originalPosition(at(changed, word)), at(sql, word); got != want {
			t.Errorf("%q: position %d maps to %d, want %d", word, at(changed, word), got, want)
		}
	}
	if got := plan.originalPosition(at(changed, "RETURNING")); got != 0 {
		t.Errorf("inside RETURNING: got %d, want 0", got)
	}
	if got := plan.originalPosition(0); got != 0 {
		t.Errorf("unknown position: got %d", got)
	}
}

// returningFixture has a table to update, with two rows.
func returningFixture(t *testing.T, setup ...string) *DBService {
	t.Helper()
	return editFixture(t, append([]string{
		"CREATE TABLE " + testSchema + ".acct (id int PRIMARY KEY, owner text, balance numeric)",
		"INSERT INTO " + testSchema + ".acct VALUES (1, 'alice', 1000), (2, 'bob', 500)",
	}, setup...)...)
}

// run runs sql like the query tab and returns every statement's result.
func run(t *testing.T, s *DBService, sql string) []StatementResult {
	t.Helper()
	result, err := s.RunQuery(t.Context(), "tab", sql)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return result.Results
}

// cells is a result's rows as "a|b|c" strings.
func cells(r StatementResult) []string {
	var out []string
	for _, row := range r.Rows {
		parts := make([]string, len(row))
		for i, v := range row {
			parts[i] = "NULL"
			if v != nil {
				parts[i] = *v
			}
		}
		out = append(out, strings.Join(parts, "|"))
	}
	return out
}

func TestReturningShowsUpdatedRows(t *testing.T) {
	s := returningFixture(t)
	r := run(t, s, "update "+testSchema+".acct set owner = 'hello kak', balance = balance + 1 where id = 1")
	if len(r) != 1 || r[0].Command != "UPDATE 1" || r[0].ReadOnly != returningNote || r[0].Target != nil {
		t.Fatalf("got %+v", r)
	}
	if got := cells(r[0]); !slices.Equal(got, []string{"1|hello kak|1001"}) {
		t.Fatalf("rows %v", got)
	}
	// Ran once: the balance went up by 1, not 2.
	if got := table(t, s, "SELECT owner, balance::text FROM "+testSchema+".acct WHERE id = 1"); got[0] != "hello kak|1001" {
		t.Fatalf("table %v", got)
	}
	// No rows matched: still a result, with the columns and no rows.
	r = run(t, s, "update "+testSchema+".acct set owner = 'x' where id = 99")
	if r[0].Command != "UPDATE 0" || len(r[0].Columns) != 3 || len(r[0].Rows) != 0 {
		t.Fatalf("no match: %+v", r[0])
	}
}

func TestReturningInsideTransaction(t *testing.T) {
	s := returningFixture(t)
	// Registered last, so it runs first: never leave the transaction (and its
	// locks) open for the schema cleanup to wait on.
	t.Cleanup(func() { s.RunQuery(context.Background(), "tab", "rollback") })
	run(t, s, "begin")
	r := run(t, s, "update "+testSchema+".acct set balance = balance + 1 where id = 1")
	if got := cells(r[0]); r[0].ReadOnly != returningNote || !slices.Equal(got, []string{"1|alice|1001"}) {
		t.Fatalf("got %v (%q)", got, r[0].ReadOnly)
	}
	if status := s.editor.Conn().PgConn().TxStatus(); status != 'T' {
		t.Fatalf("transaction status %c, want still open", status)
	}
	// Other sessions don't see it yet; the editor does.
	if got := table(t, s, "SELECT balance::text FROM "+testSchema+".acct WHERE id = 1"); got[0] != "1000" {
		t.Fatalf("pool sees %v before commit", got)
	}
	// The lookup's savepoint is gone.
	if _, err := s.RunQuery(t.Context(), "tab", "release savepoint spydb_returning"); err == nil {
		t.Fatal("the savepoint is still there")
	}
	run(t, s, "rollback")
	if got := table(t, s, "SELECT balance::text FROM "+testSchema+".acct WHERE id = 1"); got[0] != "1000" {
		t.Fatalf("after rollback %v", got)
	}
}

func TestReturningMultiStatement(t *testing.T) {
	s := returningFixture(t)
	acct := testSchema + ".acct"
	r := run(t, s, "begin; update "+acct+" set balance = balance + 1 where id = 1; update "+acct+
		" set balance = balance + 1 where id = 2 returning id; select 1; update "+acct+" set owner = 'q' where id = 99; commit;")
	if len(r) != 6 {
		t.Fatalf("%d results", len(r))
	}
	if r[1].ReadOnly != returningNote || !slices.Equal(cells(r[1]), []string{"1|alice|1001"}) {
		t.Fatalf("first update: %q %v", r[1].ReadOnly, cells(r[1]))
	}
	if r[2].ReadOnly == returningNote || !slices.Equal(cells(r[2]), []string{"2"}) {
		t.Fatalf("your own RETURNING: %q %v", r[2].ReadOnly, cells(r[2]))
	}
	if r[4].ReadOnly != returningNote || r[4].Command != "UPDATE 0" || len(r[4].Columns) != 3 {
		t.Fatalf("third update: %+v", r[4])
	}
	if got := table(t, s, "SELECT balance::text FROM "+acct+" ORDER BY id"); !slices.Equal(got, []string{"1001", "501"}) {
		t.Fatalf("table %v", got)
	}
}

func TestReturningErrorPosition(t *testing.T) {
	s := returningFixture(t)
	sql := "update " + testSchema + ".acct set owner = 'é' where id = 1; select nope from " + testSchema + ".acct"
	_, err := s.RunQuery(t.Context(), "tab", sql)
	qe, ok := err.(*QueryError)
	if !ok {
		t.Fatalf("got %v", err)
	}
	if want := utf8.RuneCountInString(sql[:strings.Index(sql, "nope")]) + 1; qe.Position != want {
		t.Fatalf("position %d, want %d (%s)", qe.Position, want, qe.Message)
	}
	// One run is one transaction: the failed SELECT undid the UPDATE.
	if got := table(t, s, "SELECT owner FROM "+testSchema+".acct WHERE id = 1"); got[0] != "alice" {
		t.Fatalf("table %v", got)
	}
}

func TestReturningLeavesUnsafeTargetsAlone(t *testing.T) {
	sc := testSchema
	s := returningFixture(t,
		"CREATE VIEW "+sc+".acct_view AS SELECT * FROM "+sc+".acct",
		"CREATE TABLE "+sc+".ruled (id int, owner text)",
		"INSERT INTO "+sc+".ruled VALUES (2, 'bob')",
		"CREATE RULE ruled_upd AS ON UPDATE TO "+sc+".ruled DO INSTEAD UPDATE "+sc+".acct SET owner = NEW.owner WHERE id = OLD.id",
		"CREATE TABLE "+sc+".secured (id int, owner text)",
		"INSERT INTO "+sc+".secured VALUES (1, 'a')",
		"ALTER TABLE "+sc+".secured ENABLE ROW LEVEL SECURITY",
		"DROP ROLE IF EXISTS spydb_ret_limited",
		"CREATE ROLE spydb_ret_limited",
		"GRANT USAGE ON SCHEMA "+sc+" TO spydb_ret_limited",
		"GRANT UPDATE ON "+sc+".acct TO spydb_ret_limited")
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+testSchema+" CASCADE")
		s.pool.Exec(context.Background(), "DROP ROLE IF EXISTS spydb_ret_limited")
	})
	for _, sql := range []string{
		"update " + sc + ".acct_view set owner = 'v' where id = 2", // a view
		"update " + sc + ".ruled set owner = 'r'",                 // a rule without RETURNING: RETURNING * would fail
		"update " + sc + ".secured set owner = 's'",               // row-level security
		"update " + sc + ".missing set owner = 'm'",               // no such table: Postgres says so, as written
	} {
		result, err := s.RunQuery(t.Context(), "tab", sql)
		if strings.Contains(sql, "missing") {
			if err == nil || !strings.Contains(err.Error(), "does not exist") {
				t.Fatalf("%s: %v", sql, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		if r := result.Results[0]; len(r.Columns) != 0 || !strings.HasPrefix(r.Command, "UPDATE") {
			t.Fatalf("%s: changed into %+v", sql, r)
		}
	}
	// Can update but not read: RETURNING * would be refused, so it runs as written.
	run(t, s, "set role spydb_ret_limited")
	r := run(t, s, "update "+sc+".acct set owner = 'z'")
	run(t, s, "reset role")
	if len(r[0].Columns) != 0 || r[0].Command != "UPDATE 2" {
		t.Fatalf("no SELECT privilege: %+v", r[0])
	}
}

func TestReturningStopsAtSetConfig(t *testing.T) {
	s := returningFixture(t)
	r := run(t, s, "select set_config('search_path', '"+testSchema+", public', false); update acct set owner = 'x' where id = 1")
	run(t, s, "reset search_path")
	if len(r) != 2 || len(r[1].Columns) != 0 || r[1].Command != "UPDATE 1" {
		t.Fatalf("got %+v", r)
	}
}
