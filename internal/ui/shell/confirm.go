package shell

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// A write on a production connection is confirmed by typing, not by clicking
// (FR-4.9).
//
// A dialog with a Yes button is answered by reflex, and by the time somebody
// is running statements they have answered a great many. What has to be typed
// is the connection's name, because the mistake this guards against is almost
// never the wrong statement — it is the right statement on the wrong
// connection, and the name is the only thing that tells one production
// database from another.
//
// Nothing has happened when this is asked. Every caller reaches here because
// a driver refused before it did anything, so asking and then running it
// confirmed is safe.

// unnamed is what has to be typed when the connection cannot be found — one
// deleted while an operation was in flight. The store refuses to save a
// connection with no name, so that is the way this arises. Without it, the
// confirmation would be given by typing nothing at all.
const unnamed = "production"

// askToType asks for a typed confirmation and runs the operation if it is
// given. declined may be nil where there is nothing to say about a no.
func (s *Shell) askToType(connID, title, body, confirmText string, run func(), declined func()) {
	s.askTyping(s.connName(connID), title, body, confirmText, run, declined)
}

// askEverything asks about a statement that changes every row it can reach,
// whatever connection it runs on (FR-4.9).
//
// What has to be typed here is the table, not the connection: on a
// development database the connection's name is not the thing worth reading
// twice, and on any database the table is. Where the statement will not say
// plainly which table it changes, the verb is typed instead — that at least
// cannot be done without reading the word DELETE.
func (s *Shell) askEverything(u *source.UnboundedError, run func(), declined func()) {
	want, changes := u.Target, "every row in "+u.Target
	if want == "" {
		want, changes = u.Verb, "every row it can reach"
	}
	s.askTyping(want, "Change Every Row?",
		fmt.Sprintf("This %s has no WHERE, so it changes %s. Nothing has run yet.", u.Verb, changes),
		"Run", run, declined)
}

// askTyping is the dialog both of those put up.
func (s *Shell) askTyping(want, title, body, confirmText string, run func(), declined func()) {
	note := widget.NewLabel(body)
	note.Wrapping = fyne.TextWrapWord

	typed := widget.NewEntry()
	typed.PlaceHolder = want
	typed.Validator = func(text string) error {
		if strings.TrimSpace(text) != want {
			// The button stays disabled while this returns an error, which
			// is what makes the confirmation typed rather than clicked.
			return fmt.Errorf("type %s exactly", want)
		}
		return nil
	}

	d := dialog.NewForm(title, confirmText, "Cancel", []*widget.FormItem{
		{Widget: note},
		{Text: "Type " + want + " to continue", Widget: typed},
	}, func(ok bool) {
		if ok {
			run()
			return
		}
		if declined != nil {
			declined()
		}
	}, s.win)
	d.Resize(fyne.NewSize(480, d.MinSize().Height))
	d.Show()
}

// productionBody is the sentence every one of these asks with: what will
// change, which connection, and that nothing has happened yet.
func productionBody(what, name, nothing string) string {
	return fmt.Sprintf("This %s “%s”, which is marked Production. %s", what, name, nothing)
}

// connName is the name to put in that sentence.
func (s *Shell) connName(connID string) string {
	c, ok := s.d.Conns.Get(connID)
	if !ok || strings.TrimSpace(c.Name) == "" {
		return unnamed
	}
	return strings.TrimSpace(c.Name)
}
