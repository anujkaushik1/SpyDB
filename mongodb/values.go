package mongodb

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Value is one field of a document: its BSON type and the value as text.
type Value struct {
	Type string `json:"type"` // string, int, long, double, decimal, bool, null, objectId, date, object, array; others by their BSON name (binary, timestamp, regex…)
	Text string `json:"text"` // "Delhi", "1500", "true", "" for null; ObjectId as hex, a date as ISO 8601 (UTC); objects, arrays and other types as relaxed Extended JSON
}

// Document is one document as the grid shows it.
type Document struct {
	ID     string           `json:"id"`     // its _id as canonical Extended JSON, which finds it again exactly; "" if it has none (an aggregate's output)
	Fields map[string]Value `json:"fields"` // field name → value
	Order  []string         `json:"order"`  // the field names in the document's own order
}

// typeNames are BSON's types as SpyDB names them (mongosh's names, mostly).
var typeNames = map[bson.Type]string{
	bson.TypeString:           "string",
	bson.TypeInt32:            "int",
	bson.TypeInt64:            "long",
	bson.TypeDouble:           "double",
	bson.TypeDecimal128:       "decimal",
	bson.TypeBoolean:          "bool",
	bson.TypeNull:             "null",
	bson.TypeObjectID:         "objectId",
	bson.TypeDateTime:         "date",
	bson.TypeEmbeddedDocument: "object",
	bson.TypeArray:            "array",
	bson.TypeBinary:           "binary",
	bson.TypeTimestamp:        "timestamp",
	bson.TypeRegex:            "regex",
	bson.TypeUndefined:        "undefined",
	bson.TypeMinKey:           "minKey",
	bson.TypeMaxKey:           "maxKey",
	bson.TypeJavaScript:       "javascript",
	bson.TypeCodeWithScope:    "javascript",
	bson.TypeSymbol:           "symbol",
	bson.TypeDBPointer:        "dbPointer",
}

// dateLayout is how a date shows: ISO 8601 in UTC with milliseconds, as mongosh shows it.
const dateLayout = "2006-01-02T15:04:05.000Z"

// toValue is how the grid shows a field.
func toValue(v bson.RawValue) Value {
	name := typeNames[v.Type]
	if name == "" {
		name = v.Type.String()
	}
	switch v.Type {
	case bson.TypeString:
		return Value{Type: name, Text: v.StringValue()}
	case bson.TypeInt32:
		return Value{Type: name, Text: strconv.FormatInt(int64(v.Int32()), 10)}
	case bson.TypeInt64:
		return Value{Type: name, Text: strconv.FormatInt(v.Int64(), 10)}
	case bson.TypeDouble:
		return Value{Type: name, Text: doubleText(v.Double())}
	case bson.TypeDecimal128:
		return Value{Type: name, Text: v.Decimal128().String()}
	case bson.TypeBoolean:
		return Value{Type: name, Text: strconv.FormatBool(v.Boolean())}
	case bson.TypeNull:
		return Value{Type: name}
	case bson.TypeObjectID:
		return Value{Type: name, Text: v.ObjectID().Hex()}
	case bson.TypeDateTime:
		return Value{Type: name, Text: time.UnixMilli(v.DateTime()).UTC().Format(dateLayout)}
	}
	return Value{Type: name, Text: extJSON(v, false)}
}

// doubleText is a double as mongosh writes it: 1.5, 2, 1e+21, NaN, Infinity.
func doubleText(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// extJSON is one value as Extended JSON: relaxed ({"$oid": …}, plain
// numbers) for showing, canonical (every number's type spelled out) for
// finding it again exactly. Wrapped in a document, as the encoder wants.
func extJSON(v any, canonical bool) string {
	b, err := bson.MarshalExtJSON(bson.D{{Key: "v", Value: v}}, canonical, false)
	if err != nil {
		return fmt.Sprint(v)
	}
	// {"v":…} → …
	return string(bytes.TrimSuffix(bytes.TrimPrefix(b, []byte(`{"v":`)), []byte("}")))
}

// toDocument is a document as the grid shows it.
func toDocument(raw bson.Raw) (Document, error) {
	elements, err := raw.Elements()
	if err != nil {
		return Document{}, err
	}
	doc := Document{Fields: make(map[string]Value, len(elements)), Order: make([]string, 0, len(elements))}
	for _, e := range elements {
		key := e.Key()
		if _, dup := doc.Fields[key]; dup {
			continue // BSON allows the same name twice; the server reads the first
		}
		v := e.Value()
		doc.Fields[key] = toValue(v)
		doc.Order = append(doc.Order, key)
		if key == "_id" {
			doc.ID = extJSON(v, true)
		}
	}
	return doc, nil
}

// parseID is a document's _id from its canonical Extended JSON (Document.ID).
func parseID(id string) (any, error) {
	var holder struct {
		V bson.RawValue `bson:"v"`
	}
	if id == "" {
		return nil, errors.New("This document has no _id, so SpyDB can’t find it again to change it.")
	}
	if err := bson.UnmarshalExtJSON([]byte(`{"v":`+id+`}`), true, &holder); err != nil {
		return nil, fmt.Errorf("Couldn’t read the document’s _id %s: %v", id, err)
	}
	return holder.V, nil
}

// Shown is a field as the grid showed it when it was loaded.
type Shown struct {
	Has  bool   `json:"has"`  // the document had the field
	Null bool   `json:"null"` // it was null
	Text string `json:"text"` // otherwise its Value.Text
}

// shownAs is how field v (absent when ok is false) shows.
func shownAs(v bson.RawValue, ok bool) Shown {
	if !ok {
		return Shown{}
	}
	if v.Type == bson.TypeNull {
		return Shown{Has: true, Null: true}
	}
	return Shown{Has: true, Text: toValue(v).Text}
}

// sameShown says whether a field still shows as it did; true / false and
// t / f (the grid's way for booleans) are the same.
func sameShown(was, now Shown) bool {
	if was.Has != now.Has || was.Null != now.Null {
		return false
	}
	switch was.Text {
	case "t":
		return now.Text == "true" || now.Text == "t"
	case "f":
		return now.Text == "false" || now.Text == "f"
	}
	return was.Text == now.Text
}

// shownText is a Shown for messages: “Delhi”, null, or “nothing”.
func shownText(s Shown) string {
	switch {
	case !s.Has:
		return "nothing (no such field)"
	case s.Null:
		return "null"
	}
	return "“" + s.Text + "”"
}

// FieldChange is one field's new value.
type FieldChange struct {
	Remove bool   `json:"remove"` // take the field off the document
	Null   bool   `json:"null"`   // set it to null
	Text   string `json:"text"`   // otherwise the value: as typed in a cell, or from the JSON view
	// Type says what Text is. With Typed, the JSON view chose it ("string",
	// "number", "bool", or "json" for an object, an array or Extended JSON
	// like {"$oid": …}) and it wins over the field's type. Otherwise it's a
	// hint for a field the document hasn't got (or holds null): its column's
	// type ("int", "double", "date", "objectId"…), or "" for text.
	Type  string `json:"type"`
	Typed bool   `json:"typed"`
}

// newValue is a field's new BSON value: of the type it has now (current,
// when it has the field), as far as the text allows; else of the type the
// change says. An object or array keeps the exact type of every part
// whose value didn't change.
func newValue(c FieldChange, current bson.RawValue, has bool) (any, error) {
	if c.Null {
		return nil, nil
	}
	kind := c.Type
	if !c.Typed && has && current.Type != bson.TypeNull {
		kind = typeNames[current.Type]
		if kind == "" {
			kind = "json"
		}
	}
	text := c.Text
	switch kind {
	case "", "string", "symbol":
		return text, nil
	case "number":
		return numberLike(strings.TrimSpace(text), current, has)
	case "int", "long":
		n, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("“%s” isn’t a whole number, and this field holds whole numbers (%s). To store something else, change it in the JSON view.", text, kind)
		}
		if kind == "int" && n == int64(int32(n)) {
			return int32(n), nil
		}
		return n, nil // an int too big for 32 bits becomes a long
	case "double":
		f, err := parseDouble(text)
		if err != nil {
			return nil, fmt.Errorf("“%s” isn’t a number, and this field holds numbers (double). To store something else, change it in the JSON view.", text)
		}
		return f, nil
	case "decimal":
		d, err := bson.ParseDecimal128(strings.TrimSpace(text))
		if err != nil {
			return nil, fmt.Errorf("“%s” isn’t a number, and this field holds exact decimals. To store something else, change it in the JSON view.", text)
		}
		return d, nil
	case "bool":
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "true", "t":
			return true, nil
		case "false", "f":
			return false, nil
		}
		return nil, fmt.Errorf("This field holds true or false, not “%s”.", text)
	case "objectId":
		id, err := bson.ObjectIDFromHex(strings.TrimSpace(text))
		if err != nil {
			return nil, fmt.Errorf("“%s” isn’t an ObjectId (24 hex digits), and this field holds ObjectIds.", text)
		}
		return id, nil
	case "date":
		t, err := parseDate(text)
		if err != nil {
			return nil, fmt.Errorf("“%s” isn’t a date like 2024-05-01T10:30:00Z, and this field holds dates.", text)
		}
		return bson.NewDateTimeFromTime(t), nil
	}
	// Objects, arrays, Extended JSON and the rarer types: the text is JSON.
	v, err := parseExtJSON(text)
	if err != nil {
		return nil, err
	}
	if has {
		return likeOld(v, current), nil
	}
	return v, nil
}

// numberLike is a number from the JSON view: of the field's own number
// type when it has one and the number fits it, else like mongosh: a whole
// number is an int (a long when too big for 32 bits), others are doubles.
func numberLike(text string, current bson.RawValue, has bool) (any, error) {
	if has {
		switch current.Type {
		case bson.TypeDecimal128:
			if d, err := bson.ParseDecimal128(text); err == nil {
				return d, nil
			}
		case bson.TypeDouble:
			if f, err := parseDouble(text); err == nil {
				return f, nil
			}
		case bson.TypeInt64:
			if n, err := strconv.ParseInt(text, 10, 64); err == nil {
				return n, nil
			}
		}
	}
	if n, err := strconv.ParseInt(text, 10, 64); err == nil {
		if n == int64(int32(n)) {
			return int32(n), nil
		}
		return n, nil
	}
	f, err := parseDouble(text)
	if err != nil {
		return nil, fmt.Errorf("“%s” isn’t a number.", text)
	}
	return f, nil
}

func parseDouble(text string) (float64, error) {
	switch t := strings.TrimSpace(text); t {
	case "NaN":
		return math.NaN(), nil
	case "Infinity":
		return math.Inf(1), nil
	case "-Infinity":
		return math.Inf(-1), nil
	default:
		return strconv.ParseFloat(t, 64)
	}
}

// Dates as people type them: with or without a time, seconds or a zone
// (no zone is UTC, as dates show).
var dateLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"}

func parseDate(text string) (time.Time, error) {
	text = strings.TrimSpace(text)
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, text); err == nil {
			return t, nil
		}
	}
	if ms, err := strconv.ParseInt(text, 10, 64); err == nil {
		return time.UnixMilli(ms), nil // milliseconds since 1970, as Date stores them
	}
	return time.Time{}, errors.New("not a date")
}

// parseExtJSON reads one value from relaxed Extended JSON: plain JSON, plus
// {"$oid": …}, {"$date": …}, {"$numberDecimal": …} and the rest. Whole
// numbers become ints (longs when too big), others doubles.
func parseExtJSON(text string) (any, error) {
	var holder struct {
		V bson.RawValue `bson:"v"`
	}
	if err := bson.UnmarshalExtJSON([]byte(`{"v":`+text+`}`), false, &holder); err != nil {
		return nil, fmt.Errorf("“%s” isn’t valid JSON: %v", shorten(text, 60), err)
	}
	return holder.V, nil
}

// likeOld is v with every part that equals the same part of old (same key
// of an object, same place in an array) replaced by old's exact value, so
// changing one field of an object doesn't turn its longs into ints or its
// dates into text.
func likeOld(v any, old bson.RawValue) any {
	nv, ok := v.(bson.RawValue)
	if !ok {
		return v
	}
	if extJSON(nv, false) == extJSON(old, false) {
		return old
	}
	switch {
	case nv.Type == bson.TypeEmbeddedDocument && old.Type == bson.TypeEmbeddedDocument:
		elements, err := nv.Document().Elements()
		if err != nil {
			return v
		}
		out := make(bson.D, 0, len(elements))
		for _, e := range elements {
			value := any(e.Value())
			if was, err := old.Document().LookupErr(e.Key()); err == nil {
				value = likeOld(e.Value(), was)
			}
			out = append(out, bson.E{Key: e.Key(), Value: value})
		}
		return out
	case nv.Type == bson.TypeArray && old.Type == bson.TypeArray:
		values, err := nv.Array().Values()
		if err != nil {
			return v
		}
		olds, _ := old.Array().Values()
		out := make(bson.A, len(values))
		for i, e := range values {
			out[i] = e
			if i < len(olds) {
				out[i] = likeOld(e, olds[i])
			}
		}
		return out
	}
	return v
}

func shorten(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
