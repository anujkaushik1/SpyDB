package mongodb

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// DocumentEdit is one document's changed fields.
type DocumentEdit struct {
	ID      string                 `json:"id"`      // its _id (Document.ID)
	Old     map[string]Shown       `json:"old"`     // each changed field as loaded: the save stops if one changed since
	Changes map[string]FieldChange `json:"changes"` // field → its new value
}

// DocumentDelete is one document to delete, as it was loaded.
type DocumentDelete struct {
	ID  string           `json:"id"`
	Old map[string]Shown `json:"old"` // its fields as the grid showed them: nothing is deleted if one changed since
}

// maxWrites is how many documents one save or delete changes: a transaction
// is kept small, and nobody edits more than this by hand.
const maxWrites = 1000

// write is one document's update or delete, checked and ready.
type write struct {
	name   string // for messages: "_id 64f1…"
	filter bson.D // finds the document, only as it was loaded
	update bson.D // $set / $unset; nil for a delete
	id     any
}

// SaveDocuments saves edited fields of a collection's documents: each
// changed field gets its new value with $set (keeping its type, see
// newValue) or goes with $unset. A document is only changed if every
// changed field still holds what was loaded; otherwise nothing is saved
// and the error says which. It returns the saved documents as they are now.
//
// On a replica set or sharded cluster all of them save in one transaction.
// A single server (standalone) can't run transactions, so there they save
// one after another, after checking all of them first: only a change made
// by someone else in the moment between the check and the save can stop
// one halfway, and then the error says which were saved.
func (s *Service) SaveDocuments(ctx context.Context, collection string, edits []DocumentEdit) ([]Document, error) {
	coll, _, err := s.collection(collection)
	if err != nil {
		return nil, err
	}
	if len(edits) == 0 {
		return []Document{}, nil
	}
	if len(edits) > maxWrites {
		return nil, fmt.Errorf("Save at most %d documents at a time (you changed %d).", maxWrites, len(edits))
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	writes := make([]write, 0, len(edits))
	for _, edit := range edits {
		w, err := planEdit(ctx, coll, edit)
		if err != nil {
			return nil, err
		}
		writes = append(writes, w)
	}
	if err := s.apply(ctx, coll, writes, "saved"); err != nil {
		return nil, err
	}

	ids := make(bson.A, len(writes))
	for i, w := range writes {
		ids[i] = w.id
	}
	saved, err := findByIDs(ctx, coll, ids)
	if err != nil {
		return nil, fmt.Errorf("Saved, but couldn’t read the documents back: %v. Reload to see them.", err)
	}
	out := make([]Document, 0, len(edits))
	for _, edit := range edits {
		if doc, ok := saved[normalID(edit.ID)]; ok {
			out = append(out, doc)
		}
	}
	return out, nil
}

// planEdit checks one edit against the document as it is now and makes its update.
func planEdit(ctx context.Context, coll *mongo.Collection, edit DocumentEdit) (write, error) {
	id, err := parseID(edit.ID)
	if err != nil {
		return write{}, err
	}
	w := write{name: "_id " + idText(id), id: id}
	if len(edit.Changes) == 0 {
		return write{}, fmt.Errorf("%s: nothing to save.", w.name)
	}
	current, err := coll.FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Raw()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return write{}, fmt.Errorf("The document with %s isn’t there any more: someone deleted it, or changed its _id. Nothing was saved.", w.name)
	}
	if err != nil {
		return write{}, err
	}

	fields := sortedKeys(edit.Changes)
	set, unset := bson.D{}, bson.D{}
	checks := bson.A{}
	w.filter = bson.D{{Key: "_id", Value: id}}
	for _, field := range fields {
		change := edit.Changes[field]
		if err := editableField(field); err != nil {
			return write{}, fmt.Errorf("%s: %v", w.name, err)
		}
		now, lookupErr := current.LookupErr(field)
		has := lookupErr == nil
		if !sameShown(edit.Old[field], shownAs(now, has)) {
			return write{}, fmt.Errorf("%s: %s changed since you loaded it (it was %s, now %s). Nothing was saved: reload to see it.",
				w.name, field, shownText(edit.Old[field]), shownText(shownAs(now, has)))
		}
		// The document is only changed if the field still holds exactly that.
		if has {
			checks = append(checks, bson.D{{Key: "$eq", Value: bson.A{"$" + field, bson.D{{Key: "$literal", Value: now}}}}})
		} else {
			w.filter = append(w.filter, bson.E{Key: field, Value: bson.D{{Key: "$exists", Value: false}}})
		}
		if change.Remove {
			unset = append(unset, bson.E{Key: field, Value: ""})
			continue
		}
		v, err := newValue(change, now, has)
		if err != nil {
			return write{}, fmt.Errorf("%s, %s: %v", w.name, field, err)
		}
		set = append(set, bson.E{Key: field, Value: v})
	}
	if len(checks) > 0 {
		w.filter = append(w.filter, bson.E{Key: "$expr", Value: bson.D{{Key: "$and", Value: checks}}})
	}
	if len(set) > 0 {
		w.update = append(w.update, bson.E{Key: "$set", Value: set})
	}
	if len(unset) > 0 {
		w.update = append(w.update, bson.E{Key: "$unset", Value: unset})
	}
	return w, nil
}

// editableField refuses fields SpyDB can't change with $set: _id (a
// document's _id never changes), and names with a dot or starting with $,
// which $set would read as a path or an operator.
func editableField(field string) error {
	switch {
	case field == "_id":
		return errors.New("_id can’t be changed (MongoDB never changes a document’s _id).")
	case field == "":
		return errors.New("a field needs a name.")
	case strings.Contains(field, ".") || strings.HasPrefix(field, "$"):
		return fmt.Errorf("SpyDB can’t change “%s”: a name with a dot or starting with $ would be read as a path. Use the query editor.", field)
	}
	return nil
}

// DeleteDocuments deletes documents of a collection, each only if it still
// holds exactly what it held when checked against the grid. All or nothing
// as SaveDocuments is; it returns how many were deleted.
func (s *Service) DeleteDocuments(ctx context.Context, collection string, deletes []DocumentDelete) (int, error) {
	coll, _, err := s.collection(collection)
	if err != nil {
		return 0, err
	}
	if len(deletes) > maxWrites {
		return 0, fmt.Errorf("Delete at most %d documents at a time (you picked %d).", maxWrites, len(deletes))
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	writes := make([]write, 0, len(deletes))
	for _, d := range deletes {
		id, err := parseID(d.ID)
		if err != nil {
			return 0, err
		}
		name := "_id " + idText(id)
		current, err := coll.FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Raw()
		if errors.Is(err, mongo.ErrNoDocuments) {
			return 0, fmt.Errorf("The document with %s isn’t there any more. Nothing was deleted.", name)
		}
		if err != nil {
			return 0, err
		}
		for _, field := range sortedKeys(d.Old) {
			now, lookupErr := current.LookupErr(field)
			if !sameShown(d.Old[field], shownAs(now, lookupErr == nil)) {
				return 0, fmt.Errorf("%s: %s changed since you loaded it (it was %s, now %s). Nothing was deleted: reload to see it.",
					name, field, shownText(d.Old[field]), shownText(shownAs(now, lookupErr == nil)))
			}
		}
		// Only exactly the document checked, whole.
		filter := bson.D{{Key: "_id", Value: id}, {Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{"$$ROOT", bson.D{{Key: "$literal", Value: current}}}}}}}
		writes = append(writes, write{name: name, filter: filter, id: id})
	}
	if err := s.apply(ctx, coll, writes, "deleted"); err != nil {
		return 0, err
	}
	return len(writes), nil
}

// errChangedMeanwhile stops a transaction whose document changed after it was checked.
var errChangedMeanwhile = errors.New("changed meanwhile")

// apply runs the writes: in one transaction when the server can, else one
// by one (see SaveDocuments). done is "saved" or "deleted", for messages.
func (s *Service) apply(ctx context.Context, coll *mongo.Collection, writes []write, done string) error {
	s.mu.Lock()
	txns := s.txns
	s.mu.Unlock()
	one := func(ctx context.Context, w write) error {
		var matched int64
		if w.update == nil {
			res, err := coll.DeleteOne(ctx, w.filter)
			if err != nil {
				return err
			}
			matched = res.DeletedCount
		} else {
			res, err := coll.UpdateOne(ctx, w.filter, w.update)
			if err != nil {
				return err
			}
			matched = res.MatchedCount
		}
		if matched == 0 {
			return fmt.Errorf("%w: %s", errChangedMeanwhile, w.name)
		}
		return nil
	}

	if txns && len(writes) > 1 {
		session, err := coll.Database().Client().StartSession()
		if err != nil {
			return err
		}
		defer session.EndSession(context.Background())
		_, err = session.WithTransaction(ctx, func(ctx context.Context) (any, error) {
			for _, w := range writes {
				if err := one(ctx, w); err != nil {
					return nil, err
				}
			}
			return nil, nil
		})
		if errors.Is(err, errChangedMeanwhile) {
			return fmt.Errorf("The document with %s changed while saving, so nothing was %s. Reload to see it.", strings.TrimPrefix(err.Error(), errChangedMeanwhile.Error()+": "), done)
		}
		return err
	}

	for i, w := range writes {
		if err := one(ctx, w); err != nil {
			reason := err.Error()
			if errors.Is(err, errChangedMeanwhile) {
				reason = "it changed a moment ago"
			}
			if i == 0 {
				return fmt.Errorf("The document with %s wasn’t %s: %s. Nothing was %s; reload to see it.", w.name, done, reason, done)
			}
			// A single server has no transactions to undo the ones before.
			return fmt.Errorf("The first %d of %d were %s, but the one with %s wasn’t: %s. This server can’t undo the others (it isn’t a replica set): reload to see where things are.", i, len(writes), done, w.name, reason)
		}
	}
	return nil
}

// findByIDs reads documents by _id, keyed by their Document.ID.
func findByIDs(ctx context.Context, coll *mongo.Collection, ids bson.A) (map[string]Document, error) {
	cursor, err := coll.Find(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	out := map[string]Document{}
	for cursor.Next(ctx) {
		doc, err := toDocument(cursor.Current)
		if err != nil {
			return nil, err
		}
		out[doc.ID] = doc
	}
	return out, cursor.Err()
}

// normalID is an ID as toDocument writes it, whatever spacing the screen sent.
func normalID(id string) string {
	v, err := parseID(id)
	if err != nil {
		return id
	}
	return extJSON(v, true)
}

// idText is an _id for messages: an ObjectId's hex, a string as it is.
func idText(id any) string {
	if v, ok := id.(bson.RawValue); ok {
		return shorten(toValue(v).Text, 40)
	}
	return fmt.Sprint(id)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
