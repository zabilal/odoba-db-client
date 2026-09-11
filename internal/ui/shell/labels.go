package shell

import (
	"context"
	"slices"
	"sync"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/export"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// Foreign keys' labels (FR-3.12, ADR-0042). Once a table's tab knows its
// foreign keys (fkeys.go), each value of a key of one column is shown with
// the label of the row it refers to: that table's first text column that
// is neither in its primary key nor the column referred to. Labels are read
// as the grid's pages arrive, with one browse per key for the values not
// yet asked about, and kept for as long as the tab is open.

// keyLabels are the labels of one key's values.
type keyLabels struct {
	ref    model.ObjectRef   // the table referred to
	refCol string            // its column the key refers to
	col    model.ColumnDef   // the key's column, for writing its values as text
	label  model.ColumnDef   // the referred table's column that labels a row
	known  map[string]string // each value's label, by the value as text
	asked  map[string]bool   // the values asked about, labelled or not
}

// valueLabels are a table tab's keys' labels, by the model column of each key.
// A tab has them from the moment its grid is attached, before any page can
// arrive; keys are added as their label columns are found.
type valueLabels struct {
	mu   sync.Mutex
	keys map[int]*keyLabels
}

func newValueLabels() *valueLabels { return &valueLabels{keys: map[int]*keyLabels{}} }

// label is a value's label in model column col, if it is known.
func (l *valueLabels) label(col int, v any) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := l.keys[col]
	if k == nil {
		return "", false
	}
	text, ok := k.known[export.Text(v, k.col)]
	return text, ok
}

func (l *valueLabels) empty() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.keys) == 0
}

// startLabels finds, off the UI goroutine, the label column of each table a
// tab's keys of one column refer to, then labels the pages already read.
// From then on the grid asks the tab's labels for every cell it draws.
func (s *Shell) startLabels(t *tab) {
	if t.table == nil || t.grid == nil || t.labels == nil {
		return
	}
	cols := t.model.Columns()
	type want struct {
		mc int
		fk model.ForeignKey
	}
	var wants []want
	for _, fk := range t.table.ForeignKeys {
		if len(fk.Columns) != 1 || len(fk.RefColumns) != 1 {
			continue
		}
		if mc := slices.IndexFunc(cols, func(c model.ColumnDef) bool { return c.Name == fk.Columns[0] }); mc >= 0 {
			wants = append(wants, want{mc, fk})
		}
	}
	if len(wants) == 0 {
		return
	}
	l := t.labels
	t.grid.Labels = l.label
	go func() {
		live, err := s.d.WS.Connect(t.ctx, t.connID)
		if err != nil {
			return
		}
		for _, w := range wants {
			ref := referenced(t.ref, w.fk)
			desc, err := app.Describe(t.ctx, live.Source, ref)
			tbl, ok := desc.(*model.Table)
			if err != nil || !ok {
				continue
			}
			if label, ok := labelColumn(tbl, w.fk.RefColumns[0]); ok {
				l.mu.Lock()
				l.keys[w.mc] = &keyLabels{ref: ref, refCol: w.fk.RefColumns[0], col: cols[w.mc], label: label,
					known: map[string]string{}, asked: map[string]bool{}}
				l.mu.Unlock()
			}
		}
		k := int64(t.model.Added())
		n, _ := t.model.Extent()
		for page := int64(0); page*grid.PageSize < n-k; page++ {
			if t.model.Resident(k + page*grid.PageSize) {
				s.readLabels(t, page)
			}
		}
	}()
}

// labelColumn is a table's first column of text that is neither in its
// primary key nor the column a key refers to: what names one of its rows.
func labelColumn(tbl *model.Table, refCol string) (model.ColumnDef, bool) {
	for _, c := range tbl.Columns {
		inKey := tbl.PrimaryKey != nil && slices.Contains(tbl.PrimaryKey.Columns, c.Name)
		if c.Type.Class == model.TypeString && !inKey && c.Name != refCol {
			return model.ColumnDef{Name: c.Name, Type: c.Type}, true
		}
	}
	return model.ColumnDef{}, false
}

// readLabels reads, off the UI goroutine, the labels of the values of a
// page of rows not yet asked about: one browse of each table referred to,
// of its key and label columns, for those values alone. The grid is drawn
// again once they are in. A tab with no labelled keys reads nothing.
func (s *Shell) readLabels(t *tab, page int64) {
	l := t.labels
	if l == nil || l.empty() {
		return
	}
	k := int64(t.model.Added())
	rows, err := t.model.Read(t.ctx, k+page*grid.PageSize, k+(page+1)*grid.PageSize)
	if err != nil {
		return
	}
	type ask struct {
		kl   *keyLabels
		vals []any
	}
	var asks []ask
	l.mu.Lock()
	for mc, kl := range l.keys {
		var vals []any
		for _, row := range rows {
			if mc >= len(row) || row[mc] == nil {
				continue
			}
			if text := export.Text(row[mc], kl.col); !kl.asked[text] {
				kl.asked[text] = true
				vals = append(vals, row[mc])
			}
		}
		if len(vals) > 0 {
			asks = append(asks, ask{kl, vals})
		}
	}
	l.mu.Unlock()
	if len(asks) == 0 {
		return
	}
	live, err := s.d.WS.Connect(t.ctx, t.connID)
	if err != nil {
		return
	}
	for _, a := range asks {
		found := lookUp(t.ctx, live.Source, a.kl, a.vals)
		l.mu.Lock()
		for text, label := range found {
			a.kl.known[text] = label
		}
		l.mu.Unlock()
	}
	s.d.Run(t.grid.ScheduleRefresh)
}

// lookUp reads the labels of vals in the table a key refers to, by each
// value as text.
func lookUp(ctx context.Context, src source.Source, kl *keyLabels, vals []any) map[string]string {
	bs, err := app.NewBrowseSource(ctx, src, kl.ref, source.BrowseOptions{
		Columns: []string{kl.refCol, kl.label.Name},
		Filters: []source.Filter{{Column: kl.refCol, Op: source.OpIn, Values: vals}},
		Limit:   int64(len(vals)),
	})
	if err != nil {
		return nil
	}
	rows, err := bs.Fetch(ctx, 0, int64(len(vals)))
	if err != nil {
		return nil
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		if len(r) >= 2 && r[1] != nil {
			out[export.Text(r[0], kl.col)] = export.Text(r[1], kl.label)
		}
	}
	return out
}
