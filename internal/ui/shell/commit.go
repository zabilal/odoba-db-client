package shell

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/l10n"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// Reviewing and committing a table's pending changes (FR-4.4, FR-4.5,
// FR-4.9, ADR-0032). Review Changes shows every statement the changes would
// run, and the values bound to each, before anything runs; Commit runs them
// in one transaction. On a production connection Commit asks first.

// canReview reports whether the grid in front has changes to review, and
// is not already committing them.
func (s *Shell) canReview() bool { return s.activeEdits().reviewable() }

func (s *Shell) reviewActive() {
	if e := s.activeEdits(); e.reviewable() {
		s.review(e)
	}
}

// review plans the changes and shows the plan. Nothing is written until
// Commit.
func (s *Shell) review(e *edits) {
	plan, err := e.writes.Plan(e.ctx, e.pending.Changeset(false))
	if err != nil {
		e.say("Could not plan the changes: " + err.Error())
		e.show()
		return
	}
	cs := e.pending.Changeset(false)
	d := dialog.NewCustomConfirm(l10n.T("Review Changes"), l10n.T("Commit"), l10n.T("Cancel"), reviewBody(plan), func(ok bool) {
		if ok {
			s.commit(e, plan, cs)
		}
	}, s.win)
	d.SetConfirmImportance(widget.HighImportance)
	d.Resize(fyne.NewSize(640, 440))
	d.Show()
}

// reviewBody lists a plan's statements: what each does, its SQL, and the
// values bound to it.
func reviewBody(plan *source.WritePlan) fyne.CanvasObject {
	head := statementsText(len(plan.Statements)) + " will run in one transaction: all of them, or, if one fails, none."
	if !plan.Atomic {
		head = statementsText(len(plan.Statements)) + " will run one by one: if one fails, those before it stay written."
	}
	if plan.Guarded {
		head += " This connection is marked Production, so Commit asks again before writing."
	}
	intro := widget.NewLabel(head)
	intro.Wrapping = fyne.TextWrapWord
	items := []fyne.CanvasObject{intro}
	for i, st := range plan.Statements {
		desc := "Statement"
		if i < len(plan.Descriptions) {
			desc = plan.Descriptions[i]
		}
		what := widget.NewLabelWithStyle(l10n.F("%d. %s", i+1, desc), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		what.Wrapping = fyne.TextWrapWord
		sql := widget.NewLabelWithStyle(st.SQL, fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
		sql.Wrapping, sql.Selectable = fyne.TextWrapWord, true
		items = append(items, what, sql)
		if len(st.Args) > 0 {
			vals := widget.NewLabel(l10n.F("Values: %s", argsText(st.Args)))
			vals.Wrapping, vals.Importance = fyne.TextWrapWord, widget.LowImportance
			items = append(items, vals)
		}
	}
	return container.NewVScroll(container.NewVBox(items...))
}

// argsText is the values bound to a statement, numbered as its
// placeholders are.
func argsText(args []any) string {
	parts := make([]string, len(args))
	for i, v := range args {
		parts[i] = fmt.Sprintf("%d = %s", i+1, valueText(v))
	}
	return strings.Join(parts, ", ")
}

// valueText is a bound value as SQL would write it, near enough to read.
func valueText(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case string:
		return "'" + strings.ReplaceAll(x, "'", "''") + "'"
	case time.Time:
		return x.Format(time.RFC3339Nano)
	case []byte:
		return fmt.Sprintf("%d bytes", len(x))
	}
	return fmt.Sprint(v)
}

// commit writes a plan, once the rows it changes have been read again and
// found as they were, or a person has said to write all the same.
func (s *Shell) commit(e *edits, plan *source.WritePlan, cs source.Changeset) {
	reader, ok := e.writes.(rowRereader)
	if !ok || !reader.CanFilter() || !e.pending.Rereads() {
		s.write(e, plan, cs)
		return
	}
	// Each row the changes touch is read again by its key first, and held
	// against what was read: somebody may have changed it since (FR-4.12).
	e.committing = true
	e.say(l10n.T("Checking that the rows are as they were read…"))
	e.show()
	go func() {
		drifted, unchecked, err := e.pending.Drift(e.ctx, reader.ReadByKey)
		s.d.Run(func() {
			if e.ctx.Err() != nil {
				return
			}
			e.committing = false
			if err == nil && len(drifted) == 0 {
				s.write(e, plan, cs)
				return
			}
			e.say("")
			e.show()
			d := dialog.NewCustomConfirm(l10n.T("Rows Changed Since They Were Read"), l10n.T("Commit Anyway"), l10n.T("Cancel"),
				driftBody(e.pending.Identity().Columns, drifted, unchecked, err), func(ok bool) {
					if ok {
						s.write(e, plan, cs)
						return
					}
					e.say(l10n.T("Not committed. Refresh to read the rows as they are now."))
					e.show()
				}, s.win)
			d.SetConfirmImportance(widget.DangerImportance)
			d.Resize(fyne.NewSize(640, 360))
			d.Show()
		})
	}()
}

// rowRereader is a writer that can read a row again by its key: a table's
// browse. A query's result is written through its session, which cannot, and
// is committed unchecked.
type rowRereader interface {
	CanFilter() bool
	ReadByKey(ctx context.Context, key []source.Filter) ([]model.Row, error)
}

// driftListed is how many rows a warning lists; the rest are counted.
const driftListed = 20

// driftBody says which rows are not as they were read, and what committing
// would do to each.
func driftBody(keys []string, drifted []app.Drifted, unchecked int, err error) fyne.CanvasObject {
	var lines []string
	if err != nil {
		lines = append(lines, l10n.F("The rows could not be read again, so whether they changed is not known: %s.", err.Error()))
	}
	for i, d := range drifted {
		if i == driftListed {
			lines = append(lines, l10n.F("And %d more.", len(drifted)-driftListed))
			break
		}
		row := keyText(keys, d.Key)
		switch {
		case d.Gone && d.Kind == source.ChangeDelete:
			lines = append(lines, l10n.F("%s is no longer there. It has been deleted already.", row))
		case d.Gone:
			lines = append(lines, l10n.F("%s is no longer there. The change to it will write nothing.", row))
		}
		for _, c := range d.Columns {
			was, now := valueText(c.Was), valueText(c.Now)
			switch {
			case d.Kind == source.ChangeDelete:
				lines = append(lines, l10n.F("%s: %s was %s when read and is %s now. The row is deleted as it is now.", row, c.Name, was, now))
			case c.Edited:
				lines = append(lines, l10n.F("%s: %s was %s when read and is %s now. Committing writes over that.", row, c.Name, was, now))
			default:
				lines = append(lines, l10n.F("%s: %s was %s when read and is %s now. It is not one of your changes, and is left as it is.", row, c.Name, was, now))
			}
		}
	}
	if unchecked > 0 {
		lines = append(lines, l10n.F("%d more rows were not read again: only the first %d changed are.", unchecked, app.DriftLimit))
	}
	items := make([]fyne.CanvasObject, 0, len(lines)+1)
	intro := widget.NewLabel(l10n.T("Somebody else has changed these rows since they were read here. Nothing has been written yet."))
	intro.Wrapping = fyne.TextWrapWord
	items = append(items, intro)
	for _, line := range lines {
		l := widget.NewLabel(line)
		l.Wrapping = fyne.TextWrapWord
		items = append(items, l)
	}
	return container.NewVScroll(container.NewVBox(items...))
}

// keyText is a row as its key says which it is: "id = 5".
func keyText(names []string, key []any) string {
	parts := make([]string, len(key))
	for i, v := range key {
		name := ""
		if i < len(names) {
			name = names[i]
		}
		parts[i] = name + " = " + valueText(v)
	}
	return strings.Join(parts, ", ")
}

// write writes a plan, asking first where the connection is marked
// Production: consent is not a flag a plan can be given after the fact, so
// the changeset is planned again with it (FR-4.9).
func (s *Shell) write(e *edits, plan *source.WritePlan, cs source.Changeset) {
	if !plan.Guarded {
		s.apply(e, plan)
		return
	}
	s.askToType(e.connID, "Change Data on Production?",
		productionBody("changes write to", s.connName(e.connID), "Nothing has been written yet."),
		"Commit",
		func() {
			cs.Confirmed = true
			confirmed, err := e.writes.Plan(e.ctx, cs)
			if err != nil {
				e.say("Could not plan the changes: " + err.Error())
				e.show()
				return
			}
			s.apply(e, confirmed)
		},
		func() {
			e.say("Not committed")
			e.show()
		})
}

// apply runs a plan off the UI goroutine.
func (s *Shell) apply(e *edits, plan *source.WritePlan) {
	e.committing = true
	e.say("Committing…")
	e.show()
	go func() {
		out, err := e.writes.Apply(e.ctx, plan)
		s.d.Run(func() {
			if e.ctx.Err() == nil {
				s.applied(e, plan, out, err)
			}
		})
	}()
}

// applied says what a commit did. Written, the changes are forgotten and
// the rows read again. Refused, or stopped by a failing statement, they
// stay as they were, and the row the failing statement was for is
// selected.
func (s *Shell) applied(e *edits, plan *source.WritePlan, out *source.WriteOutcome, err error) {
	e.committing = false
	switch {
	case errors.Is(err, source.ErrReadOnly):
		e.say("Not committed: this connection is read-only.")
	case err != nil:
		e.say("Could not commit: " + err.Error())
	case out.Err != nil && out.FailedAt >= 0 && out.FailedAt < len(plan.Descriptions):
		written := "Nothing was written."
		if !out.RolledBack {
			written = "The changes before it may have been written."
		}
		e.say(fmt.Sprintf("Not committed: %s failed: %v. %s", plan.Descriptions[out.FailedAt], out.Err, written))
		s.showFailed(e, out.FailedAt)
	case out.Err != nil:
		e.say("Not committed: " + out.Err.Error())
	default:
		n := e.pending.Len()
		e.pending.RevertAll()
		e.model.SetAdded(nil)
		e.say("Committed " + changesText(n))
		e.reload()
		return
	}
	e.show()
}

// showFailed selects the row the changeset's i'th change is to: a new row
// by its place, or a row read by its key, if it is in memory.
func (s *Shell) showFailed(e *edits, i int) {
	key, added, ok := e.pending.Change(i)
	switch {
	case !ok:
	case added >= 0:
		e.grid.GoTo(grid.CellID{Row: added, Col: 0})
	default:
		if r, found := e.model.Find(func(row model.Row) bool { return e.pending.IsRow(row, key) }); found {
			e.grid.GoTo(grid.CellID{Row: int(r), Col: 0})
		}
	}
}

func statementsText(n int) string {
	if n == 1 {
		return "1 statement"
	}
	return group(int64(n)) + " statements"
}

func changesText(n int) string {
	if n == 1 {
		return "1 change"
	}
	return group(int64(n)) + " changes"
}
