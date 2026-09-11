package shell

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/export"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/filedlg"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
	"github.com/ikigai-db/ikigai-db/internal/ui/uithread"
)

// exportSrc is what the active tab can export (FR-10.2).
type exportSrc struct {
	name  string // a file name to suggest, without its extension
	rows  func() model.RowStream
	total int64 // -1 when unknown, as for a PostgreSQL table
}

// selectionSource is the grid's selection as rows to export (FR-10.2): the
// rows it reaches, with only its columns. It streams a page at a time, so a
// selection reaching the last of a large table costs no more memory than a
// page: Copy sends a selection past its limit here. Nil with no selection.
func (s *Shell) selectionSource(g *grid.TableGrid, name string) *exportSrc {
	sel := g.Selection()
	cols, _, picked := selectionColumns(g)
	if len(picked) == 0 {
		return nil // nothing selected
	}
	defs := make([]model.ColumnDef, len(picked))
	for i, c := range picked {
		defs[i] = cols[c]
	}
	first, last := sel.Rows()
	end, total := int64(last)+1, int64(last-first+1)
	if last == grid.End {
		end, total = -1, -1
	}
	m := g.Model()
	return &exportSrc{name: name + " selection", total: total, rows: func() model.RowStream {
		return &selectionStream{m: m, cols: defs, picked: picked, next: int64(first), end: end}
	}}
}

// describeSelection names a selection's size for the export form.
func describeSelection(src *exportSrc, columns int) string {
	cols := "1 column"
	if columns != 1 {
		cols = fmt.Sprintf("%d columns", columns)
	}
	switch src.total {
	case -1:
		return "The selection: " + cols + ", to the last row"
	case 1:
		return "The selection: 1 row, " + cols
	}
	return fmt.Sprintf("The selection: %d rows, %s", src.total, cols)
}

// selectionPage is how many rows a selection's export reads at a time. A
// variable so that a test can make a small table span several reads.
var selectionPage = int64(grid.PageSize)

// selectionStream reads a selection's rows a page at a time through the
// grid's model, keeping only the selected columns. end is one past the last
// row, or -1 to read until the rows run out.
type selectionStream struct {
	m      *grid.Model
	cols   []model.ColumnDef
	picked []int
	next   int64
	end    int64
	buf    []model.Row
	done   bool
}

func (s *selectionStream) Columns() []model.ColumnDef { return s.cols }
func (s *selectionStream) Close() error               { return nil }

func (s *selectionStream) Next(ctx context.Context) (model.Row, error) {
	for len(s.buf) == 0 {
		if s.done || (s.end >= 0 && s.next >= s.end) {
			return nil, io.EOF
		}
		to := s.next + selectionPage
		if s.end >= 0 && to > s.end {
			to = s.end
		}
		rows, err := s.m.Read(ctx, s.next, to)
		if err != nil {
			return nil, err
		}
		if int64(len(rows)) < to-s.next {
			s.done = true // the end of the data
		}
		s.next += int64(len(rows))
		s.buf = rows
	}
	r := s.buf[0]
	s.buf = s.buf[1:]
	out := make(model.Row, len(s.picked))
	for i, c := range s.picked {
		if c < len(r) {
			out[i] = r[c]
		}
	}
	return out, nil
}

// exportSource is the active tab's exportable rows: a table's, or the query
// result on show. Nil when there is nothing to export.
func (s *Shell) exportSource() *exportSrc {
	t := s.activeTab()
	switch {
	case t == nil:
		return nil
	case t.browse != nil:
		total, known := t.model.Total()
		if !known {
			total = -1
		}
		return &exportSrc{name: t.item.Text, rows: t.browse.Rows, total: total}
	case t.query != nil:
		q := t.query
		i := q.results.SelectedIndex() - 1 // tab 0 is Messages
		if i < 0 || i >= len(q.sets) {
			return nil
		}
		rs, total := q.sets[i], int64(-1)
		if n, done := rs.Progress(); done {
			total = int64(n)
		}
		return &exportSrc{name: fmt.Sprintf("%s result %d", q.title, i+1), rows: rs.Rows, total: total}
	}
	return nil
}

// showExport asks for a format, then a file, then exports (T1.71–T1.72).
func (s *Shell) showExport() {
	t, src := s.activeTab(), s.exportSource()
	if src == nil {
		return
	}
	// The selection is offered, never assumed: All rows stays the default,
	// so a stray selection cannot quietly shorten an export.
	var sel *exportSrc
	var rows *widget.RadioGroup
	if g := s.activeGrid(); g != nil {
		if sel = s.selectionSource(g, src.name); sel != nil {
			_, _, picked := selectionColumns(g)
			rows = widget.NewRadioGroup([]string{allRows, describeSelection(sel, len(picked))}, nil)
			rows.SetSelected(allRows)
		}
	}
	formats := export.Formats()
	names := make([]string, len(formats))
	for i, f := range formats {
		names[i] = f.String()
	}
	format := widget.NewSelect(names, nil)
	header := widget.NewCheck("Column names as the first line", nil)
	header.SetChecked(true)
	format.OnChanged = func(string) {
		if headerApplies(formats[format.SelectedIndex()]) {
			header.Enable()
		} else {
			header.Disable() // JSON names every value already
		}
	}
	format.SetSelectedIndex(0)
	var items []*widget.FormItem
	if rows != nil {
		items = append(items, widget.NewFormItem("Rows", rows))
	}
	items = append(items, widget.NewFormItem("Format", format), widget.NewFormItem("", header))
	dialog.NewForm("Export “"+src.name+"”", "Choose File…", "Cancel", items,
		func(ok bool) {
			if !ok {
				return
			}
			src = pickSource(rows, src, sel)
			opt := exportOptions(formats[format.SelectedIndex()], header.Checked, src.name)
			ext := opt.Format.Extension()
			s.d.Files.Save(s.win, filedlg.Options{
				Message: fmt.Sprintf("Export “%s” as %s", src.name, opt.Format), Name: fileName(src.name) + "." + ext,
				Extensions: []string{ext}, Kind: opt.Format.String(), Accept: "Export",
			}, func(path string, err error) { s.exportTo(t, src, opt, path, err) })
		}, s.win).Show()
}

// headerApplies reports whether a format can begin with the column names:
// CSV and TSV as their first line, a workbook as its first row. JSON names
// every value already, and a Markdown table always has its header.
func headerApplies(f export.Format) bool {
	return f == export.CSV || f == export.TSV || f == export.XLSX
}

// exportOptions are an export's options as its form says; a workbook's sheet
// takes the name of what is exported (ADR-0055).
func exportOptions(f export.Format, header bool, name string) export.Options {
	return export.Options{Format: f, Header: header, Sheet: name}
}

// exportTo exports to the file the save dialog chose (FR-15.5). The file is
// created only now, and not at all if the tab closed while the dialog was
// open: nothing is left to fill it.
func (s *Shell) exportTo(t *tab, src *exportSrc, opt export.Options, path string, err error) *exportJob {
	switch {
	case err != nil:
		s.showError(err)
		return nil
	case path == "" || t.ctx.Err() != nil:
		return nil // the dialog was cancelled, or the tab has gone
	}
	f, err := os.Create(path)
	if err != nil {
		s.showError(formError("The export could not create its file: " + err.Error()))
		return nil
	}
	return s.runExport(t, src, opt, f, filepath.Base(path), func() { _ = os.Remove(path) })
}

// pickSource is the rows the export form's choice names: the selection if
// it was chosen, and every row otherwise.
func pickSource(rows *widget.RadioGroup, all, sel *exportSrc) *exportSrc {
	if rows != nil && sel != nil && rows.Selected != allRows {
		return sel
	}
	return all
}

// allRows is the export form's choice of every row, not just the selection.
const allRows = "All rows"

// fileName makes a tab's name safe to suggest as a file name.
func fileName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < ' ' || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(name))
	if name == "" {
		return "export"
	}
	return name
}

// exportJob is one export under way.
type exportJob struct {
	task   *task
	cancel context.CancelFunc

	mu     sync.Mutex
	latest export.Progress

	done bool // UI goroutine only
	err  error
}

// runExport writes src to w as a task in the task centre (FR-15.6), whose
// Cancel works (FR-10.7); the window stays usable while it runs. Closing
// the tab cancels it too. On failure or cancellation, discard removes what
// was written: a truncated file left behind looks like a complete export.
func (s *Shell) runExport(t *tab, src *exportSrc, opt export.Options, w io.WriteCloser, dest string, discard func()) *exportJob {
	ctx, cancel := context.WithCancel(t.ctx)
	j := &exportJob{cancel: cancel}
	j.task = s.startTask(t, "Export to "+dest, cancel)

	update := uithread.Coalesce(s.d.Run, s.d.Delay, func() {
		j.mu.Lock()
		p := j.latest
		j.mu.Unlock()
		frac := -1.0
		if src.total > 0 {
			frac = min(1, float64(p.Rows)/float64(src.total))
		}
		s.progressTask(j.task, exportStatus(p, src.total), frac)
	})
	go func() {
		p, err := export.Copy(ctx, w, src.rows(), opt, func(p export.Progress) {
			j.mu.Lock()
			j.latest = p
			j.mu.Unlock()
			update()
		})
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err != nil && discard != nil {
			discard()
		}
		close(j.task.finished)
		s.d.Run(func() {
			cancel()
			j.done, j.err = true, err
			switch {
			case err == nil:
				done := fmt.Sprintf("Exported %s to %s", rowCount(p.Rows, true), dest)
				s.endTask(j.task, taskDone, done)
				s.say(t, done)
			case errors.Is(err, context.Canceled):
				s.endTask(j.task, taskCancelled, "Cancelled; the partial file was removed")
				s.say(t, "Export cancelled; the partial file was removed")
			default:
				s.endTask(j.task, taskFailed, "Failed, and the partial file was removed: "+err.Error())
				s.showError(formError("The export failed, and the partial file was removed: " + err.Error()))
			}
		})
	}()
	return j
}

// exportStatus words an export's progress: how many rows, how fast, and, when
// the total is known, how long is left.
func exportStatus(p export.Progress, total int64) string {
	done := group(p.Rows) + " rows"
	if total > 0 {
		done = fmt.Sprintf("%s of %s rows", group(p.Rows), group(total))
	}
	secs := p.Elapsed.Seconds()
	if secs <= 0 || p.Rows == 0 {
		return done
	}
	rate := float64(p.Rows) / secs
	parts := []string{done, group(int64(rate)) + " rows/s"}
	if total > p.Rows {
		left := time.Duration(float64(total-p.Rows) / rate * float64(time.Second))
		parts = append(parts, "about "+approx(left)+" left")
	}
	return strings.Join(parts, " · ")
}

func approx(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", max(1, int(d.Seconds()+0.5)))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()+0.5))
	}
	return fmt.Sprintf("%.1f h", d.Hours())
}
