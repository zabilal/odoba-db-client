package shell

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Following a log, and starting somewhere in it (FR-13.5, FR-13.6).
//
// Everything below this was written and tested long ago and had no way in from
// the window: source.Seek is implemented by every source that has a log, and
// app.Tail keeps a following read in a window that cannot grow. What was missing
// was the two controls, which is what this is — and until they existed the
// journey they belong to (J8) could not be walked at all.
//
// A tail is not a browse. A browse is asked for a window and answers it; a tail
// answers when somebody writes, so while one is running the grid is drawn from
// the records held rather than from the log. Swapping back puts the browse
// where it was, which is the same bargain the pipeline editor strikes with a
// collection's documents.

// streamBar is the follow and seek controls for a tab showing a log.
type streamBar struct {
	s *Shell
	t *tab

	follow *widget.Button
	pause  *widget.Button
	seek   *widget.Button
	box    *fyne.Container

	// tail is the following read, or nil where the tab is showing the log as
	// it was read.
	tail *app.Tail
	// stop ends the redrawing while following.
	stop func()
	// at is how many records had arrived when the grid was last drawn, so that
	// a quiet log costs nothing.
	at int64
	// seeking is where a browse was told to start, kept so that the footer can
	// say it and a refresh can keep it.
	seeking string
}

// canFollow reports whether the tab in front is showing something that can be
// followed.
func (s *Shell) canFollow() bool {
	t := s.activeTab()
	// A tab with rows has a grid: both are set together when the grid is
	// attached, so asking about both would be asking twice.
	if t == nil || t.browse == nil || t.top == nil {
		return false
	}
	live, ok := s.d.WS.Get(t.connID)
	return ok && live.Source.Capabilities().Stream.Follow
}

// canSeek reports whether it can be told where to start.
func (s *Shell) canSeek() bool {
	t := s.activeTab()
	if t == nil || t.browse == nil || t.top == nil {
		return false
	}
	live, ok := s.d.WS.Get(t.connID)
	if !ok {
		return false
	}
	return live.Source.Capabilities().Stream.Consume
}

// streamControls is the bar for a tab, made when it is first wanted.
func (s *Shell) streamControls(t *tab) *streamBar {
	if t.stream != nil {
		return t.stream
	}
	b := &streamBar{s: s, t: t}
	b.follow = widget.NewButton("Follow", b.toggleFollow)
	b.pause = widget.NewButton("Pause", b.togglePause)
	b.pause.Disable()
	b.seek = widget.NewButton("Start at…", b.askSeek)
	b.box = container.NewHBox(widget.NewLabel("Records"), b.follow, b.pause, b.seek)
	t.stream = b
	return b
}

// showStreamBar puts the controls above the grid of a tab that has a log in it.
func (s *Shell) showStreamBar(t *tab) {
	if !s.canFollow() && !s.canSeek() {
		return
	}
	b := s.streamControls(t)
	if t.top == nil {
		return
	}
	for _, o := range t.top.Objects {
		if o == b.box {
			return // already there
		}
	}
	t.top.Objects = append(t.top.Objects, b.box)
	t.top.Refresh()
}

// toggleFollow starts following the log, or stops.
func (s *Shell) toggleFollow() {
	t := s.activeTab()
	if t == nil || !s.canFollow() {
		return
	}
	s.streamControls(t).toggleFollow()
}

func (b *streamBar) following() bool { return b.tail != nil }

func (b *streamBar) toggleFollow() {
	if b.following() {
		b.unfollow()
		return
	}
	live, ok := b.s.d.WS.Get(b.t.connID)
	if !ok {
		b.t.footer.SetText("That connection is not open.")
		return
	}
	opt := b.t.browse.Options()
	// From now on, which is what following means: a tail that replayed the
	// whole log to reach the present would be a different request, and one
	// that can still be made by starting somewhere and then following.
	if opt.Seek == nil {
		opt.Seek = &source.Seek{Mode: source.SeekEnd}
	}
	tail, err := app.NewTail(b.t.ctx, live.Source, b.t.ref, opt, followKeeps)
	if err != nil {
		b.s.showError(fmt.Errorf("the records could not be followed: %w", err))
		return
	}
	b.tail, b.at = tail, -1
	// SetFetcher drops what was cached, so the grid reads the tail rather than
	// the pages it had of the log.
	b.t.model.SetFetcher(tail)
	b.follow.SetText("Stop")
	b.pause.Enable()
	b.pause.SetText("Pause")
	b.watch()
	b.say()
}

// unfollow puts the log back as it was read.
func (b *streamBar) unfollow() {
	if b.stop != nil {
		b.stop()
		b.stop = nil
	}
	if b.tail != nil {
		b.tail.Close()
		b.tail = nil
	}
	b.follow.SetText("Follow")
	b.pause.SetText("Pause")
	b.pause.Disable()
	if b.t.model != nil && b.t.browse != nil {
		b.t.model.SetFetcher(b.t.browse)
		b.t.grid.ScheduleRefresh()
	}
	b.say()
}

// togglePause holds the records where they are, or lets them come again.
func (s *Shell) togglePause() {
	t := s.activeTab()
	if t == nil || t.stream == nil || !t.stream.following() {
		return
	}
	t.stream.togglePause()
}

func (b *streamBar) togglePause() {
	if b.tail == nil {
		return
	}
	if b.tail.Paused() {
		b.tail.Resume()
		b.pause.SetText("Pause")
	} else {
		// Backpressure rather than loss: a paused tail asks the source for
		// nothing, so records wait where they are (ADR-0097).
		b.tail.Pause()
		b.pause.SetText("Resume")
	}
	b.say()
}

// watch redraws the grid while records arrive, and not otherwise: a tail on a
// quiet log should cost nothing at all.
func (b *streamBar) watch() {
	ctx, cancel := context.WithCancel(b.t.ctx)
	b.stop = cancel
	go func() {
		tick := time.NewTicker(followRedraw)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				b.s.d.Run(func() {
					if ctx.Err() != nil {
						return
					}
					b.redraw()
				})
			}
		}
	}()
}

// followKeeps is how many records a tail holds; zero is the application layer's
// own answer (app.tailKeeps), which is enough to scroll back through what just
// happened.
//
// A variable so that a test can show a record falling out of the window without
// writing two thousand of them, as the chart's row limit is.
var followKeeps = 0

// followRedraw is how often a following grid is drawn again. Often enough to
// read a busy log by, rarely enough that the window is not redrawing itself
// instead of drawing records.
const followRedraw = 250 * time.Millisecond

// redraw draws the records that have arrived since the last time.
func (b *streamBar) redraw() {
	if b.tail == nil || b.t.model == nil || b.t.grid == nil {
		return
	}
	if n := b.tail.Changes(); n == b.at {
		return // nothing new; a quiet log costs nothing
	} else {
		b.at = n
	}
	b.t.model.Invalidate()
	b.t.grid.ScheduleRefresh()
	b.say()
}

// say puts what is happening in the footer: following or not, paused or not, and
// what has been dropped, because a person watching a log move faster than they
// can read it is owed that rather than a quiet gap.
func (b *streamBar) say() {
	if b.t.footer == nil {
		return
	}
	var parts []string
	switch {
	case b.tail == nil:
		// Not following: the count line says what the tab is showing, and where
		// it is reading from is part of that (showCount).
	default:
		held, _ := b.tail.Count(context.Background())
		state := "following"
		if b.tail.Paused() {
			state = "paused"
		}
		parts = append(parts, fmt.Sprintf("%s · %s held", state, nounCount(int(held), "record")))
		if n := b.tail.Dropped(); n > 0 {
			parts = append(parts, fmt.Sprintf("%s dropped", nounCount(int(n), "record")))
		}
		if err := b.tail.Err(); err != nil {
			parts = append(parts, "it stopped: "+err.Error())
		}
	}
	if len(parts) == 0 {
		b.s.showCount(b.t)
		return
	}
	b.t.footer.SetText(strings.Join(parts, " · "))
}

// askSeek asks where to start reading, and reads from there.
func (s *Shell) askSeek() {
	t := s.activeTab()
	if t == nil || !s.canSeek() {
		return
	}
	s.streamControls(t).askSeek()
}

// seekModes are the ways of saying where to start. The words are the question a
// person is asking, not the enumeration's own names.
var seekModes = []struct {
	name string
	mode source.SeekMode
}{
	{"The beginning", source.SeekBeginning},
	{"The last few", source.SeekLast},
	{"An offset", source.SeekOffset},
	{"A time", source.SeekTimestamp},
}

func (b *streamBar) askSeek() {
	live, ok := b.s.d.WS.Get(b.t.connID)
	if !ok {
		b.t.footer.SetText("That connection is not open.")
		return
	}
	caps := live.Source.Capabilities().Stream
	names := make([]string, 0, len(seekModes))
	for _, m := range seekModes {
		if m.mode == source.SeekTimestamp && !caps.SeekTimestamp {
			// Not offered where the source cannot answer it: a choice that
			// fails when it is taken is worse than one that is not there.
			continue
		}
		names = append(names, m.name)
	}
	where := widget.NewSelect(names, nil)
	where.Selected = names[0]
	count := widget.NewEntry()
	count.SetPlaceHolder("100")
	offset := widget.NewEntry()
	offset.SetPlaceHolder("0")
	when := widget.NewEntry()
	when.SetPlaceHolder(time.Now().UTC().Format("2006-01-02 15:04:05"))

	items := []*widget.FormItem{
		widget.NewFormItem("Start at", where),
		widget.NewFormItem("How many", count),
		widget.NewFormItem("Offset", offset),
		widget.NewFormItem("Time (UTC)", when),
		widget.NewFormItem("", quiet("The last few is that many in every log, "+
			"each partition having its own end.")),
	}
	items[1].HintText = "For the last few: how many records back, in each log."
	items[3].HintText = "For a time: a log with nothing after it is left out."

	d := dialog.NewForm("Start Reading At", "Read", "Cancel", items, func(ok bool) {
		if !ok {
			return
		}
		seek, said, err := readSeek(where.Selected, count.Text, offset.Text, when.Text)
		if err != nil {
			b.s.showError(err)
			return
		}
		b.startAt(seek, said)
	}, b.s.win)
	d.Resize(fyne.NewSize(520, d.MinSize().Height))
	d.Show()
}

// readSeek turns what was typed into a position, refusing what cannot be read
// rather than starting somewhere nobody asked for.
func readSeek(mode, count, offset, when string) (*source.Seek, string, error) {
	var chosen source.SeekMode
	found := false
	for _, m := range seekModes {
		if m.name == mode {
			chosen, found = m.mode, true
			break
		}
	}
	if !found {
		return nil, "", formError("Choose where to start.")
	}
	seek := &source.Seek{Mode: chosen}
	switch chosen {
	case source.SeekBeginning:
		return seek, "the beginning", nil
	case source.SeekLast:
		n, err := strconv.ParseInt(strings.TrimSpace(count), 10, 64)
		if err != nil || n <= 0 {
			return nil, "", formError("How many records back? A whole number above nothing.")
		}
		seek.Count = n
		return seek, fmt.Sprintf("the last %d of each log", n), nil
	case source.SeekOffset:
		n, err := strconv.ParseInt(strings.TrimSpace(offset), 10, 64)
		if err != nil || n < 0 {
			return nil, "", formError("Which offset? A whole number, or nothing for the beginning.")
		}
		seek.Offset = n
		return seek, "offset " + strconv.FormatInt(n, 10), nil
	}
	t, err := readWhen(strings.TrimSpace(when))
	if err != nil {
		return nil, "", err
	}
	seek.Time = t
	return seek, t.UTC().Format("2006-01-02 15:04:05") + " UTC", nil
}

// readWhen reads a time as somebody would type one: a date and a time, or a
// date alone, in UTC. A time nobody can read is refused rather than taken as
// now, which would read the whole log.
func readWhen(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05Z07:00",
		"2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, formError("That is not a time. Write it as 2026-09-25 14:30:00, in UTC.")
}

// startAt reads the log again from a position.
func (b *streamBar) startAt(seek *source.Seek, said string) {
	if b.following() {
		// A tail begins where the log is now; asking it to begin somewhere else
		// is asking for a read, so the tail ends first.
		b.unfollow()
	}
	opt := b.t.browse.Options()
	opt.Seek = seek
	b.seeking = said
	b.s.rebrowse(b.t, opt, b.t.applied, "Reading from "+said+"…",
		"The records could not be read from "+said+": ")
}

// tailRef is the object a tab is following, for a test to name.
func (b *streamBar) tailRef() model.ObjectRef { return b.t.ref }

// canPauseFollowing reports whether there is a tail to hold. Pausing something
// that is not following is not a command anybody means.
func (s *Shell) canPauseFollowing() bool {
	t := s.activeTab()
	return t != nil && t.stream != nil && t.stream.following()
}
