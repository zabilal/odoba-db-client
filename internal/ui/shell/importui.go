package shell

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/transfer"
	"github.com/ikigai-db/ikigai-db/internal/ui/filedlg"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// Importing a file into a table (FR-10.4, FR-10.5, ADR-0047). A panel in a
// tab of its own says how the file is read, as found from it and for a
// person to correct; which of its columns fills which of the table's; and
// its first rows as the table would take them, with what would not go in.
// Writing the rows is T2.21's.

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
	to    []model.ColumnDef // the table's columns
	pairs map[string]int    // each table column's file column, by its place; -1 for none

	format, encoding, comma *widget.Select
	header                  *widget.Check
	sheet                   *widget.Entry
	mapping                 *fyne.Container
	preview                 *fyne.Container
	grid                    *grid.TableGrid
	seq                     int // numbers reads, so that only the latest lands
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
		p.to = append(p.to, model.ColumnDef{Name: c.Name, Type: c.Type})
	}
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
	split := container.NewVSplit(container.NewVScroll(p.mapping), p.preview)
	split.Offset = 0.45
	t.body.Objects = []fyne.CanvasObject{container.NewBorder(options, nil, nil, nil, split)}
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
	switch {
	case len(rows) == 0:
		p.t.footer.SetText("The file has no rows.")
	case bad == 0:
		p.t.footer.SetText(fmt.Sprintf("The first %s go in as shown.", nounCount(len(rows), "row")))
	default:
		p.t.footer.SetText(fmt.Sprintf("%d of the first %s have a value that would not go in; the first: %v",
			bad, nounCount(len(rows), "row"), first))
	}
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
