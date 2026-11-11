package dynamo

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Every DynamoDB type, as the grid gets it.
func TestToValue(t *testing.T) {
	address := &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{
		"city": &types.AttributeValueMemberS{Value: "Delhi <south> & co"},
		"pin":  &types.AttributeValueMemberN{Value: "110001"},
		"tags": &types.AttributeValueMemberL{Value: []types.AttributeValue{
			&types.AttributeValueMemberBOOL{Value: true},
			&types.AttributeValueMemberNULL{Value: true},
			&types.AttributeValueMemberN{Value: "12345678901234567890.123456789"},
		}},
	}}
	for _, c := range []struct {
		in   types.AttributeValue
		want Value
	}{
		{&types.AttributeValueMemberS{Value: "Asha"}, Value{"S", "Asha"}},
		{&types.AttributeValueMemberS{Value: ""}, Value{"S", ""}},
		// 38 digits: more than a JavaScript number holds, kept exactly.
		{&types.AttributeValueMemberN{Value: "12345678901234567890123456789012345678"}, Value{"N", "12345678901234567890123456789012345678"}},
		{&types.AttributeValueMemberN{Value: "-0.5"}, Value{"N", "-0.5"}},
		{&types.AttributeValueMemberBOOL{Value: false}, Value{"BOOL", "false"}},
		{&types.AttributeValueMemberNULL{Value: true}, Value{"NULL", ""}},
		{&types.AttributeValueMemberB{Value: []byte("hi")}, Value{"B", "aGk="}},
		{address, Value{"M", `{"city":"Delhi <south> & co","pin":110001,"tags":[true,null,12345678901234567890.123456789]}`}},
		{&types.AttributeValueMemberL{Value: nil}, Value{"L", `[]`}},
		{&types.AttributeValueMemberSS{Value: []string{"a", "b"}}, Value{"SS", `["a","b"]`}},
		{&types.AttributeValueMemberNS{Value: []string{"1", "2.50"}}, Value{"NS", `[1,2.50]`}},
		{&types.AttributeValueMemberBS{Value: [][]byte{[]byte("hi")}}, Value{"BS", `["aGk="]`}},
	} {
		if got := toValue(c.in); got != c.want {
			t.Errorf("%T: got %+v, want %+v", c.in, got, c.want)
		}
	}
}

// The text that says where the next page starts round-trips every kind of key.
func TestNextPageKey(t *testing.T) {
	key := map[string]types.AttributeValue{
		"userId":    &types.AttributeValueMemberS{Value: `u-1 "quoted"`},
		"createdAt": &types.AttributeValueMemberN{Value: "1731400000"},
		"hash":      &types.AttributeValueMemberB{Value: []byte{0, 1, 255}},
	}
	next, err := encodeNext(key)
	if err != nil {
		t.Fatal(err)
	}
	back, err := decodeNext(next)
	if err != nil || !reflect.DeepEqual(back, key) {
		t.Fatalf("%s came back as %#v, %v", next, back, err)
	}
	for _, bad := range []string{"x", "{}", `{"id":{}}`, `{"id":"S"}`} {
		if _, err := decodeNext(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, err := encodeNext(map[string]types.AttributeValue{"id": &types.AttributeValueMemberBOOL{Value: true}}); err == nil {
		t.Error("encoded a BOOL key")
	}
}

// fakeTable is a DynamoDB with one table, "orders", of n items keyed by
// "id" ("item-000", …), each with a few fields; Scan answers up to its Limit
// at a time, as AWS does. ops records each call's operation and body.
func fakeTable(t *testing.T, n int, refuse string) (url string, ops *[]string, bodies *[]map[string]any) {
	ops, bodies = &[]string{}, &[]map[string]any{}
	items := make([]map[string]any, n)
	for i := range items {
		items[i] = map[string]any{
			"id":    map[string]any{"S": fmt.Sprintf("item-%03d", i)},
			"total": map[string]any{"N": fmt.Sprint(i * 10)},
		}
		if i%2 == 0 {
			items[i]["note"] = map[string]any{"M": map[string]any{"paid": map[string]any{"BOOL": true}}}
		}
	}
	url, _ = fakeDynamoAPI(t, func(op string, in map[string]any) (int, any) {
		*ops = append(*ops, op)
		*bodies = append(*bodies, in)
		if refuse != "" {
			return awsRefusal(refuse, op)
		}
		switch op {
		case "DescribeTable":
			return 200, map[string]any{"Table": map[string]any{
				"TableName":      in["TableName"],
				"KeySchema":      []any{map[string]any{"AttributeName": "userId", "KeyType": "HASH"}, map[string]any{"AttributeName": "createdAt", "KeyType": "RANGE"}},
				"ItemCount":      12345,
				"TableSizeBytes": 3200000,
			}}
		case "Scan":
			start := 0
			if key, ok := in["ExclusiveStartKey"].(map[string]any); ok {
				var at int
				fmt.Sscanf(key["id"].(map[string]any)["S"].(string), "item-%d", &at)
				start = at + 1
			}
			end := min(start+int(in["Limit"].(float64)), n)
			out := map[string]any{"Items": items[start:end], "Count": end - start, "ScannedCount": end - start}
			if end < n {
				out["LastEvaluatedKey"] = map[string]any{"id": items[end-1]["id"]}
			}
			return 200, out
		}
		return 400, map[string]string{"__type": "UnknownOperationException", "message": "not faked: " + op}
	})
	return url, ops, bodies
}

func connected(t *testing.T, url string) *Service {
	t.Helper()
	awsFiles(t, tablesConfig, tablesCredentials)
	s := &Service{endpoint: url}
	if _, err := s.Connect(t.Context(), "orders"); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestScanItems(t *testing.T) {
	t.Run("page after page, 50 at a time, only reading", func(t *testing.T) {
		url, ops, bodies := fakeTable(t, 120, "")
		s := connected(t, url)
		var got []map[string]Value
		next, pages := "", 0
		for {
			page, err := s.ScanItems(t.Context(), "orders", next)
			if err != nil {
				t.Fatal(err)
			}
			pages++
			got = append(got, page.Items...)
			if page.Next == "" {
				break
			}
			next = page.Next
		}
		if pages != 3 || len(got) != 120 {
			t.Fatalf("%d pages, %d items; want 3 pages (50 + 50 + 20), 120 items", pages, len(got))
		}
		for i, item := range got {
			if item["id"] != (Value{"S", fmt.Sprintf("item-%03d", i)}) || item["total"] != (Value{"N", fmt.Sprint(i * 10)}) {
				t.Fatalf("item %d: %+v", i, item)
			}
			if _, has := item["note"]; has != (i%2 == 0) {
				t.Fatalf("item %d: note %v", i, has)
			}
		}
		if got[0]["note"] != (Value{"M", `{"paid":true}`}) {
			t.Errorf("note: %+v", got[0]["note"])
		}
		for i, op := range *ops {
			b := (*bodies)[i]
			if op != "Scan" || b["TableName"] != "orders" || b["Limit"] != float64(50) || b["ConsistentRead"] != nil {
				t.Errorf("call %d: %s %v", i, op, b)
			}
		}
		if start := (*bodies)[1]["ExclusiveStartKey"]; fmt.Sprint(start) != "map[id:map[S:item-049]]" {
			t.Errorf("page 2 started at %v, want after item-049", start)
		}
	})

	t.Run("an empty table: no items, no next page", func(t *testing.T) {
		url, _, _ := fakeTable(t, 0, "")
		page, err := connected(t, url).ScanItems(t.Context(), "orders", "")
		if err != nil || page.Items == nil || len(page.Items) != 0 || page.Next != "" {
			t.Fatalf("got %+v, %v", page, err)
		}
		if b, _ := json.Marshal(page); !strings.Contains(string(b), `"items":[]`) {
			t.Errorf("the screen would get %s", b)
		}
	})

	t.Run("a broken next: says so, asks AWS nothing", func(t *testing.T) {
		url, ops, _ := fakeTable(t, 10, "")
		if _, err := connected(t, url).ScanItems(t.Context(), "orders", "not a key"); err == nil || len(*ops) != 0 {
			t.Fatalf("%v; %d calls", err, len(*ops))
		}
	})

	t.Run("not allowed: AWS's reason", func(t *testing.T) {
		url, _, _ := fakeTable(t, 10, "AccessDeniedException")
		_, err := connected(t, url).ScanItems(t.Context(), "orders", "")
		if err == nil || !strings.Contains(err.Error(), "isn't allowed") || !strings.Contains(err.Error(), "dynamodb:Scan") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("not connected", func(t *testing.T) {
		if _, err := (&Service{}).ScanItems(t.Context(), "orders", ""); err == nil {
			t.Fatal("scanned without connecting")
		}
		if _, err := (&Service{}).DescribeTable(t.Context(), "orders"); err == nil {
			t.Fatal("described without connecting")
		}
	})
}

func TestDescribeTable(t *testing.T) {
	url, ops, bodies := fakeTable(t, 0, "")
	got, err := connected(t, url).DescribeTable(t.Context(), "orders")
	want := TableDetails{Name: "orders", PartitionKey: "userId", SortKey: "createdAt", ItemCount: 12345, SizeBytes: 3200000}
	if err != nil || got != want {
		t.Fatalf("got %+v, %v; want %+v", got, err, want)
	}
	if len(*ops) != 1 || (*ops)[0] != "DescribeTable" || (*bodies)[0]["TableName"] != "orders" {
		t.Fatalf("calls %v %v", *ops, *bodies)
	}
}

// The real thing, read-only: one page of a real table. Logs only counts,
// never the items. Only with SPYDB_AWS_PROFILE and SPYDB_AWS_TABLE set.
func TestScanItemsFromAWS(t *testing.T) {
	profile, table := os.Getenv("SPYDB_AWS_PROFILE"), os.Getenv("SPYDB_AWS_TABLE")
	if profile == "" || table == "" {
		t.Skip("set SPYDB_AWS_PROFILE and SPYDB_AWS_TABLE (reads one page of that table; changes nothing)")
	}
	s := &Service{}
	if _, err := s.Connect(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	details, err := s.DescribeTable(t.Context(), table)
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.ScanItems(t.Context(), table, "")
	if err != nil {
		t.Fatal(err)
	}
	fields, types := map[string]bool{}, map[string]bool{}
	for _, item := range page.Items {
		for name, v := range item {
			fields[name], types[v.Type] = true, true
		}
	}
	t.Logf("key %q/%q, ~%d items; first page: %d items, %d field names, types %v, more: %v, %d ms",
		details.PartitionKey, details.SortKey, details.ItemCount, len(page.Items), len(fields), keysOf(types), page.Next != "", page.DurationMs)
	if page.Next == "" {
		return
	}
	// Load more: the second page starts after the first, repeating none of it.
	second, err := s.ScanItems(t.Context(), table, page.Next)
	if err != nil {
		t.Fatal(err)
	}
	key := func(item map[string]Value) string { return item[details.PartitionKey].Text + "\x00" + item[details.SortKey].Text }
	firstKeys := map[string]bool{}
	for _, item := range page.Items {
		firstKeys[key(item)] = true
	}
	repeated := 0
	for _, item := range second.Items {
		if firstKeys[key(item)] {
			repeated++
		}
	}
	if repeated > 0 || len(second.Items) == 0 {
		t.Fatalf("second page: %d items, %d of them already on the first", len(second.Items), repeated)
	}
	t.Logf("second page: %d items, none repeated", len(second.Items))
}

func keysOf(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
