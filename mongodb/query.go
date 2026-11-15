package mongodb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// QueryResult is what a query editor command did.
type QueryResult struct {
	RunID      int        `json:"runId"`      // for FetchMore
	Method     string     `json:"method"`     // "find", "aggregate", "insertOne"…
	Collection string     `json:"collection"` // the collection it ran on
	Documents  []Document `json:"documents"`  // find, findOne, aggregate, distinct: the first page; nil for the others
	More       bool       `json:"more"`       // there are more to fetch (FetchMore)
	Editable   bool       `json:"editable"`   // find / findOne: the documents can be edited (they're the collection's own, found by _id)
	Message    string     `json:"message"`    // what a write or a count did: "Inserted 1 document", "42 documents"
	DurationMs int64      `json:"durationMs"`
}

// QueryPage is more documents of a result.
type QueryPage struct {
	Documents []Document `json:"documents"`
	More      bool       `json:"more"`
}

// resultPage is how many documents a result shows at a time; Load more reads the next ones.
const resultPage = 50

// run is a query tab's result still being read: the server keeps its cursor
// open (for about 10 minutes unused) until it's read to the end or closed.
type run struct {
	id     int
	cursor *mongo.Cursor
}

// RunQuery runs one command of a query tab (see parseCommand) on the open
// database. A find or aggregate result's first page comes back now, the
// rest with FetchMore. A new run, or ForgetRun, closes the tab's last one.
func (s *Service) RunQuery(ctx context.Context, tab string, text string) (QueryResult, error) {
	cmd, err := parseCommand(text)
	if err != nil {
		return QueryResult{}, err
	}
	coll, _, err := s.collection(cmd.collection)
	if err != nil {
		return QueryResult{}, err
	}
	s.ForgetRun(tab)
	began := time.Now()
	result := QueryResult{Method: cmd.method, Collection: cmd.collection}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var cursor *mongo.Cursor
	switch cmd.method {
	case "find":
		opts := options.Find()
		if err := applyChain(cmd, opts); err != nil {
			return QueryResult{}, err
		}
		if p := arg(cmd, 1); p != nil {
			opts.SetProjection(p)
		}
		if cursor, err = coll.Find(ctx, filterArg(cmd, 0), opts); err != nil {
			return QueryResult{}, err
		}
		result.Editable = true
	case "findOne":
		opts := options.FindOne()
		if p := arg(cmd, 1); p != nil {
			opts.SetProjection(p)
		}
		raw, err := coll.FindOne(ctx, filterArg(cmd, 0), opts).Raw()
		result.Documents = []Document{}
		if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
			return QueryResult{}, err
		}
		if err == nil {
			doc, err := toDocument(raw)
			if err != nil {
				return QueryResult{}, err
			}
			result.Documents = append(result.Documents, doc)
		}
		result.Editable = true
	case "aggregate":
		pipeline, ok := arg(cmd, 0).(bson.A)
		if !ok {
			return QueryResult{}, errors.New("aggregate takes a list of stages, like aggregate([{$match: {…}}, {$group: {…}}])")
		}
		if cursor, err = coll.Aggregate(ctx, pipeline); err != nil {
			return QueryResult{}, err
		}
	case "countDocuments", "count":
		n, err := coll.CountDocuments(ctx, filterArg(cmd, 0))
		if err != nil {
			return QueryResult{}, err
		}
		result.Message = plural(n, "document")
	case "estimatedDocumentCount":
		n, err := coll.EstimatedDocumentCount(ctx)
		if err != nil {
			return QueryResult{}, err
		}
		result.Message = "About " + plural(n, "document")
	case "distinct":
		field, ok := arg(cmd, 0).(string)
		if !ok {
			return QueryResult{}, errors.New("distinct takes a field name, like distinct(\"status\")")
		}
		res := coll.Distinct(ctx, field, filterArg(cmd, 1))
		if err := res.Err(); err != nil {
			return QueryResult{}, err
		}
		raw, err := res.Raw()
		if err != nil {
			return QueryResult{}, err
		}
		values, _ := raw.Values()
		result.Documents = make([]Document, len(values))
		for i, v := range values {
			result.Documents[i] = Document{Fields: map[string]Value{field: toValue(v)}, Order: []string{field}}
		}
		result.Message = plural(int64(len(values)), "distinct value")
	case "insertOne":
		doc := arg(cmd, 0)
		if _, ok := doc.(bson.D); !ok {
			return QueryResult{}, errors.New("insertOne takes a document, like insertOne({name: \"Asha\"})")
		}
		res, err := coll.InsertOne(ctx, doc)
		if err != nil {
			return QueryResult{}, err
		}
		result.Message = fmt.Sprintf("Inserted 1 document (_id %s)", valueText(res.InsertedID))
	case "insertMany":
		docs, ok := arg(cmd, 0).(bson.A)
		if !ok {
			return QueryResult{}, errors.New("insertMany takes a list of documents, like insertMany([{…}, {…}])")
		}
		res, err := coll.InsertMany(ctx, []any(docs))
		if err != nil {
			return QueryResult{}, err
		}
		result.Message = "Inserted " + plural(int64(len(res.InsertedIDs)), "document")
	case "updateOne", "updateMany", "replaceOne":
		update := arg(cmd, 1)
		if update == nil {
			return QueryResult{}, fmt.Errorf("%s takes a filter and %s, like %s({_id: …}, %s)", cmd.method,
				map[bool]string{true: "a new document", false: "the change"}[cmd.method == "replaceOne"], cmd.method,
				map[bool]string{true: "{name: \"Asha\"}", false: "{$set: {status: \"PAID\"}}"}[cmd.method == "replaceOne"])
		}
		upsert := optionTrue(arg(cmd, 2), "upsert")
		var res *mongo.UpdateResult
		switch cmd.method {
		case "updateOne":
			res, err = coll.UpdateOne(ctx, filterArg(cmd, 0), update, options.UpdateOne().SetUpsert(upsert))
		case "updateMany":
			res, err = coll.UpdateMany(ctx, filterArg(cmd, 0), update, options.UpdateMany().SetUpsert(upsert))
		default:
			res, err = coll.ReplaceOne(ctx, filterArg(cmd, 0), update, options.Replace().SetUpsert(upsert))
		}
		if err != nil {
			return QueryResult{}, err
		}
		result.Message = fmt.Sprintf("Matched %s, changed %d", plural(res.MatchedCount, "document"), res.ModifiedCount)
		if res.UpsertedID != nil {
			result.Message += fmt.Sprintf(", inserted 1 (_id %s)", valueText(res.UpsertedID))
		}
	case "deleteOne", "deleteMany":
		var res *mongo.DeleteResult
		if cmd.method == "deleteOne" {
			res, err = coll.DeleteOne(ctx, filterArg(cmd, 0))
		} else {
			res, err = coll.DeleteMany(ctx, filterArg(cmd, 0))
		}
		if err != nil {
			return QueryResult{}, err
		}
		result.Message = "Deleted " + plural(res.DeletedCount, "document")
	default:
		return QueryResult{}, fmt.Errorf("SpyDB doesn’t run %s(…). It runs find, findOne, aggregate, countDocuments, estimatedDocumentCount, distinct, insertOne, insertMany, updateOne, updateMany, replaceOne, deleteOne and deleteMany.", cmd.method)
	}

	if cursor != nil {
		page, more, err := readPage(ctx, cursor)
		if err != nil {
			cursor.Close(context.Background())
			return QueryResult{}, err
		}
		result.Documents, result.More = page, more
		s.mu.Lock()
		s.runSeq++
		result.RunID = s.runSeq
		if more {
			if s.runs == nil {
				s.runs = map[string]*run{}
			}
			s.runs[tab] = &run{id: result.RunID, cursor: cursor}
		}
		s.mu.Unlock()
		if !more {
			cursor.Close(context.Background())
		}
	}
	result.DurationMs = time.Since(began).Milliseconds()
	return result, nil
}

// FetchMore reads the next page of a tab's result.
func (s *Service) FetchMore(ctx context.Context, tab string, runID int) (QueryPage, error) {
	s.mu.Lock()
	r := s.runs[tab]
	s.mu.Unlock()
	if r == nil || r.id != runID {
		return QueryPage{}, errors.New("This result isn’t open any more: run the command again.")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	page, more, err := readPage(ctx, r.cursor)
	if err != nil {
		if strings.Contains(err.Error(), "CursorNotFound") || strings.Contains(err.Error(), "cursor id") {
			err = errors.New("The server closed this result after it sat unused: run the command again.")
		}
		return QueryPage{}, err
	}
	if !more {
		s.ForgetRun(tab)
	}
	return QueryPage{Documents: page, More: more}, nil
}

// ForgetRun closes a tab's open result: a new run, or the tab closing.
func (s *Service) ForgetRun(tab string) {
	s.mu.Lock()
	r := s.runs[tab]
	delete(s.runs, tab)
	s.mu.Unlock()
	if r != nil {
		r.cursor.Close(context.Background())
	}
}

// closeRunsLocked closes every open result (a new connection, or none). s.mu is held.
func (s *Service) closeRunsLocked() {
	for _, r := range s.runs {
		go r.cursor.Close(context.Background())
	}
	s.runs = nil
}

// readPage reads up to resultPage documents, and whether there are more.
func readPage(ctx context.Context, cursor *mongo.Cursor) ([]Document, bool, error) {
	docs := []Document{}
	for len(docs) < resultPage {
		if !cursor.Next(ctx) {
			return docs, false, cursor.Err()
		}
		doc, err := toDocument(cursor.Current)
		if err != nil {
			return nil, false, err
		}
		docs = append(docs, doc)
	}
	// More only if there's at least one more: TryNext would wait for a tailable cursor.
	return docs, cursor.RemainingBatchLength() > 0 || cursor.ID() != 0, nil
}

// applyChain applies .sort(), .limit(), .skip() and .projection() to a find.
func applyChain(cmd command, opts *options.FindOptionsBuilder) error {
	for _, c := range cmd.chain {
		var v any
		if len(c.args) > 0 {
			v = c.args[0]
		}
		switch c.method {
		case "sort":
			if v == nil {
				return errors.New("sort takes the fields to sort by, like sort({createdAt: -1})")
			}
			opts.SetSort(v)
		case "limit":
			n, ok := wholeNumber(v)
			if !ok {
				return errors.New("limit takes a number, like limit(10)")
			}
			opts.SetLimit(n)
		case "skip":
			n, ok := wholeNumber(v)
			if !ok {
				return errors.New("skip takes a number, like skip(20)")
			}
			opts.SetSkip(n)
		case "projection", "project":
			opts.SetProjection(v)
		case "toArray", "pretty", "batchSize":
			// The shell's ways of showing a result; SpyDB pages it anyway.
		default:
			return fmt.Errorf("SpyDB doesn’t know .%s(…) after find: it knows .sort(), .limit(), .skip() and .projection()", c.method)
		}
	}
	return nil
}

func wholeNumber(v any) (int64, bool) {
	switch n := v.(type) {
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), n == float64(int64(n))
	}
	return 0, false
}

// arg is a command's i'th argument, or nil.
func arg(cmd command, i int) any {
	if i < len(cmd.args) {
		return cmd.args[i]
	}
	return nil
}

// filterArg is the i'th argument as a filter: none means every document.
func filterArg(cmd command, i int) any {
	if v := arg(cmd, i); v != nil {
		return v
	}
	return bson.D{}
}

// optionTrue says whether options (like {upsert: true}) turns name on.
func optionTrue(options any, name string) bool {
	doc, ok := options.(bson.D)
	if !ok {
		return false
	}
	for _, e := range doc {
		if e.Key == name {
			b, _ := e.Value.(bool)
			return b
		}
	}
	return false
}

// valueText is a value (an inserted _id) as the grid would show it.
func valueText(v any) string {
	t, data, err := bson.MarshalValue(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return toValue(bson.RawValue{Type: t, Value: data}).Text
}

// plural is "1 document", "3 documents".
func plural(n int64, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
