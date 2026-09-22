package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Rendering a design as statements, before any of them run (FR-6.4, UX-6).
//
// This is the half that makes the designer worth anything, and the half
// that makes it safe: what somebody reads is exactly what is sent, because
// it is the same list.

// ErrNoDDL is a source that cannot render its structure as statements. It is
// not a failure — most of these engines have no DDL at all — so whatever
// asks says so rather than treating it as one.
var ErrNoDDL = errors.New("app: this connection cannot render structural changes as statements")

// PlanDDL renders a design as the statements that would make it so.
//
// Renames come first and are applied to the table handed on, because two
// tables cannot say which column became which: only the design knows, and
// after this the rest is rendered against the names the table will have
// (ADR-0114).
func PlanDDL(src source.Source, d *Design) (_ []source.Statement, err error) {
	defer panics.Recover(&err, "rendering a structural change")
	gen, ok := ddl(src)
	if !ok {
		return nil, ErrNoDDL
	}
	if d == nil {
		return nil, errors.New("app: there is no design to render")
	}

	from := d.Original()
	var out []source.Statement
	for _, c := range d.Changes() {
		if !c.Renamed() {
			continue
		}
		stmts, err := gen.RenameColumn(d.Ref, c.From.Name, c.To.Name)
		if err != nil {
			return nil, fmt.Errorf("app: renaming %s: %w", c.From.Name, err)
		}
		out = append(out, stmts...)
		for i := range from.Columns {
			if from.Columns[i].Name == c.From.Name {
				from.Columns[i].Name = c.To.Name
			}
		}
	}

	rest, err := gen.AlterObject(d.Ref, from, d.Table())
	if err != nil {
		return nil, fmt.Errorf("app: rendering the change: %w", err)
	}
	return append(out, rest...), nil
}

// ddl is the source's DDL generator, where it claims to have one.
func ddl(src source.Source) (source.DDLGenerator, bool) {
	gen, ok := src.(source.DDLGenerator)
	if !ok || !src.Capabilities().Schema.DDL {
		return nil, false
	}
	return gen, true
}

// CanRenderDDL reports whether this source renders structural changes, so
// that a designer can say what it cannot do before somebody edits for an
// hour and finds out.
func CanRenderDDL(src source.Source) bool {
	_, ok := ddl(src)
	return ok
}

// DDLOutcome says how far a structural change got.
type DDLOutcome struct {
	// Ran is how many statements succeeded.
	Ran int

	// Failed is the statement that did not, where one did not.
	Failed string
}

// ApplyDDL runs statements a design rendered, in order, stopping at the
// first that fails.
//
// Not in one transaction. Some engines cannot roll DDL back at all, and one
// that can would still leave the others half done — so rather than promise
// atomicity that depends on which engine somebody is on, this says exactly
// how far it got and which statement stopped it. What that leaves is a
// table part-way changed, which is the truth and can be looked at.
//
// Every statement goes through the session, so the guard sees each one: a
// read-only connection refuses them all and a production connection asks
// first, exactly as it would for a script somebody typed (NFR-S4, FR-4.9).
func ApplyDDL(ctx context.Context, src source.Source, stmts []source.Statement, confirmed bool) (_ DDLOutcome, err error) {
	defer panics.Recover(&err, "running a structural change")
	if len(stmts) == 0 {
		return DDLOutcome{}, nil
	}
	sessioner, ok := src.(source.Sessioner)
	if !ok {
		return DDLOutcome{}, errors.New("app: this connection cannot run statements")
	}
	session, err := sessioner.Session(ctx)
	if err != nil {
		return DDLOutcome{}, err
	}
	defer session.Close()

	var out DDLOutcome
	for _, st := range stmts {
		st.Confirmed = confirmed
		res, err := session.Query(ctx, st)
		if res != nil && res.Rows != nil {
			res.Rows.Close()
		}
		if err != nil {
			out.Failed = st.SQL
			return out, err
		}
		out.Ran++
	}
	return out, nil
}
