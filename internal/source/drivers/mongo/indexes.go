package mongo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Index management (FR-6.3, FR-12.1, T2.36).
//
// An index is made and unmade on its own here: a collection has no DDL to
// alter, and an index is a call. Every change is planned before it runs, and
// the plan says what will be sent (FR-6.4), which is the mongosh call — the
// same rendering a write's plan carries (ADR-0066).

var _ source.IndexManager = (*mongoSource)(nil)

// PlanIndex renders making an index on a collection.
func (s *mongoSource) PlanIndex(_ context.Context, ref model.ObjectRef, idx model.DocumentIndex,
	confirmed bool) (_ *source.WritePlan, err error) {
	defer panics.Recover(&err, "planning an index")
	if err := s.indexable(ref); err != nil {
		return nil, err
	}
	keys, err := indexKeysOf(idx.Keys)
	if err != nil {
		return nil, err
	}
	opts, said := indexOptions(idx)
	call := fmt.Sprintf("db.%s.createIndex(%s", ref.Path[1], extJSON(keys))
	if len(opts) > 0 {
		call += ", " + extJSON(opts)
	}
	call += ")"
	desc := "Make an index on " + strings.Join(keyNames(idx.Keys), ", ")
	if said != "" {
		desc += " (" + said + ")"
	}
	return s.indexPlan(ref, call, desc, &indexWrite{keys: keys, opts: opts, name: idx.Name}, confirmed), nil
}

// PlanDropIndex renders unmaking one.
func (s *mongoSource) PlanDropIndex(_ context.Context, ref model.ObjectRef, name string,
	confirmed bool) (_ *source.WritePlan, err error) {
	defer panics.Recover(&err, "planning an index")
	if err := s.indexable(ref); err != nil {
		return nil, err
	}
	switch strings.TrimSpace(name) {
	case "":
		return nil, errors.New("mongodb: an index is dropped by name")
	case "*":
		// The driver takes * as every index; nothing here asks for that by
		// accident, so it is refused rather than obeyed.
		return nil, errors.New("mongodb: drop one index at a time, by its name")
	case "_id_":
		return nil, errors.New("mongodb: the _id index is how a document is found, and cannot be dropped")
	}
	call := fmt.Sprintf("db.%s.dropIndex(%q)", ref.Path[1], name)
	return s.indexPlan(ref, call, "Drop the index "+name, &indexWrite{drop: name}, confirmed), nil
}

// ApplyIndex runs a plan made here.
func (s *mongoSource) ApplyIndex(ctx context.Context, plan *source.WritePlan) (_ *source.WriteOutcome, err error) {
	defer panics.Recover(&err, "changing an index")
	if err := s.indexable(plan.Target); err != nil {
		return nil, err
	}
	for _, st := range plan.Statements {
		if err := s.cfg.Guard.Allow(source.AccessDDL, st.Confirmed); err != nil {
			return nil, err
		}
	}
	out := &source.WriteOutcome{FailedAt: -1}
	for i, st := range plan.Statements {
		// The call is the statement's own; a plan that carries none was made
		// somewhere else, and the server is not reached for it.
		op, ok := st.Op.(*indexWrite)
		if !ok {
			out.Applied, out.FailedAt, out.Err = i, i, errors.New("mongodb: this plan was not made here")
			return out, nil
		}
		coll := s.client.Database(plan.Target.Path[0]).Collection(plan.Target.Path[1])
		if err := op.run(ctx, coll); err != nil {
			out.Applied, out.FailedAt, out.Err = i, i, err
			return out, nil
		}
		out.Applied++
	}
	return out, nil
}

// indexable reports whether a ref is a collection whose indexes can be
// changed.
func (s *mongoSource) indexable(ref model.ObjectRef) error {
	if ref.Kind != model.KindCollection || len(ref.Path) < 2 {
		return fmt.Errorf("mongodb: %s has no indexes of its own", ref)
	}
	return nil
}

// indexPlan is one call, planned. A structural change is guarded as a write
// is, and says on a production connection that it will ask (FR-4.9).
func (s *mongoSource) indexPlan(ref model.ObjectRef, call, desc string, op *indexWrite, confirmed bool) *source.WritePlan {
	return &source.WritePlan{
		Target:       ref,
		Statements:   []source.Statement{{SQL: call, Op: op, Confirmed: confirmed}},
		Descriptions: []string{desc},
		Atomic:       false, // one call, and nothing to undo it
		Guarded:      s.cfg.Guard.RequiresConfirmation(source.AccessDDL),
	}
}

// indexWrite is the call an index plan carries.
type indexWrite struct {
	keys bson.D
	opts bson.D
	name string
	drop string
}

func (w *indexWrite) run(ctx context.Context, coll *mongodriver.Collection) error {
	if w.drop != "" {
		return coll.Indexes().DropOne(ctx, w.drop)
	}
	opts := options.Index()
	for _, e := range w.opts {
		switch e.Key {
		case "name":
			opts.SetName(e.Value.(string))
		case "unique":
			opts.SetUnique(e.Value.(bool))
		case "sparse":
			opts.SetSparse(e.Value.(bool))
		case "expireAfterSeconds":
			opts.SetExpireAfterSeconds(e.Value.(int32))
		}
	}
	_, err := coll.Indexes().CreateOne(ctx, mongodriver.IndexModel{Keys: w.keys, Options: opts})
	return err
}

// indexKeysOf turns the keys a person named into the document the server
// takes: a field and a direction, or a field and the kind of index it is.
func indexKeysOf(keys []model.IndexColumn) (bson.D, error) {
	if len(keys) == 0 {
		return nil, errors.New("mongodb: an index is on at least one field")
	}
	out := make(bson.D, 0, len(keys))
	seen := map[string]bool{}
	for _, k := range keys {
		name := strings.TrimSpace(k.Name)
		if name == "" {
			return nil, errors.New("mongodb: a field of an index has no name")
		}
		if seen[name] {
			return nil, fmt.Errorf("mongodb: %s is in the index twice", name)
		}
		seen[name] = true
		switch {
		case k.Expression != "":
			out = append(out, bson.E{Key: name, Value: k.Expression})
		case k.Descending:
			out = append(out, bson.E{Key: name, Value: -1})
		default:
			out = append(out, bson.E{Key: name, Value: 1})
		}
	}
	return out, nil
}

// indexOptions are what an index is beyond its keys, and how that reads.
func indexOptions(idx model.DocumentIndex) (bson.D, string) {
	out := bson.D{}
	var said []string
	if name := strings.TrimSpace(idx.Name); name != "" {
		out = append(out, bson.E{Key: "name", Value: name})
	}
	if idx.Unique {
		out = append(out, bson.E{Key: "unique", Value: true})
		said = append(said, "unique")
	}
	if idx.Sparse {
		out = append(out, bson.E{Key: "sparse", Value: true})
		said = append(said, "sparse")
	}
	if idx.TTL > 0 {
		out = append(out, bson.E{Key: "expireAfterSeconds", Value: int32(idx.TTL)})
		said = append(said, fmt.Sprintf("expiring after %ds", idx.TTL))
	}
	return out, strings.Join(said, ", ")
}

// keyNames are the fields an index is on, as a person reads them.
func keyNames(keys []model.IndexColumn) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		switch {
		case k.Expression != "":
			out = append(out, k.Name+" "+k.Expression)
		case k.Descending:
			out = append(out, k.Name+" descending")
		default:
			out = append(out, k.Name)
		}
	}
	return out
}
