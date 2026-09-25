package shell

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
)

// Saved views (FR-3.16): a table's rows as somebody arranged them, under a
// name, so that the arrangement can be had again.
//
// A view is a session tab's arrangement with a name on it. What a session
// restores when the application starts and what a view restores when
// somebody picks it are the same thing, so the same two functions do both:
// sessionTab reads the arrangement and restoreView puts it back.

// viewTimeout bounds reading or writing the local database, which is on
// this machine and should never be the thing somebody waits for.
const viewTimeout = 5 * time.Second

// canSaveView reports whether the tab in front is rows somebody could
// arrange. A query's result is not: it is arranged by the query.
func (s *Shell) canSaveView() bool {
	t := s.activeTab()
	return s.d.Views != nil && t != nil && t.query == nil && !t.structure &&
		t.browse != nil && !t.ref.IsZero()
}

// saveView asks what to call this arrangement and keeps it.
func (s *Shell) saveView() {
	t := s.activeTab()
	if !s.canSaveView() {
		return
	}
	entry := widget.NewEntry()
	entry.SetPlaceHolder("Open orders, newest first")
	entry.Validator = func(text string) error {
		if strings.TrimSpace(text) == "" {
			return errors.New("a view needs a name")
		}
		return nil
	}
	// What it will keep, said plainly, because a view saved with nothing in
	// it is the one somebody would not have meant to save.
	said := widget.NewLabel(describeArrangement(sessionTab(t)))
	said.Wrapping = fyne.TextWrapWord

	d := dialog.NewForm("Save this view of "+t.ref.Name(), "Save", "Cancel",
		[]*widget.FormItem{{Text: "Name", Widget: entry}, {Widget: said}},
		func(ok bool) {
			if ok {
				s.keepView(t, strings.TrimSpace(entry.Text))
			}
		}, s.win)
	d.Resize(fyne.NewSize(480, d.MinSize().Height))
	d.Show()
}

// describeArrangement says what a view would keep, in the order somebody
// would look for it.
func describeArrangement(st localdb.SessionTab) string {
	var has []string
	if n := len(st.Filters); n > 0 {
		has = append(has, nounCount(n, "filter"))
	}
	if st.Where != "" {
		has = append(has, "a WHERE clause")
	}
	if n := len(st.Sorts); n > 0 {
		has = append(has, nounCount(n, "sorted column"))
	}
	if n := len(st.Hidden); n > 0 {
		has = append(has, nounCount(n, "hidden column"))
	}
	if st.Frozen > 0 {
		has = append(has, nounCount(st.Frozen, "frozen column"))
	}
	if len(has) == 0 {
		return "These rows are as they first opened: a view of them would keep nothing."
	}
	return "Keeps " + strings.Join(has, ", ") + "."
}

// keepView writes the view and says it did.
func (s *Shell) keepView(t *tab, name string) {
	v := localdb.View{ID: newViewID(), Name: name, Tab: sessionTab(t), Saved: time.Now().UTC()}
	ctx, cancel := context.WithTimeout(s.ctx, viewTimeout)
	go func() {
		defer cancel()
		err := s.d.Views.PutView(ctx, v)
		s.d.Run(func() {
			if err != nil {
				s.showError(fmt.Errorf("could not save the view: %w", err))
				return
			}
			s.say(t, "Saved the view “"+name+"”.")
		})
	}()
}

// newViewID names a view. A view saved twice under the same name is two
// views, because somebody who meant to replace one picks it from the list.
func newViewID() string { return fmt.Sprintf("v%d", time.Now().UTC().UnixNano()) }

// canShowViews reports whether there is a table in front whose views could
// be listed.
func (s *Shell) canShowViews() bool { return s.canSaveView() }

// showViews lists what is saved for the table in front, to pick from or to
// forget.
func (s *Shell) showViews() {
	t := s.activeTab()
	if !s.canShowViews() {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, viewTimeout)
	go func() {
		defer cancel()
		saved, err := s.d.Views.Views(ctx, t.connID, string(t.ref.Kind), t.ref.Path)
		s.d.Run(func() {
			if err != nil && len(saved) == 0 {
				s.showError(fmt.Errorf("could not read the saved views: %w", err))
				return
			}
			s.offerViews(t, saved)
		})
	}()
}

// offerViews puts up the list. Picking one arranges the rows that way;
// forgetting one takes it off the list and leaves the rows alone.
func (s *Shell) offerViews(t *tab, saved []localdb.View) {
	if len(saved) == 0 {
		dialog.ShowInformation("Saved views of "+t.ref.Name(),
			"None yet. Arrange these rows and save the view to have it again.", s.win)
		return
	}
	rows := container.NewVBox()
	var d dialog.Dialog
	redraw := func() {}
	redraw = func() {
		rows.Objects = nil
		for _, v := range saved {
			use := widget.NewButton(v.Name, nil)
			use.Alignment = widget.ButtonAlignLeading
			forget := widget.NewButtonWithIcon("", theme.DeleteIcon(), nil)
			forget.Importance = widget.LowImportance
			use.OnTapped = func() {
				d.Hide()
				s.applyView(t, v)
			}
			forget.OnTapped = func() {
				s.forgetView(v)
				saved = without(saved, v)
				if len(saved) == 0 {
					d.Hide()
					return
				}
				redraw()
			}
			rows.Add(container.NewBorder(nil, nil, nil, forget,
				container.NewVBox(use, quiet(describeArrangement(v.Tab)))))
		}
		rows.Refresh()
	}
	redraw()
	d = dialog.NewCustom("Saved views of "+t.ref.Name(), "Close",
		container.NewVScroll(rows), s.win)
	d.Resize(fyne.NewSize(460, 360))
	d.Show()
}

func without(vs []localdb.View, drop localdb.View) []localdb.View {
	out := vs[:0:0]
	for _, v := range vs {
		if v.ID != drop.ID {
			out = append(out, v)
		}
	}
	return out
}

// applyView arranges the rows the way the view says, by the same road a
// session takes when it puts a tab back.
func (s *Shell) applyView(t *tab, v localdb.View) {
	if t.grid == nil || t.browse == nil {
		return
	}
	s.restoreView(t, v.Tab)
	s.say(t, "Showing the view “"+v.Name+"”.")
}

// forgetView takes one off the list. The rows are left as they are:
// forgetting how to get back somewhere is not going anywhere.
func (s *Shell) forgetView(v localdb.View) {
	ctx, cancel := context.WithTimeout(s.ctx, viewTimeout)
	go func() {
		defer cancel()
		if err := s.d.Views.DeleteView(ctx, v); err != nil {
			s.d.Run(func() { s.showError(fmt.Errorf("could not forget the view: %w", err)) })
		}
	}()
}
