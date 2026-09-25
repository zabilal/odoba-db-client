package shell

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Copying a table's rows into another table, here or on another
// connection (FR-10.9).
//
// It is the import with a table where the file was, so the form asks what
// the import's asks: where the rows go, and whether they are added to what
// is there or replace it. What it can say that the import cannot is which
// of the source's columns the destination has nowhere to put — both ends
// are known before anything is copied, so it says so first.

// copyTimeout bounds reading a connection's tables and opening either end.
const copyTimeout = 30 * time.Second

// canCopyRows reports whether the selection is rows that could be copied.
func (s *Shell) canCopyRows() bool {
	conn, n, ok := s.Explorer.SelectedNode()
	if !ok || !n.Browsable {
		return false
	}
	_, open := s.d.WS.Get(conn)
	return open
}

// copyForm is the Copy Rows sheet.
type copyForm struct {
	s        *Shell
	fromConn string
	fromNode model.Node
	from     *app.BrowseSource

	conns   *widget.Select
	tables  *widget.Select
	replace *widget.Check
	note    *widget.Label
	dlg     dialog.Dialog

	connIDs []string
	nodes   []model.Node
	toConn  string
	to      *app.BrowseSource
}

// copyRowsFrom opens the form over the selected object.
func (s *Shell) copyRowsFrom() *copyForm {
	conn, n, ok := s.Explorer.SelectedNode()
	if !ok || !n.Browsable {
		return nil
	}
	f := &copyForm{s: s, fromConn: conn, fromNode: n, note: widget.NewLabel("")}
	f.note.Wrapping = fyne.TextWrapWord
	f.tables = widget.NewSelect(nil, func(string) { f.chooseTable() })
	f.tables.PlaceHolder = "Reading the tables…"
	f.replace = widget.NewCheck("Replace the rows that are there", nil)

	open := s.d.WS.OpenIDs()
	names := make([]string, 0, len(open))
	for _, id := range open {
		c, ok := s.d.Conns.Get(id)
		if !ok {
			continue
		}
		f.connIDs = append(f.connIDs, id)
		names = append(names, c.Name)
	}
	f.conns = widget.NewSelect(names, func(string) { f.chooseConnection() })

	d := dialog.NewForm("Copy the rows of “"+n.Label+"”", "Copy", "Cancel", []*widget.FormItem{
		widget.NewFormItem("Into", f.conns),
		widget.NewFormItem("Table", f.tables),
		widget.NewFormItem("", f.replace),
		widget.NewFormItem("", f.note),
	}, func(ok bool) {
		if ok {
			f.start()
		}
	}, s.win)
	d.Resize(fyne.NewSize(520, d.MinSize().Height))
	f.dlg = d
	f.say("Reading the columns of “" + n.Label + "”…")
	d.Show()

	f.openSource()
	if i := indexOf(f.connIDs, conn); i >= 0 {
		f.conns.SetSelectedIndex(i) // the connection it is on, to begin with
	}
	return f
}

func indexOf(ids []string, id string) int {
	for i, s := range ids {
		if s == id {
			return i
		}
	}
	return -1
}

func (f *copyForm) say(text string) { f.note.SetText(text) }

// openSource opens the rows being copied, for their columns.
func (f *copyForm) openSource() {
	s, connID, ref := f.s, f.fromConn, f.fromNode.Ref
	go func() {
		ctx, cancel := context.WithTimeout(s.ctx, copyTimeout)
		defer cancel()
		b, err := browseFor(ctx, s, connID, ref)
		s.d.Run(func() {
			if err != nil {
				f.say("The rows to copy could not be read: " + err.Error())
				return
			}
			f.from = b
			f.describe()
		})
	}()
}

// browseFor opens one object's rows on a connection.
func browseFor(ctx context.Context, s *Shell, connID string, ref model.ObjectRef) (*app.BrowseSource, error) {
	live, err := s.d.WS.Connect(ctx, connID)
	if err != nil {
		return nil, err
	}
	return app.NewBrowseSource(ctx, live.Source, ref, source.BrowseOptions{})
}

// chooseConnection lists the tables of the connection chosen.
func (f *copyForm) chooseConnection() {
	i := f.conns.SelectedIndex()
	if i < 0 || i >= len(f.connIDs) {
		return
	}
	f.toConn, f.to, f.nodes = f.connIDs[i], nil, nil
	f.tables.Options, f.tables.PlaceHolder = nil, "Reading the tables…"
	f.tables.ClearSelected()
	f.describe()

	s, connID := f.s, f.toConn
	go func() {
		ctx, cancel := context.WithTimeout(s.ctx, copyTimeout)
		defer cancel()
		var (
			nodes []model.Node
			all   bool
		)
		live, err := s.d.WS.Connect(ctx, connID)
		if err == nil {
			nodes, all, err = app.Tables(ctx, live.Source, app.TableLimit)
		}
		s.d.Run(func() {
			if f.toConn != connID {
				return // another connection was chosen while this was read
			}
			if err != nil {
				f.tables.PlaceHolder = "The tables could not be read"
				f.tables.Refresh()
				f.say("The tables of that connection could not be read: " + err.Error())
				return
			}
			f.nodes = nodes
			labels := make([]string, len(nodes))
			for i, n := range nodes {
				labels[i] = tableLabel(n)
			}
			f.tables.Options = labels
			f.tables.PlaceHolder = "Choose a table"
			if len(labels) == 0 {
				f.tables.PlaceHolder = "Nothing here takes rows"
			}
			f.tables.Refresh()
			if !all {
				f.say(fmt.Sprintf("The first %s are listed; there are more.", nounCount(len(nodes), "table")))
			}
		})
	}()
}

// tableLabel names a table in the list by where it is, so that two of the
// same name in two schemas can be told apart.
func tableLabel(n model.Node) string {
	if len(n.Ref.Path) > 1 {
		return strings.Join(n.Ref.Path, ".")
	}
	return n.Label
}

// chooseTable opens the destination, for its columns.
func (f *copyForm) chooseTable() {
	i := f.tables.SelectedIndex()
	if i < 0 || i >= len(f.nodes) {
		return
	}
	f.to = nil
	ref := f.nodes[i].Ref
	f.say("Reading the columns of “" + tableLabel(f.nodes[i]) + "”…")

	s, connID := f.s, f.toConn
	go func() {
		ctx, cancel := context.WithTimeout(s.ctx, copyTimeout)
		defer cancel()
		b, err := browseFor(ctx, s, connID, ref)
		s.d.Run(func() {
			if f.toConn != connID {
				return
			}
			if err != nil {
				f.say("That table could not be read: " + err.Error())
				return
			}
			f.to = b
			f.describe()
		})
	}()
}

// describe says what would be copied and what would be left behind.
func (f *copyForm) describe() {
	switch {
	case f.from == nil:
		f.say("Reading the columns of “" + f.fromNode.Label + "”…")
	case f.to == nil:
		f.say("Choose where the rows go.")
	case !f.to.CanCopyRowsInto():
		f.say("That connection cannot have rows loaded into it.")
	default:
		pairs, _ := app.CopyPairs(f.from, f.to)
		if len(pairs) == 0 {
			f.say("None of the columns of “" + f.fromNode.Label + "” matches one there by name, so there is nothing to copy.")
			return
		}
		said := nounCount(len(pairs), "column") + " matched by name"
		if left := app.CopyLeftBehind(f.from, f.to); len(left) > 0 {
			said += ". Left behind: " + strings.Join(left, ", ")
		}
		f.say(said + ".")
	}
}

// start asks about production and replacing, then copies.
func (f *copyForm) start() {
	if f.from == nil || f.to == nil {
		f.s.showError(formError("Choose where the rows go first."))
		return
	}
	opt := app.CopyOptions{Replace: f.replace.Checked}
	probe, err := f.to.Plan(f.s.ctx, source.Changeset{Target: f.to.Ref()})
	if err != nil {
		f.s.showError(err)
		return
	}
	if !probe.Guarded && !opt.Replace {
		f.run(opt)
		return
	}
	c, _ := f.s.d.Conns.Get(f.toConn)
	where := "“" + f.to.Ref().Name() + "”"
	if probe.Guarded {
		where += " on “" + c.Name + "”, which is marked Production"
	}
	title, say, act := "Copy into Production?",
		fmt.Sprintf("The rows of “%s” go into %s. Nothing has been written yet.", f.fromNode.Label, where), "Copy"
	if opt.Replace {
		title, act = "Replace Every Row?", "Replace"
		say = fmt.Sprintf("The rows of “%s” replace every row of %s. If any of them would not go in, "+
			"the table is left as it was. Nothing has been written yet.", f.fromNode.Label, where)
	}
	d := dialog.NewConfirm(title, say, func(yes bool) {
		if yes {
			opt.Confirmed = true
			f.run(opt)
		}
	}, f.s.win)
	d.SetConfirmText(act)
	d.SetDismissText("Cancel")
	d.SetConfirmImportance(widget.DangerImportance)
	d.Show()
}

// run copies the rows as a task, so that the window stays usable and the
// copy can be stopped.
func (f *copyForm) run(opt app.CopyOptions) {
	s, from, to := f.s, f.from, f.to
	name := f.fromNode.Label + " → " + to.Ref().Name()
	ctx, cancel := context.WithCancel(s.ctx)
	k := s.startTask(nil, "Copying "+name, cancel)
	go func() {
		defer cancel()
		got, err := app.CopyRows(ctx, from, to, opt, func(p app.Copied) {
			s.d.Run(func() { s.progressTask(k, group(p.Rows)+" rows read", -1) })
		})
		s.d.Run(func() {
			switch {
			case errors.Is(err, context.Canceled):
				s.endTask(k, taskCancelled, "Cancelled after "+nounCount(int(got.Written), "row"))
			case err != nil:
				s.endTask(k, taskFailed, err.Error())
				s.showError(fmt.Errorf("could not copy the rows of %s: %w", f.fromNode.Label, err))
			default:
				done := "Copied " + nounCount(int(got.Written), "row") + " into " + to.Ref().Name()
				s.endTask(k, taskDone, done)
				s.say(nil, done)
			}
			s.rereadObject(f.toConn, to.Ref())
		})
	}()
}

// rereadObject reads the rows again in any tab showing the object written
// to, so that a table in front does not go on showing what was there.
func (s *Shell) rereadObject(connID string, ref model.ObjectRef) {
	for _, t := range s.open {
		if t.connID == connID && t.browse != nil && t.ref.Equal(ref) {
			s.reload(t)
		}
	}
}
