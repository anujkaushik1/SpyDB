package dynamo

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Filter is one row of a table tab's filter bar: "sip_status equals PENDING".
type Filter struct {
	Field    string `json:"field"`
	Operator string `json:"operator"` // eq, neq, lt, lte, gt, gte, starts, contains, exists, not_exists
	Value    string `json:"value"`    // as typed; not used by exists and not_exists
	Type     string `json:"type"`     // what the value is: "S" text, "N" number, "BOOL" true / false. A key field's own type wins.
}

// FindPlan is how FindItems reads a table for some filters.
type FindPlan struct {
	Query bool   `json:"query"` // true: AWS goes straight to the matching items by a key; false: it checks every item
	Index string `json:"index"` // the index whose key it uses; "" for the table's own key (or when checking every item)
}

// plan is a FindPlan with the filters sorted out: the ones on the key it
// uses (for AWS's key condition) and the rest (checked on every item read).
type plan struct {
	FindPlan
	partition Filter   // "equals" on the key's partition field
	sort      []Filter // the sort field's condition: none, one, or ≥ and ≤ (between)
	rest      []Filter
}

// keySource is a key items can be found by: the table's or an index's.
type keySource struct {
	index           string // "" for the table
	partition, sort string
}

// planFind picks how to read a table for filters. A key can only be used
// when there's an "equals" on its partition field (AWS keeps items grouped
// by it); its sort field can then narrow it down. Of the keys that fit:
//
//   - the table's whole key wins: it's exactly one item;
//   - otherwise your first filter row that can use a key decides, and the
//     later rows narrow its items down. SpyDB can't know which key finds
//     fewer items (user_id = x is a few; sip_type = primary with sip_status =
//     created may be most of the table), so it does what you started with;
//   - a field that is the first part of two keys: the one taking more filters.
//
// When none fits, every item is checked against every filter.
func planFind(details TableDetails, filters []Filter) plan {
	sources := []keySource{{"", details.PartitionKey, details.SortKey}}
	for _, ix := range details.Indexes {
		// An index that keeps only some fields would give items with fields missing.
		if ix.AllFields {
			sources = append(sources, keySource{ix.Name, ix.PartitionKey, ix.SortKey})
		}
	}
	best := plan{rest: filters}
	bestRank := [3]int{}
	for _, source := range sources {
		p, keys, row := queryPlan(source, filters)
		if keys == 0 {
			continue
		}
		whole := source.index == "" && (source.sort == "" || len(p.sort) == 1 && p.sort[0].Operator == "eq")
		rank := [3]int{0, -row, keys} // higher is better, compared in order; the first source wins a tie
		if whole {
			rank[0] = 1
		}
		if !best.Query || greater(rank, bestRank) {
			best, bestRank = p, rank
		}
	}
	return best
}

func greater(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

// queryPlan is how a Query by source's key would use filters: how many of
// them it puts in the key (0: source can't be used), and which filter row
// gives its partition field. AWS doesn't let a Query filter on its key's
// own fields, so every filter on them must fit in the key condition.
func queryPlan(source keySource, filters []Filter) (p plan, keys, row int) {
	p = plan{FindPlan: FindPlan{Query: true, Index: source.index}}
	found := false
	var sort []Filter
	for i, f := range filters {
		switch {
		case f.Field == source.partition:
			if f.Operator != "eq" || found {
				return plan{}, 0, 0
			}
			p.partition, found, row = f, true, i
		case source.sort != "" && f.Field == source.sort:
			sort = append(sort, f)
		default:
			p.rest = append(p.rest, f)
		}
	}
	if !found {
		return plan{}, 0, 0
	}
	switch {
	case len(sort) == 0:
	case len(sort) == 1 && sortOperators[sort[0].Operator]:
		p.sort = sort
	case len(sort) == 2 && between(sort) != nil:
		p.sort = between(sort)
	default:
		return plan{}, 0, 0
	}
	return p, 1 + len(p.sort), row
}

// What a sort field can do in a key condition (not "not equal" or "contains").
var sortOperators = map[string]bool{"eq": true, "lt": true, "lte": true, "gt": true, "gte": true, "starts": true}

// between is two conditions that make one "between" (AWS's is inclusive):
// ≥ low then ≤ high, in either order; nil if they don't.
func between(sort []Filter) []Filter {
	switch {
	case sort[0].Operator == "gte" && sort[1].Operator == "lte":
		return sort
	case sort[0].Operator == "lte" && sort[1].Operator == "gte":
		return []Filter{sort[1], sort[0]}
	}
	return nil
}

// PlanFind says how FindItems would read a table for these filters, without
// reading it: the table tab shows it beside Apply.
func (s *Service) PlanFind(ctx context.Context, table string, filters []Filter) FindPlan {
	return planFind(s.keysOf(ctx, table), filters).FindPlan
}

// keysOf is the table's keys and indexes for planning, or none when AWS
// won't describe the table: some profiles may read items but not describe
// tables, and then every item is checked instead.
func (s *Service) keysOf(ctx context.Context, table string) TableDetails {
	details, err := s.tableDetails(ctx, table)
	if err != nil {
		return TableDetails{}
	}
	return details
}

// FindItems reads one page of a table's items that match every filter (all
// of them with no filters): the first page when next is "", else the page
// after the one that gave next. With "equals" on a key's partition field it
// asks AWS for just those items (Query); otherwise AWS checks every item,
// page by page (Scan). Either way it reads up to 50 items a page, so a page
// can hold fewer matches than that. It only reads.
func (s *Service) FindItems(ctx context.Context, table string, filters []Filter, next string) (ItemsPage, error) {
	client, err := s.dynamoClient()
	if err != nil {
		return ItemsPage{}, err
	}
	var start map[string]types.AttributeValue
	if next != "" {
		if start, err = decodeNext(next); err != nil {
			return ItemsPage{}, err
		}
	}
	details := s.keysOf(ctx, table)
	p := planFind(details, filters)
	e := expression{}
	var keyCondition, filterExpression string
	if p.Query {
		if keyCondition, err = e.keyCondition(details, p); err != nil {
			return ItemsPage{}, err
		}
	}
	if filterExpression, err = e.conditions(details, p.rest); err != nil {
		return ItemsPage{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	began := time.Now()
	var items []map[string]types.AttributeValue
	var last map[string]types.AttributeValue
	var checked int32
	if p.Query {
		in := &dynamodb.QueryInput{
			TableName:                 aws.String(table),
			KeyConditionExpression:    aws.String(keyCondition),
			FilterExpression:          optional(filterExpression),
			ExpressionAttributeNames:  e.names,
			ExpressionAttributeValues: e.values,
			Limit:                     aws.Int32(itemsPerPage),
			ExclusiveStartKey:         start,
		}
		if p.Index != "" {
			in.IndexName = aws.String(p.Index)
		}
		out, err := client.Query(ctx, in)
		if err != nil {
			return ItemsPage{}, awsError(err)
		}
		items, last, checked = out.Items, out.LastEvaluatedKey, out.ScannedCount
	} else {
		out, err := client.Scan(ctx, &dynamodb.ScanInput{
			TableName:                 aws.String(table),
			FilterExpression:          optional(filterExpression),
			ExpressionAttributeNames:  e.names,
			ExpressionAttributeValues: e.values,
			Limit:                     aws.Int32(itemsPerPage),
			ExclusiveStartKey:         start,
		})
		if err != nil {
			return ItemsPage{}, awsError(err)
		}
		items, last, checked = out.Items, out.LastEvaluatedKey, out.ScannedCount
	}

	page := ItemsPage{Items: make([]map[string]Value, 0, len(items)), Checked: checked, Plan: p.FindPlan, DurationMs: time.Since(began).Milliseconds()}
	for _, item := range items {
		fields := make(map[string]Value, len(item))
		for name, v := range item {
			fields[name] = toValue(v)
		}
		page.Items = append(page.Items, fields)
	}
	if len(last) > 0 {
		if page.Next, err = encodeNext(last); err != nil {
			return ItemsPage{}, err
		}
	}
	return page, nil
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return aws.String(s)
}

// expression builds AWS's condition text. Field names and values never go
// in it directly: they're #n0, :v0 … with the real ones alongside, so any
// name works (AWS reserves words like "status" and "name").
type expression struct {
	names  map[string]string               // #n0 → sip_status; nil until one is used, as AWS refuses an empty list
	values map[string]types.AttributeValue // :v0 → "PENDING"
	byName map[string]string               // sip_status → #n0
}

func (e *expression) name(field string) string {
	if n, ok := e.byName[field]; ok {
		return n
	}
	if e.names == nil {
		e.names, e.byName = map[string]string{}, map[string]string{}
	}
	n := fmt.Sprintf("#n%d", len(e.names))
	e.names[n], e.byName[field] = field, n
	return n
}

func (e *expression) value(v types.AttributeValue) string {
	if e.values == nil {
		e.values = map[string]types.AttributeValue{}
	}
	n := fmt.Sprintf(":v%d", len(e.values))
	e.values[n] = v
	return n
}

// keyCondition is "#n0 = :v0", with the sort field's condition after AND.
func (e *expression) keyCondition(details TableDetails, p plan) (string, error) {
	parts := make([]string, 0, 2)
	for _, f := range append([]Filter{p.partition}, p.sort...) {
		if f.Value == "" { // keys are never empty
			return "", fmt.Errorf("Type a value for %s.", f.Field)
		}
	}
	part, err := e.condition(details, p.partition)
	if err != nil {
		return "", err
	}
	parts = append(parts, part)
	switch len(p.sort) {
	case 1:
		if part, err = e.condition(details, p.sort[0]); err != nil {
			return "", err
		}
		parts = append(parts, part)
	case 2:
		low, err := attributeValue(details, p.sort[0])
		if err != nil {
			return "", err
		}
		high, err := attributeValue(details, p.sort[1])
		if err != nil {
			return "", err
		}
		parts = append(parts, fmt.Sprintf("%s BETWEEN %s AND %s", e.name(p.sort[0].Field), e.value(low), e.value(high)))
	}
	return strings.Join(parts, " AND "), nil
}

// conditions is every filter's condition, joined with AND ("" for none).
func (e *expression) conditions(details TableDetails, filters []Filter) (string, error) {
	parts := make([]string, 0, len(filters))
	for _, f := range filters {
		part, err := e.condition(details, f)
		if err != nil {
			return "", err
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " AND "), nil
}

var comparisons = map[string]string{"eq": "=", "neq": "<>", "lt": "<", "lte": "<=", "gt": ">", "gte": ">="}

func (e *expression) condition(details TableDetails, f Filter) (string, error) {
	switch f.Operator {
	case "exists":
		return fmt.Sprintf("attribute_exists(%s)", e.name(f.Field)), nil
	case "not_exists":
		return fmt.Sprintf("attribute_not_exists(%s)", e.name(f.Field)), nil
	}
	v, err := attributeValue(details, f)
	if err != nil {
		return "", err
	}
	switch f.Operator {
	case "starts":
		return fmt.Sprintf("begins_with(%s, %s)", e.name(f.Field), e.value(v)), nil
	case "contains":
		return fmt.Sprintf("contains(%s, %s)", e.name(f.Field), e.value(v)), nil
	}
	return fmt.Sprintf("%s %s %s", e.name(f.Field), comparisons[f.Operator], e.value(v)), nil
}

// A DynamoDB number: digits, maybe a sign, a point and an exponent.
var numberText = regexp.MustCompile(`^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$`)

// attributeValue is a filter's value as the type the field holds: a key
// field's type from the table's definition, any other's from the filter
// (the screen's guess from the items it has seen), else text.
func attributeValue(details TableDetails, f Filter) (types.AttributeValue, error) {
	if _, ok := comparisons[f.Operator]; !ok && f.Operator != "starts" && f.Operator != "contains" {
		return nil, fmt.Errorf("SpyDB doesn't know the condition %q.", f.Operator)
	}
	kind := details.KeyTypes[f.Field]
	if kind == "" {
		kind = f.Type
	}
	switch kind {
	case "N":
		if f.Operator == "starts" {
			return nil, fmt.Errorf("%s holds numbers: “starts with” only works on text.", f.Field)
		}
		value := strings.TrimSpace(f.Value)
		if !numberText.MatchString(value) {
			return nil, fmt.Errorf("“%s” isn't a number, and %s holds numbers.", f.Value, f.Field)
		}
		return &types.AttributeValueMemberN{Value: value}, nil
	case "BOOL":
		if f.Operator != "eq" && f.Operator != "neq" {
			return nil, fmt.Errorf("%s holds true or false: use equals or not equal.", f.Field)
		}
		switch strings.ToLower(strings.TrimSpace(f.Value)) {
		case "true":
			return &types.AttributeValueMemberBOOL{Value: true}, nil
		case "false":
			return &types.AttributeValueMemberBOOL{Value: false}, nil
		}
		return nil, fmt.Errorf("%s holds true or false, not “%s”.", f.Field, f.Value)
	case "B":
		b, err := base64.StdEncoding.DecodeString(f.Value)
		if err != nil {
			return nil, fmt.Errorf("%s holds binary data: type it as base64.", f.Field)
		}
		return &types.AttributeValueMemberB{Value: b}, nil
	}
	return &types.AttributeValueMemberS{Value: f.Value}, nil
}
