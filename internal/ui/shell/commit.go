package shell

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// Reviewing and committing a table's pending changes (FR-4.4, FR-4.5,
// FR-4.9, ADR-0032). Review Changes shows every statement the changes would
// run, and the values bound to each, before anything runs; Commit runs them
// in one transaction. On a production connection Commit asks first.

// canReview reports whether the active tab has changes to review, and is
// not already committing them.
func (s *Shell) canReview() bool {
	t := s.activeTab()
	return t != nil && t.pending != nil && t.pending.Len() > 0 && !t.committing
}

func (s *Shell) reviewActive() {
	if s.canReview() {
		s.review(s.activeTab())
	}
}

// review plans the tab's changes and shows the plan. Nothing is written
// until Commit.
func (s *Shell) review(t *tab) {
	plan, err := t.browse.Plan(t.ctx, t.pending.Changeset(false))
	if err != nil {
		t.said = "Could not plan the changes: " + err.Error()
		s.showCount(t)
		return
	}
	d := dialog.NewCustomConfirm("Review Changes", "Commit", "Cancel", reviewBody(plan), func(ok bool) {
		if ok {
			s.commit(t, plan)
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
		what := widget.NewLabelWithStyle(fmt.Sprintf("%d. %s", i+1, desc), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		what.Wrapping = fyne.TextWrapWord
		sql := widget.NewLabelWithStyle(st.SQL, fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
		sql.Wrapping, sql.Selectable = fyne.TextWrapWord, true
		items = append(items, what, sql)
		if len(st.Args) > 0 {
			vals := widget.NewLabel("Values: " + argsText(st.Args))
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

// commit writes a plan. On a production connection it asks first; the plan
// is made again with the consent, since nothing has run yet (FR-4.9).
func (s *Shell) commit(t *tab, plan *source.WritePlan) {
	if !plan.Guarded {
		s.apply(t, plan)
		return
	}
	c, _ := s.d.Conns.Get(t.connID)
	d := dialog.NewConfirm("Change Data on Production?",
		fmt.Sprintf("These changes write to “%s”, which is marked Production. Nothing has been written yet.", c.Name),
		func(yes bool) {
			if !yes {
				t.said = "Not committed"
				s.showCount(t)
				return
			}
			confirmed, err := t.browse.Plan(t.ctx, t.pending.Changeset(true))
			if err != nil {
				t.said = "Could not plan the changes: " + err.Error()
				s.showCount(t)
				return
			}
			s.apply(t, confirmed)
		}, s.win)
	d.SetConfirmText("Commit")
	d.SetDismissText("Cancel")
	d.SetConfirmImportance(widget.DangerImportance)
	d.Show()
}

// apply runs a plan off the UI goroutine.
func (s *Shell) apply(t *tab, plan *source.WritePlan) {
	t.committing, t.said = true, "Committing…"
	s.showCount(t)
	go func() {
		out, err := t.browse.Apply(t.ctx, plan)
		s.d.Run(func() {
			if t.ctx.Err() == nil {
				s.applied(t, plan, out, err)
			}
		})
	}()
}

// applied says what a commit did. Written, the changes are forgotten and
// the rows read again. Refused, or stopped by a failing statement, they
// stay as they were, and the row the failing statement was for is
// selected.
func (s *Shell) applied(t *tab, plan *source.WritePlan, out *source.WriteOutcome, err error) {
	t.committing = false
	switch {
	case errors.Is(err, source.ErrReadOnly):
		t.said = "Not committed: this connection is read-only."
	case err != nil:
		t.said = "Could not commit: " + err.Error()
	case out.Err != nil && out.FailedAt >= 0 && out.FailedAt < len(plan.Descriptions):
		written := "Nothing was written."
		if !out.RolledBack {
			written = "The changes before it may have been written."
		}
		t.said = fmt.Sprintf("Not committed: %s failed: %v. %s", plan.Descriptions[out.FailedAt], out.Err, written)
		s.showFailed(t, out.FailedAt)
	case out.Err != nil:
		t.said = "Not committed: " + out.Err.Error()
	default:
		n := t.pending.Len()
		t.pending.RevertAll()
		t.model.SetAdded(nil)
		t.said = "Committed " + changesText(n)
		s.reload(t)
		return
	}
	s.showCount(t)
}

// showFailed selects the row the changeset's i'th change is to: a new row
// by its place, or a row read by its key, if it is in memory.
func (s *Shell) showFailed(t *tab, i int) {
	key, added, ok := t.pending.Change(i)
	switch {
	case !ok:
	case added >= 0:
		t.grid.GoTo(grid.CellID{Row: added, Col: 0})
	default:
		if r, found := t.model.Find(func(row model.Row) bool { return t.pending.IsRow(row, key) }); found {
			t.grid.GoTo(grid.CellID{Row: int(r), Col: 0})
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
