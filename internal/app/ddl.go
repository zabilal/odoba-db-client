package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
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

// PlanSource renders an object whose structure is its source (FR-6.5).
//
// Apart from PlanDDL because there is nothing to compare: a view is its
// text, and what runs is the statement that makes it so. What was edited is
// sent, and read first like every other structural change.
func PlanSource(src source.Source, ref model.ObjectRef, obj any) (_ []source.Statement, err error) {
	defer panics.Recover(&err, "rendering a source change")
	gen, ok := ddl(src)
	if !ok {
		return nil, ErrNoDDL
	}
	return gen.CreateObject(ref, obj)
}

// SplitStatements reads a script as the statements it holds, using the
// connection's own rules about where one ends.
//
// It is how a source that is already statements — a sequence's numbers,
// which have no other form — reaches the preview: what somebody edited is
// what runs, split the way that engine splits it.
func SplitStatements(src source.Source, script string) []source.Statement {
	if d, ok := src.(source.Dialect); ok {
		var out []source.Statement
		for _, st := range d.SplitScript(script) {
			if strings.TrimSpace(st.Text) != "" {
				out = append(out, source.Statement{SQL: strings.TrimRight(strings.TrimSpace(st.Text), ";")})
			}
		}
		return out
	}
	if strings.TrimSpace(script) == "" {
		return nil
	}
	return []source.Statement{{SQL: strings.TrimRight(strings.TrimSpace(script), ";")}}
}

// Renaming an object, and saying first what it costs (FR-6.6).

// ErrNoDependencies is a source that cannot say what names an object. It is
// not a failure either: whatever asks says so, rather than showing an empty
// list, which would read as "nothing depends on this" and be a promise
// nobody made.
var ErrNoDependencies = errors.New("app: this connection cannot say what depends on an object")

// PlanRename renders renaming an object as the statements that would do it.
func PlanRename(src source.Source, ref model.ObjectRef, to string) (_ []source.Statement, err error) {
	defer panics.Recover(&err, "rendering a rename")
	gen, ok := ddl(src)
	if !ok {
		return nil, ErrNoDDL
	}
	return gen.RenameObject(ref, to)
}

// CanRename reports whether an object could be renamed on this connection.
//
// It asks by rendering, because the driver is the only thing that knows
// which kinds it can rename: a second list of kinds here would be a list to
// get wrong. The name it renders with is thrown away and never sent — it
// only has to differ from the one the object has, since renaming something
// to what it is called is no change and renders nothing.
func CanRename(src source.Source, ref model.ObjectRef) bool {
	to := "a"
	if ref.Name() == to {
		to = "b"
	}
	stmts, err := PlanRename(src, ref, to)
	return err == nil && len(stmts) > 0
}

// DependentsOf lists what names an object, and what a rename would do to
// each. A source that cannot say answers ErrNoDependencies.
func DependentsOf(ctx context.Context, src source.Source, ref model.ObjectRef) (_ []model.Dependent, err error) {
	defer panics.Recover(&err, "reading what depends on an object")
	r, ok := src.(source.DependencyReader)
	if !ok {
		return nil, ErrNoDependencies
	}
	return r.Dependents(ctx, ref)
}

// Breaking is the dependents a rename would break, which is what somebody
// renaming an object needs to read first.
func Breaking(deps []model.Dependent) []model.Dependent {
	var out []model.Dependent
	for _, d := range deps {
		if d.Breaks {
			out = append(out, d)
		}
	}
	return out
}
