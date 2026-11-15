package mongodb

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Filter is one row of a collection tab's filter bar: "status equals PENDING".
type Filter struct {
	Field    string `json:"field"`    // a field, or a path into objects like "address.city"
	Operator string `json:"operator"` // eq, neq, lt, lte, gt, gte, starts, contains, exists, not_exists
	Value    string `json:"value"`    // as typed; not used by exists and not_exists
	Type     string `json:"type"`     // what the field holds, from the documents seen: string, number, bool, date, objectId; "" for text
}

// DocumentQuery is one page of a collection, sorted and filtered.
type DocumentQuery struct {
	Collection string   `json:"collection"`
	Filters    []Filter `json:"filters"` // combined with AND
	SortBy     string   `json:"sortBy"`  // "" keeps the collection's own (natural) order
	SortDesc   bool     `json:"sortDesc"`
	Page       int      `json:"page"` // from 0
	PageSize   int      `json:"pageSize"`
}

// DocumentPage is one page of documents.
type DocumentPage struct {
	Documents  []Document `json:"documents"`
	HasMore    bool       `json:"hasMore"` // there's a next page
	DurationMs int64      `json:"durationMs"`
}

// maxPageSize keeps one page's read bounded.
const maxPageSize = 500

// FindDocuments reads one page of a collection's documents that match every
// filter. It reads one more than a page to know whether there's another.
func (s *Service) FindDocuments(ctx context.Context, q DocumentQuery) (DocumentPage, error) {
	coll, _, err := s.collection(q.Collection)
	if err != nil {
		return DocumentPage{}, err
	}
	filter, err := filterDocument(q.Filters)
	if err != nil {
		return DocumentPage{}, err
	}
	size := min(max(q.PageSize, 1), maxPageSize)
	opts := options.Find().SetSkip(int64(max(q.Page, 0) * size)).SetLimit(int64(size + 1))
	if q.SortBy != "" {
		dir := 1
		if q.SortDesc {
			dir = -1
		}
		// _id after it, so documents with the same value keep one order from page to page.
		sort := bson.D{{Key: q.SortBy, Value: dir}}
		if q.SortBy != "_id" {
			sort = append(sort, bson.E{Key: "_id", Value: 1})
		}
		opts.SetSort(sort)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	began := time.Now()
	cursor, err := coll.Find(ctx, filter, opts)
	if err != nil {
		return DocumentPage{}, err
	}
	defer cursor.Close(ctx)
	page := DocumentPage{Documents: []Document{}}
	for cursor.Next(ctx) {
		if len(page.Documents) == size {
			page.HasMore = true
			break
		}
		doc, err := toDocument(cursor.Current)
		if err != nil {
			return DocumentPage{}, err
		}
		page.Documents = append(page.Documents, doc)
	}
	if err := cursor.Err(); err != nil {
		return DocumentPage{}, err
	}
	page.DurationMs = time.Since(began).Milliseconds()
	return page, nil
}

// filterDocument is the filters as a MongoDB filter, every one of them ANDed.
func filterDocument(filters []Filter) (bson.D, error) {
	var all bson.A
	for _, f := range filters {
		field := strings.TrimSpace(f.Field)
		if field == "" {
			continue
		}
		condition, err := filterCondition(f)
		if err != nil {
			return nil, err
		}
		all = append(all, bson.D{{Key: field, Value: condition}})
	}
	switch len(all) {
	case 0:
		return bson.D{}, nil
	case 1:
		return all[0].(bson.D), nil
	}
	return bson.D{{Key: "$and", Value: all}}, nil
}

var comparisons = map[string]string{"eq": "$eq", "neq": "$ne", "lt": "$lt", "lte": "$lte", "gt": "$gt", "gte": "$gte"}

// filterCondition is one filter's condition on its field.
func filterCondition(f Filter) (bson.D, error) {
	switch f.Operator {
	case "exists":
		return bson.D{{Key: "$exists", Value: true}}, nil
	case "not_exists":
		return bson.D{{Key: "$exists", Value: false}}, nil
	case "contains":
		// Any case, like Postgres's contains; the text is matched as it is, not as a pattern.
		return bson.D{{Key: "$regex", Value: regexp.QuoteMeta(f.Value)}, {Key: "$options", Value: "i"}}, nil
	case "starts":
		return bson.D{{Key: "$regex", Value: "^" + regexp.QuoteMeta(f.Value)}}, nil
	}
	op, ok := comparisons[f.Operator]
	if !ok {
		return nil, fmt.Errorf("SpyDB doesn’t know the condition %q.", f.Operator)
	}
	v, err := filterValue(f)
	if err != nil {
		return nil, err
	}
	return bson.D{{Key: op, Value: v}}, nil
}

// filterValue is a filter's value as the type its field holds, so "5"
// finds the number 5, and an _id's hex finds its ObjectId.
func filterValue(f Filter) (any, error) {
	text := strings.TrimSpace(f.Value)
	switch f.Type {
	case "number", "int", "long", "double", "decimal":
		if n, err := strconv.ParseInt(text, 10, 64); err == nil {
			return n, nil // compares equal to the same number stored as any number type
		}
		if x, err := strconv.ParseFloat(text, 64); err == nil {
			return x, nil
		}
		return nil, fmt.Errorf("“%s” isn’t a number, and %s holds numbers.", f.Value, f.Field)
	case "bool":
		switch strings.ToLower(text) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		return nil, fmt.Errorf("%s holds true or false, not “%s”.", f.Field, f.Value)
	case "date":
		t, err := parseDate(text)
		if err != nil {
			return nil, fmt.Errorf("“%s” isn’t a date like 2024-05-01 or 2024-05-01T10:30:00Z, and %s holds dates.", f.Value, f.Field)
		}
		return bson.NewDateTimeFromTime(t), nil
	case "objectId":
		if id, err := bson.ObjectIDFromHex(text); err == nil {
			return id, nil
		}
		return nil, fmt.Errorf("“%s” isn’t an ObjectId (24 hex digits), and %s holds ObjectIds.", f.Value, f.Field)
	case "null":
		if text == "" || text == "null" {
			return nil, nil
		}
	}
	return f.Value, nil
}
