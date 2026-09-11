package shell

import (
	"slices"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/app/filterexpr"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// Foreign-key navigation (FR-3.11, ADR-0040). A table's foreign keys come
// from its description, read once when its tab opens. From a cell in a
// foreign key's columns, Go to Referenced Row opens the table the key
// refers to, filtered to the row with the cell's row's values in the key.

// describe reads a table tab's description off the UI goroutine, for its
// foreign keys, and the keys of other tables that refer to it (referring.go).
// A source that cannot describe it leaves the tab without either.
func (s *Shell) describe(t *tab) {
	go func() {
		live, err := s.d.WS.Connect(t.ctx, t.connID)
		var desc any
		var refs []model.Referrer
		if err == nil {
			desc, err = app.Describe(t.ctx, live.Source, t.ref)
		}
		if _, table := desc.(*model.Table); table && err == nil {
			refs, _ = app.Referrers(t.ctx, live.Source, t.ref) // none, where they cannot be listed
		}
		s.d.Run(func() {
			if tbl, ok := desc.(*model.Table); ok && err == nil && t.ctx.Err() == nil {
				t.table, t.referrers = tbl, refs
				s.startLabels(t) // labels.go
				s.sync()
			}
		})
	}()
}

// reference is the foreign key the active cell of the table in front is in,
// and the values its row has in the key's columns, as the grid shows them.
// ok is false where there is none: a query, a table not yet described, a
// column in no key, a row not loaded, or a value that is NULL.
func (s *Shell) reference() (t *tab, fk model.ForeignKey, vals []any, ok bool) {
	t = s.activeTab()
	if t == nil || t.query != nil || t.table == nil || t.grid == nil {
		return t, fk, nil, false
	}
	c, has := t.grid.Selection().Active()
	cols := t.model.Columns()
	mc := t.grid.ColumnAt(c.Col)
	if !has || mc < 0 || mc >= len(cols) {
		return t, fk, nil, false
	}
	row, loaded := t.model.Row(t.ctx, int64(c.Row))
	if !loaded || row == nil {
		return t, fk, nil, false
	}
	for _, k := range t.table.ForeignKeys {
		if !slices.Contains(k.Columns, cols[mc].Name) || len(k.RefColumns) != len(k.Columns) {
			continue
		}
		if vals := keyValues(cols, k.Columns, func(i int) any { return t.grid.CellValue(c.Row, row, i) }); vals != nil {
			return t, k, vals, true
		}
	}
	return t, fk, nil, false
}

// keyValues are the values value gives the named columns, or nil if one is
// missing, NULL, or a new row's not given.
func keyValues(cols []model.ColumnDef, names []string, value func(col int) any) []any {
	var vals []any
	for _, name := range names {
		i := slices.IndexFunc(cols, func(c model.ColumnDef) bool { return c.Name == name })
		if i < 0 {
			return nil
		}
		v := value(i)
		if _, given := v.(model.Default); v == nil || given {
			return nil
		}
		vals = append(vals, v)
	}
	return vals
}

func (s *Shell) canGoToReferenced() bool {
	_, _, _, ok := s.reference()
	return ok
}

// goToReferenced opens the table the active cell's foreign key refers to,
// filtered to the row the key names, or filters its tab if it is open.
func (s *Shell) goToReferenced() {
	t, fk, vals, ok := s.reference()
	if !ok {
		return
	}
	filters := make(map[string]string, len(vals))
	for i, v := range vals {
		filters[fk.RefColumns[i]] = filterexpr.Pick([]any{v}, false)
	}
	s.openFiltered(t.connID, referenced(t.ref, fk), filters)
}

// openFiltered opens a table's tab, or brings it forward, filtered to the
// rows with values in columns (filterTo): at once, or once a tab still
// opening has its grid.
func (s *Shell) openFiltered(connID string, ref model.ObjectRef, filters map[string]string) {
	key := view.NodeID(connID, ref)
	to := s.tabFor(key)
	if to == nil {
		s.OpenObject(connID, model.Node{Ref: ref, Label: ref.Name()})
		to = s.tabFor(key)
	} else {
		s.selectTab(to)
	}
	if to == nil {
		return
	}
	if to.grid != nil {
		s.filterTo(to, filters)
		return
	}
	to.then = func() { s.filterTo(to, filters) } // a tab still opening
}

// referenced is the table a foreign key refers to: from's path, with the
// key's table, and its schema where it names one (PostgreSQL's schema,
// MySQL's database, SQLite's main).
func referenced(from model.ObjectRef, fk model.ForeignKey) model.ObjectRef {
	path := slices.Clone(from.Path)
	if len(path) == 0 {
		return model.NewRef(model.KindTable, fk.RefTable)
	}
	path[len(path)-1] = fk.RefTable
	if fk.RefSchema != "" && len(path) >= 2 {
		path[len(path)-2] = fk.RefSchema
	}
	return model.NewRef(model.KindTable, path...)
}

// filterTo filters a table's tab to the rows with values in columns, each in
// the filter row's notation, and clears every other column's filter, so that
// no filter left from before hides the row.
func (s *Shell) filterTo(t *tab, filters map[string]string) {
	if t.browse == nil || !t.browse.CanFilter() {
		t.said = "This table is not filtered here, so the row referred to is not picked out"
		s.showCount(t)
		return
	}
	cols := t.model.Columns()
	for i, c := range cols {
		t.grid.SetFilterText(i, filters[c.Name])
	}
	s.refilter(t, t.grid.FilterTexts())
}
