package mongodb

import (
	"errors"
	"math"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// asJSON is a parsed value as canonical Extended JSON, to compare exactly (types included).
func asJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := bson.MarshalExtJSON(bson.D{{Key: "v", Value: v}}, true, false)
	if err != nil {
		t.Fatalf("marshal %v: %v", v, err)
	}
	return string(b)
}

func TestParseCommandShapes(t *testing.T) {
	cases := []struct {
		text, collection, method string
		args                     int
		chain                    []string
	}{
		{`db.orders.find()`, "orders", "find", 0, nil},
		{`db.orders.find({})`, "orders", "find", 1, nil},
		{"  db.orders.find({status: 'PAID'})\n  .sort({_id: -1})\n  .limit(10);  ", "orders", "find", 1, []string{"sort", "limit"}},
		{`db.system.views.find()`, "system.views", "find", 0, nil},
		{`db["order-items"].countDocuments({})`, "order-items", "countDocuments", 1, nil},
		{`db.getCollection('a b').aggregate([])`, "a b", "aggregate", 1, nil},
		{`db.users.updateOne({_id: 1}, {$set: {a: 1}}, {upsert: true})`, "users", "updateOne", 3, nil},
		{"// the paid ones\ndb.orders.find(/* all */)", "orders", "find", 0, nil},
	}
	for _, c := range cases {
		cmd, err := parseCommand(c.text)
		if err != nil {
			t.Errorf("%q: %v", c.text, err)
			continue
		}
		var chain []string
		for _, ch := range cmd.chain {
			chain = append(chain, ch.method)
		}
		if cmd.collection != c.collection || cmd.method != c.method || len(cmd.args) != c.args || strings.Join(chain, ",") != strings.Join(c.chain, ",") {
			t.Errorf("%q: got %q.%s %d args chain %v", c.text, cmd.collection, cmd.method, len(cmd.args), chain)
		}
	}
}

func TestParseCommandValues(t *testing.T) {
	cases := []struct{ value, want string }{
		{`{a: 1, 'b': "two", "c d": true, e: null}`, `{"v":{"a":{"$numberInt":"1"},"b":"two","c d":true,"e":null}}`},
		{`[1, 2.5, -3, 5000000000, 1e3,]`, `{"v":[{"$numberInt":"1"},{"$numberDouble":"2.5"},{"$numberInt":"-3"},{"$numberLong":"5000000000"},{"$numberDouble":"1000.0"}]}`},
		{`ObjectId("64f1c0ffee0000000000abcd")`, `{"v":{"$oid":"64f1c0ffee0000000000abcd"}}`},
		{`ISODate("2024-05-01T10:30:00Z")`, `{"v":{"$date":{"$numberLong":"1714559400000"}}}`},
		{`new Date("2024-05-01")`, `{"v":{"$date":{"$numberLong":"1714521600000"}}}`},
		{`NumberLong("9007199254740993")`, `{"v":{"$numberLong":"9007199254740993"}}`},
		{`NumberInt(7)`, `{"v":{"$numberInt":"7"}}`},
		{`NumberDecimal("9.99")`, `{"v":{"$numberDecimal":"9.99"}}`},
		{`/^ab[/]c/i`, `{"v":{"$regularExpression":{"pattern":"^ab[/]c","options":"i"}}}`},
		{`'it\'s "quoted"\n'`, `{"v":"it's \"quoted\"\n"}`},
		{`{$gt: 0x10}`, `{"v":{"$gt":{"$numberInt":"16"}}}`},
		{`Timestamp(1700000000, 3)`, `{"v":{"$timestamp":{"t":1700000000,"i":3}}}`},
		{`UUID("0f8fad5b-d9cb-469f-a165-70867728950e")`, `{"v":{"$binary":{"base64":"D4+tW9nLRp+hZXCGdyiVDg==","subType":"04"}}}`},
	}
	for _, c := range cases {
		cmd, err := parseCommand("db.c.find(" + c.value + ")")
		if err != nil {
			t.Errorf("%s: %v", c.value, err)
			continue
		}
		if got := asJSON(t, cmd.args[0]); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.value, got, c.want)
		}
	}
	cmd, err := parseCommand(`db.c.find({x: -Infinity, y: NaN})`)
	if err != nil {
		t.Fatal(err)
	}
	doc := cmd.args[0].(bson.D)
	if !math.IsInf(doc[0].Value.(float64), -1) || !math.IsNaN(doc[1].Value.(float64)) {
		t.Errorf("Infinity / NaN: got %v", doc)
	}
}

func TestParseCommandErrors(t *testing.T) {
	cases := []struct {
		text, want   string
		line, column int
	}{
		{`orders.find()`, "Start with db", 1, 7},
		{`db.orders.find({a: 1)`, "Expected , or }", 1, 21},
		{"db.orders.find({\n  a: 'x\n})", "no closing '", 2, 6},
		{`db.orders.find({a: foo})`, "doesn’t know “foo”", 1, 20},
		{`db.orders.find() db.orders.find()`, "one command at a time", 1, 18},
		{`db.orders`, "Expected a method call", 1, 10},
		{`db.orders.find({a: ObjectId("xyz")})`, "24 hex digits", 1, 20},
	}
	for _, c := range cases {
		_, err := parseCommand(c.text)
		var syntax *SyntaxError
		if !errors.As(err, &syntax) {
			t.Errorf("%q: want a SyntaxError, got %v", c.text, err)
			continue
		}
		if !strings.Contains(syntax.Message, c.want) || syntax.Line != c.line || syntax.Column != c.column {
			t.Errorf("%q: got %q at %d:%d, want %q at %d:%d", c.text, syntax.Message, syntax.Line, syntax.Column, c.want, c.line, c.column)
		}
	}
}
