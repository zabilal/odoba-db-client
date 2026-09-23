package shell

import (
	"fmt"

	"github.com/ikigai-db/ikigai-db/internal/sqlfmt"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Laying a statement out (FR-5.12).
//
// What is laid out is what is selected, or else the whole script: somebody
// who highlighted three lines meant those three, and somebody who
// highlighted nothing meant the file.
//
// The caret goes back to the same place in the text, counted in characters
// from the start of what was laid out. It is not the same place on the same
// line — the lines have moved — but it is near what was being looked at,
// which is what matters when the whole point was to see the statement
// better.

// canFormat reports whether there is a statement in front to lay out.
func (s *Shell) canFormat() bool {
	_, q := s.activeQuery()
	return q != nil && q.editor.Document().Text() != ""
}

// formatQuery lays out the query in front.
func (s *Shell) formatQuery() {
	t, q := s.activeQuery()
	if q == nil {
		return
	}
	doc := q.editor.Document()
	d := sqllex.DialectFor(dialectName(s, t.connID))

	from, to, sel := doc.Selection()
	text := doc.Text()
	if sel {
		text = doc.SelectedText()
	}
	out, err := sqlfmt.Format(d, text, sqlfmt.Default())
	if out == text {
		// Nothing moved, so nothing is undone: a formatter that made an
		// undo step out of no change would cost somebody an undo.
		s.saidFormat(t, err, 0)
		return
	}

	if sel {
		doc.SetCaret(from, false)
		doc.SetCaret(to, true)
		doc.Insert(out)
	} else {
		at := doc.Offset(doc.Caret())
		doc.SetText(out)
		doc.SetCaret(doc.PosAt(min(at, len(out))), false)
	}
	q.editor.Refresh()
	s.saidFormat(t, err, 1)
}

// saidFormat says what happened, which is only ever interesting when
// something was left as it was.
func (s *Shell) saidFormat(t *tab, err error, changed int) {
	switch {
	case err != nil:
		t.footer.SetText(fmt.Sprintf("Laid out, except: %v", err))
	case changed == 0:
		t.footer.SetText("Already laid out.")
	default:
		t.footer.SetText("Laid out.")
	}
}
