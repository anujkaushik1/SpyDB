package dynamo

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// tm_orders's real keys (read from AWS; no item data), plus one index that
// keeps only some fields.
var tmOrders = TableDetails{
	Name: "tm_orders", PartitionKey: "id",
	Indexes: []IndexDetails{
		{Name: "bseOrderIDIndex", PartitionKey: "bse_order_id", AllFields: true},
		{Name: "order_type-createdAt-index", PartitionKey: "order_type", SortKey: "createdAt", AllFields: true},
		{Name: "sip_type_status", PartitionKey: "sip_type", SortKey: "sip_status", AllFields: true},
		{Name: "useridIndex", PartitionKey: "user_id", AllFields: true},
		{Name: "keysOnlyIndex", PartitionKey: "pan", AllFields: false},
	},
	KeyTypes: map[string]string{"id": "S", "bse_order_id": "S", "order_type": "S", "createdAt": "S", "sip_type": "S", "sip_status": "S", "user_id": "S", "pan": "S", "amount": ""},
}

func eq(field, value string) Filter { return Filter{Field: field, Operator: "eq", Value: value} }
func op(field, operator, value string) Filter {
	return Filter{Field: field, Operator: operator, Value: value}
}

func TestPlanFind(t *testing.T) {
	for _, c := range []struct {
		name    string
		filters []Filter
		want    string // "scan", or "query <index or table>: key fields | other filters"
	}{
		{"no filters: every item", nil, "scan"},
		{"the table's key", []Filter{eq("id", "o1")}, "query table: id | "},
		{"an index's key", []Filter{eq("bse_order_id", "98765")}, "query bseOrderIDIndex: bse_order_id | "},
		{"a 2nd key alone can't be used", []Filter{eq("sip_status", "PENDING")}, "scan"},
		{"1st and 2nd key", []Filter{eq("sip_type", "SIP"), eq("sip_status", "PENDING")}, "query sip_type_status: sip_type sip_status | "},
		{"in any order", []Filter{eq("sip_status", "PENDING"), eq("sip_type", "SIP")}, "query sip_type_status: sip_type sip_status | "},
		{"2nd key: starts with", []Filter{eq("order_type", "SIP"), op("createdAt", "starts", "2026-11")}, "query order_type-createdAt-index: order_type createdAt | "},
		{"2nd key: ≥ and ≤ make between", []Filter{eq("order_type", "SIP"), op("createdAt", "lte", "2026-12"), op("createdAt", "gte", "2026-11")}, "query order_type-createdAt-index: order_type createdAt createdAt | "},
		{"2nd key: > and < can't be one key condition", []Filter{eq("order_type", "SIP"), op("createdAt", "gt", "a"), op("createdAt", "lt", "b")}, "scan"},
		{"2nd key: contains can't be used", []Filter{eq("order_type", "SIP"), op("createdAt", "contains", "11")}, "scan"},
		{"1st key needs equals", []Filter{op("bse_order_id", "contains", "987")}, "scan"},
		{"1st key twice", []Filter{eq("bse_order_id", "1"), eq("bse_order_id", "2")}, "scan"},
		{"other filters go along", []Filter{eq("sip_type", "SIP"), op("amount", "gt", "1000")}, "query sip_type_status: sip_type | amount"},
		// The case from tm_orders: user_id = x, then sip_type = primary and sip_status = created.
		{"two keys fit: the first row decides, the rest narrow", []Filter{eq("user_id", "u1"), eq("sip_type", "primary"), eq("sip_status", "created")}, "query useridIndex: user_id | sip_type sip_status"},
		{"…the other way round", []Filter{eq("sip_type", "primary"), eq("sip_status", "created"), eq("user_id", "u1")}, "query sip_type_status: sip_type sip_status | user_id"},
		{"a plain filter first doesn't count", []Filter{op("amount", "gt", "5"), eq("bse_order_id", "9"), eq("user_id", "u1")}, "query bseOrderIDIndex: bse_order_id | amount user_id"},
		{"the table's whole key always wins: one item", []Filter{eq("bse_order_id", "9"), eq("id", "o1")}, "query table: id | bse_order_id"},
		{"an index with only some fields isn't used", []Filter{eq("pan", "ABCDE1234F")}, "scan"},
		{"a field no key has", []Filter{eq("status", "done")}, "scan"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := planFind(tmOrders, c.filters)
			got := "scan"
			if p.Query {
				index := p.Index
				if index == "" {
					index = "table"
				}
				keys := []string{p.partition.Field}
				for _, f := range p.sort {
					keys = append(keys, f.Field)
				}
				rest := []string{}
				for _, f := range p.rest {
					rest = append(rest, f.Field)
				}
				got = fmt.Sprintf("query %s: %s | %s", index, strings.Join(keys, " "), strings.Join(rest, " "))
			} else if len(p.rest) != len(c.filters) {
				t.Errorf("a scan dropped filters: %v", p.rest)
			}
			if got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestFilterValues(t *testing.T) {
	details := TableDetails{KeyTypes: map[string]string{"createdAt": "N", "id": "S"}}
	for _, c := range []struct {
		filter  Filter
		want    string // the value as AWS gets it, or the error
		wantErr bool
	}{
		{Filter{Field: "name", Operator: "eq", Value: "Asha"}, `S Asha`, false},
		{Filter{Field: "amount", Operator: "gt", Value: " 1500.50 ", Type: "N"}, `N 1500.50`, false},
		{Filter{Field: "amount", Operator: "eq", Value: "-1e3", Type: "N"}, `N -1e3`, false},
		{Filter{Field: "amount", Operator: "eq", Value: "15oo", Type: "N"}, "“15oo” isn't a number, and amount holds numbers.", true},
		// The table's definition wins over the screen's guess.
		{Filter{Field: "createdAt", Operator: "gte", Value: "1731400000", Type: "S"}, `N 1731400000`, false},
		{Filter{Field: "id", Operator: "eq", Value: "42", Type: "N"}, `S 42`, false},
		{Filter{Field: "createdAt", Operator: "starts", Value: "17"}, "createdAt holds numbers: “starts with” only works on text.", true},
		{Filter{Field: "paid", Operator: "eq", Value: "True", Type: "BOOL"}, `BOOL true`, false},
		{Filter{Field: "paid", Operator: "neq", Value: "false", Type: "BOOL"}, `BOOL false`, false},
		{Filter{Field: "paid", Operator: "gt", Value: "true", Type: "BOOL"}, "paid holds true or false: use equals or not equal.", true},
		{Filter{Field: "paid", Operator: "eq", Value: "yes", Type: "BOOL"}, "paid holds true or false, not “yes”.", true},
		{Filter{Field: "x", Operator: "like", Value: "a"}, `SpyDB doesn't know the condition "like".`, true},
	} {
		v, err := attributeValue(details, c.filter)
		var got string
		if err != nil {
			got = err.Error()
		} else {
			value := toValue(v)
			got = value.Type + " " + value.Text // e.g. "N 1500"
		}
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("%+v: got %q, want %q", c.filter, got, c.want)
		}
	}
}

// fakeFinder is a DynamoDB holding tm_orders's keys that records the Query
// and Scan requests it gets, answering each with two items and one more page.
func fakeFinder(t *testing.T) (url string, requests *[]map[string]any) {
	requests = &[]map[string]any{}
	url, _ = fakeDynamoAPI(t, func(op string, in map[string]any) (int, any) {
		switch op {
		case "DescribeTable":
			return 200, map[string]any{"Table": map[string]any{
				"TableName":            "tm_orders",
				"KeySchema":            []any{map[string]any{"AttributeName": "id", "KeyType": "HASH"}},
				"AttributeDefinitions": []any{map[string]any{"AttributeName": "id", "AttributeType": "S"}, map[string]any{"AttributeName": "sip_type", "AttributeType": "S"}, map[string]any{"AttributeName": "sip_status", "AttributeType": "S"}},
				"GlobalSecondaryIndexes": []any{map[string]any{"IndexName": "sip_type_status", "Projection": map[string]any{"ProjectionType": "ALL"},
					"KeySchema": []any{map[string]any{"AttributeName": "sip_type", "KeyType": "HASH"}, map[string]any{"AttributeName": "sip_status", "KeyType": "RANGE"}}}},
			}}
		case "Query", "Scan":
			in["op"] = op
			*requests = append(*requests, in)
			return 200, map[string]any{
				"Items":            []any{map[string]any{"id": map[string]any{"S": "o1"}}, map[string]any{"id": map[string]any{"S": "o2"}}},
				"Count":            2,
				"ScannedCount":     50,
				"LastEvaluatedKey": map[string]any{"id": map[string]any{"S": "o50"}, "sip_type": map[string]any{"S": "SIP"}, "sip_status": map[string]any{"S": "PENDING"}},
			}
		}
		return 400, map[string]string{"__type": "UnknownOperationException", "message": op}
	})
	return url, requests
}

// What AWS gets for each kind of search, exactly.
func TestFindItems(t *testing.T) {
	request := func(t *testing.T, filters []Filter, next string) (ItemsPage, map[string]any) {
		t.Helper()
		url, requests := fakeFinder(t)
		page, err := connected(t, url).FindItems(t.Context(), "tm_orders", filters, next)
		if err != nil {
			t.Fatal(err)
		}
		if len(*requests) != 1 {
			t.Fatalf("%d requests", len(*requests))
		}
		return page, (*requests)[0]
	}
	text := func(v any) string { return jsonText(v) } // < and > as they are

	t.Run("an index's key and its 2nd key, plus a filter", func(t *testing.T) {
		page, in := request(t, []Filter{op("amount", "gt", "1000"), eq("sip_status", "PENDING"), eq("sip_type", "SIP")}, "")
		want := map[string]string{
			"op":                        `"Query"`,
			"TableName":                 `"tm_orders"`,
			"IndexName":                 `"sip_type_status"`,
			"KeyConditionExpression":    `"#n0 = :v0 AND #n1 = :v1"`,
			"FilterExpression":          `"#n2 > :v2"`,
			"ExpressionAttributeNames":  `{"#n0":"sip_type","#n1":"sip_status","#n2":"amount"}`,
			"ExpressionAttributeValues": `{":v0":{"S":"SIP"},":v1":{"S":"PENDING"},":v2":{"S":"1000"}}`,
			"Limit":                     `50`,
		}
		for field, w := range want {
			if got := text(in[field]); got != w {
				t.Errorf("%s: got %s, want %s", field, got, w)
			}
		}
		if page.Plan != (FindPlan{Query: true, Index: "sip_type_status"}) || page.Checked != 50 || len(page.Items) != 2 || page.Next == "" {
			t.Errorf("page: %+v", page)
		}
	})

	t.Run("the table's key, the next page", func(t *testing.T) {
		_, in := request(t, []Filter{eq("id", "o1")}, `{"id":{"S":"o1"}}`)
		if in["op"] != "Query" || in["IndexName"] != nil || in["KeyConditionExpression"] != "#n0 = :v0" || in["FilterExpression"] != nil ||
			text(in["ExclusiveStartKey"]) != `{"id":{"S":"o1"}}` {
			t.Errorf("got %v", in)
		}
	})

	t.Run("no key: every item, with the filters", func(t *testing.T) {
		page, in := request(t, []Filter{eq("sip_status", "PENDING"), op("note", "exists", ""), op("name", "starts", "As"), {Field: "paid", Operator: "neq", Value: "true", Type: "BOOL"}}, "")
		if in["op"] != "Scan" || in["KeyConditionExpression"] != nil || in["IndexName"] != nil ||
			in["FilterExpression"] != "#n0 = :v0 AND attribute_exists(#n1) AND begins_with(#n2, :v1) AND #n3 <> :v2" ||
			text(in["ExpressionAttributeNames"]) != `{"#n0":"sip_status","#n1":"note","#n2":"name","#n3":"paid"}` ||
			text(in["ExpressionAttributeValues"]) != `{":v0":{"S":"PENDING"},":v1":{"S":"As"},":v2":{"BOOL":true}}` {
			t.Errorf("got %v", in)
		}
		if page.Plan.Query {
			t.Errorf("plan %+v", page.Plan)
		}
	})

	t.Run("a field used twice is named once", func(t *testing.T) {
		_, in := request(t, []Filter{op("amount", "gte", "1"), op("amount", "lte", "9")}, "")
		if in["FilterExpression"] != "#n0 >= :v0 AND #n0 <= :v1" {
			t.Errorf("got %v", in["FilterExpression"])
		}
	})

	t.Run("a bad value: says so, asks AWS nothing", func(t *testing.T) {
		url, requests := fakeFinder(t)
		_, err := connected(t, url).FindItems(t.Context(), "tm_orders", []Filter{{Field: "amount", Operator: "gt", Value: "lots", Type: "N"}}, "")
		if err == nil || !strings.Contains(err.Error(), "isn't a number") || len(*requests) != 0 {
			t.Fatalf("%v; %d requests", err, len(*requests))
		}
		_, err = connected(t, url).FindItems(t.Context(), "tm_orders", []Filter{eq("id", "")}, "")
		if err == nil || err.Error() != "Type a value for id." || len(*requests) != 0 {
			t.Fatalf("%v; %d requests", err, len(*requests))
		}
	})

	t.Run("PlanFind says the same without reading", func(t *testing.T) {
		url, requests := fakeFinder(t)
		s := connected(t, url)
		if p := s.PlanFind(t.Context(), "tm_orders", []Filter{eq("sip_type", "SIP")}); p != (FindPlan{Query: true, Index: "sip_type_status"}) {
			t.Errorf("got %+v", p)
		}
		if p := s.PlanFind(t.Context(), "tm_orders", []Filter{eq("sip_status", "PENDING")}); p.Query {
			t.Errorf("got %+v", p)
		}
		if len(*requests) != 0 {
			t.Errorf("%d reads", len(*requests))
		}
	})
}

// The real thing, read-only: on a real table, it finds a value of an
// index's key (or the table's) on the first page, looks those items up by
// it, and checks every one has it; then the same with a field no key has
// (checking every item). Logs counts and index names only, never values.
// Only with SPYDB_AWS_PROFILE and SPYDB_AWS_TABLE set.
func TestFindItemsFromAWS(t *testing.T) {
	profile, table := os.Getenv("SPYDB_AWS_PROFILE"), os.Getenv("SPYDB_AWS_TABLE")
	if profile == "" || table == "" {
		t.Skip("set SPYDB_AWS_PROFILE and SPYDB_AWS_TABLE (reads a few pages of that table; changes nothing)")
	}
	s := &Service{}
	if _, err := s.Connect(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	details, err := s.DescribeTable(t.Context(), table)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.FindItems(t.Context(), table, nil, "")
	if err != nil || len(first.Items) == 0 {
		t.Fatalf("first page: %d items, %v", len(first.Items), err)
	}
	// A key field to look up by, preferring an index's.
	keys := []string{}
	for _, ix := range details.Indexes {
		if ix.AllFields {
			keys = append(keys, ix.PartitionKey)
		}
	}
	keys = append(keys, details.PartitionKey)
	var by Filter
	for _, field := range keys {
		for _, item := range first.Items {
			if v, ok := item[field]; ok && (v.Type == "S" || v.Type == "N") && v.Text != "" {
				by = Filter{Field: field, Operator: "eq", Value: v.Text}
				break
			}
		}
		if by.Field != "" {
			break
		}
	}
	check := func(label string, filters []Filter, wantQuery bool) {
		t.Helper()
		page, err := s.FindItems(t.Context(), table, filters, "")
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if page.Plan.Query != wantQuery {
			t.Fatalf("%s: plan %+v", label, page.Plan)
		}
		for _, item := range page.Items {
			if item[filters[0].Field].Text != filters[0].Value {
				t.Fatalf("%s: an item without the value", label)
			}
		}
		t.Logf("%s: plan %+v, %d found of %d checked, more: %v", label, page.Plan, len(page.Items), page.Checked, page.Next != "")
	}
	check("by a key ("+by.Field+")", []Filter{by}, true)
	// The key first, then more filters (as user_id, then sip_type and
	// sip_status): still that key, the rest narrowing its items.
	keyed, err := s.FindItems(t.Context(), table, []Filter{by}, "")
	if err != nil || len(keyed.Items) == 0 {
		t.Fatalf("by the key: %d items, %v", len(keyed.Items), err)
	}
	for name, v := range keyed.Items[0] {
		if name != by.Field && v.Type == "S" && v.Text != "" {
			more := []Filter{by, {Field: name, Operator: "eq", Value: v.Text, Type: "S"}}
			page, err := s.FindItems(t.Context(), table, more, "")
			if err != nil {
				t.Fatal(err)
			}
			if page.Plan != keyed.Plan || len(page.Items) == 0 {
				t.Fatalf("key + a filter: plan %+v (want %+v), %d found", page.Plan, keyed.Plan, len(page.Items))
			}
			for _, item := range page.Items {
				if item[by.Field].Text != by.Value || item[name].Text != v.Text {
					t.Fatal("key + a filter: an item that doesn't match both")
				}
			}
			t.Logf("key + a filter: plan %+v, %d found of %d checked", page.Plan, len(page.Items), page.Checked)
			break
		}
	}
	// A field no key has, from the first item: every item is checked.
	for name, v := range first.Items[0] {
		if details.KeyTypes[name] == "" && v.Type == "S" && v.Text != "" {
			check("by a non-key field", []Filter{{Field: name, Operator: "eq", Value: v.Text, Type: "S"}}, false)
			break
		}
	}
}
