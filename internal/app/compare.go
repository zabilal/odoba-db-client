package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/ikigai-db/ikigai-db/internal/diff"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/schemafile"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Comparing two live databases (FR-7.1).
//
// internal/diff compares two models and touches nothing else, which is what
// RISK-8 asks of it (ADR-0119). This is where the models come from.

// ErrNoSnapshot is a connection whose structure cannot be read whole. It is
// not a failure: an engine with no schema to speak of has nothing to
// compare, and whatever asks says so.
var ErrNoSnapshot = errors.New("app: this connection cannot be read as a schema to compare")

// Snapshot reads a database's whole structure.
//
// A driver that can do it in one pass does; the rest are walked object by
// object through the tree, which is slower by a round trip per object and is
// the only way to compare an engine nobody has written a Snapshot for. The
// difference is in how long it takes and in nothing else: both fill the same
// model, and a comparison cannot tell which produced it.
func Snapshot(ctx context.Context, src source.Source, database string) (_ *model.Database, err error) {
	defer panics.Recover(&err, "reading a schema")
	if s, ok := src.(source.Snapshotter); ok {
		return s.Snapshot(ctx, database)
	}
	return walkSnapshot(ctx, src, database)
}

// CanCompare reports whether a connection's structure can be read at all.
func CanCompare(src source.Source) bool {
	if src == nil {
		return false
	}
	if _, ok := src.(source.Snapshotter); ok {
		return true
	}
	// Everything else is walked, which needs a tree with objects in it.
	return src.Capabilities().Paradigm == model.ParadigmRelational
}

// Comparison is what a comparison answers: the tree of differences, and the
// two models it was made from.
//
// The tree describes differences and deliberately does not carry the objects
// (ADR-0119). A sync script needs them, so the models come back with it
// rather than being read a second time — reading again would mean writing a
// script for a database that had moved since somebody read the comparison.
type Comparison struct {
	Live   *model.Database
	Wanted *model.Database
	Tree   diff.Node
}

// Differs reports whether anything at all differs.
func (c Comparison) Differs() bool { return c.Tree.Differs() }

// Compare reads both databases and says what differs.
//
// from is what is there and to is what is wanted, the way the diff engine
// reads it: Added is what a sync script would create.
func Compare(ctx context.Context, from source.Source, fromDB string, to source.Source, toDB string) (Comparison, error) {
	a, err := Snapshot(ctx, from, fromDB)
	if err != nil {
		return Comparison{}, fmt.Errorf("reading %s: %w", naming(fromDB), err)
	}
	b, err := Snapshot(ctx, to, toDB)
	if err != nil {
		return Comparison{}, fmt.Errorf("reading %s: %w", naming(toDB), err)
	}
	return Comparison{Live: a, Wanted: b, Tree: diff.Compare(a, b)}, nil
}

func naming(db string) string {
	if db == "" {
		return "the database"
	}
	return db
}

// walkSnapshot builds a model from the tree, for a driver with no Snapshot
// of its own.
//
// It reads what the explorer reads, through the same calls, so a driver that
// can be browsed can be compared without writing anything new for it.
func walkSnapshot(ctx context.Context, src source.Source, database string) (*model.Database, error) {
	db := &model.Database{Name: database}
	ref := model.NewRef(model.KindDatabase, database)
	under, err := src.Children(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("reading what %s holds: %w", naming(database), err)
	}

	// Not every engine has a schema between the database and its tables.
	// Where there is none the database is the schema, and the objects are
	// the same objects either way.
	var schemas []model.Node
	for _, n := range under {
		if n.Ref.Kind == model.KindSchema {
			schemas = append(schemas, n)
		}
	}
	if len(schemas) == 0 {
		s, err := walkSchema(ctx, src, ref, database)
		if err != nil {
			return nil, err
		}
		db.Schemas = []model.Schema{s}
		return db, nil
	}
	for _, n := range schemas {
		s, err := walkSchema(ctx, src, n.Ref, n.Ref.Name())
		if err != nil {
			return nil, err
		}
		db.Schemas = append(db.Schemas, s)
	}
	return db, nil
}

// walkSchema fills one schema from the class folders under a node.
func walkSchema(ctx context.Context, src source.Source, ref model.ObjectRef, name string) (model.Schema, error) {
	out := model.Schema{Name: name}
	classes, err := src.Children(ctx, ref)
	if err != nil {
		return out, fmt.Errorf("reading what %s holds: %w", name, err)
	}
	for _, c := range classes {
		kind, ok := model.ClassOf(c.Ref)
		if !ok {
			continue
		}
		objs, err := src.Children(ctx, c.Ref)
		if err != nil {
			return out, fmt.Errorf("reading the %ss of %s: %w", kind, name, err)
		}
		for _, o := range objs {
			if err := describeInto(ctx, src, o.Ref, &out); err != nil {
				return out, err
			}
		}
	}
	return out, nil
}

// describeInto reads one object and puts it where it belongs.
//
// A kind this cannot describe is skipped rather than failing the whole read:
// a schema holding one object nobody wrote a Describe for would otherwise be
// a schema nobody can compare.
func describeInto(ctx context.Context, src source.Source, ref model.ObjectRef, into *model.Schema) error {
	switch ref.Kind {
	case model.KindTable, model.KindView, model.KindMaterializedView,
		model.KindRoutine, model.KindSequence, model.KindUserType:
	default:
		// A trigger arrives with the table it is on, and an index with it
		// too, so listing them again here would double them.
		return nil
	}
	desc, err := src.Describe(ctx, ref)
	if err != nil {
		return fmt.Errorf("reading %s %s: %w", ref.Kind, ref.Name(), err)
	}
	switch v := desc.(type) {
	case *model.Table:
		into.Tables = append(into.Tables, *v)
	case *model.View:
		into.Views = append(into.Views, *v)
	case *model.Routine:
		into.Routines = append(into.Routines, *v)
	case *model.Sequence:
		into.Sequences = append(into.Sequences, *v)
	case *model.UserType:
		into.UserTypes = append(into.UserTypes, *v)
	}
	return nil
}

// Comparing a live database against a model somebody saved (FR-7.1).
//
// The saved model is the wanted state — it is the one in version control,
// reviewed and agreed — and the live database is what is there. So it goes
// on the "to" side, and Added is what the database is missing.

// SaveModel writes a database's structure to a directory as a tree of files,
// one per object, for version control to hold (FR-7.6).
func SaveModel(ctx context.Context, src source.Source, database, dir string) error {
	db, err := Snapshot(ctx, src, database)
	if err != nil {
		return err
	}
	return schemafile.Write(dir, db)
}

// ReadModel loads a model saved by SaveModel.
func ReadModel(dir string) (*model.Database, error) { return schemafile.Read(dir) }

// CompareWithSaved says what a live database is missing against a saved
// model, or has that the model does not.
func CompareWithSaved(ctx context.Context, src source.Source, database, dir string) (Comparison, error) {
	live, err := Snapshot(ctx, src, database)
	if err != nil {
		return Comparison{}, fmt.Errorf("reading %s: %w", naming(database), err)
	}
	saved, err := schemafile.Read(dir)
	if err != nil {
		return Comparison{}, err
	}
	return Comparison{Live: live, Wanted: saved, Tree: diff.Compare(live, saved)}, nil
}
