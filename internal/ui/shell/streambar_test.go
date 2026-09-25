package shell

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/store"
)

// Following a log, and starting somewhere in it (FR-13.5, FR-13.6).
//
// Both were written and tested long ago with nothing to reach them: the driver
// finds a position, app.Tail holds a following read, and the window opened every
// object with an empty BrowseOptions. These are the two controls, so the tests
// are about what the window asks for as much as about what it draws.

// followed opens the fake topic and answers the tab, its bar, and the source
// behind it.
func followedTopic(t *testing.T) (*fixture, *tab, *streamBar, *recordSource) {
	t.Helper()
	fx, tb := openRecords(t)
	fx.s.selectTab(tb)
	fx.s.sync()
	bar := fx.s.streamControls(tb)
	return fx, tb, bar, recordSourceOf(t, fx, tb.connID)
}

// A log gets its controls above its records, and a table does not: there is
// nothing to follow in a table, and a control that did nothing would be worse
// than none (REQ-DB-2).
func TestALogGetsItsControlsAndATableDoesNot(t *testing.T) {
	fx, tb, bar, _ := followedTopic(t)
	if !fx.s.canFollow() || !fx.s.canSeek() {
		t.Error("a log cannot be followed or started at")
	}
	shown := func() int {
		n := 0
		for _, o := range tb.top.Objects {
			if o == bar.box {
				n++
			}
		}
		return n
	}
	if shown() != 1 {
		t.Errorf("the controls are above the records %d times", shown())
	}
	// Asked for again — which happens whenever the rows are read again — they
	// are still there once.
	fx.s.showStreamBar(tb)
	fx.s.showStreamBar(tb)
	if shown() != 1 {
		t.Errorf("after asking three times the controls are there %d times", shown())
	}
	if fx.s.menuItems[cmdFollow].Disabled || fx.s.menuItems[cmdSeek].Disabled {
		t.Error("the menu items are disabled where the commands are offered")
	}
	// Pausing is offered only while something is being followed.
	if !fx.s.menuItems[cmdPause].Disabled {
		t.Error("pausing is offered before anything is following")
	}

	// A table, which has no log in it.
	c := fx.create(t, "db1", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	rows := fx.s.open[len(fx.s.open)-1]
	pump(t, fx.q, func() bool { return rows.browse != nil })
	fx.s.sync()
	if fx.s.canFollow() || fx.s.canSeek() {
		t.Error("a table was offered a log's controls")
	}
	if !fx.s.menuItems[cmdFollow].Disabled || !fx.s.menuItems[cmdSeek].Disabled {
		t.Error("the menu items are enabled on a table")
	}
}

// Following draws the records as they arrive, and says how many are held.
func TestFollowingDrawsRecordsAsTheyArrive(t *testing.T) {
	fx, tb, bar, src := followedTopic(t)
	bar.toggleFollow()
	if !bar.following() {
		t.Fatal("it is not following")
	}
	if bar.follow.Text != "Stop" {
		t.Errorf("the button says %q", bar.follow.Text)
	}
	// A tail begins where the log is now: following is about what happens next,
	// and replaying a week of records to reach the present is a different ask.
	asks := src.asks()
	last := asks[len(asks)-1]
	if !last.Follow {
		t.Error("the read it asked for does not follow")
	}
	if last.Seek == nil || last.Seek.Mode != source.SeekEnd {
		t.Errorf("it asked to start at %+v", last.Seek)
	}

	src.live <- model.Row{int64(1), int64(0), time.Now(), []byte("k"), []byte("one"), nil}
	src.live <- model.Row{int64(2), int64(0), time.Now(), []byte("k"), []byte("two"), nil}
	pump(t, fx.q, func() bool {
		n, _ := bar.tail.Count(tb.ctx)
		return n == 2
	})
	// Drawn, which is the grid reading the tail rather than the log.
	bar.redraw()
	if rows, err := tb.model.Read(tb.ctx, 0, 10); err != nil || len(rows) != 2 {
		t.Errorf("the grid holds %d rows: %v", len(rows), err)
	}
	if said := tb.footer.Text; !strings.Contains(said, "following") ||
		!strings.Contains(said, "2 records held") {
		t.Errorf("the footer says %q", said)
	}
}

// Pausing holds the records where they are, and says so. Backpressure rather
// than loss: a paused tail asks the source for nothing.
func TestPausingHoldsTheRecordsWhereTheyAre(t *testing.T) {
	fx, tb, bar, src := followedTopic(t)
	bar.toggleFollow()
	src.live <- model.Row{int64(1), int64(0), time.Now(), nil, []byte("one"), nil}
	pump(t, fx.q, func() bool { n, _ := bar.tail.Count(tb.ctx); return n == 1 })

	bar.togglePause()
	if !bar.tail.Paused() {
		t.Fatal("it is not paused")
	}
	if bar.pause.Text != "Resume" {
		t.Errorf("the button says %q", bar.pause.Text)
	}
	if !strings.Contains(tb.footer.Text, "paused") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
	// Three more are written while it is paused. It takes effect at the next
	// record rather than inside a read, so the one already being waited for is
	// kept — that is the one thing pausing must not discard (ADR-0097) — and
	// then it stops taking them. What must not happen is all three arriving,
	// and nothing may be dropped: records wait where they are.
	held, _ := bar.tail.Count(tb.ctx)
	for i := 2; i <= 4; i++ {
		src.live <- model.Row{int64(i), int64(0), time.Now(), nil, []byte("more"), nil}
	}
	time.Sleep(100 * time.Millisecond)
	now, _ := bar.tail.Count(tb.ctx)
	if now > held+1 {
		t.Errorf("a paused tail took %d records", now-held)
	}
	if bar.tail.Dropped() != 0 {
		t.Errorf("a paused tail dropped %d", bar.tail.Dropped())
	}

	bar.togglePause()
	pump(t, fx.q, func() bool { n, _ := bar.tail.Count(tb.ctx); return n == 4 })
	if bar.pause.Text != "Pause" {
		t.Errorf("the button says %q", bar.pause.Text)
	}
}

// Stopping puts the log back as it was read: the grid shows the records the
// browse answered, not the ones the tail held.
func TestStoppingPutsTheLogBack(t *testing.T) {
	fx, tb, bar, src := followedTopic(t)
	before, err := tb.model.Read(tb.ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	bar.toggleFollow()
	src.live <- model.Row{int64(9), int64(0), time.Now(), nil, []byte("live"), nil}
	pump(t, fx.q, func() bool { n, _ := bar.tail.Count(tb.ctx); return n == 1 })

	bar.toggleFollow()
	if bar.following() {
		t.Fatal("it is still following")
	}
	if bar.follow.Text != "Follow" || !bar.pause.Disabled() {
		t.Errorf("the buttons say %q and %v", bar.follow.Text, bar.pause.Disabled())
	}
	after, err := tb.model.Read(tb.ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Errorf("the log holds %d records where it held %d", len(after), len(before))
	}
	// And the following read is let go of: a tail left running would go on
	// consuming a log nobody is watching.
	if src.closedFollows() != 1 {
		t.Errorf("it let go of %d following reads", src.closedFollows())
	}
}

// Where to start is read as it was typed, and what cannot be read is refused
// rather than turned into a position nobody asked for.
func TestWhereToStartIsRead(t *testing.T) {
	for name, c := range map[string]struct {
		mode, count, offset, when string
		want                      source.Seek
		says                      string
	}{
		"the beginning": {mode: "The beginning", want: source.Seek{Mode: source.SeekBeginning}},
		"the last few":  {mode: "The last few", count: "50", want: source.Seek{Mode: source.SeekLast, Count: 50}},
		"an offset":     {mode: "An offset", offset: "1234", want: source.Seek{Mode: source.SeekOffset, Offset: 1234}},
		"offset zero":   {mode: "An offset", offset: "0", want: source.Seek{Mode: source.SeekOffset}},
		"a time": {mode: "A time", when: "2026-09-25 14:30:00",
			want: source.Seek{Mode: source.SeekTimestamp,
				Time: time.Date(2026, 9, 25, 14, 30, 0, 0, time.UTC)}},
		"a date alone": {mode: "A time", when: "2026-09-25",
			want: source.Seek{Mode: source.SeekTimestamp,
				Time: time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)}},
		"no count":           {mode: "The last few", count: "", says: "How many"},
		"none at all":        {mode: "The last few", count: "0", says: "above nothing"},
		"a count of sorts":   {mode: "The last few", count: "some", says: "How many"},
		"a negative offset":  {mode: "An offset", offset: "-1", says: "Which offset"},
		"an offset of sorts": {mode: "An offset", offset: "near the end", says: "Which offset"},
		"a time of sorts":    {mode: "A time", when: "yesterday", says: "not a time"},
		"a mode nobody has":  {mode: "Whenever", says: "where to start"},
	} {
		t.Run(name, func(t *testing.T) {
			seek, said, err := readSeek(c.mode, c.count, c.offset, c.when)
			if c.says != "" {
				if err == nil {
					t.Fatalf("it read %+v", seek)
				}
				if !strings.Contains(err.Error(), c.says) {
					t.Errorf("it says %q, which does not mention %q", err, c.says)
				}
				return
			}
			if err != nil {
				t.Fatalf("it said %v", err)
			}
			if seek.Mode != c.want.Mode || seek.Count != c.want.Count ||
				seek.Offset != c.want.Offset || !seek.Time.Equal(c.want.Time) {
				t.Errorf("it read %+v, want %+v", seek, c.want)
			}
			if strings.TrimSpace(said) == "" {
				t.Error("it says nothing about where it will start")
			}
		})
	}
}

// Starting somewhere reads the log again from there, and says where from: a
// window showing the middle of a log that looked like the beginning would be a
// window nobody could trust.
func TestStartingSomewhereReadsFromThere(t *testing.T) {
	fx, tb, bar, src := followedTopic(t)
	bar.startAt(&source.Seek{Mode: source.SeekLast, Count: 5}, "the last 5 of each log")
	pump(t, fx.q, func() bool {
		asks := src.asks()
		return asks[len(asks)-1].Seek != nil
	})
	asks := src.asks()
	last := asks[len(asks)-1]
	if last.Seek == nil || last.Seek.Mode != source.SeekLast || last.Seek.Count != 5 {
		t.Fatalf("it asked for %+v", last.Seek)
	}
	if last.Follow {
		t.Error("it asked to follow, and was only asked where to start")
	}
	// And the footer goes on saying where it is reading from once the rows have
	// arrived: a window showing the middle of a log that looked like the whole
	// of it would be a window nobody could trust.
	//
	// Asserted against the count line rather than against any mention of the
	// position, because "Reading from the last 5…" is what it says while it is
	// reading — a test that took that for the answer would pass on the message
	// that goes away.
	pump(t, fx.q, func() bool {
		said := tb.footer.Text
		return strings.Contains(said, "· from the last 5") &&
			!strings.HasPrefix(said, "Reading")
	})
}

// Starting somewhere while following ends the following first: a tail begins
// where the log is now, so asking it to begin elsewhere is asking for a read.
func TestStartingSomewhereWhileFollowingStopsFollowing(t *testing.T) {
	_, _, bar, _ := followedTopic(t)
	bar.toggleFollow()
	if !bar.following() {
		t.Fatal("it is not following")
	}
	bar.startAt(&source.Seek{Mode: source.SeekBeginning}, "the beginning")
	if bar.following() {
		t.Error("it is still following")
	}
}

// A position by time is not offered where the source cannot find one: a choice
// that fails when it is taken is worse than one that is not there.
func TestATimeIsNotOfferedWhereItCannotBeAnswered(t *testing.T) {
	fx := newFixture(t)
	// The fake reads its own host: a connection whose host says notime cannot
	// find a position by time, which is how one driver stands for two.
	c, err := fx.conns.Create(store.SavedConnection{Name: "kafka2", Driver: "recordfake",
		Host: "notime"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fx.s.OpenObject(c.ID, model.Node{Ref: topicRef, Label: "events", Browsable: true})
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return tb.browse != nil })
	fx.s.selectTab(tb)
	fx.s.sync()

	bar := fx.s.streamControls(tb)
	bar.askSeek()
	top := fx.s.win.Canvas().Overlays().Top()
	if top == nil {
		t.Fatal("it asked nothing")
	}
	picks := selectsIn(top)
	if len(picks) == 0 {
		t.Fatal("there is nowhere to choose from")
	}
	offered := strings.Join(picks[0].Options, " · ")
	if strings.Contains(offered, "A time") {
		t.Errorf("it offers a time on a source that cannot find one: %s", offered)
	}
	for _, want := range []string{"The beginning", "An offset", "The last few"} {
		if !strings.Contains(offered, want) {
			t.Errorf("it does not offer %q: %s", want, offered)
		}
	}
}

// A quiet log is not drawn again. A window that redrew itself on a topic nobody
// was writing to would be a window warming a room.
func TestAQuietLogIsNotDrawnAgain(t *testing.T) {
	fx, tb, bar, src := followedTopic(t)
	bar.toggleFollow()
	src.live <- model.Row{int64(1), int64(0), time.Now(), nil, []byte("one"), nil}
	pump(t, fx.q, func() bool { n, _ := bar.tail.Count(tb.ctx); return n == 1 })
	bar.redraw()
	// A page read now is resident, which is what makes the next assertion mean
	// something: an invalidated model would have to fetch it again.
	if _, err := tb.model.Read(tb.ctx, 0, 1); err != nil {
		t.Fatal(err)
	}
	if _, ok := tb.model.Row(tb.ctx, 0); !ok {
		pump(t, fx.q, func() bool { _, ok := tb.model.Row(tb.ctx, 0); return ok })
	}
	before := tb.model.Stats().ResidentPages
	if before == 0 {
		t.Fatal("nothing is resident to begin with")
	}
	// Nothing has arrived, so nothing is dropped and the page stays.
	bar.redraw()
	bar.redraw()
	if got := tb.model.Stats().ResidentPages; got != before {
		t.Errorf("a quiet log dropped its pages: %d resident, was %d", got, before)
	}
	// And when a record does arrive, it is drawn again.
	src.live <- model.Row{int64(2), int64(0), time.Now(), nil, []byte("two"), nil}
	pump(t, fx.q, func() bool { n, _ := bar.tail.Count(tb.ctx); return n == 2 })
	bar.redraw()
	if got := tb.model.Stats().ResidentPages; got != 0 {
		t.Errorf("a record arrived and the pages were kept: %d resident", got)
	}
}

// What fell out of the window is said. A person watching a log move faster than
// they can read it is owed that rather than a quiet gap (NFR-P10).
func TestWhatFellOutOfTheWindowIsSaid(t *testing.T) {
	// A window of two, so that a third record drops one.
	was := followKeeps
	followKeeps = 2
	t.Cleanup(func() { followKeeps = was })

	fx, tb, bar, src := followedTopic(t)
	bar.toggleFollow()
	for i := 1; i <= 3; i++ {
		src.live <- model.Row{int64(i), int64(0), time.Now(), nil, []byte("more"), nil}
	}
	pump(t, fx.q, func() bool { return bar.tail.Dropped() > 0 })
	bar.redraw()
	said := tb.footer.Text
	if !strings.Contains(said, "2 records held") {
		t.Errorf("the footer says %q", said)
	}
	if !strings.Contains(said, "1 record dropped") {
		t.Errorf("the footer says %q, which does not say what fell out", said)
	}
}

// A log that went away while somebody was watching says so, and keeps what it
// had: losing what was on the screen would be the worst moment to lose it.
func TestAFollowThatStoppedSaysSo(t *testing.T) {
	fx, tb, bar, src := followedTopic(t)
	bar.toggleFollow()
	src.live <- model.Row{int64(1), int64(0), time.Now(), nil, []byte("one"), nil}
	pump(t, fx.q, func() bool { n, _ := bar.tail.Count(tb.ctx); return n == 1 })

	src.goesAway(errors.New("the broker went away"))
	// Wake the read so it takes the failure.
	select {
	case src.live <- model.Row{int64(2), int64(0), time.Now(), nil, []byte("two"), nil}:
	default:
	}
	pump(t, fx.q, func() bool { return bar.tail.Err() != nil })
	bar.say()
	said := tb.footer.Text
	if !strings.Contains(said, "it stopped") || !strings.Contains(said, "broker went away") {
		t.Errorf("the footer says %q", said)
	}
	if rows, err := tb.model.Read(tb.ctx, 0, 10); err != nil || len(rows) == 0 {
		t.Errorf("it holds %d rows: %v", len(rows), err)
	}
}
