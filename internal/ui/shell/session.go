package shell

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"

	"github.com/ikigai-db/ikigai-db/internal/app/filterexpr"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// restoreTimeout bounds reading the last session at startup.
const restoreTimeout = 2 * time.Second

// restore puts the window back as the last session left it, and reopens
// unsaved query text (NFR-R2, NFR-R3). Both are small and local, so they are
// read before the window first shows, rather than after it has drawn an
// empty one; each tab then opens its own connection, as if opened by hand.
func (s *Shell) restore() {
	if s.d.Session == nil && s.d.Scratch == nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, restoreTimeout)
	defer cancel()
	var (
		last      localdb.Session
		scratches []localdb.Scratch
		problems  []error
	)
	if s.d.Session != nil {
		ss, _, err := s.d.Session.Session(ctx)
		if err != nil {
			problems = append(problems, fmt.Errorf("the last session could not be read back: %w", err))
		}
		last = ss
	}
	if s.d.Scratch != nil {
		list, err := s.d.Scratch.Scratches(ctx)
		if err != nil {
			problems = append(problems, fmt.Errorf(
				"some unsaved query text could not be read back; it is still in the local database: %w", err))
		}
		scratches = list
	}
	saved := map[string]localdb.SavedQuery{}
	if s.d.Saved != nil && (len(scratches) > 0 || len(last.Tabs) > 0) {
		all, err := s.d.Saved.SavedQueries(ctx)
		if err != nil {
			problems = append(problems, fmt.Errorf("saved queries could not be read: %w", err))
		}
		for _, sq := range all {
			saved[sq.ID] = sq
		}
	}

	s.restoring = true
	defer func() { s.restoring = false }()
	if last.Width > 0 && last.Height > 0 {
		s.win.Resize(fyne.NewSize(last.Width, last.Height))
	}
	if last.Sidebar > 0 && last.Sidebar < 1 {
		s.split.SetOffset(last.Sidebar)
	}
	byID := make(map[string]localdb.Scratch, len(scratches))
	for _, sc := range scratches {
		byID[sc.ID] = sc
	}
	used := map[string]bool{}
	opened := make([]*tab, len(last.Tabs))
	for i, st := range last.Tabs {
		opened[i] = s.reopenTab(st, byID, saved, used)
	}
	for _, sc := range scratches { // text whose tab the session does not name: a crash came first
		if !used[sc.ID] {
			s.reopenScratch(sc, saved[sc.SavedID])
		}
	}
	s.reexpand(last.Expanded)
	for i, st := range last.Tabs {
		if st.Pinned && opened[i] != nil {
			s.pin(opened[i], true)
		}
	}
	s.resplit(last, opened)
	if a := last.Active; a >= 0 && a < len(opened) && opened[a] != nil {
		s.selectTab(opened[a])
	}
	if err := errors.Join(problems...); err != nil {
		s.showError(err)
	}
}

// reexpand opens the explorer's branches as they were, but only under
// connections an open tab is already using: expanding a connection connects
// to it, and starting the app should connect to nothing a tab does not need.
func (s *Shell) reexpand(ids []string) {
	using := map[string]bool{}
	for _, t := range s.open {
		using[t.connID] = true
	}
	for _, id := range ids {
		if _, ok := view.FolderOf(id); ok { // opening a folder connects to nothing
			s.Explorer.Tree.OpenBranch(id)
			continue
		}
		if c, ok := view.ConnectionOf(id); ok && using[c] {
			s.Explorer.Tree.OpenBranch(id)
		}
	}
}

// reopenTab reopens one tab of the last session. An object on a connection
// that has gone is left closed: nothing unsaved goes with it.
func (s *Shell) reopenTab(st localdb.SessionTab, scratches map[string]localdb.Scratch,
	saved map[string]localdb.SavedQuery, used map[string]bool) *tab {
	switch st.Kind {
	case localdb.SessionObject:
		if _, ok := s.d.Conns.Get(st.ConnectionID); !ok || st.RefKind == "" {
			return nil
		}
		ref := model.NewRef(model.ObjectKind(st.RefKind), st.RefPath...)
		s.OpenObject(st.ConnectionID, model.Node{Ref: ref, Label: st.Label, Browsable: true})
		t := s.tabFor(view.NodeID(st.ConnectionID, ref))
		if t != nil && (len(st.Filters) > 0 || len(st.Sorts) > 0 || st.Where != "" || len(st.Columns) > 0) {
			v := st
			t.restore = &v
		}
		return t
	case localdb.SessionStructure:
		if _, ok := s.d.Conns.Get(st.ConnectionID); !ok || st.RefKind == "" {
			return nil
		}
		ref := model.NewRef(model.ObjectKind(st.RefKind), st.RefPath...)
		return s.OpenStructure(st.ConnectionID, model.Node{Ref: ref, Label: st.Label, Browsable: true})
	case localdb.SessionQuery:
		if sc, ok := scratches[st.ScratchID]; ok && !used[sc.ID] {
			used[sc.ID] = true
			return s.reopenScratch(sc, saved[sc.SavedID])
		}
		if _, ok := s.d.Conns.Get(st.ConnectionID); !ok {
			return nil
		}
		if sq, ok := saved[st.SavedID]; ok {
			return s.reopenSaved(st.ConnectionID, sq)
		}
		return s.OpenQuery(st.ConnectionID)
	}
	return nil
}

// restoreView puts back how a reopened tab's rows were viewed: its filters,
// sort and WHERE clause, matched to columns by name. What no longer fits,
// because a column has gone or the source cannot do it, is said rather than
// dropped unseen.
func (s *Shell) restoreView(t *tab, st localdb.SessionTab) {
	cols := t.model.Columns()
	index := make(map[string]int, len(cols))
	for i, c := range cols {
		index[c.Name] = i
	}
	if len(st.Columns) > 0 {
		s.restoreLayout(t, st, index)
	}
	var lost []string
	filtered := false
	for _, name := range slices.Sorted(maps.Keys(st.Filters)) {
		i, ok := index[name]
		if !ok {
			lost = append(lost, "the filter on "+name+", a column it no longer has")
			continue
		}
		if !t.browse.CanFilter() && !t.browse.FiltersHere() {
			lost = append(lost, "the filter on "+name)
			continue
		}
		if _, err := filterexpr.Parse(name, st.Filters[name], cols[i].Type); err != nil {
			lost = append(lost, fmt.Sprintf("the filter on %s (%v)", name, err))
			continue
		}
		t.grid.SetFilterText(i, st.Filters[name])
		filtered = true
	}
	opt := t.want
	opt.Sorts = nil
	var keys []grid.SortKey
	for _, k := range st.Sorts {
		i, ok := index[k.Column]
		switch {
		case !ok:
			lost = append(lost, "the sort on "+k.Column+", a column it no longer has")
		case !t.browse.CanSort():
			lost = append(lost, "the sort on "+k.Column)
		default:
			keys = append(keys, grid.SortKey{Column: i, Descending: k.Descending})
			opt.Sorts = append(opt.Sorts, source.Sort{Column: k.Column, Descending: k.Descending})
		}
	}
	if st.Where != "" {
		if t.browse.CanWhere() {
			opt.Where = st.Where
		} else {
			lost = append(lost, "its WHERE clause")
		}
	}
	if len(lost) > 0 {
		s.showError(fmt.Errorf("“%s” reopened without %s", t.item.Text, strings.Join(lost, "; ")))
	}
	if !filtered && len(keys) == 0 && opt.Where == "" {
		return
	}
	t.grid.SetSorts(keys)
	t.want = opt
	if opt.Where != "" {
		t.where = s.newWhereBar(t)
		t.where.show()
	}
	s.refilter(t, t.grid.FilterTexts())
}

// restoreLayout puts back a table's column layout by name. A column that has
// gone is simply not there, and one added since comes last, shown.
func (s *Shell) restoreLayout(t *tab, st localdb.SessionTab, index map[string]int) {
	var order []int
	for _, sc := range st.Columns {
		i, ok := index[sc.Name]
		if !ok || slices.Contains(order, i) {
			continue
		}
		order = append(order, i)
		if sc.Width > 0 {
			t.grid.ResizeColumn(i, sc.Width)
		}
	}
	for i, c := range t.model.Columns() {
		if !slices.Contains(order, i) && !slices.Contains(st.Hidden, c.Name) {
			order = append(order, i)
		}
	}
	t.grid.SetLayout(order, st.Frozen)
}

// layoutOf records a table's column layout, unless it is as the table first
// opened: then a column added later simply appears.
func layoutOf(g *grid.TableGrid, cols []model.ColumnDef, st *localdb.SessionTab) {
	shown := g.Shown()
	changed := g.Frozen() > 0 || len(shown) != len(cols)
	for i, c := range shown {
		changed = changed || c != i || g.Resized(c)
	}
	if !changed {
		return
	}
	for _, c := range shown {
		sc := localdb.SessionColumn{Name: cols[c].Name}
		if g.Resized(c) {
			sc.Width = g.ColumnWidth(c)
		}
		st.Columns = append(st.Columns, sc)
	}
	for i, c := range cols {
		if !slices.Contains(shown, i) {
			st.Hidden = append(st.Hidden, c.Name)
		}
	}
	st.Frozen = g.Frozen()
}

// sessionOf is the window as it is now.
func (s *Shell) sessionOf() localdb.Session {
	size := s.win.Canvas().Size()
	ss := localdb.Session{Width: size.Width, Height: size.Height, Sidebar: s.split.Offset, Active: -1}
	ss.Expanded = s.Explorer.Expanded()
	active := s.activeTab()
	for i, p := range s.panes {
		front := p.Selected()
		for _, it := range p.Items {
			t := s.tabOf(it)
			if t == nil || t.imp != nil { // an import starts from a file chosen now
				continue
			}
			if t == active {
				ss.Active = len(ss.Tabs)
			}
			st := sessionTab(t)
			st.Pane, st.Front = i, len(s.panes) > 1 && it == front
			ss.Tabs = append(ss.Tabs, st)
		}
	}
	if s.paneSplit != nil {
		ss.Split, ss.SplitOffset = s.splitDir, s.paneSplit.Offset
	}
	return ss
}

// sessionTab is one tab as it is now. For an object, that is the filters,
// sort and WHERE clause its rows are in, not what is typed but not applied.
func sessionTab(t *tab) localdb.SessionTab {
	st := localdb.SessionTab{ConnectionID: t.connID, Pinned: t.pinned}
	if q := t.query; q != nil {
		st.Kind, st.ScratchID, st.SavedID = localdb.SessionQuery, q.scratchID, q.saved.ID
		return st
	}
	if t.structure {
		st.Kind, st.RefKind, st.RefPath, st.Label = localdb.SessionStructure, string(t.ref.Kind), t.ref.Path, t.label
		return st
	}
	st.Kind, st.RefKind, st.RefPath, st.Label = localdb.SessionObject, string(t.ref.Kind), t.ref.Path, t.item.Text
	switch {
	case t.restore != nil: // not shown yet: keep what it is to show
		st.Filters, st.Sorts, st.Where = t.restore.Filters, t.restore.Sorts, t.restore.Where
		st.Columns, st.Hidden, st.Frozen = t.restore.Columns, t.restore.Hidden, t.restore.Frozen
	case t.browse != nil:
		opt := t.browse.Options()
		for _, k := range opt.Sorts {
			st.Sorts = append(st.Sorts, localdb.SessionSort{Column: k.Column, Descending: k.Descending})
		}
		st.Where = opt.Where
		cols := t.model.Columns()
		for i, text := range t.filtered {
			if i < len(cols) && strings.TrimSpace(text) != "" {
				if st.Filters == nil {
					st.Filters = map[string]string{}
				}
				st.Filters[cols[i].Name] = text
			}
		}
		layoutOf(t.grid, cols, &st)
	}
	return st
}

// sessionChanged saves the session within the autosave delay of a change,
// so that a crash loses at most that much of it.
func (s *Shell) sessionChanged() {
	if s.writer == nil || s.d.Session == nil || s.restoring || s.sessionPending || s.ctx.Err() != nil {
		return
	}
	s.sessionPending = true
	time.AfterFunc(s.autosave, func() {
		s.d.Run(func() {
			s.sessionPending = false
			if s.ctx.Err() == nil {
				s.saveSession()
			}
		})
	})
}

// saveSession writes the session, if it has changed since it was last
// written.
func (s *Shell) saveSession() {
	if s.writer == nil || s.d.Session == nil || s.restoring {
		return
	}
	ss := s.sessionOf()
	b, err := json.Marshal(ss)
	if err != nil || bytes.Equal(b, s.lastSession) {
		return
	}
	s.lastSession = b
	st := s.d.Session
	s.writer.queue("session", func(ctx context.Context) error {
		if err := st.PutSession(ctx, ss); err != nil {
			return fmt.Errorf("the open tabs could not be remembered for the next start: %w", err)
		}
		return nil
	})
}
