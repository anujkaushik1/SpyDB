package dynamo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Shown is a field as the table tab showed it when it loaded the item.
type Shown struct {
	Has  bool   `json:"has"`  // the item had the field
	Null bool   `json:"null"` // it was DynamoDB's NULL
	Text string `json:"text"` // otherwise its Value.Text, e.g. "1500"
}

// FieldChange is one field's new value.
type FieldChange struct {
	Remove bool   `json:"remove"` // take the field off the item
	Null   bool   `json:"null"`   // set it to DynamoDB's NULL
	Text   string `json:"text"`   // otherwise the value: as typed, or JSON for maps, lists and sets
	// Type is what Text is: S text, N number, BOOL, B base64, or JSON (a map,
	// list or set, from its JSON). It's used when Typed, or for a field the
	// item hasn't got; otherwise the field keeps the type it has.
	Type  string `json:"type"`
	Typed bool   `json:"typed"` // Type was chosen in the JSON view (5 a number, "5" text): it wins
}

// ItemEdit is one item's changed fields.
type ItemEdit struct {
	Key     map[string]string      `json:"key"`     // the key fields' values as text; their types are the table's
	Old     map[string]Shown       `json:"old"`     // each changed field as loaded: nothing is saved if one changed since
	Changes map[string]FieldChange `json:"changes"` // field → its new value
}

// ItemDelete is one item to delete: its key, and its fields as loaded.
type ItemDelete struct {
	Key map[string]string `json:"key"`
	Old map[string]Shown  `json:"old"` // nothing is deleted if one changed since
}

// maxWrites is how many items AWS writes in one transaction, all or none.
const maxWrites = 100

// SaveItems saves changed fields of a table's items, all of them or none.
// Each item is read first (a strongly consistent read) to check that what
// you changed still holds what you saw, and the write repeats that check,
// so a change made meanwhile (by anyone) stops the save rather than being
// overwritten. Fields keep their DynamoDB type: a number stays a number.
// One item is one UpdateItem; more are one transaction (TransactWriteItems,
// which AWS charges double for). It returns the items as saved, in order; an
// entry is nil if an item couldn't be read back (it was saved all the same).
func (s *Service) SaveItems(ctx context.Context, table string, edits []ItemEdit) ([]map[string]Value, error) {
	client, err := s.dynamoClient()
	if err != nil {
		return nil, err
	}
	if len(edits) == 0 {
		return nil, nil
	}
	if len(edits) > maxWrites {
		return nil, fmt.Errorf("You changed %d items; SpyDB saves at most %d at a time (AWS's limit for saving all or none). Save some, then the rest.", len(edits), maxWrites)
	}
	details, err := s.writeDetails(ctx, table)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	type write struct {
		key               map[string]types.AttributeValue
		name              string // "id = 7", for messages
		update, condition string
		names             map[string]string
		values            map[string]types.AttributeValue
	}
	writes := make([]write, 0, len(edits))
	for _, edit := range edits {
		key, name, err := itemKey(details, edit.Key)
		if err != nil {
			return nil, err
		}
		current, err := readItem(ctx, client, table, key)
		if err != nil {
			return nil, err
		}
		if current == nil {
			return nil, fmt.Errorf("%s isn't in %s any more (deleted since you loaded it). Nothing was saved.", name, table)
		}
		e := expression{}
		var set, remove []string
		conditions := []string{fmt.Sprintf("attribute_exists(%s)", e.name(details.PartitionKey))}
		for _, field := range slices.Sorted(maps.Keys(edit.Changes)) {
			change := edit.Changes[field]
			if field == details.PartitionKey || field == details.SortKey {
				return nil, fmt.Errorf("%s: %s is part of the key, which can't be changed. Nothing was saved.", name, field)
			}
			now := current[field]
			if !looksLike(now, edit.Old[field]) {
				return nil, fmt.Errorf("%s: %s changed since you loaded it (it’s now %s). Nothing was saved: reload to see it.", name, field, describe(now))
			}
			conditions = append(conditions, unchanged(&e, field, now))
			if change.Remove {
				if now != nil {
					remove = append(remove, e.name(field))
				}
				continue
			}
			v, err := newValue(change, now)
			if err != nil {
				return nil, fmt.Errorf("%s: %s %v Nothing was saved.", name, field, err)
			}
			set = append(set, fmt.Sprintf("%s = %s", e.name(field), e.value(v)))
		}
		var update []string
		if len(set) > 0 {
			update = append(update, "SET "+strings.Join(set, ", "))
		}
		if len(remove) > 0 {
			update = append(update, "REMOVE "+strings.Join(remove, ", "))
		}
		if len(update) == 0 {
			continue // only fields it hasn't got were removed: nothing to write
		}
		writes = append(writes, write{key, name, strings.Join(update, " "), strings.Join(conditions, " AND "), e.names, e.values})
	}

	saved := make([]map[string]Value, len(edits))
	switch len(writes) {
	case 0:
	case 1:
		w := writes[0]
		_, err = client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName:                 aws.String(table),
			Key:                       w.key,
			UpdateExpression:          aws.String(w.update),
			ConditionExpression:       aws.String(w.condition),
			ExpressionAttributeNames:  w.names,
			ExpressionAttributeValues: w.values,
		})
		var failed *types.ConditionalCheckFailedException
		if errors.As(err, &failed) {
			return nil, fmt.Errorf("%s changed while saving. Nothing was saved: reload to see it.", w.name)
		}
	default:
		items := make([]types.TransactWriteItem, len(writes))
		for i, w := range writes {
			items[i] = types.TransactWriteItem{Update: &types.Update{
				TableName:                 aws.String(table),
				Key:                       w.key,
				UpdateExpression:          aws.String(w.update),
				ConditionExpression:       aws.String(w.condition),
				ExpressionAttributeNames:  w.names,
				ExpressionAttributeValues: w.values,
			}}
		}
		_, err = client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items})
		if i := failedCheck(err); i >= 0 {
			return nil, fmt.Errorf("%s changed while saving. Nothing was saved: reload to see it.", writes[i].name)
		}
	}
	if err != nil {
		return nil, awsError(err)
	}

	// Read them back, so the tab shows each as DynamoDB now holds it.
	for i, edit := range edits {
		key, _, _ := itemKey(details, edit.Key)
		if item, err := readItem(ctx, client, table, key); err == nil && item != nil {
			saved[i] = toValues(item)
		}
	}
	return saved, nil
}

// DeleteItems deletes items of a table, all of them or none, checking first
// (and again while deleting) that none changed since you loaded it.
func (s *Service) DeleteItems(ctx context.Context, table string, deletes []ItemDelete) (int, error) {
	client, err := s.dynamoClient()
	if err != nil {
		return 0, err
	}
	if len(deletes) == 0 {
		return 0, nil
	}
	if len(deletes) > maxWrites {
		return 0, fmt.Errorf("SpyDB deletes at most %d items at a time (AWS's limit for deleting all or none); you picked %d.", maxWrites, len(deletes))
	}
	details, err := s.writeDetails(ctx, table)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	items := make([]types.TransactWriteItem, len(deletes))
	names := make([]string, len(deletes))
	for i, d := range deletes {
		key, name, err := itemKey(details, d.Key)
		if err != nil {
			return 0, err
		}
		current, err := readItem(ctx, client, table, key)
		if err != nil {
			return 0, err
		}
		if current == nil {
			return 0, fmt.Errorf("%s isn't in %s any more (deleted since you loaded it). Nothing was deleted.", name, table)
		}
		e := expression{}
		conditions := []string{fmt.Sprintf("attribute_exists(%s)", e.name(details.PartitionKey))}
		for _, field := range slices.Sorted(maps.Keys(d.Old)) {
			if field == details.PartitionKey || field == details.SortKey {
				continue
			}
			if !looksLike(current[field], d.Old[field]) {
				return 0, fmt.Errorf("%s: %s changed since you loaded it (it’s now %s). Nothing was deleted: reload to see it.", name, field, describe(current[field]))
			}
			// AWS limits a condition to 4 KB of text: the read above
			// checked every field; the delete itself rechecks the first 100.
			if len(conditions) <= 100 {
				conditions = append(conditions, unchanged(&e, field, current[field]))
			}
		}
		names[i] = name
		items[i] = types.TransactWriteItem{Delete: &types.Delete{
			TableName:                 aws.String(table),
			Key:                       key,
			ConditionExpression:       aws.String(strings.Join(conditions, " AND ")),
			ExpressionAttributeNames:  e.names,
			ExpressionAttributeValues: e.values,
		}}
	}

	if len(items) == 1 {
		d := items[0].Delete
		_, err = client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
			TableName:                 d.TableName,
			Key:                       d.Key,
			ConditionExpression:       d.ConditionExpression,
			ExpressionAttributeNames:  d.ExpressionAttributeNames,
			ExpressionAttributeValues: d.ExpressionAttributeValues,
		})
		var failed *types.ConditionalCheckFailedException
		if errors.As(err, &failed) {
			return 0, fmt.Errorf("%s changed while deleting. Nothing was deleted: reload to see it.", names[0])
		}
	} else {
		_, err = client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items})
		if i := failedCheck(err); i >= 0 {
			return 0, fmt.Errorf("%s changed while deleting. Nothing was deleted: reload to see it.", names[i])
		}
	}
	if err != nil {
		return 0, awsError(err)
	}
	return len(deletes), nil
}

// writeDetails is the table's key, which saving and deleting need.
func (s *Service) writeDetails(ctx context.Context, table string) (TableDetails, error) {
	details, err := s.tableDetails(ctx, table)
	if err != nil {
		return TableDetails{}, fmt.Errorf("SpyDB needs the table's key to change items, and AWS wouldn't describe the table: %w", err)
	}
	return details, nil
}

// itemKey is an item's key, typed as the table defines its key fields, and
// how messages name it: "userId = u1, createdAt = 5".
func itemKey(details TableDetails, key map[string]string) (map[string]types.AttributeValue, string, error) {
	out := map[string]types.AttributeValue{}
	var name []string
	for _, field := range []string{details.PartitionKey, details.SortKey} {
		if field == "" {
			continue
		}
		text, ok := key[field]
		if !ok {
			return nil, "", fmt.Errorf("An item's key has no %s: reload the table.", field)
		}
		v, err := attributeValue(details, Filter{Field: field, Operator: "eq", Value: text})
		if err != nil {
			return nil, "", err
		}
		out[field] = v
		name = append(name, field+" = "+text)
	}
	return out, strings.Join(name, ", "), nil
}

// readItem is the item now (a strongly consistent read), or nil if there's none.
func readItem(ctx context.Context, client *dynamodb.Client, table string, key map[string]types.AttributeValue) (map[string]types.AttributeValue, error) {
	out, err := client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(table), Key: key, ConsistentRead: aws.Bool(true)})
	if err != nil {
		return nil, awsError(err)
	}
	return out.Item, nil
}

func toValues(item map[string]types.AttributeValue) map[string]Value {
	fields := make(map[string]Value, len(item))
	for name, v := range item {
		fields[name] = toValue(v)
	}
	return fields
}

// looksLike says whether a field (nil: the item hasn't got it) is still what
// the tab showed.
func looksLike(av types.AttributeValue, shown Shown) bool {
	if av == nil || !shown.Has {
		return av == nil && !shown.Has
	}
	v := toValue(av)
	if shown.Null || v.Type == "NULL" {
		return shown.Null && v.Type == "NULL"
	}
	if v.Type == "BOOL" && (shown.Text == "t" || shown.Text == "f") { // as a boolean column shows them
		return v.Text == map[string]string{"t": "true", "f": "false"}[shown.Text]
	}
	return v.Text == shown.Text
}

// describe is a field's value for a message: "1500", or "not there".
func describe(av types.AttributeValue) string {
	if av == nil {
		return "not there"
	}
	v := toValue(av)
	switch {
	case v.Type == "NULL":
		return "NULL"
	case len(v.Text) > 60:
		return "“" + v.Text[:57] + "…”"
	}
	return "“" + v.Text + "”"
}

// unchanged is the condition that field still is av (nil: still not there).
func unchanged(e *expression, field string, av types.AttributeValue) string {
	if av == nil {
		return fmt.Sprintf("attribute_not_exists(%s)", e.name(field))
	}
	return fmt.Sprintf("%s = %s", e.name(field), e.value(av))
}

// failedCheck is which write of a transaction failed its condition (the item
// changed meanwhile), or -1.
func failedCheck(err error) int {
	var cancelled *types.TransactionCanceledException
	if !errors.As(err, &cancelled) {
		return -1
	}
	for i, reason := range cancelled.CancellationReasons {
		if aws.ToString(reason.Code) == "ConditionalCheckFailed" {
			return i
		}
	}
	return -1
}

// typeOf is a value's DynamoDB type: S, N, BOOL, NULL, B, M, L, SS, NS or BS.
func typeOf(av types.AttributeValue) string {
	return toValue(av).Type
}

// newValue is a field's new value. A field it replaces (now) keeps its type
// unless the JSON view chose another (change.Typed); a field the item hasn't
// got takes change.Type.
func newValue(change FieldChange, now types.AttributeValue) (types.AttributeValue, error) {
	if change.Null {
		return &types.AttributeValueMemberNULL{Value: true}, nil
	}
	kind := change.Type
	if !change.Typed && now != nil && typeOf(now) != "NULL" {
		kind = typeOf(now)
	}
	text := change.Text
	switch kind {
	case "", "S":
		return &types.AttributeValueMemberS{Value: text}, nil
	case "N":
		n := strings.TrimSpace(text)
		if !numberText.MatchString(n) {
			if change.Typed {
				return nil, fmt.Errorf("“%s” isn't a number.", text)
			}
			return nil, fmt.Errorf("holds numbers, and “%s” isn't one. (To make it text, change it to \"%s\" in the JSON view.)", text, text)
		}
		return &types.AttributeValueMemberN{Value: n}, nil
	case "BOOL":
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "true", "t":
			return &types.AttributeValueMemberBOOL{Value: true}, nil
		case "false", "f":
			return &types.AttributeValueMemberBOOL{Value: false}, nil
		}
		return nil, fmt.Errorf("holds true or false, not “%s”.", text)
	case "B":
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
		if err != nil {
			return nil, errors.New("holds binary data: type it as base64.")
		}
		return &types.AttributeValueMemberB{Value: b}, nil
	}
	// JSON: a map, list or set (M, L, SS, NS, BS), as JSON.
	d := json.NewDecoder(strings.NewReader(text))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil || d.More() {
		return nil, fmt.Errorf("isn't valid JSON (it holds a %s).", typeNames[kind])
	}
	like := now
	if like == nil || typeOf(like) == "NULL" {
		like = emptyOf(kind)
	}
	return fromJSON(v, like)
}

// What messages call each type.
var typeNames = map[string]string{"M": "map", "L": "list", "SS": "string set", "NS": "number set", "BS": "binary set", "JSON": "map or list"}

// emptyOf is an empty value of a set type, so new JSON of that kind becomes
// that set; nil for other types.
func emptyOf(kind string) types.AttributeValue {
	switch kind {
	case "SS":
		return &types.AttributeValueMemberSS{}
	case "NS":
		return &types.AttributeValueMemberNS{}
	case "BS":
		return &types.AttributeValueMemberBS{}
	}
	return nil
}

// fromJSON is JSON (decoded with UseNumber) as a DynamoDB value: an object a
// map, an array a list, a string text, a number a number. Where it holds the
// same as like (the value it replaces), like is kept, so parts left alone
// keep their exact types (sets, binary) inside a map you changed; an array
// that fits like's set type stays that set.
func fromJSON(v any, like types.AttributeValue) (types.AttributeValue, error) {
	if like != nil && !isEmptySet(like) && jsonText(v) == jsonText(plain(like)) {
		return like, nil
	}
	switch v := v.(type) {
	case nil:
		return &types.AttributeValueMemberNULL{Value: true}, nil
	case bool:
		return &types.AttributeValueMemberBOOL{Value: v}, nil
	case json.Number:
		return &types.AttributeValueMemberN{Value: v.String()}, nil
	case string:
		if _, ok := like.(*types.AttributeValueMemberB); ok {
			if b, err := base64.StdEncoding.DecodeString(v); err == nil {
				return &types.AttributeValueMemberB{Value: b}, nil
			}
		}
		return &types.AttributeValueMemberS{Value: v}, nil
	case map[string]any:
		likeMap, _ := like.(*types.AttributeValueMemberM)
		m := make(map[string]types.AttributeValue, len(v))
		for name, field := range v {
			var was types.AttributeValue
			if likeMap != nil {
				was = likeMap.Value[name]
			}
			av, err := fromJSON(field, was)
			if err != nil {
				return nil, err
			}
			m[name] = av
		}
		return &types.AttributeValueMemberM{Value: m}, nil
	case []any:
		switch like.(type) {
		case *types.AttributeValueMemberSS, *types.AttributeValueMemberNS, *types.AttributeValueMemberBS:
			return toSet(v, like)
		}
		likeList, _ := like.(*types.AttributeValueMemberL)
		l := make([]types.AttributeValue, len(v))
		for i, e := range v {
			var was types.AttributeValue
			if likeList != nil && i < len(likeList.Value) {
				was = likeList.Value[i]
			}
			av, err := fromJSON(e, was)
			if err != nil {
				return nil, err
			}
			l[i] = av
		}
		return &types.AttributeValueMemberL{Value: l}, nil
	}
	return nil, fmt.Errorf("can't hold %T.", v)
}

func isEmptySet(av types.AttributeValue) bool {
	switch v := av.(type) {
	case *types.AttributeValueMemberSS:
		return len(v.Value) == 0
	case *types.AttributeValueMemberNS:
		return len(v.Value) == 0
	case *types.AttributeValueMemberBS:
		return len(v.Value) == 0
	}
	return false
}

// toSet is a JSON array as like's kind of set. DynamoDB sets can't be empty
// or hold a value twice.
func toSet(items []any, like types.AttributeValue) (types.AttributeValue, error) {
	kind := typeOf(like)
	if len(items) == 0 {
		return nil, fmt.Errorf("is a %s, and DynamoDB sets can't be empty: remove the field instead.", typeNames[kind])
	}
	var texts []string
	var bins [][]byte
	seen := map[string]bool{}
	for _, item := range items {
		var text string
		switch kind {
		case "SS":
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("is a string set: every value must be text, like \"a\".")
			}
			text = s
		case "NS":
			n, ok := item.(json.Number)
			if !ok {
				return nil, fmt.Errorf("is a number set: every value must be a number.")
			}
			text = n.String()
		case "BS":
			s, ok := item.(string)
			b, err := base64.StdEncoding.DecodeString(s)
			if !ok || err != nil {
				return nil, fmt.Errorf("is a binary set: every value must be base64 text.")
			}
			text, bins = s, append(bins, b)
		}
		if seen[text] {
			return nil, fmt.Errorf("is a %s, which can't hold %s twice.", typeNames[kind], text)
		}
		seen[text] = true
		texts = append(texts, text)
	}
	switch kind {
	case "SS":
		return &types.AttributeValueMemberSS{Value: texts}, nil
	case "NS":
		return &types.AttributeValueMemberNS{Value: texts}, nil
	}
	return &types.AttributeValueMemberBS{Value: bins}, nil
}
