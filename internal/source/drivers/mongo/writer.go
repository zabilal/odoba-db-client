package mongo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
)

// Writing documents (FR-4.4, FR-4.5, FR-12.1, T2.34).
//
// A plan is the calls that would be made, rendered as a person reads them —
// what mongosh would be typed — and carried beside them in the form the
// driver takes. The rules for what the outcome means are the contract's, not
// SQL's, so the shared ApplyWith writes them: a change that matches no
// document is a document changed or deleted since it was read, and a change
// that matches more than one is refused.

var _ source.Writer = (*mongoSource)(nil)

// Plan renders a changeset as the writes it would make.
func (s *mongoSource) Plan(_ context.Context, cs source.Changeset) (_ *source.WritePlan, err error) {
	defer panics.Recover(&err, "planning a write")
	if cs.Target.Kind != model.KindCollection || len(cs.Target.Path) < 2 {
		return nil, fmt.Errorf("mongodb: %s holds no documents", cs.Target)
	}
	if !insertsOnly(cs.Changes) {
		if !cs.Identity.Editable() {
			return nil, errors.New("these documents have no _id to tell them apart, so they cannot be written")
		}
		// A document is told from another by its _id and nothing else. Any
		// other field may name several documents, and updateOne would change
		// one of them and report that it changed one — which would read as a
		// change made precisely (ADR-0034).
		if k := cs.Identity.Columns; len(k) != 1 || k[0] != "_id" {
			return nil, fmt.Errorf("a document is told from another by its _id, and these changes are keyed by %s",
				strings.Join(k, ", "))
		}
	}
	plan := &source.WritePlan{
		Target: cs.Target,
		// A standalone server has no transactions, so the writes go one by
		// one and the person is told so before committing (FR-4.5).
		Atomic:  false,
		Guarded: s.cfg.Guard.RequiresConfirmation(source.AccessWrite),
	}
	coll := cs.Target.Path[1]
	for i, c := range cs.Changes {
		st, desc, err := writeOf(coll, c)
		if err != nil {
			return nil, fmt.Errorf("change %d: %w", i+1, err)
		}
		st.Confirmed = cs.Confirmed
		plan.Statements = append(plan.Statements, st)
		plan.Descriptions = append(plan.Descriptions, desc)
	}
	return plan, nil
}

// Apply runs a plan's writes in order. Nothing is undone on a failure: a
// standalone MongoDB has no transaction to roll back, and the outcome says
// which writes had already been made.
func (s *mongoSource) Apply(ctx context.Context, plan *source.WritePlan) (_ *source.WriteOutcome, err error) {
	defer panics.Recover(&err, "writing documents")
	if err := sqlscript.AllowWrites(s.cfg.Guard, plan); err != nil {
		return nil, err
	}
	if plan.Target.Kind != model.KindCollection || len(plan.Target.Path) < 2 {
		return nil, fmt.Errorf("mongodb: %s holds no documents", plan.Target)
	}
	return sqlscript.ApplyWith(plan, func(st source.Statement) (int64, error) {
		// The call is the statement's own; a plan that carries none was made
		// somewhere else, and nothing of it is run.
		op, ok := st.Op.(*documentWrite)
		if !ok {
			return 0, errors.New("mongodb: this plan was not made here")
		}
		return op.run(ctx, s.client.Database(plan.Target.Path[0]).Collection(plan.Target.Path[1]))
	}, func() error { return nil }, func() error {
		// Nothing was undone, and the outcome must not say it was.
		return errNoRollback
	}), nil
}

var errNoRollback = errors.New("mongodb: this server writes one document at a time, so the writes before the failure stand")

func insertsOnly(changes []source.RowChange) bool {
	for _, c := range changes {
		if c.Kind != source.ChangeInsert {
			return false
		}
	}
	return true
}

// documentWrite is one call, in the form the driver takes it.
type documentWrite struct {
	kind   source.ChangeKind
	filter bson.D
	update bson.D
	doc    bson.D
}

// run makes the call and returns how many documents it matched. A write that
// matches none, or more than one, is the caller's to refuse: the count is
// what it reads (sqlscript.ApplyWith).
func (w *documentWrite) run(ctx context.Context, coll *mongodriver.Collection) (int64, error) {
	switch w.kind {
	case source.ChangeInsert:
		if _, err := coll.InsertOne(ctx, w.doc); err != nil {
			return 0, err
		}
		return 1, nil
	case source.ChangeUpdate:
		res, err := coll.UpdateOne(ctx, w.filter, w.update)
		if err != nil {
			return 0, err
		}
		return res.MatchedCount, nil
	case source.ChangeDelete:
		res, err := coll.DeleteOne(ctx, w.filter)
		if err != nil {
			return 0, err
		}
		return res.DeletedCount, nil
	}
	return 0, fmt.Errorf("mongodb: a change of unknown kind %d", w.kind)
}

// writeOf renders one change, and says in a line what it does. The change is
// addressed by its _id, which Plan has already insisted on.
func writeOf(coll string, c source.RowChange) (source.Statement, string, error) {
	names := make([]string, 0, len(c.Values))
	for name := range c.Values {
		names = append(names, name)
	}
	sort.Strings(names)

	filter := func() (bson.D, string, error) {
		if len(c.Key) != 1 {
			return nil, "", fmt.Errorf("a document is addressed by one _id, and %d values were given", len(c.Key))
		}
		if c.Key[0] == nil {
			return nil, "", errors.New("the document's _id is empty, which addresses no document")
		}
		return bson.D{{Key: "_id", Value: idValue(c.Key[0])}}, fmt.Sprintf("_id = %v", c.Key[0]), nil
	}

	switch c.Kind {
	case source.ChangeUpdate:
		if len(names) == 0 {
			return source.Statement{}, "", errors.New("an update that changes nothing")
		}
		f, said, err := filter()
		if err != nil {
			return source.Statement{}, "", err
		}
		set, unset := bson.D{}, bson.D{}
		for _, name := range names {
			if _, gone := c.Values[name].(model.Removed); gone {
				unset = append(unset, bson.E{Key: name, Value: ""})
				continue
			}
			set = append(set, bson.E{Key: name, Value: bsonValue(c.Values[name])})
		}
		update := bson.D{}
		if len(set) > 0 {
			update = append(update, bson.E{Key: "$set", Value: set})
		}
		if len(unset) > 0 {
			update = append(update, bson.E{Key: "$unset", Value: unset})
		}
		st := source.Statement{
			SQL: fmt.Sprintf("db.%s.updateOne(%s, %s)", coll, extJSON(f), extJSON(update)),
			Op:  &documentWrite{kind: c.Kind, filter: f, update: update},
		}
		return st, "Change " + strings.Join(names, ", ") + " in the document where " + said, nil

	case source.ChangeDelete:
		f, said, err := filter()
		if err != nil {
			return source.Statement{}, "", err
		}
		st := source.Statement{
			SQL: fmt.Sprintf("db.%s.deleteOne(%s)", coll, extJSON(f)),
			Op:  &documentWrite{kind: c.Kind, filter: f},
		}
		return st, "Delete the document where " + said, nil

	case source.ChangeInsert:
		doc := bson.D{}
		for _, name := range names {
			if _, gone := c.Values[name].(model.Removed); gone {
				continue // a new document simply does not have it
			}
			v := bsonValue(c.Values[name])
			if name == "_id" {
				v = idValue(c.Values[name])
			}
			doc = append(doc, bson.E{Key: name, Value: v})
		}
		st := source.Statement{
			SQL: fmt.Sprintf("db.%s.insertOne(%s)", coll, extJSON(doc)),
			Op:  &documentWrite{kind: c.Kind, doc: doc},
		}
		if len(doc) == 0 {
			// A document of nothing is a document with only the _id the
			// server gives it, which is a document all the same.
			return st, "Add an empty document", nil
		}
		return st, "Add a document: " + strings.Join(names, ", "), nil
	}
	return source.Statement{}, "", fmt.Errorf("a change of unknown kind %d", c.Kind)
}

// idValue is the _id a key names. The grid holds what a browse gave it — an
// ObjectID written out as its hex, or whatever else the document's _id was —
// so a value that reads as an ObjectID is one again here.
func idValue(v any) any {
	if s, ok := v.(string); ok {
		if oid, err := bson.ObjectIDFromHex(s); err == nil {
			return oid
		}
	}
	return bsonValue(v)
}

// bsonValue is a value as the driver writes it. What the grid holds as text
// to keep it exact goes back as what it is.
func bsonValue(v any) any {
	switch x := v.(type) {
	case model.Decimal:
		if d, err := bson.ParseDecimal128(string(x)); err == nil {
			return d
		}
		return string(x)
	case model.JSON:
		var doc bson.D
		if err := bson.UnmarshalExtJSON([]byte(x), false, &doc); err == nil {
			return doc
		}
		return string(x)
	case []byte:
		return bson.Binary{Subtype: 0, Data: x}
	case model.Default:
		// The server gives a document its _id; nothing else has a default.
		return nil
	}
	return v
}

// extJSON renders a document as the extended JSON a person can read and
// mongosh takes back.
func extJSON(d bson.D) string {
	b, err := bson.MarshalExtJSON(d, false, false)
	if err != nil {
		return "{}"
	}
	return string(b)
}
