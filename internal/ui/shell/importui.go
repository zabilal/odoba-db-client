package shell

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/export"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/transfer"
	"github.com/ikigai-db/ikigai-db/internal/ui/filedlg"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
	"github.com/ikigai-db/ikigai-db/internal/ui/uithread"
)

// Importing a file into a table (FR-10.4, FR-10.5, ADR-0047). A panel in a
// tab of its own says how the file is read, as found from it and for a
// person to correct; which of its columns fills which of the table's; and
// its first rows as the table would take them, with what would not go in;
// and, on asking, what of the whole file would not go in; and then its rows
// written into the table.

// previewRows is how many of a file's rows the panel shows.
const previewRows = 20

// importFiles are the kinds of file an import offers.
var importFiles = []string{"csv", "tsv", "txt", "json", "ndjson", "jsonl", "xlsx"}

var (
	importFormats   = []transfer.Format{transfer.CSV, transfer.TSV, transfer.JSON, transfer.NDJSON, transfer.XLSX}
	importEncodings = []transfer.Encoding{transfer.UTF8, transfer.UTF16LE, transfer.UTF16BE, transfer.Windows1252}
	importCommas    = []rune{',', '\t', ';', '|'}
	commaNames      = []string{"Comma", "Tab", "Semicolon", "Pipe"}
)

// maxProblems is how many values that would not go in a dry run lists.
const maxProblems = 1000

// dryRunIntro is what the Dry Run tab says before a dry run.
const dryRunIntro = "A dry run reads every row of the file as the import would write it, " +
	"and lists each value that would not go in. Nothing is written."

// notImported is the choice of no file column for a table column.
const notImported = "— not imported —"

// importPanel is an import's tab: the file, how it is read, and the
// mapping of its columns to the table's.
type importPanel struct {
	s     *Shell
	t     *tab // the import's own tab
	into  *tab // the table's tab
	f     *os.File
	size  int64
	opt   transfer.Options
	from  []model.ColumnDef // the file's columns, as last read
	rows  []model.Row       // the file's first rows, as last read
	to    []model.ColumnDef // the table's columns a file can fill
	cols  []model.Column    // the table's columns, as described
	pairs map[string]int    // each table column's file column, by its place; -1 for none

	format, encoding, comma *widget.Select
	header                  *widget.Check
	sheet                   *widget.Entry
	mapping                 *fyne.Container
	preview                 *fyne.Container
	grid                    *grid.TableGrid
	seq                     int // numbers reads, so that only the latest lands

	// The dry run: its button, the tab it answers in, what it says there
	// and the values it lists. checks numbers the findings, so that a dry
	// run the import's changes overtook lands nowhere; running is the one
	// under way.
	dry      *widget.Button
	tabs     *container.AppTabs
	problems *fyne.Container
	summary  *widget.Label
	checked  *grid.TableGrid
	checks   int
	running  *task

	// The import itself: its button, the task writing the rows, and how
	// many rows the file has, as the last dry run counted them, or -1.
	load      *widget.Button
	importing *task
	known     int64
}

// canImport reports whether the tab in front is a table rows can be
// imported into: its columns known, on a connection that edits. Only a
// table's tab has a table's description (fkeys.go).
func (s *Shell) canImport() bool {
	t := s.activeTab()
	if t == nil || t.table == nil {
		return false
	}
	_, readOnly := s.envOf(t)
	return !readOnly
}

// showImport asks which file to import into the table in front.
func (s *Shell) showImport() {
	if !s.canImport() {
		return
	}
	into := s.activeTab()
	s.d.Files.Open(s.win, filedlg.Options{Message: "Import into “" + into.item.Text + "”", Extensions: importFiles,
		Kind: "Data", Accept: "Import"}, func(path string, err error) { s.importFrom(into, path, err) })
}

// importFrom opens a file chosen for a table, finds how to read it off the
// UI goroutine, and shows the import in a tab of its own.
func (s *Shell) importFrom(into *tab, path string, err error) {
	switch {
	case err != nil:
		s.showError(err)
		return
	case path == "":
		return // cancelled
	}
	go func() {
		f, err := os.Open(path)
		var size int64
		var opt transfer.Options
		if err == nil {
			var st os.FileInfo
			if st, err = f.Stat(); err == nil {
				size = st.Size()
				opt, err = transfer.Detect(f, size, path)
			}
		}
		s.d.Run(func() {
			if err != nil {
				if f != nil {
					f.Close()
				}
				s.showError(fmt.Errorf("could not read %s: %w", filepath.Base(path), err))
				return
			}
			s.openImport(into, f, size, opt)
		})
	}()
}

// openImport shows an import in a tab of its own, or brings forward the tab
// already importing that file into that table.
func (s *Shell) openImport(into *tab, f *os.File, size int64, opt transfer.Options) {
	key := "import:" + into.key + ":" + f.Name()
	if t := s.tabFor(key); t != nil {
		f.Close()
		s.selectTab(t)
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	t := &tab{key: key, connID: into.connID, ref: into.ref, ctx: ctx, cancel: cancel,
		body: container.NewStack(), footer: widget.NewLabel("Reading…")}
	t.footer.Importance = widget.LowImportance
	t.footer.Wrapping = fyne.TextWrapWord
	p := &importPanel{s: s, t: t, into: into, f: f, size: size, opt: opt, pairs: map[string]int{},
		mapping: container.NewVBox(), preview: container.NewStack()}
	for _, c := range into.table.Columns {
		if c.Generated != "" {
			continue // the database fills it
		}
		p.to = append(p.to, model.ColumnDef{Name: c.Name, Type: c.Type})
	}
	p.cols = into.table.Columns
	t.imp = p
	go func() { <-ctx.Done(); f.Close() }() // the file is open for as long as its tab is

	p.format = widget.NewSelect(optionNames(importFormats), nil)
	p.format.SetSelectedIndex(slices.Index(importFormats, opt.Format))
	p.encoding = widget.NewSelect(optionNames(importEncodings), nil)
	p.encoding.SetSelectedIndex(slices.Index(importEncodings, opt.Encoding))
	p.comma = widget.NewSelect(commaNames, nil)
	p.comma.SetSelectedIndex(max(slices.Index(importCommas, opt.Comma), 0))
	p.header = widget.NewCheck("First row names the columns", nil)
	p.header.SetChecked(opt.Header)
	p.sheet = widget.NewEntry()
	p.sheet.SetPlaceHolder("the first sheet")
	p.sheet.SetText(opt.Sheet)
	changed := func() { p.changed() }
	p.format.OnChanged = func(string) { changed() }
	p.encoding.OnChanged = func(string) { changed() }
	p.comma.OnChanged = func(string) { changed() }
	p.header.OnChanged = func(bool) { changed() }
	p.sheet.OnSubmitted = func(string) { changed() }
	p.enableOptions()

	options := widget.NewForm(widget.NewFormItem("Format", p.format), widget.NewFormItem("Encoding", p.encoding),
		widget.NewFormItem("Delimiter", p.comma), widget.NewFormItem("", p.header), widget.NewFormItem("Sheet", p.sheet))
	p.dry = widget.NewButton("Dry Run", p.dryRun)
	p.load = widget.NewButton("Import", p.startImport)
	p.load.Importance = widget.HighImportance
	p.summary = widget.NewLabel("")
	p.summary.Wrapping = fyne.TextWrapWord
	p.problems = container.NewStack()
	p.tabs = container.NewAppTabs(container.NewTabItem("First Rows", p.preview),
		container.NewTabItem("Dry Run", container.NewBorder(p.summary, nil, nil, nil, p.problems)))
	p.forget()
	split := container.NewVSplit(container.NewVScroll(p.mapping), p.tabs)
	split.Offset = 0.45
	actions := container.NewHBox(layout.NewSpacer(), p.dry, p.load)
	t.body.Objects = []fyne.CanvasObject{container.NewBorder(options, actions, nil, nil, split)}
	t.item = container.NewTabItem("Import "+filepath.Base(f.Name())+" into "+into.item.Text,
		container.NewBorder(nil, t.footer, nil, nil, t.body))
	s.open = append(s.open, t)
	s.addTab(t)
	s.sync()
	p.read()
}

// optionNames are values' names, as a picker lists them.
func optionNames[T fmt.Stringer](vals []T) []string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = v.String()
	}
	return out
}

// enableOptions offers the options a format has: a delimiter and an
// encoding for text, a sheet and a header for a workbook, and a header for
// delimited text.
func (p *importPanel) enableOptions() {
	f := p.opt.Format
	text, delimited := f != transfer.XLSX, f == transfer.CSV || f == transfer.TSV
	setWidgetEnabled(p.encoding, text)
	setWidgetEnabled(p.comma, delimited)
	setWidgetEnabled(p.header, delimited || f == transfer.XLSX)
	setWidgetEnabled(p.sheet, f == transfer.XLSX)
}

func setWidgetEnabled(w fyne.Disableable, on bool) {
	if on {
		w.Enable()
	} else {
		w.Disable()
	}
}

// changed reads the options as they are set, and reads the file again.
func (p *importPanel) changed() {
	p.forget()
	p.opt = transfer.Options{
		Format:   importFormats[max(p.format.SelectedIndex(), 0)],
		Encoding: importEncodings[max(p.encoding.SelectedIndex(), 0)],
		Comma:    importCommas[max(p.comma.SelectedIndex(), 0)],
		Header:   p.header.Checked,
		Sheet:    p.sheet.Text,
	}
	if p.opt.Format == transfer.TSV {
		p.opt.Comma = '\t'
	}
	p.enableOptions()
	p.read()
}

// read reads the file's first rows off the UI goroutine, as the options
// say, and shows them; only the latest read lands. A file whose columns
// changed has its columns paired afresh.
func (p *importPanel) read() {
	p.seq++
	seq, opt, t := p.seq, p.opt, p.t
	t.footer.SetText("Reading…")
	go func() {
		var cols []model.ColumnDef
		var rows []model.Row
		rs, err := transfer.Open(p.f, p.size, opt)
		if err == nil {
			cols = rs.Columns()
			for len(rows) < previewRows {
				row, rerr := rs.Next(t.ctx)
				if rerr != nil {
					if !errors.Is(rerr, io.EOF) {
						err = rerr
					}
					break
				}
				rows = append(rows, row)
			}
			rs.Close()
		}
		p.s.d.Run(func() {
			if seq != p.seq || t.ctx.Err() != nil {
				return
			}
			if err != nil {
				t.footer.SetText("Could not read the file as " + opt.Format.String() + ": " + err.Error())
				p.rows = nil
				p.preview.Objects = nil
				p.preview.Refresh()
				return
			}
			if !sameColumns(cols, p.from) {
				p.from = cols
				p.suggest()
				p.showMapping()
			}
			p.rows = rows
			p.showPreview()
		})
	}()
}

func sameColumns(a, b []model.ColumnDef) bool {
	return slices.EqualFunc(a, b, func(x, y model.ColumnDef) bool { return x.Name == y.Name })
}

// suggest pairs the file's columns with the table's by name.
func (p *importPanel) suggest() {
	p.pairs = map[string]int{}
	for _, c := range p.to {
		p.pairs[c.Name] = -1
	}
	for _, pr := range transfer.Suggest(p.from, p.to) {
		p.pairs[pr.To] = pr.From
	}
}

// showMapping lists the table's columns, each with the file column that
// fills it, to be changed.
func (p *importPanel) showMapping() {
	choices := []string{notImported}
	for _, c := range p.from {
		choices = append(choices, c.Name)
	}
	form := widget.NewForm()
	for _, c := range p.to {
		name := c.Name
		pick := widget.NewSelect(choices, nil)
		pick.SetSelectedIndex(p.pairs[name] + 1)
		pick.OnChanged = func(string) {
			p.pairs[name] = pick.SelectedIndex() - 1
			p.forget()
			p.showPreview()
		}
		hint := c.Type.Native
		if hint == "" {
			hint = c.Type.Class.String()
		}
		if !c.Type.Nullable {
			hint += " · needs a value"
		}
		form.AppendItem(&widget.FormItem{Text: name, Widget: pick, HintText: hint})
	}
	p.mapping.Objects = []fyne.CanvasObject{form}
	p.mapping.Refresh()
}

// pairList is the pairs in the table's column order, those with a file
// column alone.
func (p *importPanel) pairList() ([]transfer.Pair, []model.ColumnDef) {
	var pairs []transfer.Pair
	var cols []model.ColumnDef
	for _, c := range p.to {
		if from := p.pairs[c.Name]; from >= 0 {
			pairs = append(pairs, transfer.Pair{From: from, To: c.Name})
			cols = append(cols, c)
		}
	}
	return pairs, cols
}

// showPreview shows the file's first rows as the table would take them. A
// value that would not go in is shown as the file has it, and the first is
// said, with how many rows have one.
func (p *importPanel) showPreview() {
	pairs, cols := p.pairList()
	if len(pairs) == 0 {
		p.t.footer.SetText("No column of the file fills one of the table's: pick which fills which.")
		p.preview.Objects = nil
		p.preview.Refresh()
		return
	}
	to := map[string]model.ColumnDef{}
	for _, c := range p.to {
		to[c.Name] = c
	}
	at := map[string]int{}
	for i, pr := range pairs {
		at[pr.To] = i
	}
	var rows []model.Row
	bad := 0
	var first *transfer.CellError
	for _, r := range p.rows {
		vals, errs := transfer.Coerce(r, p.from, pairs, to)
		for _, e := range errs {
			vals[at[e.Column]] = e.Value // as the file has it
			if first == nil {
				e := e
				first = &e
			}
		}
		if len(errs) > 0 {
			bad++
		}
		rows = append(rows, vals)
	}
	shown := make([]model.ColumnDef, len(cols))
	for i, c := range cols {
		shown[i] = c
		shown[i].Type.Nullable = true // a value shown as the file has it may be anything
	}
	m := grid.NewModel(rowsFetcher{cols: shown, rows: rows})
	p.grid = grid.NewTableGridWith(p.t.ctx, m, p.s.colours(), p.s.d.Run, p.s.d.Delay)
	p.preview.Objects = []fyne.CanvasObject{p.grid.View()}
	p.preview.Refresh()
	var say string
	switch {
	case len(rows) == 0:
		say = "The file has no rows."
	case bad == 0 && len(rows) == 1:
		say = "The first row goes in as shown."
	case bad == 0:
		say = fmt.Sprintf("The first %d rows go in as shown.", len(rows))
	default:
		verb := "has"
		if bad > 1 {
			verb = "have"
		}
		say = fmt.Sprintf("%d of the first %s %s a value that would not go in; the first: %v",
			bad, nounCount(len(rows), "row"), verb, first)
	}
	if miss := transfer.Unfilled(p.cols, pairs); len(miss) > 0 {
		say = unfilledText(miss) + " " + say
	}
	p.t.footer.SetText(say)
}

// unfilledText says which columns need a value no file column gives.
func unfilledText(names []string) string {
	if len(names) == 1 {
		return names[0] + " needs a value, and no column of the file fills it."
	}
	return strings.Join(names, ", ") + " need a value, and no column of the file fills them."
}

// forget stops a dry run under way and lets go of the last one's findings:
// they were for the options and the mapping as they were.
func (p *importPanel) forget() {
	p.checks++
	if p.running != nil {
		p.s.stopTask(p.running)
		p.running = nil
	}
	p.known = -1
	p.buttons()
	p.showChecked(dryRunIntro, nil)
}

// buttons offers Dry Run and Import while neither is under way.
func (p *importPanel) buttons() {
	idle := p.running == nil && p.importing == nil
	setWidgetEnabled(p.dry, idle)
	setWidgetEnabled(p.load, idle)
}

// refused says, of a mapping that gives no value to a column that needs one,
// that every row would be refused.
func (p *importPanel) refused(pairs []transfer.Pair) bool {
	miss := transfer.Unfilled(p.cols, pairs)
	if len(miss) > 0 {
		p.showChecked("Every row would be refused: "+unfilledText(miss), nil)
		p.tabs.SelectIndex(1)
	}
	return len(miss) > 0
}

// dryRun reads every row of the file as the import would write it, writing
// nothing, as a task in the task centre (FR-10.5, ADR-0048). What would not
// go in is listed under Dry Run, with the row it is in.
func (p *importPanel) dryRun() {
	pairs, _ := p.pairList()
	if len(pairs) == 0 || p.running != nil || p.importing != nil || p.refused(pairs) {
		return // nothing to write, something under way, or nothing would go in
	}
	s, t := p.s, p.t
	to := map[string]model.ColumnDef{}
	for _, c := range p.to {
		to[c.Name] = c
	}
	opt, seq := p.opt, p.checks
	ctx, cancel := context.WithCancel(t.ctx)
	k := s.startTask(t, "Dry run of "+filepath.Base(p.f.Name()), cancel)
	p.running = k
	p.buttons()
	var mu sync.Mutex
	var latest transfer.Checked
	update := uithread.Coalesce(s.d.Run, s.d.Delay, func() {
		mu.Lock()
		c := latest
		mu.Unlock()
		s.progressTask(k, checkedStatus(c), -1)
	})
	go func() {
		var c transfer.Checked
		rs, err := transfer.Open(p.f, p.size, opt)
		if err == nil {
			c, err = transfer.Check(ctx, rs, pairs, to, maxProblems, func(c transfer.Checked) {
				mu.Lock()
				latest = c
				mu.Unlock()
				update()
			})
			rs.Close()
		}
		close(k.finished)
		s.d.Run(func() {
			stopped := ctx.Err() != nil
			cancel()
			if seq != p.checks {
				s.endTask(k, taskCancelled, "Stopped, as the import changed")
				return
			}
			p.running = nil
			p.buttons()
			var say string
			state := taskDone
			switch {
			case stopped:
				state, say = taskCancelled, fmt.Sprintf("Cancelled after %s; nothing was written.", nounCount(int(c.Rows), "row"))
			case err != nil:
				state, say = taskFailed, fmt.Sprintf("Could not read the file past %s: %v", nounCount(int(c.Rows), "row"), err)
			default:
				say, p.known = checkedSummary(c), c.Rows
			}
			s.endTask(k, state, say)
			p.showChecked(say, c.Problems)
			p.tabs.SelectIndex(1)
		})
	}()
}

// checkedStatus is how far a dry run has got, as the task centre says it.
func checkedStatus(c transfer.Checked) string {
	say := group(c.Rows) + " rows read"
	if c.Bad > 0 {
		say += ", " + group(c.Bad) + " would not go in"
	}
	return say
}

// checkedSummary is what a dry run found.
func checkedSummary(c transfer.Checked) string {
	switch {
	case c.Rows == 0:
		return "The file has no rows."
	case c.Bad == 0 && c.Rows == 1:
		return "The file's one row would go in."
	case c.Bad == 0:
		return "All " + group(c.Rows) + " rows would go in."
	}
	say := fmt.Sprintf("%s of %s would not go in.", group(c.Bad), nounCount(int(c.Rows), "row"))
	if n := int64(len(c.Problems)); n < c.Values {
		say += fmt.Sprintf(" The first %s of %s values that would not are listed.", group(n), group(c.Values))
	}
	return say
}

// showChecked says what a dry run found, and lists the values that would
// not go in: the row each is in, its column, the value and why.
func (p *importPanel) showChecked(say string, problems []transfer.Problem) {
	p.summary.SetText(say)
	p.checked = nil
	p.problems.Objects = nil
	if len(problems) > 0 {
		cols := []model.ColumnDef{
			{Name: "Row", Type: model.DataType{Class: model.TypeInteger, Nullable: true}},
			{Name: "Column", Type: model.DataType{Class: model.TypeString, Nullable: true}},
			{Name: "Value", Type: model.DataType{Class: model.TypeString, Nullable: true}},
			{Name: "Why", Type: model.DataType{Class: model.TypeString, Nullable: true}},
		}
		rows := make([]model.Row, len(problems))
		for i, pr := range problems {
			rows[i] = model.Row{pr.Row, pr.Column, pr.Value, pr.Err.Error()}
		}
		p.checked = grid.NewTableGridWith(p.t.ctx, grid.NewModel(rowsFetcher{cols: cols, rows: rows}),
			p.s.colours(), p.s.d.Run, p.s.d.Delay)
		p.problems.Objects = []fyne.CanvasObject{p.checked.View()}
	}
	p.problems.Refresh()
}

// startImport writes the file's rows into the table (FR-10.6, ADR-0049). On
// a production connection it asks first, as a commit does (FR-4.9): the
// driver's own guard says whether it must, of a plan of no rows.
func (p *importPanel) startImport() {
	pairs, _ := p.pairList()
	if len(pairs) == 0 || p.running != nil || p.importing != nil || p.refused(pairs) {
		return
	}
	s, into := p.s, p.into
	probe, err := into.browse.Plan(p.t.ctx, source.Changeset{Target: into.ref})
	if err != nil {
		s.showError(err)
		return
	}
	if !probe.Guarded {
		p.runImport(pairs, false)
		return
	}
	c, _ := s.d.Conns.Get(into.connID)
	d := dialog.NewConfirm("Import into Production?",
		fmt.Sprintf("The rows of %s go into “%s” on “%s”, which is marked Production. Nothing has been written yet.",
			filepath.Base(p.f.Name()), into.item.Text, c.Name),
		func(yes bool) {
			if yes {
				p.runImport(pairs, true)
			}
		}, s.win)
	d.SetConfirmText("Import")
	d.SetDismissText("Cancel")
	d.SetConfirmImportance(widget.DangerImportance)
	d.Show()
}

// runImport writes the rows as a task in the task centre, reading the file
// again from its start with the options and the mapping as they are. How
// long is left is said where a dry run counted the rows (FR-10.7). The
// table's tab reads its rows again once any are written.
func (p *importPanel) runImport(pairs []transfer.Pair, confirmed bool) {
	s, t, into := p.s, p.t, p.into
	to := map[string]model.ColumnDef{}
	for _, c := range p.to {
		to[c.Name] = c
	}
	opt, total := p.opt, p.known
	ctx, cancel := context.WithCancel(t.ctx)
	k := s.startTask(t, "Import "+filepath.Base(p.f.Name())+" into "+into.item.Text, cancel)
	p.importing = k
	p.buttons()
	var mu sync.Mutex
	var latest transfer.Loaded
	update := uithread.Coalesce(s.d.Run, s.d.Delay, func() {
		mu.Lock()
		l := latest
		mu.Unlock()
		frac := -1.0
		if total > 0 {
			frac = min(1, float64(l.Written)/float64(total))
		}
		s.progressTask(k, exportStatus(export.Progress{Rows: l.Written, Elapsed: l.Elapsed}, total), frac)
	})
	go func() {
		var l transfer.Loaded
		rs, err := transfer.Open(p.f, p.size, opt)
		if err == nil {
			l, err = transfer.Load(ctx, rs, pairs, to, into.ref, into.browse, transfer.LoadOptions{Confirmed: confirmed},
				func(l transfer.Loaded) {
					mu.Lock()
					latest = l
					mu.Unlock()
					update()
				})
			rs.Close()
		}
		close(k.finished)
		s.d.Run(func() {
			stopped := ctx.Err() != nil
			cancel()
			p.importing = nil
			p.buttons()
			if l.Written > 0 && into.ctx.Err() == nil {
				s.reload(into)
			}
			state, say := importEnd(l, err, stopped, into.item.Text)
			s.endTask(k, state, say)
			t.footer.SetText(say)
			if state == taskFailed {
				s.showError(formError(say))
			}
		})
	}()
}

// importEnd says how an import ended, and what it left written.
func importEnd(l transfer.Loaded, err error, stopped bool, table string) (taskState, string) {
	var le *transfer.LoadError
	switch {
	case stopped:
		return taskCancelled, "Cancelled." + wroteText(l.Written, true)
	case errors.As(err, &le):
		return taskFailed, fmt.Sprintf("Stopped at row %s: %v.", group(le.Row), le.Err) + wroteText(l.Written, le.Undone)
	case err != nil:
		return taskFailed, "Not imported: " + err.Error() + "." + wroteText(l.Written, true)
	}
	return taskDone, fmt.Sprintf("Imported %s into %s.", nounCount(int(l.Written), "row"), table)
}

// wroteText says what an import that stopped left written: the batches
// before the one it stopped in, and of that one nothing, unless the server
// could not undo it.
func wroteText(written int64, undone bool) string {
	switch {
	case !undone && written == 0:
		return " The server could not undo the rows before it, so some may have been written."
	case !undone:
		return " " + firstWritten(written) + "; the server could not undo the rows after them, so some may have been too."
	case written == 0:
		return " Nothing was written."
	}
	return " " + firstWritten(written) + ", and none after."
}

// firstWritten says the first n rows were written.
func firstWritten(n int64) string {
	if n == 1 {
		return "The first row was written"
	}
	return "The first " + group(n) + " rows were written"
}

// rowsFetcher serves rows held in memory, for a preview.
type rowsFetcher struct {
	cols []model.ColumnDef
	rows []model.Row
}

func (f rowsFetcher) Columns() []model.ColumnDef { return f.cols }

func (f rowsFetcher) Fetch(_ context.Context, offset, limit int64) ([]model.Row, error) {
	n := int64(len(f.rows))
	if offset >= n {
		return nil, nil
	}
	return f.rows[offset:min(offset+limit, n)], nil
}

func (f rowsFetcher) Count(context.Context) (int64, error) { return int64(len(f.rows)), nil }
