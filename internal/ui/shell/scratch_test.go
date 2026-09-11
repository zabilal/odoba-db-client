package shell

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
)

func scratches(fx *fixture) []localdb.Scratch {
	got, _ := fx.hist.Scratches(context.Background())
	return got
}

// relaunch starts another window on the same stores, as the app's next start
// would.
func (fx *fixture) relaunch(t *testing.T) *Shell {
	t.Helper()
	s := New(fx.s.app, fx.deps)
	t.Cleanup(s.shutdown)
	return s
}

func TestUnsavedTextSurvivesACrash(t *testing.T) {
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	test.Type(q.editor.Focusable(), "rows 3;")
	pump(t, fx.q, func() bool { l := scratches(fx); return len(l) == 1 && l[0].Body == "rows 3;" })

	s := fx.relaunch(t) // no shutdown first: the app died
	pump(t, fx.q, func() bool { return len(s.open) == 1 && s.open[0].query.session != nil })
	got := s.open[0]
	if got.connID != tb.connID || got.query.editor.Document().Text() != "rows 3;" {
		t.Fatalf("reopened on %q with %q", got.connID, got.query.editor.Document().Text())
	}
	if !got.query.dirty || !strings.HasSuffix(got.item.Text, "•") {
		t.Errorf("tab %q should still show unsaved edits", got.item.Text)
	}
	if got.query.scratchID != q.scratchID {
		t.Error("the reopened tab should keep its text under the same buffer, not a second one")
	}
}

func TestSavingOrClosingForgetsTheText(t *testing.T) {
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	test.Type(q.editor.Focusable(), "rows 1;")
	pump(t, fx.q, func() bool { return len(scratches(fx)) == 1 })

	p := fx.s.promptSave(tb, false)
	p.name.SetText("Kept")
	p.dlg.Submit()
	pump(t, fx.q, func() bool { return q.saved.ID != "" && len(scratches(fx)) == 0 })

	test.Type(q.editor.Focusable(), " ")
	pump(t, fx.q, func() bool {
		l := scratches(fx)
		return len(l) == 1 && l[0].Body == "rows 1; " && l[0].SavedID == q.saved.ID
	})
	fx.s.closeTab(tb.item) // on purpose: the user confirmed or had nothing to lose
	pump(t, fx.q, func() bool { return len(scratches(fx)) == 0 })
}

func TestQuittingKeepsUnsavedText(t *testing.T) {
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	fx.s.autosave = time.Hour // so that only quitting writes it
	test.Type(q.editor.Focusable(), "rows 2;")
	fx.s.shutdown()
	if l := scratches(fx); len(l) != 1 || l[0].Body != "rows 2;" || l[0].ConnectionID != tb.connID {
		t.Errorf("after quitting: %+v", l)
	}
}

func TestDisconnectingKeepsTheText(t *testing.T) {
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	fx.s.autosave = time.Hour // so that only disconnecting writes it
	test.Type(q.editor.Focusable(), "rows 4;")
	fx.s.disconnect(tb.connID)
	pump(t, fx.q, func() bool { l := scratches(fx); return len(l) == 1 && l[0].Body == "rows 4;" })
}

func TestDeletingTheConnectionForgetsTheText(t *testing.T) {
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	test.Type(q.editor.Focusable(), "rows 5;")
	pump(t, fx.q, func() bool { return len(scratches(fx)) == 1 })
	fx.s.deleteConnection(tb.connID) // its dialog says the tabs close
	pump(t, fx.q, func() bool { return len(scratches(fx)) == 0 })
}

func TestTextWhoseConnectionIsGoneReopensWithoutConnecting(t *testing.T) {
	fx := newFixture(t)
	fx.hist.PutScratch(context.Background(),
		localdb.Scratch{ID: "orphan", ConnectionID: "gone", Body: "select 1;", Opened: time.Now()})
	s := fx.relaunch(t)
	pump(t, fx.q, func() bool { return len(s.open) == 1 })
	got := s.open[0]
	q := got.query
	if q.editor.Document().Text() != "select 1;" || !q.dirty {
		t.Fatalf("reopened %q, dirty %v", q.editor.Document().Text(), q.dirty)
	}
	time.Sleep(20 * time.Millisecond)
	fx.q.Flush()
	if !strings.Contains(q.messages.Text, "no longer exists") || strings.Contains(q.messages.Text, "Could not connect") {
		t.Errorf("messages %q", q.messages.Text)
	}
	if l := scratches(fx); len(l) != 1 {
		t.Errorf("the text should stay kept until its tab is closed: %+v", l)
	}
	s.closeTab(got.item)
	pump(t, fx.q, func() bool { return len(scratches(fx)) == 0 })
}

func TestReopenedEditsOfASavedQueryStillSaveInPlace(t *testing.T) {
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	test.Type(q.editor.Focusable(), "rows 1;")
	p := fx.s.promptSave(tb, false)
	p.name.SetText("Revenue")
	p.dlg.Submit()
	pump(t, fx.q, func() bool { return q.saved.ID != "" })
	id := q.saved.ID
	test.Type(q.editor.Focusable(), " ")
	pump(t, fx.q, func() bool { l := scratches(fx); return len(l) == 1 && l[0].SavedID == id })

	s := fx.relaunch(t)
	pump(t, fx.q, func() bool { return len(s.open) == 1 && s.open[0].query.session != nil })
	r := s.open[0]
	if r.item.Text != "Revenue •" || r.query.saved.ID != id {
		t.Fatalf("reopened as %q editing %q", r.item.Text, r.query.saved.ID)
	}
	s.tabs.Select(r.item)
	s.run(cmdQuerySave)
	pump(t, fx.q, func() bool {
		l := savedList(fx)
		return len(l) == 1 && l[0].ID == id && l[0].Body == "rows 1; " && len(scratches(fx)) == 0
	})
}

type failingScratch struct{}

func (failingScratch) PutScratch(context.Context, localdb.Scratch) error {
	return errors.New("disk full")
}
func (failingScratch) DeleteScratch(context.Context, string) error { return nil }
func (failingScratch) Scratches(context.Context) ([]localdb.Scratch, error) {
	return nil, nil
}

func TestAutosaveThatFailsSaysSo(t *testing.T) {
	fx := newFixture(t)
	fx.s.d.Scratch = failingScratch{}
	_, q := openQuery(t, fx, "")
	test.Type(q.editor.Focusable(), "rows 1;")
	pump(t, fx.q, func() bool { return strings.Contains(fx.s.errors.text, "disk full") })
	if !strings.Contains(fx.s.errors.text, "could not be kept safe") {
		t.Errorf("error band says %q", fx.s.errors.text)
	}
}
