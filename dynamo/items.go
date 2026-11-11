package dynamo

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// TableDetails is what AWS says about a table: its key, its indexes and
// roughly its size.
type TableDetails struct {
	Name         string            `json:"name"`
	PartitionKey string            `json:"partitionKey"` // the field every item is found by, e.g. "userId"
	SortKey      string            `json:"sortKey"`      // the key's second part, e.g. "createdAt"; "" if the table has none
	Indexes      []IndexDetails    `json:"indexes"`      // other keys to find items by quickly
	KeyTypes     map[string]string `json:"keyTypes"`     // every key field's type, table's and indexes': "S" text, "N" number, "B" binary
	ItemCount    int64             `json:"itemCount"`    // AWS's count, updated about every 6 hours
	SizeBytes    int64             `json:"sizeBytes"`    // likewise
}

// IndexDetails is one of a table's secondary indexes: a copy of its items
// kept in the order of another key, so they can be found by that key too.
type IndexDetails struct {
	Name         string `json:"name"`         // e.g. "sip_type_status"
	PartitionKey string `json:"partitionKey"` // e.g. "sip_type"
	SortKey      string `json:"sortKey"`      // e.g. "sip_status"; "" if none
	AllFields    bool   `json:"allFields"`    // it keeps every field of an item (projection ALL), not just some
}

// Value is one field of an item: its DynamoDB type and the value as text.
type Value struct {
	Type string `json:"type"` // S text, N number, B binary, BOOL, NULL, M map, L list, SS / NS / BS sets
	Text string `json:"text"` // "Delhi", "1500", "true", "" for NULL; base64 for binary; JSON for M, L and sets
}

// ItemsPage is one page of a table's items, in the order DynamoDB keeps them.
type ItemsPage struct {
	Items      []map[string]Value `json:"items"`      // field name → value; items needn't have the same fields
	Next       string             `json:"next"`       // where the next page starts (pass it to FindItems); "" after the last
	Checked    int32              `json:"checked"`    // items AWS read for this page; more than len(Items) when filters left some out
	Plan       FindPlan           `json:"plan"`       // how it read them: by a key, or every item
	DurationMs int64              `json:"durationMs"` // how long AWS took
}

// itemsPerPage is how many items FindItems has AWS read at a time. AWS
// charges for every item read, so SpyDB reads a page only when you ask.
const itemsPerPage = 50

// DescribeTable says what a table's key and indexes are and about how many
// items it has. FindItems keeps using this answer (see tableDetails).
func (s *Service) DescribeTable(ctx context.Context, table string) (TableDetails, error) {
	client, err := s.dynamoClient()
	if err != nil {
		return TableDetails{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(table)})
	if err != nil {
		return TableDetails{}, awsError(err)
	}
	t := out.Table
	details := TableDetails{
		Name:      table,
		Indexes:   []IndexDetails{},
		KeyTypes:  make(map[string]string, len(t.AttributeDefinitions)),
		ItemCount: aws.ToInt64(t.ItemCount),
		SizeBytes: aws.ToInt64(t.TableSizeBytes),
	}
	details.PartitionKey, details.SortKey = keyNames(t.KeySchema)
	for _, ix := range t.GlobalSecondaryIndexes {
		details.Indexes = append(details.Indexes, indexDetails(aws.ToString(ix.IndexName), ix.KeySchema, ix.Projection))
	}
	for _, ix := range t.LocalSecondaryIndexes {
		details.Indexes = append(details.Indexes, indexDetails(aws.ToString(ix.IndexName), ix.KeySchema, ix.Projection))
	}
	// AWS lists the type of every field used in a key, and only those.
	for _, a := range t.AttributeDefinitions {
		details.KeyTypes[aws.ToString(a.AttributeName)] = string(a.AttributeType)
	}
	s.mu.Lock()
	if s.tables == nil {
		s.tables = map[string]TableDetails{}
	}
	s.tables[table] = details
	s.mu.Unlock()
	return details, nil
}

// tableDetails is DescribeTable's answer for the table: the one it gave
// before (it opened the table's tab), or a new one.
func (s *Service) tableDetails(ctx context.Context, table string) (TableDetails, error) {
	s.mu.Lock()
	details, ok := s.tables[table]
	s.mu.Unlock()
	if ok {
		return details, nil
	}
	return s.DescribeTable(ctx, table)
}

// keyNames is a key's partition and sort fields ("" if it has no sort key).
func keyNames(schema []types.KeySchemaElement) (partition, sort string) {
	for _, k := range schema {
		switch k.KeyType {
		case types.KeyTypeHash:
			partition = aws.ToString(k.AttributeName)
		case types.KeyTypeRange:
			sort = aws.ToString(k.AttributeName)
		}
	}
	return partition, sort
}

func indexDetails(name string, schema []types.KeySchemaElement, projection *types.Projection) IndexDetails {
	ix := IndexDetails{Name: name}
	ix.PartitionKey, ix.SortKey = keyNames(schema)
	ix.AllFields = projection != nil && projection.ProjectionType == types.ProjectionTypeAll
	return ix
}

// dynamoClient is the connected profile's DynamoDB, or an error if none.
// The lock is only held to read the field, not during calls to AWS, so
// Disconnect never waits for one.
func (s *Service) dynamoClient() (*dynamodb.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return nil, errors.New("Not connected to DynamoDB: sign in first.")
	}
	return s.client, nil
}

// toValue is how the grid shows a field.
func toValue(av types.AttributeValue) Value {
	switch v := av.(type) {
	case *types.AttributeValueMemberS:
		return Value{Type: "S", Text: v.Value}
	case *types.AttributeValueMemberN:
		return Value{Type: "N", Text: v.Value} // as DynamoDB keeps it: up to 38 digits, exact
	case *types.AttributeValueMemberBOOL:
		return Value{Type: "BOOL", Text: strconv.FormatBool(v.Value)}
	case *types.AttributeValueMemberNULL:
		return Value{Type: "NULL"}
	case *types.AttributeValueMemberB:
		return Value{Type: "B", Text: base64.StdEncoding.EncodeToString(v.Value)}
	case *types.AttributeValueMemberM:
		return Value{Type: "M", Text: jsonText(plain(v))}
	case *types.AttributeValueMemberL:
		return Value{Type: "L", Text: jsonText(plain(v))}
	case *types.AttributeValueMemberSS:
		return Value{Type: "SS", Text: jsonText(plain(v))}
	case *types.AttributeValueMemberNS:
		return Value{Type: "NS", Text: jsonText(plain(v))}
	case *types.AttributeValueMemberBS:
		return Value{Type: "BS", Text: jsonText(plain(v))}
	}
	return Value{Type: "?"} // a type newer than this library
}

// plain is a value as ordinary JSON, without DynamoDB's type labels:
// {"M": {"pin": {"N": "110001"}}} becomes {"pin": 110001}. Numbers stay
// exactly as stored (json.Number); binary becomes base64 text.
func plain(av types.AttributeValue) any {
	switch v := av.(type) {
	case *types.AttributeValueMemberS:
		return v.Value
	case *types.AttributeValueMemberN:
		return json.Number(v.Value)
	case *types.AttributeValueMemberBOOL:
		return v.Value
	case *types.AttributeValueMemberB:
		return base64.StdEncoding.EncodeToString(v.Value)
	case *types.AttributeValueMemberM:
		m := make(map[string]any, len(v.Value))
		for name, field := range v.Value {
			m[name] = plain(field)
		}
		return m
	case *types.AttributeValueMemberL:
		l := make([]any, len(v.Value))
		for i, e := range v.Value {
			l[i] = plain(e)
		}
		return l
	case *types.AttributeValueMemberSS:
		return v.Value
	case *types.AttributeValueMemberNS:
		ns := make([]json.Number, len(v.Value))
		for i, n := range v.Value {
			ns[i] = json.Number(n)
		}
		return ns
	case *types.AttributeValueMemberBS:
		bs := make([]string, len(v.Value))
		for i, b := range v.Value {
			bs[i] = base64.StdEncoding.EncodeToString(b)
		}
		return bs
	}
	return nil // NULL, or a type newer than this library
}

// jsonText is v as compact JSON, with <, > and & left as they are.
func jsonText(v any) string {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return fmt.Sprint(v)
	}
	return string(bytes.TrimRight(b.Bytes(), "\n"))
}

// keyPart is one field of the key a page ended at. Keys are only ever text,
// numbers or binary.
type keyPart struct {
	S *string `json:"S,omitempty"`
	N *string `json:"N,omitempty"`
	B []byte  `json:"B,omitempty"`
}

// encodeNext turns the key AWS's last page ended at into text the screen
// keeps and hands back for the next page, e.g. {"id":{"S":"item-049"}}.
func encodeNext(key map[string]types.AttributeValue) (string, error) {
	parts := make(map[string]keyPart, len(key))
	for name, av := range key {
		switch v := av.(type) {
		case *types.AttributeValueMemberS:
			parts[name] = keyPart{S: &v.Value}
		case *types.AttributeValueMemberN:
			parts[name] = keyPart{N: &v.Value}
		case *types.AttributeValueMemberB:
			parts[name] = keyPart{B: v.Value}
		default:
			return "", fmt.Errorf("AWS ended the page at a key SpyDB can't read (%s is a %T).", name, av)
		}
	}
	b, err := json.Marshal(parts)
	return string(b), err
}

// decodeNext is encodeNext backwards.
func decodeNext(next string) (map[string]types.AttributeValue, error) {
	var parts map[string]keyPart
	if err := json.Unmarshal([]byte(next), &parts); err != nil || len(parts) == 0 {
		return nil, errors.New("Couldn't tell where the next page starts: reload the table.")
	}
	key := make(map[string]types.AttributeValue, len(parts))
	for name, p := range parts {
		switch {
		case p.S != nil:
			key[name] = &types.AttributeValueMemberS{Value: *p.S}
		case p.N != nil:
			key[name] = &types.AttributeValueMemberN{Value: *p.N}
		case p.B != nil:
			key[name] = &types.AttributeValueMemberB{Value: p.B}
		default:
			return nil, errors.New("Couldn't tell where the next page starts: reload the table.")
		}
	}
	return key, nil
}
