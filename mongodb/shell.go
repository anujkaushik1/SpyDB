package mongodb

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// command is one line of the query editor, as mongosh writes it:
//
//	db.orders.find({status: "PAID", total: {$gt: 500}}).sort({_id: -1}).limit(10)
//
// SpyDB doesn't run JavaScript: it reads this one shape (a collection, a
// method, its arguments, then .sort() and the like) and runs it with Go's
// MongoDB driver.
type command struct {
	collection string
	method     string
	args       []any
	chain      []chained
}

// chained is a call after the method: .sort({…}), .limit(10), .skip(20).
type chained struct {
	method string
	args   []any
}

// parseCommand reads one command. Arguments are JavaScript values the way
// mongosh takes them: keys with or without quotes, 'single' or "double"
// quoted strings, trailing commas, comments, /regex/i, and ObjectId("…"),
// ISODate("…"), new Date(…), NumberLong(…), NumberInt(…), NumberDecimal(…),
// Timestamp(…), UUID("…"), BinData(…), MinKey(), MaxKey().
func parseCommand(text string) (command, error) {
	p := &shellParser{src: text}
	cmd, err := p.command()
	if err != nil {
		return command{}, err
	}
	return cmd, nil
}

type shellParser struct {
	src string
	pos int
}

// fail is an error at the parser's position, with its line and column.
func (p *shellParser) fail(format string, args ...any) error {
	line, col := 1, 1
	for _, r := range p.src[:min(p.pos, len(p.src))] {
		if r == '\n' {
			line, col = line+1, 1
		} else {
			col++
		}
	}
	return &SyntaxError{Message: fmt.Sprintf(format, args...), Line: line, Column: col, Offset: utf8.RuneCountInString(p.src[:min(p.pos, len(p.src))])}
}

// SyntaxError is a command SpyDB couldn't read, and where.
type SyntaxError struct {
	Message string `json:"message"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Offset  int    `json:"offset"` // in characters from the start, for putting the cursor there
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("%s (line %d, column %d)", e.Message, e.Line, e.Column)
}

func (p *shellParser) peek() rune {
	if p.pos >= len(p.src) {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(p.src[p.pos:])
	return r
}

func (p *shellParser) next() rune {
	r, size := utf8.DecodeRuneInString(p.src[p.pos:])
	p.pos += size
	return r
}

// space skips white space and // or /* */ comments.
func (p *shellParser) space() {
	for p.pos < len(p.src) {
		switch {
		case unicode.IsSpace(p.peek()):
			p.next()
		case strings.HasPrefix(p.src[p.pos:], "//"):
			for p.pos < len(p.src) && p.peek() != '\n' {
				p.next()
			}
		case strings.HasPrefix(p.src[p.pos:], "/*"):
			end := strings.Index(p.src[p.pos+2:], "*/")
			if end < 0 {
				p.pos = len(p.src)
				return
			}
			p.pos += end + 4
		default:
			return
		}
	}
}

func (p *shellParser) expect(r rune) error {
	p.space()
	if p.peek() != r {
		if p.pos >= len(p.src) {
			return p.fail("Expected %q, but the command ends here", r)
		}
		return p.fail("Expected %q, found %q", r, p.peek())
	}
	p.next()
	return nil
}

func isIdentStart(r rune) bool { return r == '_' || r == '$' || unicode.IsLetter(r) }
func isIdentPart(r rune) bool  { return isIdentStart(r) || unicode.IsDigit(r) }

func (p *shellParser) ident() string {
	p.space()
	start := p.pos
	if !isIdentStart(p.peek()) {
		return ""
	}
	for p.pos < len(p.src) && isIdentPart(p.peek()) {
		p.next()
	}
	return p.src[start:p.pos]
}

func (p *shellParser) command() (command, error) {
	var cmd command
	if p.ident() != "db" {
		return cmd, p.fail("Start with db, as in db.orders.find({})")
	}
	p.space()
	// db["order-items"].find(), db.getCollection("x").find(), or db.a.b.find()
	// for the collection "a.b": the name runs up to the part before "(".
	switch p.peek() {
	case '[':
		p.next()
		p.space()
		name, err := p.string()
		if err != nil {
			return cmd, err
		}
		if err := p.expect(']'); err != nil {
			return cmd, err
		}
		cmd.collection = name
		if err := p.expect('.'); err != nil {
			return cmd, err
		}
		if cmd.method = p.ident(); cmd.method == "" {
			return cmd, p.fail("Expected a method, like find")
		}
	case '.':
		var parts []string
		for {
			p.next()
			part := p.ident()
			if part == "" {
				return cmd, p.fail("Expected a collection name after db.")
			}
			p.space()
			if p.peek() == '(' {
				if part == "getCollection" && len(parts) == 0 {
					p.next()
					p.space()
					name, err := p.string()
					if err != nil {
						return cmd, err
					}
					if err := p.expect(')'); err != nil {
						return cmd, err
					}
					parts = []string{name}
					if err := p.expect('.'); err != nil {
						return cmd, err
					}
					if cmd.method = p.ident(); cmd.method == "" {
						return cmd, p.fail("Expected a method, like find")
					}
					break
				}
				cmd.method = part
				break
			}
			parts = append(parts, part)
			if p.peek() != '.' {
				return cmd, p.fail("Expected a method call, as in db.%s.find({})", strings.Join(parts, "."))
			}
		}
		if len(parts) == 0 {
			return cmd, p.fail("Name the collection, as in db.orders.%s(…)", cmd.method)
		}
		cmd.collection = strings.Join(parts, ".")
	default:
		return cmd, p.fail("Expected db.<collection>.<method>(…)")
	}

	args, err := p.arguments()
	if err != nil {
		return cmd, err
	}
	cmd.args = args
	for {
		p.space()
		if p.peek() != '.' {
			break
		}
		p.next()
		name := p.ident()
		if name == "" {
			return cmd, p.fail("Expected a method after the dot")
		}
		args, err := p.arguments()
		if err != nil {
			return cmd, err
		}
		cmd.chain = append(cmd.chain, chained{method: name, args: args})
	}
	p.space()
	if p.peek() == ';' {
		p.next()
		p.space()
	}
	if p.pos < len(p.src) {
		return cmd, p.fail("Run one command at a time: select it, or put the cursor in it and press Run current")
	}
	return cmd, nil
}

// arguments reads "(a, b, …)".
func (p *shellParser) arguments() ([]any, error) {
	if err := p.expect('('); err != nil {
		return nil, err
	}
	args := []any{}
	for {
		p.space()
		if p.peek() == ')' {
			p.next()
			return args, nil
		}
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		args = append(args, v)
		p.space()
		switch p.peek() {
		case ',':
			p.next()
		case ')':
		default:
			return nil, p.fail("Expected , or ) after an argument")
		}
	}
}

// value reads one JavaScript value as a BSON one: objects keep their key order.
func (p *shellParser) value() (any, error) {
	p.space()
	switch r := p.peek(); {
	case r == 0:
		return nil, p.fail("Expected a value, but the command ends here")
	case r == '{':
		return p.object()
	case r == '[':
		return p.array()
	case r == '"' || r == '\'':
		return p.string()
	case r == '/':
		return p.regex()
	case r == '-' || r == '+' || r == '.' || unicode.IsDigit(r):
		return p.number()
	case isIdentStart(r):
		return p.word()
	default:
		return nil, p.fail("Unexpected %q", r)
	}
}

func (p *shellParser) object() (bson.D, error) {
	p.next() // {
	doc := bson.D{}
	for {
		p.space()
		if p.peek() == '}' {
			p.next()
			return doc, nil
		}
		var key string
		switch r := p.peek(); {
		case r == '"' || r == '\'':
			k, err := p.string()
			if err != nil {
				return nil, err
			}
			key = k
		case isIdentStart(r):
			key = p.ident()
		case unicode.IsDigit(r):
			start := p.pos
			for p.pos < len(p.src) && unicode.IsDigit(p.peek()) {
				p.next()
			}
			key = p.src[start:p.pos]
		default:
			return nil, p.fail("Expected a key or }")
		}
		if err := p.expect(':'); err != nil {
			return nil, err
		}
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		doc = append(doc, bson.E{Key: key, Value: v})
		p.space()
		switch p.peek() {
		case ',':
			p.next()
		case '}':
		default:
			return nil, p.fail("Expected , or } after %s’s value", key)
		}
	}
}

func (p *shellParser) array() (bson.A, error) {
	p.next() // [
	arr := bson.A{}
	for {
		p.space()
		if p.peek() == ']' {
			p.next()
			return arr, nil
		}
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		arr = append(arr, v)
		p.space()
		switch p.peek() {
		case ',':
			p.next()
		case ']':
		default:
			return nil, p.fail("Expected , or ] in the list")
		}
	}
}

// string reads a 'single' or "double" quoted string with JavaScript's escapes.
func (p *shellParser) string() (string, error) {
	quote := p.peek()
	if quote != '"' && quote != '\'' {
		return "", p.fail("Expected a quoted string")
	}
	start := p.pos
	unclosed := func(format string) error {
		p.pos = start
		return p.fail(format, quote)
	}
	p.next()
	var b strings.Builder
	for {
		if p.pos >= len(p.src) {
			return "", unclosed("This string has no closing %c")
		}
		r := p.next()
		switch {
		case r == quote:
			return b.String(), nil
		case r == '\n':
			return "", unclosed("This string has no closing %c on its line")
		case r == '\\':
			if p.pos >= len(p.src) {
				return "", unclosed("This string has no closing %c")
			}
			e := p.next()
			switch e {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'v':
				b.WriteByte('\v')
			case '0':
				b.WriteByte(0)
			case 'u':
				if p.pos+4 > len(p.src) {
					return "", p.fail(`Expected 4 hex digits after \u`)
				}
				n, err := strconv.ParseUint(p.src[p.pos:p.pos+4], 16, 32)
				if err != nil {
					return "", p.fail(`Expected 4 hex digits after \u`)
				}
				p.pos += 4
				b.WriteRune(rune(n))
			case '\n':
				// a line continuation
			default:
				b.WriteRune(e) // \" \' \\ \/ and any other character as itself
			}
		default:
			b.WriteRune(r)
		}
	}
}

// regex reads /pattern/flags.
func (p *shellParser) regex() (bson.Regex, error) {
	p.next() // /
	var b strings.Builder
	inClass := false
	for {
		if p.pos >= len(p.src) || p.peek() == '\n' {
			return bson.Regex{}, p.fail("This /regex/ has no closing /")
		}
		r := p.next()
		if r == '\\' && p.pos < len(p.src) {
			b.WriteRune(r)
			b.WriteRune(p.next())
			continue
		}
		if r == '[' {
			inClass = true
		} else if r == ']' {
			inClass = false
		} else if r == '/' && !inClass {
			break
		}
		b.WriteRune(r)
	}
	start := p.pos
	for p.pos < len(p.src) && unicode.IsLetter(p.peek()) {
		p.next()
	}
	return bson.Regex{Pattern: b.String(), Options: p.src[start:p.pos]}, nil
}

// number reads a number: a whole one is an int, or a long when too big
// for 32 bits (mongosh would make that a double and lose digits); others are doubles.
func (p *shellParser) number() (any, error) {
	start := p.pos
	if r := p.peek(); r == '-' || r == '+' {
		p.next()
		if strings.HasPrefix(p.src[p.pos:], "Infinity") {
			p.pos += len("Infinity")
			if r == '-' {
				return math.Inf(-1), nil
			}
			return math.Inf(1), nil
		}
	}
	if strings.HasPrefix(strings.ToLower(p.src[p.pos:]), "0x") {
		p.pos += 2
		hexStart := p.pos
		for p.pos < len(p.src) && strings.ContainsRune("0123456789abcdefABCDEF", p.peek()) {
			p.next()
		}
		n, err := strconv.ParseInt(p.src[hexStart:p.pos], 16, 64)
		if err != nil {
			return nil, p.fail("“%s” isn’t a number", p.src[start:p.pos])
		}
		if strings.HasPrefix(p.src[start:], "-") {
			n = -n
		}
		return intValue(n), nil
	}
	for p.pos < len(p.src) {
		r := p.peek()
		if unicode.IsDigit(r) || r == '.' || r == 'e' || r == 'E' || ((r == '-' || r == '+') && strings.ContainsAny(p.src[p.pos-1:p.pos], "eE")) {
			p.next()
			continue
		}
		break
	}
	text := p.src[start:p.pos]
	if n, err := strconv.ParseInt(strings.TrimPrefix(text, "+"), 10, 64); err == nil {
		return intValue(n), nil
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return nil, p.fail("“%s” isn’t a number", text)
	}
	return f, nil
}

func intValue(n int64) any {
	if n == int64(int32(n)) {
		return int32(n)
	}
	return n
}

// word is true, false, null, undefined, NaN, Infinity, or a constructor like ObjectId("…").
func (p *shellParser) word() (any, error) {
	at := p.pos
	name := p.ident()
	if name == "new" {
		at = p.pos
		if name = p.ident(); name == "" {
			return nil, p.fail("Expected a type after new, like new Date(…)")
		}
	}
	switch name {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null", "undefined":
		return nil, nil
	case "NaN":
		return math.NaN(), nil
	case "Infinity":
		return math.Inf(1), nil
	}
	p.space()
	if p.peek() != '(' {
		p.pos = at
		return nil, p.fail("SpyDB doesn’t know “%s”: values are numbers, 'strings', {objects}, [lists], true, false, null, or ObjectId(…), ISODate(…) and the like", name)
	}
	args, err := p.arguments()
	if err != nil {
		return nil, err
	}
	// A constructor's mistakes point at its name.
	bad := func(format string, args ...any) error {
		p.pos = at
		return p.fail(format, args...)
	}
	arg := func(i int) any {
		if i < len(args) {
			return args[i]
		}
		return nil
	}
	text := func(i int) (string, bool) {
		switch v := arg(i).(type) {
		case string:
			return v, true
		case int32:
			return strconv.FormatInt(int64(v), 10), true
		case int64:
			return strconv.FormatInt(v, 10), true
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64), true
		}
		return "", false
	}
	switch name {
	case "ObjectId", "ObjectID":
		if len(args) == 0 {
			return bson.NewObjectID(), nil
		}
		s, _ := text(0)
		id, err := bson.ObjectIDFromHex(s)
		if err != nil {
			return nil, bad("ObjectId(…) takes 24 hex digits, like ObjectId(\"64f1c0ffee0000000000abcd\")")
		}
		return id, nil
	case "ISODate", "Date":
		switch v := arg(0).(type) {
		case nil:
			return bson.NewDateTimeFromTime(time.Now()), nil
		case string:
			t, err := parseDate(v)
			if err != nil {
				return nil, bad("%s(…) takes a date like \"2024-05-01T10:30:00Z\"", name)
			}
			return bson.NewDateTimeFromTime(t), nil
		case int32, int64, float64:
			ms, _ := text(0)
			n, _ := strconv.ParseFloat(ms, 64)
			return bson.DateTime(int64(n)), nil // milliseconds since 1970
		}
		return nil, bad("%s(…) takes a date like \"2024-05-01T10:30:00Z\" or milliseconds", name)
	case "NumberLong", "Long":
		s, _ := text(0)
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, bad("NumberLong(…) takes a whole number")
		}
		return n, nil
	case "NumberInt", "Int32":
		s, _ := text(0)
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil {
			return nil, bad("NumberInt(…) takes a whole number that fits in 32 bits")
		}
		return int32(n), nil
	case "NumberDecimal", "Decimal128":
		s, _ := text(0)
		d, err := bson.ParseDecimal128(s)
		if err != nil {
			return nil, bad("%s(…) takes a number, like NumberDecimal(\"9.99\")", name)
		}
		return d, nil
	case "Double":
		s, _ := text(0)
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, bad("Double(…) takes a number")
		}
		return f, nil
	case "Timestamp":
		if doc, ok := arg(0).(bson.D); ok { // Timestamp({t: 1700000000, i: 1})
			var t, i uint32
			for _, e := range doc {
				n, _ := strconv.ParseUint(fmt.Sprint(e.Value), 10, 32)
				if e.Key == "t" {
					t = uint32(n)
				} else if e.Key == "i" {
					i = uint32(n)
				}
			}
			return bson.Timestamp{T: t, I: i}, nil
		}
		ts, _ := text(0)
		is, _ := text(1)
		t, err1 := strconv.ParseUint(ts, 10, 32)
		i, err2 := strconv.ParseUint(is, 10, 32)
		if err1 != nil || err2 != nil {
			return nil, bad("Timestamp(…) takes seconds and an increment, like Timestamp(1700000000, 1)")
		}
		return bson.Timestamp{T: uint32(t), I: uint32(i)}, nil
	case "UUID":
		s, _ := text(0)
		b, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
		if err != nil || len(b) != 16 {
			return nil, bad("UUID(…) takes a UUID like \"0f8fad5b-d9cb-469f-a165-70867728950e\"")
		}
		return bson.Binary{Subtype: 4, Data: b}, nil
	case "BinData":
		sub, _ := text(0)
		data, _ := text(1)
		n, err1 := strconv.ParseUint(sub, 10, 8)
		b, err2 := base64.StdEncoding.DecodeString(data)
		if err1 != nil || err2 != nil {
			return nil, bad("BinData(…) takes a subtype and base64, like BinData(0, \"aGVsbG8=\")")
		}
		return bson.Binary{Subtype: byte(n), Data: b}, nil
	case "MinKey":
		return bson.MinKey{}, nil
	case "MaxKey":
		return bson.MaxKey{}, nil
	}
	return nil, bad("SpyDB doesn’t know %s(…): it knows ObjectId, ISODate, Date, NumberLong, NumberInt, NumberDecimal, Timestamp, UUID, BinData, MinKey and MaxKey", name)
}
