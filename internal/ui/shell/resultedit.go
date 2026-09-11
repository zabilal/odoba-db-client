package shell

import (
	"slices"

	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// Editing a query's result in its grid (FR-4.8, ADR-0036). A result known
// by a table's key (ADR-0035), made by a statement that only reads, is
// edited as a table's rows are: its changes are its own, counted under it,
// reviewed and committed to the table. Once they are written, the
// statement runs again, for the rows as they now are.

// resultKeyText is what a result read from a table, but not known by its
// key, says under its rows (FR-4.7).
const resultKeyText = "Read-only: these rows are not one table's with its key among the columns"

// result is one result of a query tab's run, in a grid of its own.
type result struct {
	rs    *app.ResultSet // the rows shown, replaced when they are read again
	stmt  string         // the statement that made it
	named map[string]any // the values that statement ran with
	count *widget.Label  // what it says, under its rows
	said  string         // what the last action on it said
	ed    *edits         // nil for a result that is not edited
}

// show says how many rows the result has, how many changes are pending,
// and what the last action said.
func (r *result) show() {
	text := resultCount(r.rs)
	if n := r.ed.changes(); n > 0 {
		text += " · " + pendingText(n)
	}
	if r.said != "" {
		text += " — " + r.said
	}
	r.count.SetText(text)
	if r.ed != nil {
		r.ed.showReview()
	}
}

// editsResult reports whether a result made by stmt is edited: on a
// connection that edits (FR-1.8), when the session can write it and read it
// again.
func (s *Shell) editsResult(t *tab, rs *app.ResultSet, stmt string) bool {
	_, readOnly := s.envOf(t)
	return !readOnly && t.query.session.Editable(rs, stmt)
}

// saysUnkeyed reports whether a result that is not edited says why: it
// reads a table, on a connection that edits, but is not known by its key.
func (s *Shell) saysUnkeyed(t *tab, rs *app.ResultSet) bool {
	_, readOnly := s.envOf(t)
	return !readOnly && !rs.Identity().Editable() &&
		slices.ContainsFunc(rs.Columns(), func(c model.ColumnDef) bool { return !c.Origin.IsZero() })
}

// editResult lets a result's rows be edited in its grid, by the key it is
// known by, and committed to its table.
func (s *Shell) editResult(t *tab, r *result, g *grid.TableGrid, m *grid.Model, update func()) {
	e := &edits{ctx: t.ctx, connID: t.connID, grid: g, model: m, writes: t.query.session,
		say: func(text string) { r.said = text }, show: r.show,
		reload: func() { s.reread(t, r, g, m, update) }}
	e.review = widget.NewButton("Review Changes…", func() {
		if e.reviewable() {
			s.review(e)
		}
	})
	e.review.Hide()
	r.ed = e
	_ = s.edit(e, r.rs.Columns(), r.rs.Identity()) // it is known by a key, which edit takes
}

// reread runs the statement that made a result again once its changes are
// written, and shows the rows as they now are in the same grid. If they
// cannot be read, the rows read before stay, and it is said.
func (s *Shell) reread(t *tab, r *result, g *grid.TableGrid, m *grid.Model, update func()) {
	q := t.query
	r.rs.Close() // nothing more is read into it: the session is about to run the statement
	go func() {
		rs, err := q.session.Reread(t.ctx, r.stmt, r.named)
		s.d.Run(func() {
			i := slices.Index(q.grids, g)
			switch {
			case t.ctx.Err() != nil || i < 0: // the tab closed, or the script ran again
				if rs != nil {
					rs.Close()
				}
				return
			case err != nil:
				r.said += ", but the rows could not be read again: " + err.Error()
			default:
				r.rs, q.sets[i] = rs, rs
				m.SetFetcher(rs)
				g.ScheduleRefresh()
				s.follow(t, rs, m, g, update)
			}
			r.show()
		})
	}()
}

// resultChanges is how many changes not committed a query tab's results
// hold.
func resultChanges(q *queryTab) int {
	n := 0
	for _, r := range q.res {
		n += r.ed.changes()
	}
	return n
}

// committing reports whether any of a query tab's results is having its
// changes written.
func committing(q *queryTab) bool {
	return slices.ContainsFunc(q.res, func(r *result) bool { return r.ed != nil && r.ed.committing })
}
