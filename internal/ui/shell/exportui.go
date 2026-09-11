package shell

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/export"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/uithread"
)

// exportSrc is what the active tab can export (FR-10.2).
type exportSrc struct {
	name  string // a file name to suggest, without its extension
	rows  func() model.RowStream
	total int64 // -1 when unknown, as for a PostgreSQL table
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
	formats := export.Formats()
	names := make([]string, len(formats))
	for i, f := range formats {
		names[i] = f.String()
	}
	format := widget.NewSelect(names, nil)
	header := widget.NewCheck("Column names as the first line", nil)
	header.SetChecked(true)
	format.OnChanged = func(string) {
		if f := formats[format.SelectedIndex()]; f == export.CSV || f == export.TSV {
			header.Enable()
		} else {
			header.Disable() // JSON names every value already
		}
	}
	format.SetSelectedIndex(0)
	dialog.NewForm("Export “"+src.name+"”", "Choose File…", "Cancel",
		[]*widget.FormItem{widget.NewFormItem("Format", format), widget.NewFormItem("", header)},
		func(ok bool) {
			if !ok {
				return
			}
			opt := export.Options{Format: formats[format.SelectedIndex()], Header: header.Checked}
			save := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
				if err != nil {
					s.showError(err)
					return
				}
				if w == nil {
					return // the user closed the save dialog
				}
				uri := w.URI()
				s.runExport(t, src, opt, w, uri.Name(), func() { _ = storage.Delete(uri) })
			}, s.win)
			save.SetFileName(fileName(src.name) + "." + opt.Format.Extension())
			save.Show()
		}, s.win).Show()
}

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
	dlg    *dialog.CustomDialog
	status *widget.Label
	bar    *widget.ProgressBar // nil when the total is unknown
	cancel context.CancelFunc

	mu     sync.Mutex
	latest export.Progress

	done bool // UI goroutine only
	err  error
}

// runExport writes src to w under a progress sheet whose Cancel works
// (FR-10.7). Closing the tab cancels it too. On failure or cancellation,
// discard removes what was written: a truncated file left behind looks like a
// complete export.
func (s *Shell) runExport(t *tab, src *exportSrc, opt export.Options, w io.WriteCloser, dest string, discard func()) *exportJob {
	ctx, cancel := context.WithCancel(t.ctx)
	j := &exportJob{status: widget.NewLabel("Starting…"), cancel: cancel}
	var bar fyne.CanvasObject = widget.NewProgressBarInfinite()
	if src.total > 0 {
		j.bar = widget.NewProgressBar()
		bar = j.bar
	}
	j.dlg = dialog.NewCustomWithoutButtons("Exporting to "+dest, container.NewVBox(j.status, bar), s.win)
	j.dlg.SetButtons([]fyne.CanvasObject{widget.NewButton("Cancel", cancel)})
	j.dlg.Resize(fyne.NewSize(440, j.dlg.MinSize().Height))
	j.dlg.Show()

	update := uithread.Coalesce(s.d.Run, s.d.Delay, func() {
		j.mu.Lock()
		p := j.latest
		j.mu.Unlock()
		if j.done {
			return
		}
		j.status.SetText(exportStatus(p, src.total))
		if j.bar != nil {
			j.bar.SetValue(min(1, float64(p.Rows)/float64(src.total)))
		}
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
		s.d.Run(func() {
			cancel()
			j.done, j.err = true, err
			j.dlg.Hide()
			switch {
			case err == nil:
				s.say(t, fmt.Sprintf("Exported %s to %s", rowCount(p.Rows, true), dest))
			case errors.Is(err, context.Canceled):
				s.say(t, "Export cancelled; the partial file was removed")
			default:
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
