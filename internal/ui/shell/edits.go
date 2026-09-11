package shell

import (
	"context"
	"slices"

	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// edits is one grid's editing (FR-4.1 to FR-4.8): its rows' pending
// changes, the grid and model that show them, where they are written, and
// how their state is said. A table tab has one, and so does each query
// result that is edited (resultedit.go). The Edit menu's commands act on
// the edits of the grid in front (activeEdits).
type edits struct {
	ctx     context.Context
	connID  string
	grid    *grid.TableGrid
	model   *grid.Model
	pending *app.Pending // nil where the rows are not edited
	writes  rowWriter    // plans the changes, and applies the plan
	// review is Review Changes…, beside the count while changes are
	// pending; committing is set while they are being written (commit.go).
	review     *widget.Button
	committing bool
	// say sets what the last action said; show draws the count, the
	// changes and that word again; reload reads the rows again, once the
	// changes are written.
	say    func(string)
	show   func()
	reload func()
}

// rowWriter plans a changeset and applies a plan: a table's browse, or a
// query's session.
type rowWriter interface {
	Plan(ctx context.Context, cs source.Changeset) (*source.WritePlan, error)
	Apply(ctx context.Context, plan *source.WritePlan) (*source.WriteOutcome, error)
}

// activeEdits is the editing of the grid in front, if it has any.
func (s *Shell) activeEdits() *edits { return editsFor(s.activeTab(), s.activeGrid()) }

// editsFor is the editing of a tab's grid, if it has any: a table tab's, or
// a query result's.
func editsFor(t *tab, g *grid.TableGrid) *edits {
	switch {
	case t == nil || g == nil:
		return nil
	case t.query != nil:
		if i := slices.Index(t.query.grids, g); i >= 0 {
			return t.query.res[i].ed
		}
		return nil
	case t.ed != nil && t.ed.grid == g:
		return t.ed
	}
	return nil
}

// changes is how many rows have changes not yet committed.
func (e *edits) changes() int {
	if e == nil || e.pending == nil {
		return 0
	}
	return e.pending.Len()
}

// reviewable reports whether there are changes to review, and they are
// not being committed.
func (e *edits) reviewable() bool {
	return e.changes() > 0 && !e.committing
}

// showReview offers Review Changes… while changes are pending, and not
// while they are being committed.
func (e *edits) showReview() {
	setShown(e.review, e.changes() > 0)
	if e.committing {
		e.review.Disable()
	} else {
		e.review.Enable()
	}
}

// showAdded puts the pending new rows in the grid and says the changes.
func (e *edits) showAdded() {
	e.model.SetAdded(e.pending.Added())
	e.grid.Table.Refresh()
	e.show()
}
