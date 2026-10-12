package shell

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/l10n"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// One script run on several connections (FR-5.17).
//
// A line that is "@name" and nothing else says the statements after it are
// for the saved connection of that name (app.Sections). The parts are run in
// the order written, each on its own connection, one after another: the
// second does not start until the first has answered, and a part that fails
// stops the script, so that a statement meant to follow another on another
// server never runs when the first did not happen.
//
// Every safeguard is the connection's own and is asked of that connection.
// A part that would change data on a connection marked Production is asked
// about by that connection's name, when the script reaches it — and the
// question says that what came before has already run, because it has.
//
// A result from a connection other than the tab's is shown and not edited:
// the tab's editing writes through the tab's connection, and a row changed
// there would be written to the wrong database.

// part is a section of a script and the saved connection it is for.
type part struct {
	app.Section
	connID, name string
}

// partsOf finds each section's connection among those saved. A name that is
// nobody's, or more than one connection's, is refused before anything runs.
func (s *Shell) partsOf(t *tab, sections []app.Section) ([]part, error) {
	parts := make([]part, 0, len(sections))
	for _, sec := range sections {
		if sec.Connection == "" {
			parts = append(parts, part{Section: sec, connID: t.connID, name: s.connName(t.connID)})
			continue
		}
		var found []string
		for _, c := range s.d.Conns.List() {
			if strings.EqualFold(strings.TrimSpace(c.Name), strings.TrimSpace(sec.Connection)) {
				found = append(found, c.ID)
			}
		}
		switch len(found) {
		case 0:
			return nil, errors.New(l10n.F("no saved connection is called “%s”", sec.Connection))
		case 1:
			parts = append(parts, part{Section: sec, connID: found[0], name: s.connName(found[0])})
		default:
			return nil, errors.New(l10n.F("%d saved connections are called “%s”, and the script does not say which", len(found), sec.Connection))
		}
	}
	return parts, nil
}

// runAcross runs a script that names connections: all of it, or what is
// selected, or the statement the caret is in — each on the connection the
// part of the script it is in is for.
func (s *Shell) runAcross(t *tab, all bool, sections []app.Section) {
	q := t.query
	doc := q.editor.Document()
	run := sections
	if !all {
		if from, _, sel := doc.Selection(); sel {
			in, _ := app.SectionAt(sections, doc.Offset(from))
			run = app.SectionsFrom(doc.SelectedText(), in.Connection, doc.Offset(from))
		} else {
			in, ok := app.SectionAt(sections, doc.Offset(doc.Caret()))
			if !ok {
				return
			}
			run = []app.Section{in}
			// The statement the caret is in, as the tab's own dialect
			// divides the part; where it cannot, the part.
			if stmt, start, ok := q.session.StatementAt(in.Text, max(doc.Offset(doc.Caret())-in.Offset, 0)); ok {
				run = []app.Section{{Connection: in.Connection, Text: stmt, Offset: in.Offset + start}}
			}
		}
	}
	if len(run) == 0 {
		return
	}
	parts, err := s.partsOf(t, run)
	if err != nil {
		s.clearResults(q)
		s.note(q, l10n.F("Not run: %s.", err.Error()))
		t.footer.SetText(l10n.T("Not run"))
		return
	}
	var names []string
	for _, p := range parts {
		for _, name := range q.session.Params(p.Text) {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	if len(names) > 0 {
		s.askParams(t, "", 0, names).across = parts // its Run runs the parts
		return
	}
	s.executeAcross(t, parts, 0, 0, source.ScriptOptions{})
}

// sessionOn is the tab's session on a connection: its own, or one made for a
// part of a script that is for another, kept until the tab closes.
func (s *Shell) sessionOn(t *tab, connID string) (*app.QuerySession, error) {
	q := t.query
	if connID == t.connID {
		return q.session, nil
	}
	q.othersMu.Lock()
	defer q.othersMu.Unlock()
	if qs, ok := q.others[connID]; ok {
		return qs, nil
	}
	// For as long as the tab is open, not as long as this run: what the
	// session holds — a transaction, a result still arriving — outlives it.
	qs, _, _, _, err := s.querySession(t.ctx, connID)
	if err != nil {
		return nil, err
	}
	if q.others == nil {
		q.others = map[string]*app.QuerySession{}
	}
	q.others[connID] = qs
	return qs, nil
}

// closeOthers closes the sessions a query tab made on other connections.
func (q *queryTab) closeOthers() {
	q.othersMu.Lock()
	defer q.othersMu.Unlock()
	for id, qs := range q.others {
		_ = qs.Close()
		delete(q.others, id)
	}
}

// executeAcross runs the parts from the one at from, in order, showing each
// statement's result as it arrives. ran is how many statements earlier parts
// ran, so that results are numbered through the script; opts.Confirmed is
// consent for the part at from, and no other.
func (s *Shell) executeAcross(t *tab, parts []part, from, ran int, opts source.ScriptOptions) {
	q := t.query
	if q.run != nil {
		q.run()
	}
	ctx, cancel := context.WithCancel(t.ctx)
	q.run, q.executing = cancel, true
	if from == 0 {
		q.runRev = q.editor.Document().Revision()
		q.named = opts.Named
		s.clearResults(q)
	}
	t.footer.SetText(l10n.T("Running…"))
	s.sync()
	start := time.Now()
	go func() {
		n, failed, k := ran, false, from
		for ; k < len(parts) && !failed && ctx.Err() == nil; k++ {
			p := parts[k]
			o := source.ScriptOptions{Named: opts.Named, Confirmed: opts.Confirmed && k == from}
			var ch <-chan app.StatementResult
			qs, err := s.sessionOn(t, p.connID)
			if err == nil {
				ch, err = qs.Run(ctx, p.Text, o)
			}
			if err != nil {
				at, before := k, n
				s.d.Run(func() {
					q.executing = false
					cancel() // this part did not run, so nothing of it streams
					if t.ctx.Err() == nil {
						s.acrossRefused(t, parts, at, before, opts, err)
						s.sync()
					}
				})
				return
			}
			s.d.Run(func() {
				if t.ctx.Err() == nil {
					s.note(q, l10n.F("On %s:", p.name))
				}
			})
			for r := range ch {
				r.Index = n
				n++
				failed = failed || r.Err != nil
				s.d.Run(func() {
					if t.ctx.Err() == nil {
						s.showResultOn(t, r, p.Offset, p)
					}
				})
			}
		}
		elapsed, stopped, left := time.Since(start), ctx.Err() != nil, len(parts)-k
		s.d.Run(func() {
			q.executing = false
			if q.schema != nil && slices.ContainsFunc(parts, func(p part) bool { return p.connID == t.connID && q.session.Alters(p.Text) }) {
				q.schema.Invalidate()
			}
			if t.ctx.Err() != nil {
				return
			}
			if failed && left > 0 {
				s.note(q, l10n.T("The rest of the script was not run."))
			}
			summary := runSummary(n-ran, elapsed, stopped, failed)
			if said := txSummary(t); said != "" {
				summary += " " + said
			}
			t.footer.SetText(summary)
			s.retitle(t)
			if !stopped && elapsed >= notifyAfter {
				s.notify(t.item.Text, summary)
			}
			s.sync()
		})
	}()
}

// acrossRefused explains a part a connection would not start, by that
// connection's name; and where it is a question, asks it and goes on from
// that part.
func (s *Shell) acrossRefused(t *tab, parts []part, at, ran int, opts source.ScriptOptions, err error) {
	p := parts[at]
	t.footer.SetText("")
	nothing := l10n.T("Nothing has run yet.")
	if at > 0 {
		nothing = l10n.T("What came before it in the script has already run.")
	}
	again := func() {
		opts.Confirmed = true
		s.executeAcross(t, parts, at, ran, opts)
	}
	declined := func() {
		t.footer.SetText(l10n.T("Not run"))
		if at > 0 {
			s.note(t.query, l10n.T("The rest of the script was not run."))
		}
	}
	var everything *source.UnboundedError
	switch {
	case errors.As(err, &everything):
		// Asked here and not by askEverything, which says nothing has run:
		// of a later part that would not be true.
		want, changes := everything.Target, "every row in "+everything.Target
		if want == "" {
			want, changes = everything.Verb, "every row it can reach"
		}
		s.askTyping(want, "Change Every Row?",
			l10n.F("This %s has no WHERE, so it changes %s, on “%s”. %s", everything.Verb, changes, p.name, nothing),
			"Run", again, declined)
	case errors.Is(err, source.ErrConfirmationRequired):
		s.askToType(p.connID, "Change Data on Production?",
			productionBody("part of the script changes data on", p.name, nothing), "Run", again, declined)
	case errors.Is(err, source.ErrReadOnly):
		s.note(t.query, l10n.F("Not run on %s: this part changes data, and that connection is read-only.", p.name))
		declined()
	default:
		s.note(t.query, l10n.F("Could not run on %s: %s", p.name, err.Error()))
		declined()
	}
}
