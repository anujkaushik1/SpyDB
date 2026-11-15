package mongodb

import (
	"math"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// raw is v as the server would send it back: a RawValue.
func raw(t *testing.T, v any) bson.RawValue {
	t.Helper()
	if v == nil {
		return bson.RawValue{Type: bson.TypeNull}
	}
	typ, data, err := bson.MarshalValue(v)
	if err != nil {
		t.Fatalf("marshal %v: %v", v, err)
	}
	return bson.RawValue{Type: typ, Value: data}
}

func TestToValue(t *testing.T) {
	id, _ := bson.ObjectIDFromHex("64f1c0ffee0000000000abcd")
	d, _ := bson.ParseDecimal128("12.50")
	when := time.Date(2024, 5, 1, 10, 30, 0, 0, time.UTC)
	cases := []struct {
		v         any
		typ, text string
	}{
		{"Delhi", "string", "Delhi"},
		{int32(7), "int", "7"},
		{int64(9007199254740993), "long", "9007199254740993"},
		{1.5, "double", "1.5"},
		{2.0, "double", "2"},
		{math.Inf(-1), "double", "-Infinity"},
		{d, "decimal", "12.50"},
		{true, "bool", "true"},
		{nil, "null", ""},
		{id, "objectId", "64f1c0ffee0000000000abcd"},
		{bson.NewDateTimeFromTime(when), "date", "2024-05-01T10:30:00.000Z"},
		{bson.D{{Key: "pin", Value: int32(110001)}, {Key: "at", Value: id}}, "object", `{"pin":110001,"at":{"$oid":"64f1c0ffee0000000000abcd"}}`},
		{bson.A{"a", int64(2), "<b>&"}, "array", `["a",2,"<b>&"]`},
		{bson.Binary{Subtype: 0, Data: []byte("hi")}, "binary", `{"$binary":{"base64":"aGk=","subType":"00"}}`},
	}
	for _, c := range cases {
		got := toValue(raw(t, c.v))
		if got.Type != c.typ || got.Text != c.text {
			t.Errorf("%#v: got %s %q, want %s %q", c.v, got.Type, got.Text, c.typ, c.text)
		}
	}
}

func TestDocumentIDRoundTrip(t *testing.T) {
	id, _ := bson.ObjectIDFromHex("64f1c0ffee0000000000abcd")
	for _, v := range []any{id, "order-1", int32(5), int64(5), 5.0, bson.D{{Key: "a", Value: int32(1)}, {Key: "b", Value: "x"}}} {
		doc, err := toDocument(mustRaw(t, bson.D{{Key: "_id", Value: v}, {Key: "n", Value: 1}}))
		if err != nil {
			t.Fatal(err)
		}
		back, err := parseID(doc.ID)
		if err != nil {
			t.Fatalf("%v: %v", v, err)
		}
		if !back.(bson.RawValue).Equal(raw(t, v)) {
			t.Errorf("%v: came back as %v (from %s)", v, back, doc.ID)
		}
	}
	if doc, _ := toDocument(mustRaw(t, bson.D{{Key: "n", Value: 1}})); doc.ID != "" {
		t.Errorf("no _id: ID = %q", doc.ID)
	}
}

func mustRaw(t *testing.T, d bson.D) bson.Raw {
	t.Helper()
	b, err := bson.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestNewValueKeepsTypes(t *testing.T) {
	d, _ := bson.ParseDecimal128("1.10")
	id, _ := bson.ObjectIDFromHex("64f1c0ffee0000000000abcd")
	cases := []struct {
		name    string
		change  FieldChange
		current any    // nil: the document hasn't got the field
		want    string // canonical Extended JSON, or an error's start
	}{
		{"int stays int", FieldChange{Text: "42"}, int32(1), `{"$numberInt":"42"}`},
		{"int too big becomes long", FieldChange{Text: "5000000000"}, int32(1), `{"$numberLong":"5000000000"}`},
		{"long stays long", FieldChange{Text: "7"}, int64(1), `{"$numberLong":"7"}`},
		{"int refuses text", FieldChange{Text: "abc"}, int32(1), "error: “abc” isn’t a whole number"},
		{"double stays double", FieldChange{Text: "3"}, 1.5, `{"$numberDouble":"3.0"}`},
		{"decimal stays exact", FieldChange{Text: "2.50"}, d, `{"$numberDecimal":"2.50"}`},
		{"bool from t", FieldChange{Text: "t"}, false, `true`},
		{"objectId", FieldChange{Text: "64f1c0ffee0000000000abce"}, id, `{"$oid":"64f1c0ffee0000000000abce"}`},
		{"date", FieldChange{Text: "2024-05-01"}, bson.NewDateTimeFromTime(time.Now()), `{"$date":{"$numberLong":"1714521600000"}}`},
		{"string stays string", FieldChange{Text: "5"}, "x", `"5"`},
		{"null", FieldChange{Null: true}, "x", `null`},
		{"new field, no type: text", FieldChange{Text: "5"}, nil, `"5"`},
		{"new field, number column", FieldChange{Text: "5", Type: "int"}, nil, `{"$numberInt":"5"}`},
		{"null field takes the hint", FieldChange{Text: "2024-01-02", Type: "date"}, nilValue{}, `{"$date":{"$numberLong":"1704153600000"}}`},
		{"JSON view: string over a number", FieldChange{Text: "5", Type: "string", Typed: true}, int32(5), `"5"`},
		{"JSON view: number keeps long", FieldChange{Text: "8", Type: "number", Typed: true}, int64(1), `{"$numberLong":"8"}`},
		{"JSON view: number over text", FieldChange{Text: "8.5", Type: "number", Typed: true}, "x", `{"$numberDouble":"8.5"}`},
		{"JSON view: Extended JSON", FieldChange{Text: `{"$oid":"64f1c0ffee0000000000abcd"}`, Type: "json", Typed: true}, nil, `{"$oid":"64f1c0ffee0000000000abcd"}`},
		{"bad JSON", FieldChange{Text: `{a:}`, Type: "json", Typed: true}, nil, "error: “{a:}” isn’t valid JSON"},
	}
	for _, c := range cases {
		var current bson.RawValue
		has := c.current != nil
		if _, isNull := c.current.(nilValue); isNull {
			current = raw(t, nil)
		} else if has {
			current = raw(t, c.current)
		}
		v, err := newValue(c.change, current, has)
		got := ""
		if err != nil {
			got = "error: " + err.Error()
		} else {
			got = strings.TrimSuffix(strings.TrimPrefix(asJSON(t, v), `{"v":`), "}")
		}
		if !strings.HasPrefix(got, c.want) || (err == nil && got != c.want) {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

// nilValue marks a field that holds null (rather than not being there).
type nilValue struct{}

func TestNewValueKeepsUnchangedParts(t *testing.T) {
	when := bson.NewDateTimeFromTime(time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC))
	old := raw(t, bson.D{
		{Key: "count", Value: int64(3)},
		{Key: "at", Value: when},
		{Key: "tags", Value: bson.A{int64(1), "x"}},
		{Key: "city", Value: "Delhi"},
	})
	// The grid shows it as relaxed JSON; the user changed only the city.
	edited := strings.Replace(toValue(old).Text, "Delhi", "Pune", 1)
	v, err := newValue(FieldChange{Text: edited}, old, true)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"v":{"count":{"$numberLong":"3"},"at":{"$date":{"$numberLong":"1714521600000"}},"tags":[{"$numberLong":"1"},"x"],"city":"Pune"}}`
	if got := asJSON(t, v); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestSameShown(t *testing.T) {
	if !sameShown(Shown{Has: true, Text: "t"}, shownAs(raw(t, true), true)) {
		t.Error("t should match true")
	}
	if sameShown(Shown{Has: true, Text: "5"}, Shown{}) {
		t.Error("a field that went away still matches")
	}
	if sameShown(Shown{Has: true, Null: true}, shownAs(raw(t, ""), true)) {
		t.Error("null matches an empty string")
	}
}

func TestURIParts(t *testing.T) {
	cases := []struct{ uri, hosts, user, db string }{
		{"mongodb+srv://ana:p%40ss@cluster0.ab12.mongodb.net/shop?retryWrites=true", "cluster0.ab12.mongodb.net", "ana", "shop"},
		{"mongodb://localhost:27017", "localhost:27017", "", ""},
		{"mongodb://u@h1:1,h2:2/?replicaSet=rs0", "h1:1,h2:2", "u", ""},
		{"mongodb://a%2Fb:x@h/db%20x", "h", "a/b", "db x"},
	}
	for _, c := range cases {
		hosts, user, db := uriParts(c.uri)
		if hosts != c.hosts || user != c.user || db != c.db {
			t.Errorf("%s: got %q %q %q", c.uri, hosts, user, db)
		}
	}
	h, u, d := Config{URI: "mongodb://u:p@h:1/a", Database: "b"}.target()
	if h != "h:1" || u != "u" || d != "b" {
		t.Errorf("Database should win over the string's: %q %q %q", h, u, d)
	}
}

func TestConfigValidate(t *testing.T) {
	for _, c := range []Config{{URI: "postgres://x"}, {Host: "", Port: 27017}, {Host: "h", Port: 0}, {Host: "h", Port: 1, Database: "a.b"}} {
		if c.validate() == nil {
			t.Errorf("%+v should be refused", c)
		}
	}
	for _, c := range []Config{{URI: "mongodb+srv://x.y"}, {Host: "h", Port: 27017, Database: "shop"}} {
		if err := c.validate(); err != nil {
			t.Errorf("%+v: %v", c, err)
		}
	}
}

func TestFilterDocument(t *testing.T) {
	got, err := filterDocument([]Filter{
		{Field: "_id", Operator: "eq", Value: "64f1c0ffee0000000000abcd", Type: "objectId"},
		{Field: "total", Operator: "gte", Value: "500", Type: "number"},
		{Field: "name", Operator: "contains", Value: "a.b"},
		{Field: "note", Operator: "not_exists"},
		{Field: "", Operator: "eq", Value: "ignored"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"v":{"$and":[{"_id":{"$eq":{"$oid":"64f1c0ffee0000000000abcd"}}},{"total":{"$gte":{"$numberLong":"500"}}},{"name":{"$regex":"a\\.b","$options":"i"}},{"note":{"$exists":false}}]}}`
	if s := asJSON(t, got); s != want {
		t.Errorf("got  %s\nwant %s", s, want)
	}
	if _, err := filterDocument([]Filter{{Field: "total", Operator: "gt", Value: "lots", Type: "number"}}); err == nil || !strings.Contains(err.Error(), "isn’t a number") {
		t.Errorf("text in a number filter: %v", err)
	}
}
