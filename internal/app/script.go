package app

import (
	"context"
	"fmt"
	"slices"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Writing an object, or a whole schema, as the statements that would build
// it (FR-6.7).
//
// This renders and hands back text. Nothing here runs anything and nothing
// here asks to: a script is something to read, keep, put in a repository or
// edit and run somewhere else, and the moment it could also execute it would
// need every guard a change needs. What is generated opens in a query tab
// like any other script somebody typed.

// ScriptCreate renders one object as the statements that would build it.
func ScriptCreate(ctx context.Context, src source.Source, ref model.ObjectRef) (_ []source.Statement, err error) {
	defer panics.Recover(&err, "writing an object's DDL")
	gen, ok := ddl(src)
	if !ok {
		return nil, ErrNoDDL
	}
	obj, err := src.Describe(ctx, ref)
	if err != nil {
		return nil, err
	}
	return gen.CreateObject(ref, obj)
}

// ScriptDrop writes the DDL that would remove an object, for a person to
// read, keep or run (FR-2.8). Nothing is run here: what this returns is
// text, and a DROP somebody has not read is the one they did not mean.
//
// cascade is false, because a DROP that takes what depends on it with it
// is a choice to make with the list in front of you rather than a default.
func ScriptDrop(src source.Source, ref model.ObjectRef) (_ []source.Statement, err error) {
	defer panics.Recover(&err, "writing an object's DROP")
	gen, ok := ddl(src)
	if !ok {
		return nil, ErrNoDDL
	}
	return gen.DropObject(ref, false)
}

// buildOrder is the order a schema's classes can be built in.
//
// Sequences first, because a column's default may call one. Then tables,
// then the views over them, then the routines, then the triggers, which are
// the only things that need both a table and a routine to exist already.
var buildOrder = []model.ObjectKind{
	model.KindSequence,
	model.KindTable,
	model.KindView,
	model.KindMaterializedView,
	model.KindRoutine,
	model.KindTrigger,
}

// ScriptSchema renders everything in a schema, in an order it can be run in.
//
// It is not the objects' DDL one after another. A schema is a graph, and two
// things in it cannot be put in an order at all:
//
//   - Foreign keys can be circular — two tables referring to each other is
//     ordinary — so no sequence of CREATE TABLE statements can satisfy them.
//     Every table is written without its foreign keys, and the keys are
//     added afterwards when all the tables exist.
//   - Views select from views, so they are sorted by what they select from
//     rather than by name.
func ScriptSchema(ctx context.Context, src source.Source, schema model.ObjectRef) (_ []source.Statement, err error) {
	defer panics.Recover(&err, "writing a schema's DDL")
	gen, ok := ddl(src)
	if !ok {
		return nil, ErrNoDDL
	}
	byKind, err := schemaContents(ctx, src, schema)
	if err != nil {
		return nil, err
	}

	var out, keys []source.Statement
	for _, kind := range buildOrder {
		refs := byKind[kind]
		if kind == model.KindView || kind == model.KindMaterializedView {
			refs = inDependencyOrder(ctx, src, refs)
		}
		for _, ref := range refs {
			obj, err := src.Describe(ctx, ref)
			if err != nil {
				return nil, fmt.Errorf("reading %s: %w", ref.Name(), err)
			}
			stmts, added, err := build(gen, ref, obj)
			if err != nil {
				return nil, err
			}
			out = append(out, stmts...)
			keys = append(keys, added...)
		}
	}
	// The references between tables go last, when every table they name is
	// there to be named.
	return append(out, keys...), nil
}

// build renders one object, and for a table hands back its foreign keys
// separately so they can be added once every table exists.
func build(gen source.DDLGenerator, ref model.ObjectRef, obj any) (stmts, keys []source.Statement, err error) {
	t, ok := obj.(*model.Table)
	if !ok {
		stmts, err = gen.CreateObject(ref, obj)
		return stmts, nil, err
	}
	// The table without its keys, then the keys as a change to it: the
	// generator already knows how to add a constraint to a table that is
	// missing one, so there is no second way of writing them to get wrong.
	//
	// A table with no keys goes the same way rather than taking a short cut
	// round it. The short cut would render exactly the same statements — a
	// table compared with itself is no change — and it would be a branch
	// nothing could tell from the other one.
	bare := *t
	bare.ForeignKeys = nil
	if stmts, err = gen.CreateObject(ref, &bare); err != nil {
		return nil, nil, err
	}
	if keys, err = gen.AlterObject(ref, &bare, t); err != nil {
		return nil, nil, err
	}
	return stmts, keys, nil
}

// schemaContents is what a schema holds, by kind, asked of the source the
// way the tree asks it.
func schemaContents(ctx context.Context, src source.Source, schema model.ObjectRef) (map[model.ObjectKind][]model.ObjectRef, error) {
	classes, err := src.Children(ctx, schema)
	if err != nil {
		return nil, fmt.Errorf("reading what %s holds: %w", schema.Name(), err)
	}
	out := map[model.ObjectKind][]model.ObjectRef{}
	for _, c := range classes {
		kind, ok := model.ClassOf(c.Ref)
		if !ok || !slices.Contains(buildOrder, kind) {
			continue
		}
		objs, err := src.Children(ctx, c.Ref)
		if err != nil {
			return nil, fmt.Errorf("reading the %ss: %w", kind, err)
		}
		for _, o := range objs {
			out[kind] = append(out[kind], o.Ref)
		}
	}
	return out, nil
}

// inDependencyOrder puts each object before the ones that select from it.
//
// A source that cannot say what depends on what leaves the order as it was
// listed, which is the honest answer: guessing at an order and being wrong
// produces a script that fails halfway, and that is worse than one whose
// order somebody has to fix themselves.
func inDependencyOrder(ctx context.Context, src source.Source, refs []model.ObjectRef) []model.ObjectRef {
	if len(refs) < 2 {
		return refs
	}
	here := map[string]int{}
	for i, r := range refs {
		here[r.String()] = i
	}
	// after[i] are the objects that select from refs[i], so they come later.
	after := make([][]int, len(refs))
	for i, r := range refs {
		deps, err := DependentsOf(ctx, src, r)
		if err != nil {
			return refs
		}
		for _, d := range deps {
			if j, ok := here[d.Ref.String()]; ok && j != i {
				after[i] = append(after[i], j)
			}
		}
	}

	// Depth-first, emitting what something selects from before it. A cycle
	// cannot happen between views — PostgreSQL will not create one — but a
	// driver that reported one must not send this round forever.
	const unseen, onStack, done = 0, 1, 2
	state := make([]int, len(refs))
	var out []model.ObjectRef
	var visit func(int) bool
	visit = func(i int) bool {
		switch state[i] {
		case done:
			return true
		case onStack:
			return false // a cycle: leave the order alone
		}
		state[i] = onStack
		for _, j := range after[i] {
			if !visit(j) {
				return false
			}
		}
		state[i] = done
		out = append(out, refs[i])
		return true
	}
	for i := range refs {
		if !visit(i) {
			return refs
		}
	}
	slices.Reverse(out) // visited dependents-first, so turn it round
	return out
}
