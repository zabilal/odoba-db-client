package shell

import (
	"fmt"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/app/filterexpr"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// Master and detail (FR-3.13, ADR-0043). Under a table's grid, a panel shows
// the rows of a table that refer to the active row (referring.go): the rows
// whose key holds its written values. It follows the active row, and a
// picker chooses the referring table where several refer.

// detailPanel is a table tab's panel of rows that refer to its active row.
type detailPanel struct {
	s      *Shell
	t      *tab
	box    fyne.CanvasObject // the panel, under the grid in split
	split  *container.Split  // the grid's place and the panel
	pick   *widget.Select    // which referring table's rows
	status *widget.Label     // how many rows, or why none
	open   *widget.Button    // Open in Tab
	place  *fyne.Container   // the rows' grid, or a word in its place
	which  int               // the referrer shown, by its place in t.referrers
	ref    model.ObjectRef   // the table shown
	grid   *grid.TableGrid
	model  *grid.Model
	shown  string // the filter shown, so that the same row reads nothing again
	seq    int    // numbers reads, so that only the latest lands
}

func (s *Shell) canShowDetail() bool {
	t := s.activeTab()
	return t != nil && t.query == nil && t.grid != nil && t.center != nil && len(t.referrers) > 0
}

// toggleDetail shows the rows that refer to the active row under the grid,
// or takes the panel away.
func (s *Shell) toggleDetail() {
	if !s.canShowDetail() {
		return
	}
	t := s.activeTab()
	if t.detail == nil {
		t.detail = s.newDetail(t)
	}
	d := t.detail
	if d.isShown() {
		t.center.Objects = []fyne.CanvasObject{t.body}
		t.center.Refresh()
		return
	}
	d.split.Leading = t.body
	t.center.Objects = []fyne.CanvasObject{d.split}
	t.center.Refresh()
	d.follow()
}

func (s *Shell) newDetail(t *tab) *detailPanel {
	d := &detailPanel{s: s, t: t, status: widget.NewLabel(""), place: container.NewStack()}
	d.status.Importance = widget.LowImportance
	d.status.Truncation = fyne.TextTruncateEllipsis
	labels := make([]string, len(t.referrers))
	for i, rf := range t.referrers {
		labels[i] = referrerLabel(rf)
	}
	d.pick = widget.NewSelect(labels, func(label string) {
		if i := slices.Index(labels, label); i >= 0 && i != d.which {
			d.which = i
			d.follow()
		}
	})
	d.pick.SetSelectedIndex(0)
	d.open = widget.NewButtonWithIcon("Open in Tab", fynetheme.ViewFullScreenIcon(), d.openInTab)
	d.open.Importance = widget.LowImportance
	closeIt := widget.NewButtonWithIcon("Close", fynetheme.CancelIcon(), s.toggleDetail)
	closeIt.Importance = widget.LowImportance
	head := container.NewBorder(nil, nil, d.pick, container.NewHBox(d.open, closeIt), d.status)
	d.box = container.NewBorder(head, nil, nil, nil, d.place)
	d.split = container.NewVSplit(t.body, d.box)
	d.split.Offset = 0.6
	return d
}

func (d *detailPanel) isShown() bool {
	c := d.t.center
	return len(c.Objects) == 1 && c.Objects[0] == fyne.CanvasObject(d.split)
}

// referrerLabel names a referring table by its key's columns.
func referrerLabel(rf model.Referrer) string {
	return rf.From.Name() + ", by " + strings.Join(rf.Key.Columns, ", ")
}

// filters are the referring table's rows' filter for the active row: its
// key's columns equal to the row's written values; ok is false where
// nothing can refer to the row, a new row or one with NULL in the key.
func (d *detailPanel) filters() (rf model.Referrer, fs []source.Filter, ok bool) {
	t := d.t
	rf = t.referrers[d.which]
	c, has := t.grid.Selection().Active()
	if !has || c.Row < t.model.Added() {
		return rf, nil, false
	}
	row, loaded := t.model.Row(t.ctx, int64(c.Row))
	if !loaded || row == nil {
		return rf, nil, false
	}
	vals := keyValues(t.model.Columns(), rf.Key.RefColumns, func(i int) any { return row[i] })
	if vals == nil || len(vals) != len(rf.Key.Columns) {
		return rf, nil, false
	}
	for i, v := range vals {
		fs = append(fs, source.Filter{Column: rf.Key.Columns[i], Op: source.OpEqual, Values: []any{v}})
	}
	return rf, fs, true
}

// follow shows the rows that refer to the active row, read off the UI
// goroutine: in the grid already there when only the row has changed, in a
// new one when the table has.
func (d *detailPanel) follow() {
	if !d.isShown() {
		return
	}
	rf, fs, ok := d.filters()
	d.open.Disable()
	if !ok {
		d.shown, d.seq = "", d.seq+1
		d.status.SetText("Nothing refers to this row: it is not written, or its key is empty")
		d.place.Objects = nil
		d.place.Refresh()
		return
	}
	key := rf.From.String() + fmt.Sprint(fs)
	if key == d.shown {
		d.open.Enable()
		return
	}
	d.shown = key
	d.seq++
	seq, t := d.seq, d.t
	d.status.SetText("Reading the rows of " + rf.From.Name() + "…")
	go func() {
		var bs *app.BrowseSource
		live, err := d.s.d.WS.Connect(t.ctx, t.connID)
		if err == nil {
			bs, err = app.NewBrowseSource(t.ctx, live.Source, rf.From, source.BrowseOptions{Filters: fs})
		}
		d.s.d.Run(func() {
			if seq != d.seq || t.ctx.Err() != nil {
				return
			}
			if err != nil {
				d.status.SetText("Could not read the rows of " + rf.From.Name() + ": " + err.Error())
				return
			}
			d.show(rf, bs)
		})
	}()
}

// show puts a browse's rows in the panel: in the grid there when it is on
// the same table, else in a grid of their own.
func (d *detailPanel) show(rf model.Referrer, bs *app.BrowseSource) {
	t := d.t
	if d.grid == nil || !d.ref.Equal(rf.From) {
		d.model = grid.NewModel(bs)
		d.grid = grid.NewTableGridWith(t.ctx, d.model, d.s.colours(), d.s.d.Run, d.s.d.Delay)
		m, g := d.model, d.grid
		m.OnPageLoaded = func(int64) {
			g.ScheduleRefresh()
			d.s.d.Run(d.count)
		}
		d.ref = rf.From
		d.place.Objects = []fyne.CanvasObject{g.View()}
		d.place.Refresh()
	} else {
		d.model.SetFetcher(bs)
		d.grid.ScheduleRefresh()
	}
	d.open.Enable()
	d.count()
}

// count says how many rows refer to the row, as far as they are read.
func (d *detailPanel) count() {
	if d.model == nil {
		return
	}
	n, final := d.model.Extent()
	text := rowCount(n, final)
	if !final {
		text = group(n) + "+ rows"
	}
	d.status.SetText(text + " of " + d.ref.Name() + " refer to this row")
}

// openInTab opens the rows shown in a tab of their own, filtered in its
// filter row, where they can be edited.
func (d *detailPanel) openInTab() {
	rf, fs, ok := d.filters()
	if !ok {
		return
	}
	texts := make(map[string]string, len(fs))
	for _, f := range fs {
		texts[f.Column] = filterexpr.Pick(f.Values, false)
	}
	d.s.openFiltered(d.t.connID, rf.From, texts)
}
