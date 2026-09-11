package app

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
)

// ScriptKind is a statement "script as" writes (FR-2.4).
type ScriptKind int

const (
	ScriptSelect ScriptKind = iota
	ScriptInsert
	ScriptUpdate
)

// ScriptAs writes a statement for an object, for a person to edit and run.
// The columns and key come from Describe, and every name and placeholder
// from the source's dialect (ARCH-2). INSERT leaves out the columns the
// server fills in itself: generated, identity and auto-increment ones.
// UPDATE sets the columns outside the primary key and matches on the key.
// A view is written only as SELECT.
func ScriptAs(ctx context.Context, src source.Source, ref model.ObjectRef, kind ScriptKind) (_ string, err error) {
	defer panics.Recover(&err, "writing a script")
	d, ok := src.(source.Dialect)
	if !ok {
		return "", errors.New("app: this source has no query language to write a script in")
	}
	desc, err := src.Describe(ctx, ref)
	if err != nil {
		return "", err
	}
	var cols []model.Column
	var key []string
	switch v := desc.(type) {
	case *model.Table:
		cols = v.Columns
		if v.PrimaryKey != nil {
			key = v.PrimaryKey.Columns
		}
	case *model.View:
		if kind != ScriptSelect {
			return "", errors.New("app: a view is written only as SELECT")
		}
		cols = v.Columns
	default:
		return "", fmt.Errorf("app: a script cannot be written for a %s", ref.Kind)
	}
	if len(cols) == 0 {
		return "", errors.New("app: the object has no columns to write")
	}
	quote := func(cs []model.Column) []string {
		out := make([]string, len(cs))
		for i, c := range cs {
			out[i] = d.QuoteIdentifier(c.Name)
		}
		return out
	}
	table := d.QualifyRef(ref)
	switch kind {
	case ScriptInsert:
		given := slices.DeleteFunc(slices.Clone(cols), func(c model.Column) bool {
			return c.Generated != "" || c.Identity || c.AutoIncrement
		})
		if len(given) == 0 {
			given = cols
		}
		return sqlscript.Insert(table, quote(given), d.Placeholder), nil
	case ScriptUpdate:
		sets := slices.DeleteFunc(slices.Clone(cols), func(c model.Column) bool {
			return c.Generated != "" || slices.Contains(key, c.Name)
		})
		if len(sets) == 0 {
			sets = cols
		}
		keys := make([]string, len(key))
		for i, k := range key {
			keys[i] = d.QuoteIdentifier(k)
		}
		return sqlscript.Update(table, quote(sets), keys, d.Placeholder), nil
	}
	return sqlscript.Select(table, quote(cols)), nil
}
