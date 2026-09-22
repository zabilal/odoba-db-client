package shell

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// The DDL preview (FR-6.4, UX principle 6).
//
// Nothing structural runs without being read first, and what is read is what
// runs: the statements shown are the list handed to the server, not a
// rendering of them. A preview that regenerated on the way to running would
// be a preview of something else.

// preview renders the design and shows what it would run.
func (p *designPanel) preview() {
	live, ok := p.s.d.WS.Get(p.t.connID)
	if !ok {
		p.t.footer.SetText("This connection is not open.")
		return
	}
	if !app.CanRenderDDL(live.Source) {
		p.t.footer.SetText("This connection cannot render structural changes as statements yet, " +
			"so there is nothing to preview and nothing to run.")
		return
	}
	stmts, err := app.PlanDDL(live.Source, p.design)
	if err != nil {
		p.s.showError(err)
		return
	}
	if len(stmts) == 0 {
		p.t.footer.SetText("No changes yet.")
		return
	}
	p.show(stmts)
}

// show puts the statements up, and runs exactly those if asked.
func (p *designPanel) show(stmts []source.Statement) {
	p.s.previewDDL(p.t.ddl(), stmts, func() { p.s.reopenDesign(p.t) })
}

// ddlTarget is where a structural change says what happened, and whose life
// it runs under. A tab is one. A rename asked for from the tree is another,
// and has no tab at all — which is why this is not simply a *tab.
type ddlTarget struct {
	connID string
	ctx    context.Context
	say    func(string)
}

// ddl is a tab as a place to report a structural change: its own footer,
// under its own context, so closing the tab stops what it started.
func (t *tab) ddl() ddlTarget {
	return ddlTarget{connID: t.connID, ctx: t.ctx, say: t.footer.SetText}
}

// previewDDL shows what would run, and runs exactly that if asked. after is
// what to do once it has, which for every caller is to read the object again
// rather than assume it is now what was asked for.
func (s *Shell) previewDDL(t ddlTarget, stmts []source.Statement, after func()) {
	body := widget.NewTextGridFromString(scriptOf(stmts))
	head := widget.NewLabel(fmt.Sprintf("%s will run, in this order. Nothing has run yet.",
		nounCount(len(stmts), "statement")))
	head.Wrapping = fyne.TextWrapWord

	d := dialog.NewCustomConfirm("Run These Changes?", "Run", "Cancel",
		container.NewBorder(head, nil, nil, nil, container.NewVScroll(body)),
		func(ok bool) {
			if ok {
				s.runDDL(t, stmts, false, after)
			}
		}, s.win)
	d.Resize(fyne.NewSize(680, 460))
	d.Show()
}

// runDDL sends the statements that were read, and nothing else.
func (s *Shell) runDDL(t ddlTarget, stmts []source.Statement, confirmed bool, after func()) {
	live, ok := s.d.WS.Get(t.connID)
	if !ok {
		t.say("This connection is not open.")
		return
	}
	t.say("Running…")
	ctx := t.ctx
	go func() {
		out, err := app.ApplyDDL(ctx, live.Source, stmts, confirmed)
		s.d.Run(func() {
			if ctx.Err() != nil {
				return
			}
			s.ranDDL(t, stmts, out, err, after)
		})
	}()
}

// ranDDL says what happened, and asks again where the connection wants
// asking.
func (s *Shell) ranDDL(t ddlTarget, stmts []source.Statement, out app.DDLOutcome, err error, after func()) {
	switch {
	case err == nil:
		// Read again from the server rather than assumed: what the object is
		// now is what the server says it is, not what was asked for.
		t.say(nounCount(out.Ran, "statement") + " ran.")
		if after != nil {
			after()
		}

	case errors.Is(err, source.ErrConfirmationRequired):
		// Nothing ran: the guard refused before the first statement
		// reached the server, so asking and then running is safe (FR-4.9).
		s.askToType(t.connID, "Change Structure on Production?",
			productionBody("changes the structure of", s.connName(t.connID), "Nothing has run yet."),
			"Run", func() { s.runDDL(t, stmts, true, after) },
			func() { t.say("Not run") })

	case errors.Is(err, source.ErrReadOnly):
		t.say("Not run: this connection is read-only, and these statements change it.")

	default:
		// Part-way is the truth and is said as such: which statement
		// stopped it, and how many had already run.
		t.say(fmt.Sprintf("%s ran, then %s failed: %v", nounCount(out.Ran, "statement"),
			firstLineOf(out.Failed), err))
		s.showError(err)
	}
}

// scriptOf is the statements as somebody reads them, one per line, each
// ended the way they would type it.
func scriptOf(stmts []source.Statement) string {
	lines := make([]string, len(stmts))
	for i, s := range stmts {
		lines[i] = s.SQL + ";"
	}
	return strings.Join(lines, "\n\n")
}

// firstLineOf keeps a failed statement to one line in a footer.
func firstLineOf(sql string) string {
	if i := strings.IndexByte(sql, '\n'); i >= 0 {
		sql = sql[:i] + "…"
	}
	if len(sql) > 80 {
		sql = sql[:80] + "…"
	}
	return sql
}
