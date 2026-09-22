package shell

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// Moving a consumer group's offsets (T2.80, FR-13.13).

func TestWhereAGroupIsMovedToIsReadFromWhatWasChosen(t *testing.T) {
	for _, c := range []struct {
		where, at, offset string
		mode              source.SeekMode
	}{
		{toBeginning, "", "", source.SeekBeginning},
		{toEnd, "", "", source.SeekEnd},
		{toOffset, "", "42", source.SeekOffset},
		{toTime, "2026-09-22T10:30:00Z", "", source.SeekTimestamp},
	} {
		seek, err := resetFrom(c.where, c.at, c.offset)
		if err != nil {
			t.Errorf("%q: %v", c.where, err)
			continue
		}
		if seek.Mode != c.mode {
			t.Errorf("%q reads as mode %d, not %d", c.where, seek.Mode, c.mode)
		}
	}
	if seek, _ := resetFrom(toOffset, "", "42"); seek.Offset != 42 {
		t.Errorf("an offset of 42 reads as %d", seek.Offset)
	}
	// Nothing chosen is not a move: this is too destructive to guess at.
	if _, err := resetFrom("", "", ""); err == nil {
		t.Error("a move with nowhere to move to was accepted")
	}
	for _, bad := range []string{"", "-1", "soon", "1.5"} {
		if _, err := resetFrom(toOffset, "", bad); err == nil {
			t.Errorf("an offset written as %q was accepted", bad)
		}
	}
	for _, bad := range []string{"", "yesterday", "22/09/2026"} {
		if _, err := resetFrom(toTime, bad, ""); err == nil {
			t.Errorf("a time written as %q was accepted", bad)
		}
	}
}

func TestATimeIsReadWithItsZoneOrInThisMachinesOwn(t *testing.T) {
	// With a zone, the zone is what it means.
	when, err := timeFrom("2026-09-22T10:30:00Z")
	if err != nil || !when.Equal(time.Date(2026, 9, 22, 10, 30, 0, 0, time.UTC)) {
		t.Errorf("a time with a zone reads as %v: %v", when, err)
	}
	// Without one it means this machine's own, rather than silently UTC:
	// moving a group by half a day because nobody said which noon was meant
	// is exactly the sort of incident this is guarded against.
	when, err = timeFrom("2026-09-22 10:30")
	if err != nil {
		t.Fatalf("a time with no zone: %v", err)
	}
	if want := time.Date(2026, 9, 22, 10, 30, 0, 0, time.Local); !when.Equal(want) {
		t.Errorf("a time with no zone reads as %v, not %v", when, want)
	}
	// A date on its own is its midnight, which is a position worth naming.
	if when, err = timeFrom(" 2026-09-22 "); err != nil || when.Hour() != 0 || when.Day() != 22 {
		t.Errorf("a date alone reads as %v: %v", when, err)
	}
}

func TestWhatWillHappenIsSaidBeforeItDoes(t *testing.T) {
	// The confirmation reads the position back, because a time somebody
	// typed and a time this understood are not always the same time.
	when := time.Date(2026, 9, 22, 10, 30, 0, 0, time.UTC)
	for _, c := range []struct {
		seek source.Seek
		says string
	}{
		{source.Seek{Mode: source.SeekBeginning}, "the beginning of each log"},
		{source.Seek{Mode: source.SeekEnd}, "the end of each log"},
		{source.Seek{Mode: source.SeekOffset, Offset: 42}, "offset 42"},
		{source.Seek{Mode: source.SeekTimestamp, Time: when}, "2026-09-22T10:30:00Z"},
	} {
		if got := movedTo(c.seek); !strings.Contains(got, c.says) {
			t.Errorf("%+v reads as %q, which does not say %q", c.seek, got, c.says)
		}
	}
}

func TestAGroupsMenuOffersMovingItAndNotWhatBelongsToTopics(t *testing.T) {
	fx := newFixture(t)
	c, err := fx.conns.Create(store.SavedConnection{Name: "kafka1", Driver: "recordfake", Host: "kafka1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fx.s.Explorer.Refresh(explorer.RootID)
	loaded(t, fx, explorer.RootID)
	loaded(t, fx, view.ConnectionID(c.ID))
	loaded(t, fx, view.NodeID(c.ID, model.NewRef(model.KindCluster, "cluster")))

	got := labels(fx.s.explorerMenu(view.NodeID(c.ID, groupRef)))
	if !slices.Contains(got, titleOf(fx, cmdGroupReset)) {
		t.Errorf("a group's menu does not offer to move it: %q", got)
	}
	// A group holds no records and is not a topic: none of what belongs to
	// one belongs here.
	for _, id := range []string{cmdProduce, cmdTopicDelete, cmdTopicPartition, cmdTopicConfig} {
		if slices.Contains(got, titleOf(fx, id)) {
			t.Errorf("a group's menu offers %q: %q", titleOf(fx, id), got)
		}
	}
	// And a topic's menu does not offer moving offsets.
	tm := labels(fx.s.explorerMenu(view.NodeID(c.ID, topicRef)))
	if slices.Contains(tm, titleOf(fx, cmdGroupReset)) {
		t.Errorf("a topic's menu offers to move offsets: %q", tm)
	}
}

func TestMovingAGroupOnProductionAsksAndMovesNothingUntilAnswered(t *testing.T) {
	fx, tb := openRecords(t)
	live, ok := fx.s.d.WS.Get(tb.connID)
	if !ok {
		t.Fatal("the connection is not open")
	}
	src := live.Source.(*recordSource)
	src.refuse = fmt.Errorf("%w: moving offsets on a production connection", source.ErrConfirmationRequired)

	moving := func(s source.Source, confirmed bool) error {
		return app.ResetOffsets(fx.s.ctx, s, source.ResetRequest{
			GroupID: "readers", Seek: source.Seek{Mode: source.SeekBeginning}, Confirmed: confirmed})
	}
	fx.s.changeCluster(tb.connID, "move readers to the beginning of each log", moving)
	pump(t, fx.q, func() bool { return fx.s.win.Canvas().Overlays().Top() != nil })

	// What it asks about says what would happen, not merely that something
	// would.
	if text := labelText(fx.s.win.Canvas().Overlays().Top()); !strings.Contains(text, "marked Production") ||
		!strings.Contains(text, "move readers to the beginning of each log") {
		t.Errorf("moving a group on production asks: %q", text)
	}
	if len(src.changed) != 0 {
		t.Fatalf("a group moved before the question was answered: %v", src.changed)
	}

	tapOnTop(t, fx, "Cancel")
	if len(src.changed) != 0 {
		t.Errorf("saying no moved %v", src.changed)
	}

	fx.s.changeCluster(tb.connID, "move readers to the beginning of each log", moving)
	pump(t, fx.q, func() bool { return fx.s.win.Canvas().Overlays().Top() != nil })
	tapOnTop(t, fx, "Continue")
	pump(t, fx.q, func() bool { return len(src.changed) == 1 })
	if src.changed[0] != "move readers" {
		t.Errorf("what happened was %v", src.changed)
	}
}
