package shell

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/export"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
	"github.com/ikigai-db/ikigai-db/internal/ui/filedlg"
)

// Batch operations on marked objects (FR-2.8): script fifty tables, write
// the DROPs for a schema somebody is retiring, export a dozen tables into
// one directory.
//
// The tree selects one row at a time, so what several objects have in
// common here is a mark rather than a selection: a tick in front of the
// row, kept as branches open and close, and cleared when the batch is
// done with. Marking is a thing somebody does on purpose, which is also
// what keeps a batch from being the accident of where the cursor was.

// canMark reports whether the selection is something to mark.
func (s *Shell) canMark() bool {
	_, _, ok := s.Explorer.SelectedNode()
	return ok
}

// markSelected marks the selected object, or unmarks it if it is marked.
// How many are marked is in the status line, which says so for as long as
// they are marked rather than for a moment.
func (s *Shell) markSelected() {
	if _, _, ok := s.Explorer.SelectedNode(); !ok {
		return
	}
	s.Explorer.ToggleMark(s.Explorer.Selected())
	s.sync()
}

// canClearMarks reports whether there is anything marked to clear.
func (s *Shell) canClearMarks() bool { return len(s.Explorer.Marks()) > 0 }

// clearMarks unmarks everything. It changes nothing in any database: a
// mark is a note about what to do next.
func (s *Shell) clearMarks() {
	s.Explorer.ClearMarks()
	s.sync()
}

// marked is the batch: the marked objects, and the connection they are
// all on. ok is false when nothing is marked, or when the marks are
// spread over more than one connection — a batch is done through one
// connection, and objects on two are two batches.
func (s *Shell) marked() (connID string, nodes []view.MarkedNode, ok bool) {
	nodes = s.Explorer.MarkedNodes()
	if len(nodes) == 0 {
		return "", nil, false
	}
	connID = nodes[0].ConnID
	for _, n := range nodes {
		if n.ConnID != connID {
			return "", nil, false
		}
	}
	return connID, nodes, true
}

// canScriptMarked reports whether a script could be written for the batch.
func (s *Shell) canScriptMarked() bool {
	conn, _, ok := s.marked()
	if !ok {
		return false
	}
	_, open := s.d.WS.Get(conn)
	return open
}

// scriptMarkedSelect writes a SELECT for every marked object.
func (s *Shell) scriptMarkedSelect() {
	s.scriptMarked("SELECT", func(ctx context.Context, src source.Source, n view.MarkedNode) (string, error) {
		return app.ScriptAs(ctx, src, n.Node.Ref, app.ScriptSelect)
	})
}

// scriptMarkedCreate writes the DDL that would recreate every marked
// object.
func (s *Shell) scriptMarkedCreate() {
	s.scriptMarked("CREATE", func(ctx context.Context, src source.Source, n view.MarkedNode) (string, error) {
		stmts, err := app.ScriptCreate(ctx, src, n.Node.Ref)
		return scriptOf(stmts), err
	})
}

// scriptMarkedDrop writes the DROP for every marked object.
func (s *Shell) scriptMarkedDrop() {
	s.scriptMarked("DROP", func(_ context.Context, src source.Source, n view.MarkedNode) (string, error) {
		stmts, err := app.ScriptDrop(src, n.Node.Ref)
		return scriptOf(stmts), err
	})
}

// scriptMarked writes one script for the whole batch and opens it as
// unsaved text in a query tab. Nothing runs: it is a script to read, keep
// or edit, which is what every script this window writes is (ADR-0011 §15).
//
// An object the connection cannot write that kind of statement for is
// named in the script as a comment rather than left out, because a script
// that is quietly short of what was marked is the one somebody runs
// thinking it is all of it.
func (s *Shell) scriptMarked(kind string, write func(context.Context, source.Source, view.MarkedNode) (string, error)) {
	conn, nodes, ok := s.marked()
	if !ok {
		return
	}
	s.status.SetText("Writing the " + kind + " script for " + nounCount(len(nodes), "object") + "…")
	go func() {
		ctx, cancel := context.WithTimeout(s.ctx, scriptTimeout)
		defer cancel()
		live, err := s.d.WS.Connect(ctx, conn)
		var parts []string
		if err == nil {
			for _, n := range nodes {
				text, werr := write(ctx, live.Source, n)
				switch {
				case werr != nil:
					parts = append(parts, "-- "+n.Node.Label+": "+werr.Error())
				case strings.TrimSpace(text) == "":
					parts = append(parts, "-- "+n.Node.Label+": nothing to write")
				default:
					parts = append(parts, strings.TrimRight(text, "\n"))
				}
			}
		}
		s.d.Run(func() {
			if s.ctx.Err() != nil {
				return
			}
			if err != nil {
				s.status.SetText("")
				s.showError(fmt.Errorf("could not write the %s script: %w", kind, err))
				s.crashed(conn, err)
				return
			}
			s.openScript(conn, marksHeader(kind, len(nodes))+strings.Join(parts, "\n\n")+"\n")
		})
	}()
}

// marksHeader says what the script is and that it has not run, because a
// script for fifty objects is longer than the window it opens in.
func marksHeader(kind string, n int) string {
	return "-- " + kind + " for " + nounCount(n, "marked object") + ".\n" +
		"-- Nothing here has run. This is a script to read, keep or edit.\n\n"
}

// canExportMarked reports whether the batch has rows to export.
func (s *Shell) canExportMarked() bool {
	conn, nodes, ok := s.marked()
	if !ok {
		return false
	}
	if _, open := s.d.WS.Get(conn); !open {
		return false
	}
	for _, n := range nodes {
		if !n.Node.Browsable {
			return false
		}
	}
	return true
}

// exportMarked asks for a format and where the files go, and writes one
// file per marked object.
//
// What is chosen is where the first file goes, and the rest are written
// beside it, which is the bargain a file dialog strikes when what is
// wanted is a directory (ADR-0121).
func (s *Shell) exportMarked() {
	conn, nodes, ok := s.marked()
	if !ok || !s.canExportMarked() {
		return
	}
	formats := exportFormats(&exportSrc{}) // a batch writes rows, never INSERTs
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
			return
		}
		header.Disable() // JSON names every value already
	}
	format.SetSelectedIndex(0)
	d := dialog.NewForm("Export "+nounCount(len(nodes), "marked object"), "Choose Where…", "Cancel",
		[]*widget.FormItem{
			widget.NewFormItem("Format", format),
			widget.NewFormItem("", header),
			widget.NewFormItem("", quiet("One file each, named after the object, all in the directory you choose.")),
		}, func(ok bool) {
			if !ok {
				return
			}
			f := formats[format.SelectedIndex()]
			ext := f.Extension()
			s.d.Files.Save(s.win, filedlg.Options{
				Message:    fmt.Sprintf("Export %s as %s", nounCount(len(nodes), "object"), f),
				Name:       fileName(nodes[0].Node.Label) + "." + ext,
				Extensions: []string{ext}, Kind: f.String(), Accept: "Export",
			}, func(path string, err error) {
				switch {
				case err != nil:
					s.showError(fmt.Errorf("could not choose where the files go: %w", err))
				case path == "":
					// Cancelled, which is an answer and not a failure.
				default:
					s.exportMarkedTo(conn, nodes, f, header.Checked, filepath.Dir(path))
				}
			})
		}, s.win)
	d.Resize(fyne.NewSize(460, d.MinSize().Height))
	d.Show()
}

// exportMarkedTo exports each object into dir, one file each.
func (s *Shell) exportMarkedTo(connID string, nodes []view.MarkedNode, f export.Format, header bool, dir string) {
	for _, n := range nodes {
		s.exportObjectTo(connID, n.Node, f, header, filepath.Join(dir, fileName(n.Node.Label)+"."+f.Extension()))
	}
}

// exportObjectTo exports one object's rows to a path, as a task of its
// own, so that one failing leaves the rest running and says which.
func (s *Shell) exportObjectTo(connID string, n model.Node, f export.Format, header bool, path string) {
	opt := export.Options{Format: f, Header: header, Name: n.Label}
	go func() {
		ctx, cancel := context.WithTimeout(s.ctx, scriptTimeout)
		defer cancel()
		live, err := s.d.WS.Connect(ctx, connID)
		var b *app.BrowseSource
		if err == nil {
			b, err = app.NewBrowseSource(ctx, live.Source, n.Ref, source.BrowseOptions{})
		}
		s.d.Run(func() {
			if s.ctx.Err() != nil {
				return
			}
			if err != nil {
				s.showError(fmt.Errorf("could not read %s to export it: %w", n.Label, err))
				return
			}
			// How many rows there are is not asked: counting every
			// marked table before exporting any would double the round
			// trips for a progress bar. The task says how many rows have
			// gone rather than how far through it is.
			file, err := os.Create(path)
			if err != nil {
				s.showError(formError("The export could not create its file: " + err.Error()))
				return
			}
			s.runExport(nil, &exportSrc{name: n.Label, rows: b.Rows, total: -1}, opt, file,
				filepath.Base(path), func() { _ = os.Remove(path) })
		})
	}()
}
