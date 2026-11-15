package dynamo

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// PartiQLResult is what one PartiQL statement gave.
type PartiQLResult struct {
	Items      []map[string]Value `json:"items"`      // a SELECT's items (a page of them), or an UPDATE … RETURNING's
	Next       string             `json:"next"`       // more items: pass it back to RunPartiQL for the next page; "" after the last
	Select     bool               `json:"select"`     // it was a SELECT
	Table      string             `json:"table"`      // the table a SELECT read (not an index): its items can be edited there; "" otherwise
	Capacity   float64            `json:"capacity"`   // capacity units AWS charged for it, as it reports them
	DurationMs int64              `json:"durationMs"` // how long AWS took
}

// RunPartiQL runs one PartiQL statement (DynamoDB's SQL): SELECT, INSERT,
// UPDATE or DELETE, e.g. SELECT * FROM "orders" WHERE "userId" = 'u1'. A
// SELECT reads 50 items at a time, like the table tab, and next (from the
// page before) gets the page after: with a key in its WHERE AWS goes straight
// to those items, otherwise it reads the table page by page. UPDATE and
// DELETE change one item, which their WHERE must name by its whole key.
func (s *Service) RunPartiQL(ctx context.Context, statement, next string) (PartiQLResult, error) {
	client, err := s.dynamoClient()
	if err != nil {
		return PartiQLResult{}, err
	}
	statement = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(statement), ";"))
	in := &dynamodb.ExecuteStatementInput{
		Statement:              aws.String(statement),
		ReturnConsumedCapacity: types.ReturnConsumedCapacityTotal,
	}
	if next != "" {
		in.NextToken = aws.String(next)
	}
	result := PartiQLResult{Select: firstWord(statement) == "select", Items: []map[string]Value{}}
	if result.Select {
		in.Limit = aws.Int32(itemsPerPage)
		result.Table = selectTable(statement)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	began := time.Now()
	out, err := client.ExecuteStatement(ctx, in)
	if err != nil {
		return PartiQLResult{}, awsError(err)
	}
	result.DurationMs = time.Since(began).Milliseconds()
	for _, item := range out.Items {
		result.Items = append(result.Items, toValues(item))
	}
	result.Next = aws.ToString(out.NextToken)
	if c := out.ConsumedCapacity; c != nil {
		result.Capacity = aws.ToFloat64(c.CapacityUnits)
	}
	return result, nil
}

// firstWord is a statement's first word in lower case, after any comments.
func firstWord(statement string) string {
	for {
		statement = strings.TrimSpace(statement)
		switch {
		case strings.HasPrefix(statement, "--"):
			_, rest, _ := strings.Cut(statement, "\n")
			statement = rest
		case strings.HasPrefix(statement, "/*"):
			_, rest, _ := strings.Cut(statement, "*/")
			statement = rest
		default:
			word, _, _ := strings.Cut(statement, " ")
			word, _, _ = strings.Cut(word, "\n")
			return strings.ToLower(word)
		}
	}
}

// FROM "table", FROM table, or FROM "table"."index" (an index: not editable).
var fromTable = regexp.MustCompile(`(?is)\bfrom\s+("(?:[^"]|"")+"|[A-Za-z0-9_.-]+)(\s*\.\s*"(?:[^"]|"")+")?`)

// selectTable is the table a SELECT reads, if it reads one table rather than
// an index; "" otherwise.
func selectTable(statement string) string {
	all := fromTable.FindAllStringSubmatch(statement, -1)
	if len(all) != 1 || all[0][2] != "" {
		return ""
	}
	name := all[0][1]
	if strings.HasPrefix(name, `"`) {
		return strings.ReplaceAll(name[1:len(name)-1], `""`, `"`)
	}
	if strings.Contains(name, ".") {
		return "" // table.index
	}
	return name
}
