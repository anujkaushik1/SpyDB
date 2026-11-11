package postgres

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// Editing query results needs to know, for every result row, exactly which
// table row it is. That's only certain for a plain SELECT from one table: no
// joins, grouping, DISTINCT, set operations, CTEs or subqueries in FROM, so
// each result row is one table row. (Expressions in the select list don't
// change that; set-returning ones that repeat rows are caught afterwards, as
// repeated keys.) analyzeSelect checks the SQL
// has that shape. When the table's primary key isn't among the columns,
// RunQuery adds it (or, for a table without one, the row's address and a hash
// of the whole row) as hidden columns at the end of the select list.

// selectShape is a plain single-table SELECT, as analyzeSelect found it.
type selectShape struct {
	table      []string     // the table's name parts as written, e.g. [public users] or ["Orders"]
	qualifier  string       // how to refer to the table in the select list: its alias, else its name
	items      []selectItem // the select list
	insertAt   int          // byte offset of the top-level FROM; added columns go just before it
	maxOrdinal int          // the largest position in ORDER BY (ORDER BY 2), 0 if none
}

// width is how many columns the select list returns, given the table's count.
func (s selectShape) width(tableColumns int) int {
	n := 0
	for _, item := range s.items {
		if item.star {
			n += tableColumns
		} else {
			n++
		}
	}
	return n
}

type selectItem struct {
	star   bool   // * or t.*
	column string // the column's name as Postgres sees it (unquoted names folded to lower case); "" if unknown
}

// sqlToken is one token of SQL text.
type sqlToken struct {
	kind  byte   // 'w' word, 'q' quoted identifier, 's' string, 'n' number, '$' parameter, 'p' punctuation or operator
	text  string // as written
	start int    // byte offset in the SQL
	depth int    // how many ( or [ enclose it
}

func (t sqlToken) is(word string) bool { return t.kind == 'w' && strings.EqualFold(t.text, word) }
func (t sqlToken) punct(p string) bool { return t.kind == 'p' && t.text == p }
func (t sqlToken) ident() bool         { return t.kind == 'w' || t.kind == 'q' }

// Words that end a table reference instead of being its alias.
var clauseWords = map[string]bool{
	"WHERE": true, "ORDER": true, "LIMIT": true, "OFFSET": true, "FETCH": true, "FOR": true,
	"GROUP": true, "HAVING": true, "WINDOW": true, "UNION": true, "INTERSECT": true, "EXCEPT": true,
	"JOIN": true, "INNER": true, "LEFT": true, "RIGHT": true, "FULL": true, "CROSS": true, "NATURAL": true,
	"TABLESAMPLE": true, "ON": true, "USING": true, "INTO": true, "RETURNING": true,
}

// Top-level words after FROM that mean result rows aren't single table rows.
var multiRowWords = map[string]string{
	"JOIN":      "a join",
	"GROUP":     "GROUP BY",
	"HAVING":    "HAVING",
	"WINDOW":    "a WINDOW clause",
	"UNION":     "UNION, INTERSECT or EXCEPT",
	"INTERSECT": "UNION, INTERSECT or EXCEPT",
	"EXCEPT":    "UNION, INTERSECT or EXCEPT",
	"INTO":      "SELECT INTO",
}

const notPlainSelect = "not a plain SELECT from one table"

// Words that start an expression rather than name a column (NOT a, NULL AS x).
var valueWords = map[string]bool{"NOT": true, "NULL": true, "TRUE": true, "FALSE": true, "CASE": true,
	"EXISTS": true, "ARRAY": true, "ROW": true, "DEFAULT": true}

// analyzeSelect returns sql's shape when it is a single plain SELECT of
// columns from one table, or why not: a short phrase for "read-only: …".
// backslashQuotes is true when the server treats \ in '…' strings as an
// escape (standard_conforming_strings off). Anything it doesn't fully
// understand counts as not plain, so a query is never wrongly found editable.
func analyzeSelect(sql string, backslashQuotes bool) (selectShape, string) {
	tokens, ok := lexSQL(sql, backslashQuotes)
	if !ok {
		return selectShape{}, notPlainSelect
	}
	// One statement: a top-level ; may only be followed by more of them.
	for i, t := range tokens {
		if t.punct(";") && t.depth == 0 {
			for _, rest := range tokens[i:] {
				if !rest.punct(";") {
					return selectShape{}, "more than one statement"
				}
			}
			tokens = tokens[:i]
			break
		}
	}
	at := func(i int) sqlToken {
		if i < len(tokens) {
			return tokens[i]
		}
		return sqlToken{}
	}

	if !at(0).is("SELECT") {
		return selectShape{}, notPlainSelect
	}
	pos := 1
	if at(pos).is("ALL") {
		pos++
	}
	if at(pos).is("DISTINCT") {
		return selectShape{}, "DISTINCT"
	}

	// The select list runs to the top-level FROM (not the one in IS DISTINCT
	// FROM, nor a column named from after a dot). Items split at top-level
	// commas; plain column references are noted, anything else is fine.
	from := -1
	for i := pos; i < len(tokens) && from < 0; i++ {
		t := tokens[i]
		if t.depth != 0 || tokens[i-1].punct(".") {
			continue
		}
		switch {
		case t.is("INTO"):
			return selectShape{}, "SELECT INTO"
		case t.is("FROM") && !tokens[i-1].is("DISTINCT"):
			from = i
		}
	}
	if from < 0 {
		return selectShape{}, "no FROM table"
	}
	var shape selectShape
	start := pos
	for i := pos; i <= from; i++ {
		if i == from || tokens[i].punct(",") && tokens[i].depth == 0 {
			shape.items = append(shape.items, selectItemOf(tokens[start:i]))
			start = i + 1
		}
	}
	if !slices.ContainsFunc(shape.items, func(item selectItem) bool { return item.star || item.column != "" }) {
		return selectShape{}, "no table columns in the result"
	}
	shape.insertAt = tokens[from].start
	pos = from + 1

	// FROM: one table, [schema.]name, with an optional alias.
	switch t := at(pos); {
	case t.kind == 0:
		return selectShape{}, "no FROM table"
	case t.punct("(") || t.is("LATERAL"):
		return selectShape{}, "a subquery or function in FROM"
	case t.is("ONLY"):
		return selectShape{}, "FROM ONLY"
	case !t.ident():
		return selectShape{}, notPlainSelect
	}
	shape.table = []string{at(pos).text}
	pos++
	if at(pos).punct(".") && at(pos+1).ident() {
		shape.table = append(shape.table, at(pos+1).text)
		pos += 2
	}
	if at(pos).punct(".") {
		return selectShape{}, notPlainSelect // a.b.c: another database
	}
	if at(pos).punct("(") {
		return selectShape{}, "a subquery or function in FROM"
	}
	shape.qualifier = shape.table[len(shape.table)-1]
	if at(pos).is("AS") {
		if !at(pos + 1).ident() {
			return selectShape{}, notPlainSelect
		}
		shape.qualifier = at(pos + 1).text
		pos += 2
	} else if t := at(pos); t.kind == 'q' || t.kind == 'w' && !clauseWords[strings.ToUpper(t.text)] {
		shape.qualifier = t.text
		pos++
	}
	if at(pos).punct("(") {
		return selectShape{}, "column aliases on the table"
	}

	// What may follow: WHERE, ORDER BY, LIMIT, OFFSET, FETCH, FOR UPDATE.
	switch t := at(pos); {
	case t.kind == 0:
		return shape, ""
	case t.punct(","):
		return selectShape{}, "a join"
	case t.kind == 'w' && multiRowWords[strings.ToUpper(t.text)] != "":
		return selectShape{}, multiRowWords[strings.ToUpper(t.text)]
	case t.is("INNER") || t.is("LEFT") || t.is("RIGHT") || t.is("FULL") || t.is("CROSS") || t.is("NATURAL"):
		return selectShape{}, "a join"
	case t.is("TABLESAMPLE"):
		return selectShape{}, "TABLESAMPLE"
	case !(t.is("WHERE") || t.is("ORDER") || t.is("LIMIT") || t.is("OFFSET") || t.is("FETCH") || t.is("FOR")):
		return selectShape{}, notPlainSelect
	}
	// Inside those clauses anything goes in parentheses (a subquery in WHERE
	// only filters); at the top level, nothing may combine rows. A word right
	// after a dot is a column name (t.group), not a keyword. Positions in
	// ORDER BY (ORDER BY 2, 1) are noted.
	inOrder := false
	for i := pos; i < len(tokens); i++ {
		t := tokens[i]
		if t.depth != 0 {
			continue
		}
		prev := tokens[i-1]
		switch {
		case t.kind == 'w' && !prev.punct("."):
			if reason := multiRowWords[strings.ToUpper(t.text)]; reason != "" {
				return selectShape{}, reason
			}
			if t.is("ORDER") {
				inOrder = true
			} else if t.is("LIMIT") || t.is("OFFSET") || t.is("FETCH") || t.is("FOR") {
				inOrder = false
			}
		case t.kind == 'n' && inOrder && (prev.is("BY") || prev.punct(",")):
			if n, err := strconv.Atoi(t.text); err == nil && n > shape.maxOrdinal {
				shape.maxOrdinal = n
			}
		}
	}
	return shape, ""
}

// selectItemOf reads one select-list item: a plain column reference (a,
// t.a, s.t.a), * or t.*, with an optional alias. Anything else (functions,
// casts, literals, operators, subqueries) is an expression, column "".
func selectItemOf(tokens []sqlToken) selectItem {
	item, pos, ok := selectListItem(tokens, 0)
	if !ok || len(tokens) > 1 && valueWords[strings.ToUpper(tokens[0].text)] && tokens[0].kind == 'w' {
		return selectItem{}
	}
	switch {
	case pos == len(tokens):
	case !item.star && tokens[pos].is("AS") && pos+2 == len(tokens) && tokens[pos+1].ident():
	case !item.star && tokens[pos].ident() && pos+1 == len(tokens): // alias without AS
	default:
		return selectItem{} // e.g. "a + b", "a IS NULL", "date 'x'"
	}
	return item
}

// selectListItem reads *, or up to three dotted names optionally ending in
// *, at tokens[pos]. ok is false for anything else.
func selectListItem(tokens []sqlToken, pos int) (selectItem, int, bool) {
	if pos >= len(tokens) {
		return selectItem{}, pos, false
	}
	if tokens[pos].punct("*") {
		return selectItem{star: true}, pos + 1, true
	}
	if !tokens[pos].ident() {
		return selectItem{}, pos, false
	}
	parts := []sqlToken{tokens[pos]}
	pos++
	for pos+1 < len(tokens) && tokens[pos].punct(".") && len(parts) < 3 {
		next := tokens[pos+1]
		if next.punct("*") {
			return selectItem{star: true}, pos + 2, true
		}
		if !next.ident() {
			return selectItem{}, pos, false
		}
		parts = append(parts, next)
		pos += 2
	}
	if pos < len(tokens) && (tokens[pos].punct(".") || tokens[pos].punct("(") || tokens[pos].punct("[") || tokens[pos].punct("::")) {
		return selectItem{}, pos, false
	}
	return selectItem{column: identName(parts[len(parts)-1])}, pos, true
}

// identName is the name Postgres uses for an identifier token: unquoted
// names fold to lower case, quoted ones keep their text. "" when unknown
// (a U&"…" identifier).
func identName(t sqlToken) string {
	if t.kind == 'w' {
		return strings.Map(func(r rune) rune {
			if r >= 'A' && r <= 'Z' {
				return r + 'a' - 'A'
			}
			return r
		}, t.text)
	}
	if t.kind == 'q' && strings.HasPrefix(t.text, `"`) {
		return strings.ReplaceAll(t.text[1:len(t.text)-1], `""`, `"`)
	}
	return ""
}

// shows reports whether every one of columns is in the select list.
func (s selectShape) shows(columns []string) bool {
	for _, item := range s.items {
		if item.star {
			return true
		}
	}
	for _, c := range columns {
		found := false
		for _, item := range s.items {
			if item.column == c {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// insertion is text added to a query, so error positions can be mapped back.
type insertion struct {
	at     int // character (not byte) offset in the original SQL
	length int // characters added
}

// addColumns returns sql with exprs added at the end of the select list, as
// columns named by aliases. They go last, so ORDER BY 2 still means the same.
func (s selectShape) addColumns(sql string, exprs, aliases []string) (string, insertion) {
	parts := make([]string, len(exprs))
	for i := range exprs {
		parts[i] = exprs[i] + " AS " + pgx.Identifier{aliases[i]}.Sanitize()
	}
	text := " , " + strings.Join(parts, ", ") + " "
	return sql[:s.insertAt] + text + sql[s.insertAt:],
		insertion{at: utf8.RuneCountInString(sql[:s.insertAt]), length: utf8.RuneCountInString(text)}
}

// originalPosition maps a 1-based error position in the changed SQL back to
// the SQL as written; 0 when it falls inside the added text.
func (in insertion) originalPosition(position int) int {
	switch {
	case position <= in.at:
		return position
	case position <= in.at+in.length:
		return 0
	default:
		return position - in.length
	}
}

// lexSQL splits sql into tokens the way Postgres's scanner does, closely
// enough to find the statement's structure: comments are skipped; strings
// ('…', E'…', $tag$…$tag$), quoted identifiers and numbers are single tokens.
// ok is false for anything unterminated or unbalanced, or a number run into
// a word (1union), which Postgres versions read differently.
func lexSQL(sql string, backslashQuotes bool) ([]sqlToken, bool) {
	var tokens []sqlToken
	depth := 0
	n := len(sql)
	add := func(kind byte, start, end int) {
		tokens = append(tokens, sqlToken{kind: kind, text: sql[start:end], start: start, depth: depth})
	}
	next := func(i int) byte {
		if i+1 < n {
			return sql[i+1]
		}
		return 0
	}
	for i := 0; i < n; {
		c := sql[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			i++
		case c == '-' && next(i) == '-':
			for i < n && sql[i] != '\n' && sql[i] != '\r' {
				i++
			}
		case c == '/' && next(i) == '*':
			end, ok := skipBlockComment(sql, i)
			if !ok {
				return nil, false
			}
			i = end
		case c == '\'':
			end, ok := skipString(sql, i, backslashQuotes)
			if !ok {
				return nil, false
			}
			add('s', i, end)
			i = end
		case (c == 'e' || c == 'E') && next(i) == '\'':
			end, ok := skipString(sql, i+1, true)
			if !ok {
				return nil, false
			}
			add('s', i, end)
			i = end
		case strings.IndexByte("bBxXnN", c) >= 0 && next(i) == '\'':
			end, ok := skipString(sql, i+1, backslashQuotes)
			if !ok {
				return nil, false
			}
			add('s', i, end)
			i = end
		case (c == 'u' || c == 'U') && next(i) == '&' && i+2 < n && (sql[i+2] == '\'' || sql[i+2] == '"'):
			var end int
			var ok bool
			if sql[i+2] == '\'' {
				end, ok = skipString(sql, i+2, false)
			} else {
				end, ok = skipQuoted(sql, i+2)
			}
			if !ok {
				return nil, false
			}
			kind := byte('s')
			if sql[i+2] == '"' {
				kind = 'q'
			}
			add(kind, i, end)
			i = end
		case c == '"':
			end, ok := skipQuoted(sql, i)
			if !ok {
				return nil, false
			}
			add('q', i, end)
			i = end
		case c == '$' && isDigit(next(i)):
			j := i + 1
			for j < n && isDigit(sql[j]) {
				j++
			}
			add('$', i, j)
			i = j
		case c == '$':
			tag := i + 1
			for tag < n && (isIdentStart(sql[tag]) || tag > i+1 && isDigit(sql[tag])) {
				tag++
			}
			if tag < n && sql[tag] == '$' {
				delimiter := sql[i : tag+1]
				close := strings.Index(sql[tag+1:], delimiter)
				if close < 0 {
					return nil, false
				}
				end := tag + 1 + close + len(delimiter)
				add('s', i, end)
				i = end
			} else {
				add('p', i, i+1)
				i++
			}
		case isIdentStart(c):
			j := i + 1
			for j < n && (isIdentStart(sql[j]) || isDigit(sql[j]) || sql[j] == '$') {
				j++
			}
			add('w', i, j)
			i = j
		case isDigit(c) || c == '.' && isDigit(next(i)):
			j := scanNumber(sql, i)
			if j < n && isIdentStart(sql[j]) {
				return nil, false
			}
			add('n', i, j)
			i = j
		case c == ':' && next(i) == ':':
			add('p', i, i+2)
			i += 2
		case c == '(' || c == '[':
			add('p', i, i+1)
			depth++
			i++
		case c == ')' || c == ']':
			depth--
			if depth < 0 {
				return nil, false
			}
			add('p', i, i+1)
			i++
		default:
			add('p', i, i+1)
			i++
		}
	}
	return tokens, depth == 0
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// Postgres identifiers start with a letter, _ or any non-ASCII character.
func isIdentStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c >= 0x80
}

// skipBlockComment returns the offset after the /* … */ starting at i;
// Postgres nests them.
func skipBlockComment(sql string, i int) (int, bool) {
	level := 0
	for i < len(sql)-1 {
		switch {
		case sql[i] == '/' && sql[i+1] == '*':
			level++
			i += 2
		case sql[i] == '*' && sql[i+1] == '/':
			level--
			i += 2
			if level == 0 {
				return i, true
			}
		default:
			i++
		}
	}
	return 0, false
}

// skipString returns the offset after the '…' whose opening quote is at i.
// ” is a quote inside; with backslashes on, \ escapes the next character.
func skipString(sql string, i int, backslashes bool) (int, bool) {
	for j := i + 1; j < len(sql); j++ {
		switch {
		case backslashes && sql[j] == '\\':
			j++
		case sql[j] == '\'':
			if j+1 < len(sql) && sql[j+1] == '\'' {
				j++
				continue
			}
			return j + 1, true
		}
	}
	return 0, false
}

// skipQuoted returns the offset after the "…" whose opening quote is at i.
func skipQuoted(sql string, i int) (int, bool) {
	for j := i + 1; j < len(sql); j++ {
		if sql[j] == '"' {
			if j+1 < len(sql) && sql[j+1] == '"' {
				j++
				continue
			}
			return j + 1, true
		}
	}
	return 0, false
}

// scanNumber returns the offset after the number at i: digits (with _
// separators), an optional fraction and an optional exponent.
func scanNumber(sql string, i int) int {
	n := len(sql)
	digits := func(j int) int {
		for j < n && (isDigit(sql[j]) || sql[j] == '_') {
			j++
		}
		return j
	}
	j := digits(i)
	if j < n && sql[j] == '.' && !(j+1 < n && sql[j+1] == '.') {
		j = digits(j + 1)
	}
	if j < n && (sql[j] == 'e' || sql[j] == 'E') {
		k := j + 1
		if k < n && (sql[k] == '+' || sql[k] == '-') {
			k++
		}
		if k < n && isDigit(sql[k]) {
			j = digits(k)
		}
	}
	return j
}
