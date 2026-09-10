package shell

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app/filterexpr"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// picklistLimit is how many of a column's values a picklist offers: the
// most frequent, when there are more.
var picklistLimit = 1000

// pick is a column's filter chosen from its picklist. The values are typed
// as the source listed them, so they filter exactly the rows they were
// counted from. text is how the filter row shows the choice; once the person
// edits it, the text is the filter again.
type pick struct {
	values []any
	negate bool
	text   string
}

// picklist is a column's values with their counts, to filter the column by
// ticking them (FR-3.4, ADR-0016).
type picklist struct {
	s      *Shell
	t      *tab
	col    int
	column model.ColumnDef
	search *widget.Entry
	list   *widget.List
	status *widget.Label
	apply  *widget.Button
	all    []source.DistinctValue
	labels []string
	on     []bool // ticked, by index into all
	shown  []int  // what the search leaves, by index into all
	full   bool   // every value of the column is listed
	loaded bool
	ctx    context.Context
	cancel context.CancelFunc
	dlg    *dialog.CustomDialog
}

// canPickValues reports whether Filter by Values has a column to list: the
// selected cell's, in a browse whose source can list values.
func (s *Shell) canPickValues() bool {
	t := s.activeTab()
	return t != nil && t.browse != nil && t.grid != nil && t.browse.CanListValues() && t.grid.SelectedColumn() >= 0
}

// showPicklist lists a column's values among the rows the other columns'
// filters leave, to filter the column by ticking them.
func (s *Shell) showPicklist(t *tab, col int) *picklist {
	if t.browse == nil || t.model == nil || !t.browse.CanListValues() {
		return nil
	}
	cols := t.model.Columns()
	if col < 0 || col >= len(cols) {
		return nil
	}
	p := &picklist{s: s, t: t, col: col, column: cols[col], search: widget.NewEntry(),
		status: widget.NewLabel("Counting values…")}
	p.ctx, p.cancel = context.WithCancel(t.ctx)
	p.search.SetPlaceHolder("Search values")
	p.search.OnChanged = func(string) { p.narrow() }
	p.search.OnSubmitted = func(string) { p.confirm() }
	p.status.Importance = widget.LowImportance
	p.list = widget.NewList(func() int { return len(p.shown) }, p.newRow, p.updateRow)
	p.apply = widget.NewButton("Filter", p.confirm)
	p.apply.Importance = widget.HighImportance
	p.apply.Disable()
	top := container.NewBorder(nil, nil, nil, container.NewHBox(
		widget.NewButton("Select All", func() { p.setShown(true) }),
		widget.NewButton("Deselect All", func() { p.setShown(false) })), p.search)
	bottom := container.NewBorder(nil, nil, p.status, container.NewHBox(widget.NewButton("Cancel", p.close), p.apply))
	p.dlg = dialog.NewCustomWithoutButtons(fmt.Sprintf("Filter “%s” by Values", p.column.Name),
		container.NewBorder(top, bottom, nil, nil, p.list), s.win)
	p.dlg.SetOnClosed(p.cancel) // closing stops a count still running
	p.dlg.Resize(fyne.NewSize(460, 480))
	p.dlg.Show()
	s.win.Canvas().Focus(p.search)
	p.load()
	return p
}

func (p *picklist) newRow() fyne.CanvasObject {
	count := widget.NewLabel("")
	count.Importance = widget.LowImportance
	return container.NewBorder(nil, nil, nil, count, widget.NewCheck("", nil))
}

func (p *picklist) updateRow(i widget.ListItemID, o fyne.CanvasObject) {
	if i >= len(p.shown) {
		return
	}
	k := p.shown[i]
	row := o.(*fyne.Container)
	check, count := row.Objects[0].(*widget.Check), row.Objects[1].(*widget.Label)
	check.OnChanged = nil // a recycled row must not tick the value it showed before
	check.SetText(p.labels[k])
	check.SetChecked(p.on[k])
	check.OnChanged = func(on bool) { p.set(k, on) }
	count.SetText("")
	if n := p.all[k].Count; n >= 0 {
		count.SetText(group(n))
	}
}

// load counts the column's values away from the UI goroutine. Values the
// column is already filtered by start ticked; with no pick, all do.
func (p *picklist) load() {
	bs, name, ctx := p.t.browse, p.column.Name, p.ctx
	go func() {
		vals, err := bs.Distinct(ctx, name, picklistLimit)
		p.s.d.Run(func() {
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				p.status.SetText("Could not list the values: " + err.Error())
				return
			}
			prev, had := p.t.picked[p.col]
			p.all, p.full, p.loaded = vals, len(vals) < picklistLimit, true
			p.on, p.labels = make([]bool, len(vals)), make([]string, len(vals))
			for k, v := range vals {
				p.labels[k] = grid.Format(v.Value, p.column, time.Local).Text
				p.on[k] = !had || listed(prev.values, v.Value) != prev.negate
			}
			p.narrow()
		})
	}()
}

// narrow shows the values whose text holds what is typed in the search.
func (p *picklist) narrow() {
	q := strings.ToLower(strings.TrimSpace(p.search.Text))
	p.shown = p.shown[:0]
	for k, l := range p.labels {
		if q == "" || strings.Contains(strings.ToLower(l), q) {
			p.shown = append(p.shown, k)
		}
	}
	p.list.Refresh()
	p.refreshStatus()
}

func (p *picklist) set(k int, on bool) {
	p.on[k] = on
	p.refreshStatus()
}

// setShown ticks or unticks every value the search shows.
func (p *picklist) setShown(on bool) {
	for _, k := range p.shown {
		p.on[k] = on
	}
	p.list.Refresh()
	p.refreshStatus()
}

func (p *picklist) refreshStatus() {
	if !p.loaded {
		return
	}
	n := 0
	for _, on := range p.on {
		if on {
			n++
		}
	}
	switch {
	case len(p.all) == 0:
		p.status.SetText("No values.")
	case p.full:
		p.status.SetText(fmt.Sprintf("%s of %s values ticked", group(int64(n)), group(int64(len(p.all)))))
	default:
		p.status.SetText(fmt.Sprintf("%s of the %s most frequent values ticked", group(int64(n)), group(int64(len(p.all)))))
	}
	if n == 0 {
		p.apply.Disable()
	} else {
		p.apply.Enable()
	}
}

// confirm filters the column by what is ticked, and closes the list.
//
// Everything ticked is no filter on the column. Otherwise the shorter side is
// sent: a,b for a few ticked, !c for all but a few. When every value is
// listed the two select the same rows. When the list was cut short at its
// limit they do not, and the shorter side is the one a person means: ticking
// a few means only those, and unticking a few means everything else,
// unlisted values included.
func (p *picklist) confirm() {
	var on, off []any
	for k, v := range p.all {
		if p.on[k] {
			on = append(on, v.Value)
		} else {
			off = append(off, v.Value)
		}
	}
	if len(on) == 0 {
		return
	}
	t := p.t
	if len(off) == 0 {
		delete(t.picked, p.col)
		t.grid.SetFilterText(p.col, "")
	} else {
		pk := pick{values: on}
		if len(off) < len(on) {
			pk = pick{values: off, negate: true}
		}
		pk.text = filterexpr.Pick(pk.values, pk.negate)
		if t.picked == nil {
			t.picked = map[int]pick{}
		}
		t.picked[p.col] = pk
		t.grid.SetFilterText(p.col, pk.text)
	}
	p.close()
	t.grid.ApplyFilters()
}

func (p *picklist) close() {
	p.cancel()
	p.dlg.Hide()
}

// listed reports whether v is among vals, as the source typed them.
func listed(vals []any, v any) bool {
	key := fmt.Sprintf("%T(%v)", v, v)
	for _, x := range vals {
		if fmt.Sprintf("%T(%v)", x, x) == key {
			return true
		}
	}
	return false
}
