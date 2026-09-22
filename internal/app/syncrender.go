package app

import (
	"fmt"
	"slices"

	"github.com/ikigai-db/ikigai-db/internal/diff"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Turning the objects a selection touches into statements.
//
// The order is a whole schema's order (ADR-0118) with one addition: what is
// dropped goes first. A table on its way out can be the thing stopping a
// table on its way in from being made — the same name, or a constraint on
// it — and dropping afterwards would fail halfway with the mess already
// made.

// render writes the statements for everything chosen, in an order that runs.
func (s *chosenSet) render(gen source.DDLGenerator, live, wanted *model.Database) ([]source.Statement, error) {
	var drops, makes, alters, keys []source.Statement

	for _, kind := range buildOrder {
		for _, id := range s.order {
			c := s.byID[id]
			if c.kind != kind {
				continue
			}
			ref := model.NewRef(c.kind, live.Name, c.schema, c.name)
			switch {
			case c.status == diff.Removed:
				st, err := gen.DropObject(ref, false)
				if err != nil {
					return nil, err
				}
				drops = append(drops, st...)

			case c.status == diff.Added:
				obj, ok := objectIn(wanted, c.schema, c.kind, c.name)
				if !ok {
					return nil, fmt.Errorf("app: %s %s is not in the model it was compared against",
						c.kind, c.name)
				}
				st, added, err := build(gen, ref, obj)
				if err != nil {
					return nil, err
				}
				makes = append(makes, st...)
				keys = append(keys, added...)

			default:
				st, err := s.alter(gen, ref, c, live, wanted)
				if err != nil {
					return nil, err
				}
				alters = append(alters, st...)
			}
		}
	}
	return slices.Concat(drops, makes, alters, keys), nil
}

// alter writes the change to one object that is in both, for the parts of it
// that were chosen.
//
// A table is altered by comparing two tables, which is what the generator
// already does: the one that is there, and a copy of it with the chosen
// differences applied. Choosing three columns of eight therefore renders
// exactly those three, because the other five are the same on both sides of
// the comparison the generator makes.
func (s *chosenSet) alter(gen source.DDLGenerator, ref model.ObjectRef, c *choice,
	live, wanted *model.Database) ([]source.Statement, error) {
	from, inLive := objectIn(live, c.schema, c.kind, c.name)
	to, inWanted := objectIn(wanted, c.schema, c.kind, c.name)
	if !inLive || !inWanted {
		return nil, fmt.Errorf("app: %s %s is not in both, so it cannot be altered", c.kind, c.name)
	}
	if c.kind != model.KindTable {
		// Everything else is its own source, and is sent whole: a view is
		// replaced, a routine re-created. There is no part of one to choose.
		return gen.CreateObject(ref, to)
	}

	fromTable, okFrom := from.(*model.Table)
	toTable, okTo := to.(*model.Table)
	if !okFrom || !okTo {
		return nil, fmt.Errorf("app: %s is not a table on both sides", c.name)
	}
	target := applyChosen(*fromTable, *toTable, c.parts)
	return gen.AlterObject(ref, fromTable, &target)
}

// applyChosen is the table as it is, with the chosen differences taken from
// the table as it is wanted.
//
// What is not chosen is left exactly as it is, which is what makes the
// generator render only what was asked for.
func applyChosen(from, to model.Table, parts []diff.Node) model.Table {
	out := from
	for _, p := range parts {
		switch p.Kind {
		case model.KindColumn:
			out.Columns = takeOne(out.Columns, to.Columns, p.Name,
				func(c model.Column) string { return c.Name })
		case model.KindIndex:
			out.Indexes = takeOne(out.Indexes, to.Indexes, p.Name,
				func(i model.Index) string { return i.Name })
		case model.KindForeignKey:
			out.ForeignKeys = takeOne(out.ForeignKeys, to.ForeignKeys, p.Name,
				func(f model.ForeignKey) string { return f.Name })
		case model.KindTrigger:
			out.Triggers = takeOne(out.Triggers, to.Triggers, p.Name,
				func(t model.Trigger) string { return t.Name })
		case model.KindPrimaryKey:
			out.PrimaryKey = to.PrimaryKey
		case model.KindUnique:
			out.Uniques = takeOne(out.Uniques, to.Uniques, p.Name,
				func(u model.UniqueConstraint) string { return u.Name })
		case model.KindCheck:
			out.Checks = takeOne(out.Checks, to.Checks, p.Name,
				func(c model.CheckConstraint) string { return c.Name })
		}
	}
	return out
}

// takeOne replaces, adds or removes one named thing, so that the list
// becomes what the other side says about that one and stays what this side
// says about everything else.
func takeOne[T any](have, want []T, name string, nameOf func(T) string) []T {
	out := slices.Clone(have)
	at := slices.IndexFunc(out, func(v T) bool { return nameOf(v) == name })
	i := slices.IndexFunc(want, func(v T) bool { return nameOf(v) == name })
	switch {
	case i < 0 && at >= 0:
		return slices.Delete(out, at, at+1) // only here: it goes
	case i < 0:
		return out
	case at >= 0:
		out[at] = want[i] // in both: it becomes what is wanted
		return out
	}
	return append(out, want[i]) // only there: it comes
}

// objectIn finds one object in a model.
func objectIn(db *model.Database, schema string, kind model.ObjectKind, name string) (any, bool) {
	for i := range db.Schemas {
		s := &db.Schemas[i]
		if s.Name != schema {
			continue
		}
		switch kind {
		case model.KindTable:
			if j := slices.IndexFunc(s.Tables, func(t model.Table) bool { return t.Name == name }); j >= 0 {
				return &s.Tables[j], true
			}
		case model.KindView, model.KindMaterializedView:
			if j := slices.IndexFunc(s.Views, func(v model.View) bool { return v.Name == name }); j >= 0 {
				return &s.Views[j], true
			}
		case model.KindRoutine:
			if j := slices.IndexFunc(s.Routines, func(r model.Routine) bool { return r.Name == name }); j >= 0 {
				return &s.Routines[j], true
			}
		case model.KindSequence:
			if j := slices.IndexFunc(s.Sequences, func(q model.Sequence) bool { return q.Name == name }); j >= 0 {
				return &s.Sequences[j], true
			}
		}
	}
	return nil, false
}
