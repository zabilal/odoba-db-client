package shell

import (
	"context"
	"fmt"
	"time"

	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Explicit transactions, and saying one is open (FR-5.14).
//
// An unnoticed open transaction holds locks: other people's statements wait
// on it, and on PostgreSQL the tables it touched cannot be altered until it
// ends. So the window says so until it does — in the tab's name, where it
// can be seen from another tab, and in the footer, which says what can still
// be done with it.
//
// The state is asked of the connection rather than kept here. A transaction
// a statement failed inside can no longer be committed, and a window
// offering a commit that cannot happen would be worse than one offering
// none.

// txTimeout bounds beginning or ending a transaction. Ending one can wait on
// locks, which is the thing itself rather than a stall.
const txTimeout = 2 * time.Minute

// txOf is the state of the query tab in front, and the tab it belongs to.
func (s *Shell) txOf() (*tab, source.TxState) {
	t, q := s.activeQuery()
	if q == nil || q.session == nil || !q.session.CanTransact() {
		return nil, source.TxNone
	}
	return t, q.session.Transaction()
}

// canBegin reports whether a transaction can be opened here: one that holds
// them, with none open already.
func (s *Shell) canBegin() bool {
	t, state := s.txOf()
	return t != nil && state == source.TxNone
}

// canEndTx reports whether there is a transaction to end.
func (s *Shell) canEndTx() bool {
	t, state := s.txOf()
	return t != nil && state != source.TxNone
}

// canCommit reports whether it can still be committed. A transaction a
// statement failed inside cannot, so the window does not offer to.
func (s *Shell) canCommit() bool {
	t, state := s.txOf()
	return t != nil && state == source.TxOpen
}

func (s *Shell) beginTx() {
	t, _ := s.txOf()
	if t == nil {
		return
	}
	s.runTx(t, "Begin", func(ctx context.Context) error { return t.query.session.Begin(ctx) })
}

func (s *Shell) commitTx() {
	t, _ := s.txOf()
	if t == nil {
		return
	}
	s.runTx(t, "Commit", func(ctx context.Context) error { return t.query.session.Commit(ctx) })
}

func (s *Shell) rollbackTx() {
	t, _ := s.txOf()
	if t == nil {
		return
	}
	s.runTx(t, "Roll Back", func(ctx context.Context) error { return t.query.session.Rollback(ctx) })
}

// runTx does one of the three off the UI goroutine, because ending a
// transaction waits on whatever it was waiting on.
func (s *Shell) runTx(t *tab, what string, fn func(context.Context) error) {
	t.footer.SetText(what + "…")
	go func() {
		ctx, cancel := context.WithTimeout(t.ctx, txTimeout)
		defer cancel()
		err := fn(ctx)
		s.d.Run(func() {
			if t.ctx.Err() != nil {
				return
			}
			if err != nil {
				s.showError(fmt.Errorf("could not %s: %w", lowerFirst(what), err))
			}
			s.sayTransaction(t)
			s.retitle(t)
			s.sync()
		})
	}()
}

// lowerFirst is a command's name said inside a sentence.
func lowerFirst(s string) string {
	switch s {
	case "Begin":
		return "begin a transaction"
	case "Commit":
		return "commit"
	}
	return "roll back"
}

// sayTransaction puts the transaction in the footer, which is where a query
// tab says what is true of it.
func (s *Shell) sayTransaction(t *tab) {
	said := txSummary(t)
	if said == "" {
		said = "No transaction open."
	}
	t.footer.SetText(said)
}

// txSummary is what is true of a tab's transaction, or nothing where none is
// open.
func txSummary(t *tab) string {
	switch txState(t) {
	case source.TxOpen:
		return "Transaction open: nothing it has done is visible to anybody else until it is committed."
	case source.TxFailed:
		return "Transaction failed: a statement in it did not run, " +
			"so it can no longer be committed. Roll it back to carry on."
	}
	return ""
}

// txState is a tab's transaction state, or none where it holds none.
func txState(t *tab) source.TxState {
	if t == nil || t.query == nil || t.query.session == nil || !t.query.session.CanTransact() {
		return source.TxNone
	}
	return t.query.session.Transaction()
}

// txMark is what a tab's name carries while a transaction is open, so that a
// transaction left open in one tab is visible from another.
func txMark(t *tab) string {
	switch txState(t) {
	case source.TxOpen:
		return " ⧗"
	case source.TxFailed:
		return " ⚠"
	}
	return ""
}

// askAboutTransaction asks before closing a tab whose transaction is open,
// because closing it rolls the transaction back and what it did is gone.
//
// It answers true when there is nothing to ask about.
func (s *Shell) askAboutTransaction(t *tab, then func()) bool {
	state := txState(t)
	if state == source.TxNone {
		return true
	}
	what := "Closing this tab rolls it back, and everything it has done is undone."
	if state == source.TxFailed {
		what = "It has already failed, so closing this tab rolls it back."
	}
	d := dialog.NewConfirm("Close and Roll Back?",
		"“"+t.query.title+"” has a transaction open. "+what,
		func(yes bool) {
			if yes {
				then()
			}
		}, s.win)
	d.SetConfirmText("Close")
	d.SetDismissText("Cancel")
	d.SetConfirmImportance(widget.DangerImportance)
	d.Show()
	return false
}
