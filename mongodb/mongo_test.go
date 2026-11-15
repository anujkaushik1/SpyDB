package mongodb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// testServer connects a Service to the MongoDB at SPYDB_MONGO_URL (e.g.
// mongodb://127.0.0.1:27017), in a new database spydb_test_<random> filled
// with made-up documents; the database is dropped afterwards. Only
// spydb_test_* databases are ever written. Skipped without the variable.
func testServer(t *testing.T) (*Service, *mongo.Database) {
	t.Helper()
	url := os.Getenv("SPYDB_MONGO_URL")
	if url == "" {
		t.Skip("set SPYDB_MONGO_URL to a MongoDB to test against (only spydb_test_* databases are written)")
	}
	// Recent connections go to a settings folder of the test's own.
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AppData", dir)

	b := make([]byte, 4)
	rand.Read(b)
	name := "spydb_test_" + hex.EncodeToString(b)
	client, err := mongo.Connect(options.Client().ApplyURI(url))
	if err != nil {
		t.Fatal(err)
	}
	db := client.Database(name)
	t.Cleanup(func() {
		if !strings.HasPrefix(db.Name(), "spydb_test_") {
			t.Fatalf("refusing to drop %s", db.Name())
		}
		db.Drop(context.Background())
		client.Disconnect(context.Background())
	})
	ctx := context.Background()
	orders := bson.A{}
	for i := 1; i <= 120; i++ {
		order := bson.D{
			{Key: "_id", Value: int32(i)},
			{Key: "status", Value: []string{"PAID", "NEW", "SHIPPED"}[i%3]},
			{Key: "total", Value: int64(i * 100)},
			{Key: "placed", Value: bson.NewDateTimeFromTime(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i))},
			{Key: "address", Value: bson.D{{Key: "city", Value: "Delhi"}, {Key: "pin", Value: int64(110001)}}},
		}
		if i%10 == 0 {
			order = append(order, bson.E{Key: "note", Value: "gift"})
		}
		orders = append(orders, order)
	}
	if _, err := db.Collection("orders").InsertMany(ctx, []any(orders)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("people").InsertOne(ctx, bson.D{{Key: "name", Value: "Asha"}}); err != nil {
		t.Fatal(err)
	}

	s := &Service{}
	t.Cleanup(s.Disconnect)
	info, err := s.Connect(Config{URI: url, Database: name})
	if err != nil {
		t.Fatal(err)
	}
	if info.Database != name || info.ServerVersion == "" {
		t.Fatalf("connected to %+v", info)
	}
	return s, db
}

func TestBrowse(t *testing.T) {
	s, db := testServer(t)
	ctx := context.Background()

	names, err := s.ListDatabases(ctx)
	if err != nil || !slices.Contains(names, db.Name()) {
		t.Fatalf("databases: %v %v", names, err)
	}
	colls, err := s.ListCollections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(colls) != 2 || colls[0].Name != "orders" || colls[0].Count == nil || *colls[0].Count != 120 || colls[1].Name != "people" {
		t.Fatalf("collections: %+v", colls)
	}

	page, err := s.FindDocuments(ctx, DocumentQuery{Collection: "orders", PageSize: 50})
	if err != nil || len(page.Documents) != 50 || !page.HasMore {
		t.Fatalf("first page: %d documents, more %v, %v", len(page.Documents), page.HasMore, err)
	}
	doc := page.Documents[0]
	if doc.ID != `{"$numberInt":"1"}` || strings.Join(doc.Order, ",") != "_id,status,total,placed,address" ||
		doc.Fields["total"] != (Value{"long", "100"}) || doc.Fields["placed"].Text != "2024-01-02T00:00:00.000Z" {
		t.Fatalf("document: %+v", doc)
	}
	last, err := s.FindDocuments(ctx, DocumentQuery{Collection: "orders", PageSize: 50, Page: 2})
	if err != nil || len(last.Documents) != 20 || last.HasMore {
		t.Fatalf("last page: %d, more %v, %v", len(last.Documents), last.HasMore, err)
	}

	found, err := s.FindDocuments(ctx, DocumentQuery{Collection: "orders", PageSize: 50, SortBy: "total", SortDesc: true, Filters: []Filter{
		{Field: "status", Operator: "eq", Value: "PAID"},
		{Field: "total", Operator: "gte", Value: "6000", Type: "number"},
		{Field: "address.city", Operator: "starts", Value: "Del"},
		{Field: "placed", Operator: "lt", Value: "2024-04-01", Type: "date"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// PAID is every 3rd; total ≥ 6000 is i ≥ 60; placed before April is i ≤ 90.
	var ids []string
	for _, d := range found.Documents {
		ids = append(ids, d.Fields["_id"].Text)
	}
	if strings.Join(ids, ",") != "90,87,84,81,78,75,72,69,66,63,60" {
		t.Fatalf("filtered, sorted: %v", ids)
	}
	gifts, _ := s.FindDocuments(ctx, DocumentQuery{Collection: "orders", PageSize: 50, Filters: []Filter{{Field: "note", Operator: "exists"}}})
	if len(gifts.Documents) != 12 {
		t.Fatalf("note exists: %d", len(gifts.Documents))
	}
}

// read is one document straight from the server.
func read(t *testing.T, db *mongo.Database, id any) bson.Raw {
	t.Helper()
	r, err := db.Collection("orders").FindOne(context.Background(), bson.D{{Key: "_id", Value: id}}).Raw()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSaveDocuments(t *testing.T) {
	s, db := testServer(t)
	ctx := context.Background()
	id1, id2 := `{"$numberInt":"1"}`, `{"$numberInt":"2"}`
	city := `{"city":"Delhi","pin":110001}`

	saved, err := s.SaveDocuments(ctx, "orders", []DocumentEdit{
		{ID: id1, Old: map[string]Shown{
			"total":   {Has: true, Text: "100"},
			"status":  {Has: true, Text: "NEW"},
			"rating":  {},
			"address": {Has: true, Text: city},
		}, Changes: map[string]FieldChange{
			"total":   {Text: "150"},                                                     // a typed cell: stays a long
			"status":  {Remove: true},                                                    // ✕ in the cell editor
			"rating":  {Text: "4.5", Type: "double"},                                     // an added column of numbers
			"address": {Text: `{"city":"Pune","pin":110001}`, Type: "json", Typed: true}, // the JSON view
		}},
		{ID: id2, Old: map[string]Shown{"note": {}}, Changes: map[string]FieldChange{"note": {Text: "5", Type: "number", Typed: true}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 || saved[0].Fields["total"].Text != "150" || saved[1].Fields["note"] != (Value{"int", "5"}) {
		t.Fatalf("saved: %+v", saved)
	}
	r := read(t, db, int32(1))
	if r.Lookup("total").Type != bson.TypeInt64 || r.Lookup("total").Int64() != 150 {
		t.Errorf("total: %v", r.Lookup("total"))
	}
	if _, err := r.LookupErr("status"); err == nil {
		t.Error("status should be gone")
	}
	if r.Lookup("rating").Double() != 4.5 {
		t.Errorf("rating: %v", r.Lookup("rating"))
	}
	if pin := r.Lookup("address", "pin"); pin.Type != bson.TypeInt64 || r.Lookup("address", "city").StringValue() != "Pune" {
		t.Errorf("address: %v (pin should stay a long)", r.Lookup("address"))
	}

	// Someone else changes order 3's total after it was loaded: nothing saves, order 4 included.
	if _, err := db.Collection("orders").UpdateOne(ctx, bson.D{{Key: "_id", Value: int32(3)}}, bson.D{{Key: "$set", Value: bson.D{{Key: "total", Value: int64(999)}}}}); err != nil {
		t.Fatal(err)
	}
	_, err = s.SaveDocuments(ctx, "orders", []DocumentEdit{
		{ID: `{"$numberInt":"4"}`, Old: map[string]Shown{"total": {Has: true, Text: "400"}}, Changes: map[string]FieldChange{"total": {Text: "1"}}},
		{ID: `{"$numberInt":"3"}`, Old: map[string]Shown{"total": {Has: true, Text: "300"}}, Changes: map[string]FieldChange{"total": {Text: "1"}}},
	})
	if err == nil || !strings.Contains(err.Error(), "changed since you loaded it (it was “300”, now “999”)") {
		t.Fatalf("conflict: %v", err)
	}
	if read(t, db, int32(4)).Lookup("total").Int64() != 400 {
		t.Error("order 4 was saved although order 3 had changed")
	}

	for _, bad := range []DocumentEdit{
		{ID: id1, Old: map[string]Shown{"_id": {Has: true, Text: "1"}}, Changes: map[string]FieldChange{"_id": {Text: "9"}}},
		{ID: id1, Old: map[string]Shown{"total": {Has: true, Text: "150"}}, Changes: map[string]FieldChange{"total": {Text: "lots"}}},
		{ID: id1, Old: map[string]Shown{"a.b": {}}, Changes: map[string]FieldChange{"a.b": {Text: "x"}}},
		{ID: `{"$numberInt":"9999"}`, Old: map[string]Shown{"total": {}}, Changes: map[string]FieldChange{"total": {Text: "1"}}},
	} {
		if _, err := s.SaveDocuments(ctx, "orders", []DocumentEdit{bad}); err == nil {
			t.Errorf("%+v should be refused", bad)
		}
	}
}

func TestDeleteDocuments(t *testing.T) {
	s, db := testServer(t)
	ctx := context.Background()
	page, _ := s.FindDocuments(ctx, DocumentQuery{Collection: "orders", PageSize: 3})
	shown := func(d Document) map[string]Shown {
		old := map[string]Shown{}
		for name, v := range d.Fields {
			old[name] = Shown{Has: true, Null: v.Type == "null", Text: v.Text}
		}
		old["rating"] = Shown{} // a column other documents have
		return old
	}
	// Order 3 changes behind the grid's back: nothing is deleted.
	db.Collection("orders").UpdateOne(ctx, bson.D{{Key: "_id", Value: int32(3)}}, bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: "X"}}}})
	_, err := s.DeleteDocuments(ctx, "orders", []DocumentDelete{{ID: page.Documents[0].ID, Old: shown(page.Documents[0])}, {ID: page.Documents[2].ID, Old: shown(page.Documents[2])}})
	if err == nil || !strings.Contains(err.Error(), "status changed since you loaded it") {
		t.Fatalf("conflict: %v", err)
	}
	n, err := s.DeleteDocuments(ctx, "orders", []DocumentDelete{{ID: page.Documents[0].ID, Old: shown(page.Documents[0])}, {ID: page.Documents[1].ID, Old: shown(page.Documents[1])}})
	if err != nil || n != 2 {
		t.Fatalf("deleted %d: %v", n, err)
	}
	if left, _ := db.Collection("orders").CountDocuments(ctx, bson.D{}); left != 118 {
		t.Fatalf("%d left", left)
	}
}

func TestRunQuery(t *testing.T) {
	s, db := testServer(t)
	ctx := context.Background()
	run := func(text string) QueryResult {
		t.Helper()
		r, err := s.RunQuery(ctx, "tab1", text)
		if err != nil {
			t.Fatalf("%s: %v", text, err)
		}
		return r
	}

	r := run(`db.orders.find({status: 'PAID'}).sort({total: -1})`)
	if len(r.Documents) != 40 || r.More || !r.Editable || r.Documents[0].Fields["total"].Text != "12000" {
		t.Fatalf("find: %d docs, more %v, editable %v", len(r.Documents), r.More, r.Editable)
	}
	r = run(`db.orders.find({}, {total: 1})`)
	if len(r.Documents) != 50 || !r.More || len(r.Documents[0].Order) != 2 {
		t.Fatalf("find all: %d, more %v, fields %v", len(r.Documents), r.More, r.Documents[0].Order)
	}
	total := len(r.Documents)
	for more := r.More; more; {
		p, err := s.FetchMore(ctx, "tab1", r.RunID)
		if err != nil {
			t.Fatal(err)
		}
		total += len(p.Documents)
		more = p.More
	}
	if total != 120 {
		t.Fatalf("paged through %d", total)
	}
	if _, err := s.FetchMore(ctx, "tab1", r.RunID); err == nil {
		t.Error("a finished result should be closed")
	}

	r = run(`db.orders.aggregate([{$group: {_id: "$status", n: {$sum: 1}}}, {$sort: {_id: 1}}])`)
	if len(r.Documents) != 3 || r.Editable || r.Documents[0].Fields["_id"].Text != "NEW" || r.Documents[0].Fields["n"].Text != "40" {
		t.Fatalf("aggregate: %+v", r.Documents)
	}
	if r = run(`db.orders.countDocuments({note: {$exists: true}})`); r.Message != "12 documents" {
		t.Fatalf("count: %q", r.Message)
	}
	if r = run(`db.orders.distinct("status")`); len(r.Documents) != 3 || r.Message != "3 distinct values" {
		t.Fatalf("distinct: %+v %q", r.Documents, r.Message)
	}
	if r = run(`db.people.insertOne({_id: ObjectId("64f1c0ffee0000000000abcd"), name: 'Ravi', joined: ISODate("2024-05-01T00:00:00Z")})`); r.Message != "Inserted 1 document (_id 64f1c0ffee0000000000abcd)" {
		t.Fatalf("insertOne: %q", r.Message)
	}
	if r = run(`db.people.insertMany([{name: "A"}, {name: "B"}])`); r.Message != "Inserted 2 documents" {
		t.Fatalf("insertMany: %q", r.Message)
	}
	if r = run(`db.orders.updateMany({status: "NEW"}, {$set: {status: "OPEN"}})`); r.Message != "Matched 40 documents, changed 40" {
		t.Fatalf("updateMany: %q", r.Message)
	}
	if r = run(`db.people.updateOne({name: "Zed"}, {$set: {age: 3}}, {upsert: true})`); !strings.HasPrefix(r.Message, "Matched 0 documents, changed 0, inserted 1 (_id ") {
		t.Fatalf("upsert: %q", r.Message)
	}
	if r = run(`db.orders.deleteMany({status: "OPEN"})`); r.Message != "Deleted 40 documents" {
		t.Fatalf("deleteMany: %q", r.Message)
	}
	if r = run(`db.people.findOne({name: "Ravi"})`); len(r.Documents) != 1 || r.Documents[0].Fields["joined"].Type != "date" {
		t.Fatalf("findOne: %+v", r.Documents)
	}
	if n, _ := db.Collection("orders").CountDocuments(ctx, bson.D{}); n != 80 {
		t.Fatalf("%d orders left", n)
	}

	var syntax *SyntaxError
	if _, err := s.RunQuery(ctx, "tab1", "db.orders.find({a: })"); !errors.As(err, &syntax) {
		t.Errorf("a typo should be a SyntaxError: %v", err)
	}
	if _, err := s.RunQuery(ctx, "tab1", "db.orders.drop()"); err == nil || !strings.Contains(err.Error(), "doesn’t run drop") {
		t.Errorf("drop: %v", err)
	}

	// Esc: the call's context ends.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.RunQuery(cancelled, "tab1", `db.orders.find({$where: "sleep(100) || true"})`); err == nil {
		t.Error("a cancelled run should fail")
	}
}

func TestSavedConnections(t *testing.T) {
	s, _ := testServer(t) // its Connect put one in Recent
	lists, err := s.SavedConnections()
	if err != nil || len(lists.Recent) != 1 || len(lists.Saved) != 0 {
		t.Fatalf("after connecting: %+v %v", lists, err)
	}
	saved, err := s.SaveConnection(SavedConnection{Name: " Local ", Config: lists.Recent[0].Config})
	if err != nil || saved.Name != "Local" || saved.ID == "" {
		t.Fatalf("save: %+v %v", saved, err)
	}
	if _, err := s.SaveConnection(SavedConnection{Name: "", Config: saved.Config}); err == nil {
		t.Error("a nameless connection should be refused")
	}
	if err := s.DeleteConnection(saved.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearRecent(); err != nil {
		t.Fatal(err)
	}
	if lists, _ := s.SavedConnections(); len(lists.Saved)+len(lists.Recent) != 0 {
		t.Fatalf("after clearing: %+v", lists)
	}
}

// TestApplyAllOrNothing: a document that changed between the check and the
// write stops the save. A replica set undoes the ones before it (one
// transaction); a single server can't, and says which were saved.
func TestApplyAllOrNothing(t *testing.T) {
	s, db := testServer(t)
	ctx := context.Background()
	coll := db.Collection("orders")
	set := bson.D{{Key: "$set", Value: bson.D{{Key: "total", Value: int64(1)}}}}
	writes := []write{
		{name: "_id 1", filter: bson.D{{Key: "_id", Value: int32(1)}}, update: set},
		{name: "_id 2", filter: bson.D{{Key: "_id", Value: int32(2)}, {Key: "total", Value: int64(-5)}}, update: set}, // as if it changed meanwhile
	}
	err := s.apply(ctx, coll, writes, "saved")
	first := read(t, db, int32(1)).Lookup("total").Int64()
	if s.txns {
		if err == nil || !strings.Contains(err.Error(), "_id 2 changed while saving, so nothing was saved") || first != 100 {
			t.Fatalf("replica set: %v, order 1's total %d (should be undone)", err, first)
		}
	} else if err == nil || !strings.Contains(err.Error(), "The first 1 of 2 were saved, but the one with _id 2 wasn’t") || first != 1 {
		t.Fatalf("single server: %v, order 1's total %d", err, first)
	}
	t.Logf("transactions: %v", s.txns)
}
