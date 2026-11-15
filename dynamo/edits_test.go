package dynamo

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func TestNewValue(t *testing.T) {
	s := func(v string) types.AttributeValue { return &types.AttributeValueMemberS{Value: v} }
	n := func(v string) types.AttributeValue { return &types.AttributeValueMemberN{Value: v} }
	ss := &types.AttributeValueMemberSS{Value: []string{"a", "b"}}
	tagged := &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{
		"tags": ss, "pin": n("110001"), "raw": &types.AttributeValueMemberB{Value: []byte("hi")},
	}}
	cases := []struct {
		name   string
		change FieldChange
		now    types.AttributeValue
		want   string // the new value as typed JSON, or "error: …" (part of the message)
	}{
		{"text stays text", FieldChange{Text: "Pune"}, s("Delhi"), `{"S":"Pune"}`},
		{"a number stays a number", FieldChange{Text: " 12.50 "}, n("3"), `{"N":"12.50"}`},
		{"not a number", FieldChange{Text: "abc"}, n("3"), "error: holds numbers, and “abc” isn't one"},
		{"the JSON view makes it text", FieldChange{Text: "abc", Type: "S", Typed: true}, n("3"), `{"S":"abc"}`},
		{"the JSON view makes it a number", FieldChange{Text: "5", Type: "N", Typed: true}, s("5"), `{"N":"5"}`},
		{"a boolean as the grid shows it", FieldChange{Text: "f"}, &types.AttributeValueMemberBOOL{Value: true}, `{"BOOL":false}`},
		{"not a boolean", FieldChange{Text: "maybe"}, &types.AttributeValueMemberBOOL{Value: true}, "error: holds true or false"},
		{"NULL", FieldChange{Null: true}, s("x"), `{"NULL":true}`},
		{"a new field takes its column's type", FieldChange{Text: "7", Type: "N"}, nil, `{"N":"7"}`},
		{"a new field without one is text", FieldChange{Text: "7"}, nil, `{"S":"7"}`},
		{"a NULL field takes the column's type", FieldChange{Text: "true", Type: "BOOL"}, &types.AttributeValueMemberNULL{Value: true}, `{"BOOL":true}`},
		{"binary as base64", FieldChange{Text: "aGk="}, &types.AttributeValueMemberB{Value: []byte("x")}, `{"B":"aGk="}`},
		{"a set stays a set", FieldChange{Text: `["a","c"]`}, ss, `{"SS":["a","c"]}`},
		{"a set can't be empty", FieldChange{Text: `[]`}, ss, "error: DynamoDB sets can't be empty"},
		{"nor hold a value twice", FieldChange{Text: `["a","a"]`}, ss, "error: can't hold a twice"},
		{"a new set column's type", FieldChange{Text: `["x"]`, Type: "SS"}, nil, `{"SS":["x"]}`},
		{"JSON becomes a map", FieldChange{Text: `{"a":1,"b":[true,null,"x"]}`, Type: "JSON", Typed: true}, nil,
			`{"M":{"a":{"N":"1"},"b":{"L":[{"BOOL":true},{"NULL":true},{"S":"x"}]}}}`},
		// Only pin changed: the set and the binary inside keep their types.
		{"a map keeps what wasn't changed", FieldChange{Text: `{"pin":110002,"raw":"aGk=","tags":["a","b"]}`}, tagged,
			`{"M":{"pin":{"N":"110002"},"raw":{"B":"aGk="},"tags":{"SS":["a","b"]}}}`},
		{"not JSON", FieldChange{Text: `{oops`}, tagged, "error: isn't valid JSON (it holds a map)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, err := newValue(c.change, c.now)
			got := ""
			if err != nil {
				got = "error: " + err.Error()
				if !strings.HasPrefix(c.want, "error: ") || !strings.Contains(got, strings.TrimPrefix(c.want, "error: ")) {
					t.Fatalf("got %s; want %s", got, c.want)
				}
				return
			}
			got = typedJSON(v)
			if got != c.want {
				t.Fatalf("got %s; want %s", got, c.want)
			}
		})
	}
}

// typedJSON is a value as DynamoDB's own JSON, map keys sorted.
func typedJSON(av types.AttributeValue) string {
	var walk func(types.AttributeValue) any
	walk = func(av types.AttributeValue) any {
		switch v := av.(type) {
		case *types.AttributeValueMemberS:
			return map[string]any{"S": v.Value}
		case *types.AttributeValueMemberN:
			return map[string]any{"N": v.Value}
		case *types.AttributeValueMemberBOOL:
			return map[string]any{"BOOL": v.Value}
		case *types.AttributeValueMemberNULL:
			return map[string]any{"NULL": true}
		case *types.AttributeValueMemberB, *types.AttributeValueMemberSS, *types.AttributeValueMemberNS, *types.AttributeValueMemberBS:
			return map[string]any{typeOf(v): plain(v)}
		case *types.AttributeValueMemberM:
			m := map[string]any{}
			for k, f := range v.Value {
				m[k] = walk(f)
			}
			return map[string]any{"M": m}
		case *types.AttributeValueMemberL:
			l := []any{}
			for _, e := range v.Value {
				l = append(l, walk(e))
			}
			return map[string]any{"L": l}
		}
		return nil
	}
	return jsonText(walk(av))
}

func TestLooksLike(t *testing.T) {
	cases := []struct {
		av    types.AttributeValue
		shown Shown
		want  bool
	}{
		{nil, Shown{}, true},
		{nil, Shown{Has: true, Text: ""}, false},
		{&types.AttributeValueMemberS{Value: ""}, Shown{}, false},
		{&types.AttributeValueMemberS{Value: "x"}, Shown{Has: true, Text: "x"}, true},
		{&types.AttributeValueMemberS{Value: "y"}, Shown{Has: true, Text: "x"}, false},
		{&types.AttributeValueMemberNULL{Value: true}, Shown{Has: true, Null: true}, true},
		{&types.AttributeValueMemberS{Value: "NULL"}, Shown{Has: true, Null: true}, false},
		{&types.AttributeValueMemberBOOL{Value: true}, Shown{Has: true, Text: "t"}, true},
		{&types.AttributeValueMemberBOOL{Value: true}, Shown{Has: true, Text: "true"}, true},
		{&types.AttributeValueMemberBOOL{Value: false}, Shown{Has: true, Text: "t"}, false},
	}
	for i, c := range cases {
		if got := looksLike(c.av, c.shown); got != c.want {
			t.Errorf("%d: looksLike(%v, %+v) = %v", i, c.av, c.shown, got)
		}
	}
}

func TestSelectTable(t *testing.T) {
	cases := map[string]string{
		`SELECT * FROM "orders"`:                                 "orders",
		`select a, b from orders where id = 'x'`:                 "orders",
		`SELECT * FROM "my-table" WHERE "pk" = 'a'`:              "my-table",
		`SELECT * FROM "odd""name"`:                              `odd"name`,
		`SELECT * FROM "orders"."byStatus" WHERE "s" = 'x'`:      "",
		`SELECT * FROM orders.byStatus`:                          "",
		`SELECT * FROM "a" WHERE x IN (SELECT y FROM "b")`:       "",
		"-- all of them\nSELECT *\nFROM \"orders\"\nWHERE a = 1": "orders",
	}
	for statement, want := range cases {
		if got := selectTable(statement); got != want {
			t.Errorf("selectTable(%q) = %q; want %q", statement, got, want)
		}
	}
	for statement, want := range map[string]string{"SELECT 1": "select", "  -- x\n/* y */ insert into": "insert", "update\n\"t\"": "update"} {
		if got := firstWord(statement); got != want {
			t.Errorf("firstWord(%q) = %q; want %q", statement, got, want)
		}
	}
}

// localDynamo is a Service connected to DynamoDB Local (SPYDB_DYNAMO_LOCAL,
// e.g. http://127.0.0.1:8001 for `docker run -p 8001:8000
// amazon/dynamodb-local`) with a new, empty table: partition key id (text),
// sort key n (number). Never AWS: these tests write.
func localDynamo(t *testing.T) (*Service, *dynamodb.Client, string) {
	t.Helper()
	url := os.Getenv("SPYDB_DYNAMO_LOCAL")
	if url == "" {
		t.Skip("set SPYDB_DYNAMO_LOCAL to a DynamoDB Local, e.g. http://127.0.0.1:8001 (these tests write)")
	}
	s := connected(t, url)
	client, _ := s.dynamoClient()
	table := fmt.Sprintf("spydb-test-%s", strings.NewReplacer("/", "-", " ", "-").Replace(t.Name()))
	if len(table) > 200 {
		table = table[:200]
	}
	client.DeleteTable(t.Context(), &dynamodb.DeleteTableInput{TableName: aws.String(table)}) // left by a run that was stopped
	_, err := client.CreateTable(t.Context(), &dynamodb.CreateTableInput{
		TableName: aws.String(table),
		AttributeDefinitions: []types.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: aws.String("n"), AttributeType: types.ScalarAttributeTypeN},
		},
		KeySchema: []types.KeySchemaElement{
			{AttributeName: aws.String("id"), KeyType: types.KeyTypeHash},
			{AttributeName: aws.String("n"), KeyType: types.KeyTypeRange},
		},
		BillingMode: types.BillingModePayPerRequest,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Not t.Context(): it has ended by the time cleanups run.
	t.Cleanup(func() {
		client.DeleteTable(context.Background(), &dynamodb.DeleteTableInput{TableName: aws.String(table)})
	})
	return s, client, table
}

func put(t *testing.T, client *dynamodb.Client, table string, item map[string]types.AttributeValue) {
	t.Helper()
	if _, err := client.PutItem(t.Context(), &dynamodb.PutItemInput{TableName: aws.String(table), Item: item}); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, client *dynamodb.Client, table, id, n string) map[string]types.AttributeValue {
	t.Helper()
	out, err := client.GetItem(t.Context(), &dynamodb.GetItemInput{TableName: aws.String(table), ConsistentRead: aws.Bool(true),
		Key: map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: id}, "n": &types.AttributeValueMemberN{Value: n}}})
	if err != nil {
		t.Fatal(err)
	}
	return out.Item
}

func sampleItem(id string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"id":     &types.AttributeValueMemberS{Value: id},
		"n":      &types.AttributeValueMemberN{Value: "1"},
		"city":   &types.AttributeValueMemberS{Value: "Delhi"},
		"amount": &types.AttributeValueMemberN{Value: "1500"},
		"paid":   &types.AttributeValueMemberBOOL{Value: false},
		"tags":   &types.AttributeValueMemberSS{Value: []string{"new", "vip"}},
		"address": &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{
			"pin":   &types.AttributeValueMemberN{Value: "110001"},
			"lines": &types.AttributeValueMemberSS{Value: []string{"a", "b"}},
		}},
	}
}

func TestSaveItemsOnDynamoDBLocal(t *testing.T) {
	s, client, table := localDynamo(t)
	put(t, client, table, sampleItem("a"))
	put(t, client, table, sampleItem("b"))
	key := func(id string) map[string]string { return map[string]string{"id": id, "n": "1"} }
	text := func(s string) Shown { return Shown{Has: true, Text: s} }

	t.Run("one item: change, add, remove, NULL; types kept", func(t *testing.T) {
		saved, err := s.SaveItems(t.Context(), table, []ItemEdit{{
			Key: key("a"),
			Old: map[string]Shown{"city": text("Delhi"), "amount": text("1500"), "paid": text("f"), "note": {}, "tags": text(`["new","vip"]`),
				"address": text(`{"lines":["a","b"],"pin":110001}`)},
			Changes: map[string]FieldChange{
				"city":    {Text: "Pune"},
				"amount":  {Text: "2000.5"},
				"paid":    {Text: "true"},
				"note":    {Text: "added from SpyDB"},
				"tags":    {Remove: true},
				"address": {Text: `{"lines":["a","b"],"pin":110002,"floor":3}`},
			},
		}})
		if err != nil {
			t.Fatal(err)
		}
		item := get(t, client, table, "a", "1")
		want := map[string]string{
			"city": `{"S":"Pune"}`, "amount": `{"N":"2000.5"}`, "paid": `{"BOOL":true}`, "note": `{"S":"added from SpyDB"}`,
			"address": `{"M":{"floor":{"N":"3"},"lines":{"SS":["a","b"]},"pin":{"N":"110002"}}}`,
		}
		for field, w := range want {
			if got := typedJSON(item[field]); got != w {
				t.Errorf("%s = %s; want %s", field, got, w)
			}
		}
		if _, ok := item["tags"]; ok {
			t.Errorf("tags wasn't removed")
		}
		if len(saved) != 1 || saved[0]["city"].Text != "Pune" || saved[0]["address"].Type != "M" {
			t.Errorf("saved %+v", saved)
		}
	})

	t.Run("several items: one transaction", func(t *testing.T) {
		put(t, client, table, sampleItem("c"))
		_, err := s.SaveItems(t.Context(), table, []ItemEdit{
			{Key: key("b"), Old: map[string]Shown{"city": text("Delhi")}, Changes: map[string]FieldChange{"city": {Text: "Agra"}}},
			{Key: key("c"), Old: map[string]Shown{"city": text("Delhi")}, Changes: map[string]FieldChange{"city": {Text: "Goa"}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if b, c := get(t, client, table, "b", "1")["city"], get(t, client, table, "c", "1")["city"]; typedJSON(b) != `{"S":"Agra"}` || typedJSON(c) != `{"S":"Goa"}` {
			t.Fatalf("b %s c %s", typedJSON(b), typedJSON(c))
		}
	})

	t.Run("changed since loaded: nothing is saved", func(t *testing.T) {
		before := get(t, client, table, "b", "1")
		_, err := s.SaveItems(t.Context(), table, []ItemEdit{
			{Key: key("c"), Old: map[string]Shown{"city": text("Goa")}, Changes: map[string]FieldChange{"city": {Text: "X"}}},
			{Key: key("b"), Old: map[string]Shown{"city": text("Delhi")}, Changes: map[string]FieldChange{"city": {Text: "Y"}}}, // it's Agra now
		})
		if err == nil || !strings.Contains(err.Error(), "id = b, n = 1: city changed since you loaded it (it’s now “Agra”)") {
			t.Fatalf("got %v", err)
		}
		if c := get(t, client, table, "c", "1")["city"]; typedJSON(c) != `{"S":"Goa"}` {
			t.Fatalf("c was saved anyway: %s", typedJSON(c))
		}
		if !reflect.DeepEqual(typedJSON(before["city"]), typedJSON(get(t, client, table, "b", "1")["city"])) {
			t.Fatal("b changed")
		}
	})

	t.Run("a field added since loaded: not overwritten", func(t *testing.T) {
		_, err := s.SaveItems(t.Context(), table, []ItemEdit{
			{Key: key("a"), Old: map[string]Shown{"note": {}}, Changes: map[string]FieldChange{"note": {Text: "mine"}}},
		})
		if err == nil || !strings.Contains(err.Error(), "note changed since you loaded it") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("refused: the key, a deleted item, a bad number", func(t *testing.T) {
		for _, c := range []struct {
			edit ItemEdit
			want string
		}{
			{ItemEdit{Key: key("a"), Old: map[string]Shown{"id": text("a")}, Changes: map[string]FieldChange{"id": {Text: "z"}}}, "id is part of the key"},
			{ItemEdit{Key: key("gone"), Old: map[string]Shown{"city": text("x")}, Changes: map[string]FieldChange{"city": {Text: "y"}}}, "isn't in"},
			{ItemEdit{Key: key("b"), Old: map[string]Shown{"amount": text("1500")}, Changes: map[string]FieldChange{"amount": {Text: "lots"}}}, "holds numbers"},
		} {
			if _, err := s.SaveItems(t.Context(), table, []ItemEdit{c.edit}); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("got %v; want …%s…", err, c.want)
			}
		}
	})

	t.Run("delete: checked, then all or none", func(t *testing.T) {
		put(t, client, table, sampleItem("d"))
		put(t, client, table, sampleItem("e"))
		old := func(city string) map[string]Shown {
			return map[string]Shown{"city": text(city), "amount": text("1500"), "missing": {}}
		}
		_, err := s.DeleteItems(t.Context(), table, []ItemDelete{{Key: key("d"), Old: old("Delhi")}, {Key: key("e"), Old: old("Mumbai")}})
		if err == nil || !strings.Contains(err.Error(), "id = e, n = 1: city changed") || get(t, client, table, "d", "1") == nil {
			t.Fatalf("got %v; d deleted: %v", err, get(t, client, table, "d", "1") == nil)
		}
		n, err := s.DeleteItems(t.Context(), table, []ItemDelete{{Key: key("d"), Old: old("Delhi")}, {Key: key("e"), Old: old("Delhi")}})
		if err != nil || n != 2 || get(t, client, table, "d", "1") != nil || get(t, client, table, "e", "1") != nil {
			t.Fatalf("got %d, %v", n, err)
		}
		if n, err := s.DeleteItems(t.Context(), table, []ItemDelete{{Key: key("c"), Old: old("Goa")}}); err != nil || n != 1 || get(t, client, table, "c", "1") != nil {
			t.Fatalf("one item: %d, %v", n, err)
		}
	})
}

func TestPartiQLOnDynamoDBLocal(t *testing.T) {
	s, client, table := localDynamo(t)
	run := func(statement, next string) PartiQLResult {
		t.Helper()
		r, err := s.RunPartiQL(t.Context(), statement, next)
		if err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
		return r
	}
	for i := range 120 {
		run(fmt.Sprintf(`INSERT INTO "%s" VALUE {'id': 'p', 'n': %d, 'odd': %t}`, table, i, i%2 == 1), "")
	}
	r := run(fmt.Sprintf(`SELECT * FROM "%s" WHERE "id" = 'p';`, table), "")
	if !r.Select || r.Table != table || len(r.Items) != 50 || r.Next == "" || r.Items[0]["odd"].Type != "BOOL" {
		t.Fatalf("first page: select %v table %q, %d items, next %q", r.Select, r.Table, len(r.Items), r.Next)
	}
	total := len(r.Items)
	for r.Next != "" {
		r = run(fmt.Sprintf(`SELECT * FROM "%s" WHERE "id" = 'p'`, table), r.Next)
		total += len(r.Items)
	}
	if total != 120 {
		t.Fatalf("read %d items in all", total)
	}
	run(fmt.Sprintf(`UPDATE "%s" SET city = 'Pune' WHERE "id" = 'p' AND "n" = 7`, table), "")
	if got := get(t, client, table, "p", "7")["city"]; typedJSON(got) != `{"S":"Pune"}` {
		t.Fatalf("UPDATE: %s", typedJSON(got))
	}
	if w := run(fmt.Sprintf(`DELETE FROM "%s" WHERE "id" = 'p' AND "n" = 7`, table), ""); w.Select || len(w.Items) != 0 {
		t.Fatalf("DELETE: %+v", w)
	}
	if get(t, client, table, "p", "7") != nil {
		t.Fatal("DELETE didn't delete")
	}
	if _, err := s.RunPartiQL(t.Context(), `SELEC oops`, ""); err == nil || !strings.HasPrefix(err.Error(), "AWS: ") {
		t.Fatalf("a bad statement: %v", err)
	}
}
