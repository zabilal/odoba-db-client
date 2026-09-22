package shell

import (
	"errors"
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

// Moving a consumer group's offsets (FR-13.13).
//
// This decides what every consumer in a group reads next, and it deletes
// nothing — which is what makes it easy to underrate. The dialog says what
// will actually happen, and the confirmation reads back the position as it
// was understood, because a time somebody typed and a time this understood
// are not always the same time.

// Where a group can be moved to, in the words the dialog offers.
const (
	toBeginning = "The beginning of each log"
	toEnd       = "The end of each log"
	toTime      = "A time"
	toOffset    = "A named offset"
)

// selectionIsGroup reports whether a consumer group is selected on a
// connection that can move one.
func (s *Shell) selectionIsGroup() bool {
	conn, n, ok := s.Explorer.SelectedNode()
	if !ok || n.Ref.Kind != model.KindConsumerGroup {
		return false
	}
	live, ok := s.d.WS.Get(conn)
	return ok && live.Source.Capabilities().Stream.ResetOffsets
}

func (s *Shell) resetSelectedGroup() {
	conn, n, ok := s.Explorer.SelectedNode()
	if !ok || n.Ref.Kind != model.KindConsumerGroup {
		return
	}
	group := n.Ref.Name()

	where := widget.NewSelect([]string{toBeginning, toEnd, toTime, toOffset}, nil)
	where.Selected = toBeginning
	at := widget.NewEntry()
	at.SetPlaceHolder("2026-09-22T10:30:00Z, or 2026-09-22 10:30 in this machine's own time")
	offset := widget.NewEntry()
	offset.SetPlaceHolder("a whole number; past the end of a log means the end of it")

	note := widget.NewLabel("This changes what every consumer in this group reads next. " +
		"Nothing is deleted: moved back, the group processes again what it has already done; " +
		"moved forward, it never sees what it skipped. The group must have nothing reading " +
		"through it, so stop its consumers first.")
	note.Wrapping = fyne.TextWrapWord

	body := container.NewVBox(widget.NewForm(
		widget.NewFormItem("Move to", where),
		widget.NewFormItem("Time", at),
		widget.NewFormItem("Offset", offset),
	), note)

	d := dialog.NewCustomConfirm("Move "+group+"?", "Move", "Cancel", body, func(ok bool) {
		if !ok {
			return
		}
		seek, err := resetFrom(where.Selected, at.Text, offset.Text)
		if err != nil {
			s.showError(err)
			return
		}
		s.changeCluster(conn, fmt.Sprintf("move %s to %s", group, movedTo(seek)),
			func(src source.Source, confirmed bool) error {
				return app.ResetOffsets(s.ctx, src, source.ResetRequest{
					GroupID: group, Seek: seek, Confirmed: confirmed})
			})
	}, s.win)
	d.Resize(fyne.NewSize(600, 420))
	d.Show()
}

// resetFrom reads what was chosen into the position it names.
func resetFrom(where, at, offset string) (source.Seek, error) {
	switch where {
	case toBeginning:
		return source.Seek{Mode: source.SeekBeginning}, nil
	case toEnd:
		return source.Seek{Mode: source.SeekEnd}, nil
	case toOffset:
		n, err := strconv.ParseInt(strings.TrimSpace(offset), 10, 64)
		if err != nil || n < 0 {
			return source.Seek{}, fmt.Errorf(
				"an offset is a whole number from 0 upwards, and %q is not", offset)
		}
		return source.Seek{Mode: source.SeekOffset, Offset: n}, nil
	case toTime:
		when, err := timeFrom(at)
		if err != nil {
			return source.Seek{}, err
		}
		return source.Seek{Mode: source.SeekTimestamp, Time: when}, nil
	}
	return source.Seek{}, errors.New("choose where the group is to be moved to")
}

// timeFrom reads a time written with a zone, or written without one and meant
// in this machine's own.
//
// A time is read strictly rather than generously: this moves a running
// group, and a date understood as the wrong day would move it somewhere
// nobody asked for. What was understood is read back before anything happens.
func timeFrom(text string) (time.Time, error) {
	text = strings.TrimSpace(text)
	for _, layout := range []string{
		time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02",
	} {
		if when, err := time.ParseInLocation(layout, text, time.Local); err == nil {
			return when, nil
		}
	}
	return time.Time{}, fmt.Errorf("a time reads as 2026-09-22T10:30:00Z, or as "+
		"2026-09-22 10:30 in this machine's own time, and %q is neither", text)
}

// movedTo says where a group is being moved, in the words the confirmation
// uses. A time is said back as it was understood, zone and all.
func movedTo(seek source.Seek) string {
	switch seek.Mode {
	case source.SeekBeginning:
		return "the beginning of each log"
	case source.SeekEnd:
		return "the end of each log"
	case source.SeekOffset:
		return fmt.Sprintf("offset %d", seek.Offset)
	case source.SeekTimestamp:
		return "what was written at or after " + seek.Time.Format(time.RFC3339)
	}
	return "somewhere unnamed"
}
