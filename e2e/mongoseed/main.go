// Command mongoseed fills and empties the MongoDB database the end-to-end
// tests use (e2e/run.mjs), on every system:
//
//	go run ./e2e/mongoseed <url> reset spydb_test_e2e   (drop it, then add made-up documents)
//	go run ./e2e/mongoseed <url> drop spydb_test_e2e …
//	go run ./e2e/mongoseed <url> dump spydb_test_e2e orders   (every document, as JSON by _id, to compare)
//
// It only ever touches databases named spydb_test_*, so pointing it at a
// MongoDB with real data in it can't harm that data.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func main() {
	if len(os.Args) < 4 || (os.Args[2] != "reset" && os.Args[2] != "drop" && os.Args[2] != "dump") {
		fail("usage: mongoseed <url> reset|drop spydb_test_<name> … | dump spydb_test_<name> <collection>")
	}
	url, action, names := os.Args[1], os.Args[2], os.Args[3:]
	if action == "dump" {
		names = names[:1]
	}
	for _, name := range names {
		if !strings.HasPrefix(name, "spydb_test_") {
			fail("refusing " + name + ": only spydb_test_* databases")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(url).SetServerSelectionTimeout(10 * time.Second))
	if err != nil {
		fail(err.Error())
	}
	defer client.Disconnect(context.Background())
	if action == "dump" {
		dump(ctx, client.Database(names[0]).Collection(os.Args[4]))
		return
	}
	for _, name := range names {
		if err := client.Database(name).Drop(ctx); err != nil {
			fail(err.Error())
		}
	}
	if action == "drop" {
		return
	}
	db := client.Database(names[0])
	// orders 1–120: status every third PAID / NEW / SHIPPED, total a long
	// (i × 100), placed a date, address an object, and a note on every tenth.
	orders := make([]any, 0, 120)
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
	if _, err := db.Collection("orders").InsertMany(ctx, orders); err != nil {
		fail(err.Error())
	}
	if _, err := db.Collection("people").InsertMany(ctx, []any{
		bson.D{{Key: "name", Value: "Asha"}, {Key: "city", Value: "Pune"}},
		bson.D{{Key: "name", Value: "Ravi"}, {Key: "age", Value: int32(31)}},
	}); err != nil {
		fail(err.Error())
	}
}

// dump prints every document of the collection as one JSON object: its _id
// (canonical extended JSON) → the whole document (canonical extended JSON,
// so types count: 150 as a long differs from 150 as a double). The tests
// compare two dumps to see exactly which documents a save or delete changed.
func dump(ctx context.Context, coll *mongo.Collection) {
	cursor, err := coll.Find(ctx, bson.D{})
	if err != nil {
		fail(err.Error())
	}
	out := map[string]string{}
	for cursor.Next(ctx) {
		id, err := bson.MarshalExtJSON(bson.D{{Key: "_id", Value: cursor.Current.Lookup("_id")}}, true, false)
		if err != nil {
			fail(err.Error())
		}
		doc, err := bson.MarshalExtJSON(cursor.Current, true, false)
		if err != nil {
			fail(err.Error())
		}
		out[string(id)] = string(doc)
	}
	if err := cursor.Err(); err != nil {
		fail(err.Error())
	}
	text, _ := json.Marshal(out)
	fmt.Println(string(text))
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "mongoseed:", message)
	os.Exit(1)
}
