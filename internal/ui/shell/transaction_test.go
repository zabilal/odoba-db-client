package shell

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2/container"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Explicit transactions, and saying one is open (FR-5.14).

func txTab(t *testing.T, host string) (*fixture, *tab, *queryTab) {
	t.Helper()
	fx := newFixture(t)
	tb, q := openQueryOn(t, fx, "", host)
	fx.s.sync()
	return fx, tb, q
}

// afterTx waits for the window to have caught up with the transaction,
// which happens on the UI goroutine after the server answered.
func afterTx(t *testing.T, fx *fixture, tb *tab, said string) {
	t.Helper()
	pump(t, fx.q, func() bool { return strings.Contains(tb.footer.Text, said) })
}

func runCmd(t *testing.T, fx *fixture, id string) {
	t.Helper()
	c, ok := fx.s.reg.Get(id)
	if !ok {
		t.Fatalf("no command %s", id)
	}
	if c.Enabled != nil && !c.Enabled() {
		t.Fatalf("%s is disabled", id)
	}
	c.Run()
}

// A transaction opens, and the window says so until it ends.
func TestATransactionIsOpenedAndSaidSo(t *testing.T) {
	fx, tb, q := txTab(t, "db1")
	if !fx.s.canBegin() {
		t.Fatal("a connection that holds transactions does not offer to begin one")
	}
	runCmd(t, fx, cmdTxBegin)
	afterTx(t, fx, tb, "Transaction open")
	if q.session.Transaction() != source.TxOpen {
		t.Fatalf("the transaction is %v", q.session.Transaction())
	}

	if !strings.Contains(tb.footer.Text, "Transaction open") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
	if !strings.Contains(tb.item.Text, "⧗") {
		t.Errorf("the tab is called %q; a transaction open in another tab is the one worth marking", tb.item.Text)
	}
	if fx.s.canBegin() {
		t.Error("a second transaction was offered on top of the first")
	}
	if !fx.s.canCommit() || !fx.s.canEndTx() {
		t.Error("an open transaction can be committed and rolled back")
	}
}

// Committing ends it, and so does rolling back.
func TestATransactionEndsOneWayOrTheOther(t *testing.T) {
	for _, tc := range []struct{ name, cmd string }{
		{"commit", cmdTxCommit}, {"rollback", cmdTxRollback},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx, tb, q := txTab(t, "db1")
			runCmd(t, fx, cmdTxBegin)
			afterTx(t, fx, tb, "Transaction open")
			if !strings.Contains(tb.item.Text, "⧗") {
				t.Fatalf("the tab is called %q while a transaction is open", tb.item.Text)
			}
			runCmd(t, fx, tc.cmd)
			afterTx(t, fx, tb, "No transaction open")
			if q.session.Transaction() != source.TxNone {
				t.Fatalf("the transaction is %v", q.session.Transaction())
			}

			if strings.Contains(tb.item.Text, "⧗") {
				t.Errorf("the tab is still called %q", tb.item.Text)
			}
			if !strings.Contains(tb.footer.Text, "No transaction open") {
				t.Errorf("the footer says %q", tb.footer.Text)
			}
			if !fx.s.canBegin() {
				t.Error("another transaction cannot be opened once this one ended")
			}
		})
	}
}

// A transaction a statement failed inside can no longer be committed, so the
// window does not offer to — and says why.
func TestAFailedTransactionOffersOnlyARollback(t *testing.T) {
	fx, tb, q := txTab(t, "txpoisons")
	runCmd(t, fx, cmdTxBegin)
	afterTx(t, fx, tb, "Transaction open")

	q.editor.Document().SetText("oops")
	fx.s.runIn(tb, false)
	afterTx(t, fx, tb, "can no longer be committed")
	if q.session.Transaction() != source.TxFailed {
		t.Fatalf("the transaction is %v", q.session.Transaction())
	}

	if fx.s.canCommit() {
		t.Error("a failed transaction was offered a commit it cannot do")
	}
	if !fx.s.canEndTx() {
		t.Error("a failed transaction cannot be rolled back")
	}
	if !strings.Contains(tb.footer.Text, "can no longer be committed") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
	if !strings.Contains(tb.item.Text, "⚠") {
		t.Errorf("the tab is called %q", tb.item.Text)
	}
	runCmd(t, fx, cmdTxRollback)
	afterTx(t, fx, tb, "No transaction open")
	if !fx.s.canBegin() {
		t.Error("rolling back a failed transaction did not end it")
	}
}

// A run says what is true of the transaction it ran in, because what ran is
// not committed.
func TestARunInsideATransactionSaysSo(t *testing.T) {
	fx, tb, q := txTab(t, "db1")
	runCmd(t, fx, cmdTxBegin)
	afterTx(t, fx, tb, "Transaction open")

	q.editor.Document().SetText("items")
	fx.s.runIn(tb, false)
	pump(t, fx.q, func() bool { return strings.Contains(tb.footer.Text, "Transaction open") })
	if !strings.Contains(tb.footer.Text, "1 statement") {
		t.Errorf("the footer says %q; what ran is still worth saying", tb.footer.Text)
	}
}

// A connection that holds no transactions offers none.
func TestAConnectionThatHoldsNoTransactions(t *testing.T) {
	fx, _, _ := txTab(t, "notx")
	if fx.s.canBegin() || fx.s.canCommit() || fx.s.canEndTx() {
		t.Error("a connection that holds no transactions offered one")
	}
	if !fx.s.menuItems[cmdTxBegin].Disabled {
		t.Error("Begin Transaction is offered on a connection that holds none")
	}
}

// Nothing is offered with no query tab in front.
func TestTransactionsNeedAQueryTab(t *testing.T) {
	fx := newFixture(t)
	if fx.s.canBegin() || fx.s.canEndTx() {
		t.Error("a transaction was offered with nothing open")
	}
	if !fx.s.menuItems[cmdTxBegin].Disabled {
		t.Error("Begin Transaction should be disabled with nothing open")
	}
}

// Closing a tab with a transaction open asks first, because closing rolls it
// back and everything it did is undone.
func TestClosingATabWithATransactionOpenAsksFirst(t *testing.T) {
	fx, tb, q := txTab(t, "db1")
	runCmd(t, fx, cmdTxBegin)
	afterTx(t, fx, tb, "Transaction open")

	fx.s.requestClose(tb.item)
	if len(fx.s.open) != 1 {
		t.Fatalf("%d tabs open; the question was not asked", len(fx.s.open))
	}
	// Answering no leaves the tab, and its transaction, where they were.
	cancel := findButton(fx.s.win.Canvas().Overlays().Top(), "Cancel")
	if cancel == nil {
		t.Fatal("the question has no way to say no")
	}
	cancel.Tapped(nil)
	if len(fx.s.open) != 1 {
		t.Errorf("%d tabs open after saying no", len(fx.s.open))
	}
	if q.session.Transaction() != source.TxOpen {
		t.Errorf("the transaction is %v after saying no", q.session.Transaction())
	}

	// Answering yes closes it.
	fx.s.requestClose(tb.item)
	close := findButton(fx.s.win.Canvas().Overlays().Top(), "Close")
	if close == nil {
		t.Fatal("the question has no way to say yes")
	}
	close.Tapped(nil)
	if len(fx.s.open) != 0 {
		t.Errorf("%d tabs open after saying yes", len(fx.s.open))
	}
}

// And a tab with none closes without a word.
func TestClosingATabWithNoTransactionDoesNotAsk(t *testing.T) {
	fx, tb, _ := txTab(t, "db1")
	fx.s.requestClose(tb.item)
	if len(fx.s.open) != 0 {
		t.Errorf("%d tabs open; a tab with nothing open closes", len(fx.s.open))
	}
}

// A transaction that will not begin says so and leaves nothing open.
func TestATransactionThatWillNotBegin(t *testing.T) {
	fx, tb, q := txTab(t, "db1")
	txFailsToBegin.Store(true)
	t.Cleanup(func() { txFailsToBegin.Store(false) })

	runCmd(t, fx, cmdTxBegin)
	pump(t, fx.q, func() bool { return fx.s.errors.text != "" })
	if !strings.Contains(fx.s.errors.text, "could not begin a transaction") {
		t.Errorf("it said %q", fx.s.errors.text)
	}
	if q.session.Transaction() != source.TxNone {
		t.Error("a transaction that would not begin is open")
	}
	if strings.Contains(tb.item.Text, "⧗") {
		t.Errorf("the tab is called %q", tb.item.Text)
	}
}

// confirmShown reports whether a confirmation is on the window.
func confirmShown(t *testing.T, fx *fixture) bool {
	t.Helper()
	for _, o := range fx.s.win.Canvas().Overlays().List() {
		if _, ok := o.(*container.Scroll); ok {
			continue
		}
		return true
	}
	return false
}
