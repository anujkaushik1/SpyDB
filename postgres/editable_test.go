package postgres

import (
	"slices"
	"strings"
	"testing"
)

func TestAnalyzeSelectPlain(t *testing.T) {
	tests := []struct {
		sql       string
		table     []string
		qualifier string
		columns   []string // item columns; "*" for a star
	}{
		{"SELECT * FROM users", []string{"users"}, "users", []string{"*"}},
		{"select * from users;", []string{"users"}, "users", []string{"*"}},
		{"SELECT * FROM users;;  ", []string{"users"}, "users", []string{"*"}},
		{"SELECT name, email FROM public.users", []string{"public", "users"}, "users", []string{"name", "email"}},
		{"SELECT u.name FROM users u WHERE u.id > 5", []string{"users"}, "u", []string{"name"}},
		{"SELECT u.* FROM users AS u", []string{"users"}, "u", []string{"*"}},
		{`SELECT "Name" FROM "Sales"."Order Items" oi`, []string{`"Sales"`, `"Order Items"`}, "oi", []string{"Name"}},
		{`SELECT "a""b" FROM t`, []string{"t"}, "t", []string{`a"b`}},
		{"SELECT Name AS n, EMAIL e FROM Users", []string{"Users"}, "Users", []string{"name", "email"}},
		{"SELECT public.users.name FROM public.users", []string{"public", "users"}, "users", []string{"name"}},
		{"SELECT ALL name FROM t", []string{"t"}, "t", []string{"name"}},
		{"SELECT *, name FROM t", []string{"t"}, "t", []string{"*", "name"}},
		// Anything inside WHERE / ORDER BY parentheses only filters or sorts.
		{"SELECT name FROM t WHERE id IN (SELECT id FROM u JOIN v ON true GROUP BY id UNION SELECT 1)", []string{"t"}, "t", []string{"name"}},
		{"SELECT name FROM t WHERE EXISTS (SELECT 1 FROM t t2 WHERE t2.x = t.x) ORDER BY lower(name) DESC LIMIT 10 OFFSET 5", []string{"t"}, "t", []string{"name"}},
		{"SELECT name FROM t WHERE a IS DISTINCT FROM b AND c BETWEEN 1 AND 2 FOR UPDATE SKIP LOCKED", []string{"t"}, "t", []string{"name"}},
		{"SELECT name FROM t FETCH FIRST 3 ROWS ONLY", []string{"t"}, "t", []string{"name"}},
		{"SELECT name FROM t WHERE t.group = 1 AND t.union = 2", []string{"t"}, "t", []string{"name"}},
		// Keywords in strings, quoted names and comments don't count.
		{"SELECT name FROM t WHERE note = 'x UNION SELECT 1 FROM y JOIN z' AND \"group\" = 'GROUP BY'", []string{"t"}, "t", []string{"name"}},
		{"SELECT name FROM t WHERE note = $$ JOIN $$ AND x = $q$ GROUP $q$", []string{"t"}, "t", []string{"name"}},
		{"SELECT name FROM t WHERE note = E'it\\'s UNION' -- UNION SELECT\n", []string{"t"}, "t", []string{"name"}},
		{"/* SELECT x FROM a JOIN b; */ SELECT name /* , count(*) */ FROM t /* nested /* JOIN */ GROUP */", []string{"t"}, "t", []string{"name"}},
		{"SELECT name\n-- , other\nFROM t", []string{"t"}, "t", []string{"name"}},
		{"SELECT name FROM t WHERE s = 'it''s' ORDER BY 1", []string{"t"}, "t", []string{"name"}},
		// Expressions next to columns: the rows are still the table's rows.
		{"SELECT id, upper(name) AS shout, a + b, id::text, tags[1] FROM t", []string{"t"}, "t", []string{"id", "", "", "", ""}},
		{"SELECT name, extract(year FROM born), substring(name FROM 2), trim(both FROM name) FROM people p", []string{"people"}, "p", []string{"name", "", "", ""}},
		{"SELECT name, x IS DISTINCT FROM y AS changed FROM t", []string{"t"}, "t", []string{"name", ""}},
		{"SELECT name, (SELECT count(*) FROM u JOIN v ON true) FROM t", []string{"t"}, "t", []string{"name", ""}},
		{"SELECT name, row_number() OVER (ORDER BY id) FROM t", []string{"t"}, "t", []string{"name", ""}},
		{"SELECT name, CASE WHEN a THEN 1 END, date '2024-01-01', NOT a FROM t", []string{"t"}, "t", []string{"name", "", "", ""}},
		{"SELECT t.from, name FROM t", []string{"t"}, "t", []string{"from", "name"}},
		{"SELECT unnest(tags), name FROM t", []string{"t"}, "t", []string{"", "name"}},
	}
	for _, tc := range tests {
		shape, reason := analyzeSelect(tc.sql, false)
		if reason != "" {
			t.Errorf("%q: read-only (%s), want plain", tc.sql, reason)
			continue
		}
		var columns []string
		for _, item := range shape.items {
			if item.star {
				columns = append(columns, "*")
			} else {
				columns = append(columns, item.column)
			}
		}
		if !slices.Equal(shape.table, tc.table) || shape.qualifier != tc.qualifier || !slices.Equal(columns, tc.columns) {
			t.Errorf("%q: got table %v qualifier %q columns %v; want %v %q %v", tc.sql, shape.table, shape.qualifier, columns, tc.table, tc.qualifier, tc.columns)
		}
		if !strings.EqualFold(tc.sql[shape.insertAt:shape.insertAt+4], "FROM") {
			t.Errorf("%q: insertAt %d isn't the FROM", tc.sql, shape.insertAt)
		}
	}
}

func TestAnalyzeSelectReadOnly(t *testing.T) {
	tests := []struct{ sql, reason string }{
		{"", notPlainSelect},
		{"UPDATE t SET a = 1", notPlainSelect},
		{"INSERT INTO t VALUES (1) RETURNING *", notPlainSelect},
		{"WITH x AS (SELECT * FROM t) SELECT * FROM x", notPlainSelect},
		{"(SELECT * FROM t)", notPlainSelect},
		{"TABLE t", notPlainSelect},
		{"VALUES (1)", notPlainSelect},
		{"EXPLAIN SELECT * FROM t", notPlainSelect},
		{"SELECT * FROM t; SELECT * FROM u", "more than one statement"},
		{"BEGIN; SELECT * FROM t", "more than one statement"},
		{"SELECT * FROM t; DELETE FROM t", "more than one statement"},
		{"SELECT DISTINCT name FROM t", "DISTINCT"},
		{"SELECT DISTINCT ON (a) a, b FROM t", "DISTINCT"},
		// Nothing from the table to edit.
		{"SELECT count(*) FROM t", "no table columns in the result"},
		{"SELECT 1 FROM t", "no table columns in the result"},
		{"SELECT now(), 'x', a + b FROM t", "no table columns in the result"},
		{"SELECT FROM t", "no table columns in the result"},
		{"SELECT name INTO copy FROM t", "SELECT INTO"},
		{"SELECT * INTO copy FROM t", "SELECT INTO"},
		{"SELECT name", "no FROM table"},
		{"SELECT 1", "no FROM table"},
		{"SELECT a IS DISTINCT FROM b", "no FROM table"},
		{"SELECT * FROM a JOIN b ON a.id = b.id", "a join"},
		{"SELECT a.id, b.name FROM t a JOIN t b ON b.parent = a.id", "a join"},
		{"SELECT * FROM a LEFT JOIN b USING (id)", "a join"},
		{"SELECT * FROM a NATURAL JOIN b", "a join"},
		{"SELECT * FROM a CROSS JOIN b", "a join"},
		{"SELECT * FROM a, b", "a join"},
		{"SELECT * FROM a x, a y", "a join"},
		{"SELECT * FROM a INNER JOIN b ON true", "a join"},
		{"SELECT * FROM a WHERE x = 1 JOIN b ON true", "a join"},
		{"SELECT * FROM (SELECT * FROM t) s", "a subquery or function in FROM"},
		{"SELECT * FROM generate_series(1, 3) g", "a subquery or function in FROM"},
		{"SELECT * FROM LATERAL f(1)", "a subquery or function in FROM"},
		{"SELECT * FROM ONLY t", "FROM ONLY"},
		{"SELECT * FROM t TABLESAMPLE SYSTEM (10)", "TABLESAMPLE"},
		{"SELECT * FROM t AS x (a, b)", "column aliases on the table"},
		{"SELECT * FROM db.public.t", notPlainSelect},
		{"SELECT name FROM t GROUP BY name", "GROUP BY"},
		{"SELECT name FROM t WHERE a = 1 GROUP BY name", "GROUP BY"},
		{"SELECT name FROM t HAVING true", "HAVING"},
		{"SELECT name FROM t WINDOW w AS ()", "a WINDOW clause"},
		{"SELECT name FROM t UNION SELECT name FROM u", "UNION, INTERSECT or EXCEPT"},
		{"SELECT name FROM t WHERE a UNION ALL SELECT name FROM t", "UNION, INTERSECT or EXCEPT"},
		{"SELECT name FROM t EXCEPT SELECT name FROM u", "UNION, INTERSECT or EXCEPT"},
		{"SELECT name FROM t INTERSECT SELECT name FROM u", "UNION, INTERSECT or EXCEPT"},
		{"SELECT name FROM t ORDER BY name UNION SELECT 1", "UNION, INTERSECT or EXCEPT"},
		// Unterminated or unbalanced SQL: never guessed at.
		{"SELECT name FROM t WHERE a = 'open", notPlainSelect},
		{"SELECT name FROM t WHERE a = (1", notPlainSelect},
		{"SELECT name FROM t WHERE a = $x$ open", notPlainSelect},
		{"SELECT name FROM t /* open", notPlainSelect},
		{`SELECT "open FROM t`, notPlainSelect},
		// A number run into a word reads differently across Postgres versions.
		{"SELECT name FROM t WHERE a = 1union select name from u", notPlainSelect},
	}
	for _, tc := range tests {
		if _, reason := analyzeSelect(tc.sql, false); reason != tc.reason {
			t.Errorf("%q: reason %q, want %q", tc.sql, reason, tc.reason)
		}
	}
}

// With standard_conforming_strings off, \' inside '…' doesn't end the string.
func TestAnalyzeSelectBackslashQuotes(t *testing.T) {
	sql := `SELECT name FROM t WHERE a = 'x\' UNION SELECT name FROM u --'`
	if _, reason := analyzeSelect(sql, true); reason != "" {
		t.Errorf("backslash quotes on: got %q, want plain (the UNION is inside the string)", reason)
	}
	if _, reason := analyzeSelect(sql, false); reason == "" {
		t.Error("backslash quotes off: the UNION is real SQL, so it must be read-only")
	}
	sql = `SELECT name FROM t WHERE a = 'x\'' UNION SELECT name FROM u --'`
	if _, reason := analyzeSelect(sql, true); reason != "UNION, INTERSECT or EXCEPT" {
		t.Errorf("backslash quotes on: got %q, want the UNION found", reason)
	}
}

func TestAddColumns(t *testing.T) {
	sql := "SELECT name FROM t WHERE id = 1"
	shape, reason := analyzeSelect(sql, false)
	if reason != "" {
		t.Fatal(reason)
	}
	got, in := shape.addColumns(sql, []string{"t.ctid", rowHash("t")}, addedAliases("__spydb_key", 2))
	want := `SELECT name  , t.ctid AS "__spydb_key_1", pg_catalog.md5(pg_catalog.record_send(t.*)) AS "__spydb_key_2" FROM t WHERE id = 1`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	// Error positions after the added text move back; inside it they're dropped.
	where := strings.Index(got, "id = 1") + 1
	if p := in.originalPosition(where); p != strings.Index(sql, "id = 1")+1 {
		t.Errorf("position after: got %d", p)
	}
	if p := in.originalPosition(3); p != 3 {
		t.Errorf("position before: got %d", p)
	}
	if p := in.originalPosition(strings.Index(got, "ctid") + 1); p != 0 {
		t.Errorf("position inside: got %d, want 0", p)
	}
	// Characters, not bytes: Postgres counts characters.
	sql = "SELECT naïve FROM t WHERE x = 'é'"
	shape, _ = analyzeSelect(sql, false)
	got, in = shape.addColumns(sql, []string{"t.ctid"}, addedAliases("__spydb_key", 1))
	if p := in.originalPosition(len([]rune(got[:strings.Index(got, "'é'")])) + 1); p != len([]rune(sql[:strings.Index(sql, "'é'")]))+1 {
		t.Errorf("multibyte position: got %d", p)
	}
}

func TestShows(t *testing.T) {
	shape, _ := analyzeSelect(`SELECT a, "B", c AS id FROM t`, false)
	if !shape.shows([]string{"a", "B"}) || shape.shows([]string{"id"}) || shape.shows([]string{"b"}) {
		t.Error("shows matches column names, not aliases, with case as Postgres folds it")
	}
	shape, _ = analyzeSelect(`SELECT x.* FROM t x`, false)
	if !shape.shows([]string{"anything"}) {
		t.Error("a star shows every column")
	}
}

func TestDistinctKeys(t *testing.T) {
	s := func(v string) *string { return &v }
	if !distinctKeys([][]*string{{s("1"), s("a")}, {s("1"), s("b")}}) {
		t.Error("different composite keys are distinct")
	}
	if distinctKeys([][]*string{{s("1"), s("a")}, {s("1"), s("a")}}) {
		t.Error("repeated key")
	}
	if distinctKeys([][]*string{{s("1"), nil}}) {
		t.Error("a NULL in a key")
	}
	// The separator keeps ("ab","c") and ("a","bc") apart.
	if !distinctKeys([][]*string{{s("ab"), s("c")}, {s("a"), s("bc")}}) {
		t.Error("keys must not run together")
	}
}
