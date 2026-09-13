package shell

import (
	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Opening what a row names (FR-12.2, ADR-0074). Some rows are objects of
// their own: a Redis database browses as its keys, and a key's value is a
// table in its own right. The source says what a row names, and this opens
// it in a tab of its own, as the explorer opens a node.

// rowObject is what the row the grid is on names, or false. It is false for
// every source that does not say rows name anything, which is all of them
// but the key-value ones.
func (s *Shell) rowObject() (t *tab, ref model.ObjectRef, ok bool) {
	t = s.activeTab()
	if t == nil || t.browse == nil || t.grid == nil || !t.browse.CanOpenRows() {
		return t, ref, false
	}
	c, has := t.grid.Selection().Active()
	if !has {
		return t, ref, false
	}
	row, loaded := t.model.Row(t.ctx, int64(c.Row))
	if !loaded || row == nil {
		return t, ref, false
	}
	ref, ok = t.browse.ObjectOf(row)
	return t, ref, ok
}

func (s *Shell) canOpenRowObject() bool {
	_, _, ok := s.rowObject()
	return ok
}

// openRowObject opens what the row names, or brings its tab forward.
func (s *Shell) openRowObject() {
	t, ref, ok := s.rowObject()
	if !ok {
		return
	}
	// A tab already on it comes forward rather than a second one opening:
	// that is OpenObject's own rule, and it is the same rule the explorer's
	// nodes follow.
	s.OpenObject(t.connID, model.Node{Ref: ref, Label: ref.Name(), Browsable: true})
}
