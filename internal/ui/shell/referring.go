package shell

import (
	"slices"

	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app/filterexpr"
	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Rows that refer to a row (FR-3.11, ADR-0041). A table's tab lists, when it
// opens, the foreign keys of other tables that refer to its table, where the
// source lists them. From a row, Show Referring Rows opens the rows of such
// a table whose key holds the row's values: at once where one key refers,
// else after asking which.

// referral is one table's rows that may refer to a row: which table, and
// the filter that picks them out.
type referral struct {
	label   string
	ref     model.ObjectRef
	filters map[string]string
}

// referrals are the tables whose rows may refer to the active row of the
// table in front, each with the filter for the row's values in its key.
// The row's stored values are followed, not a pending change: other rows
// refer to what is written, so a new row has none referring to it.
func (s *Shell) referrals() (*tab, []referral) {
	t := s.activeTab()
	if t == nil || t.query != nil || t.grid == nil {
		return t, nil
	}
	c, ok := t.grid.Selection().Active()
	if !ok || c.Row < t.model.Added() {
		return t, nil
	}
	row, loaded := t.model.Row(t.ctx, int64(c.Row))
	if !loaded || row == nil {
		return t, nil
	}
	cols := t.model.Columns()
	var out []referral
	for _, rf := range t.referrers {
		vals := keyValues(cols, rf.Key.RefColumns, func(i int) any { return row[i] })
		if vals == nil || len(vals) != len(rf.Key.Columns) {
			continue
		}
		filters := make(map[string]string, len(vals))
		for i, v := range vals {
			filters[rf.Key.Columns[i]] = filterexpr.Pick([]any{v}, false)
		}
		out = append(out, referral{label: referrerLabel(rf), ref: rf.From, filters: filters}) // detail.go
	}
	return t, out
}

func (s *Shell) canShowReferring() bool {
	_, rs := s.referrals()
	return len(rs) > 0
}

// showReferring opens the rows that may refer to the active row: of the one
// table whose key refers to it, or of the one picked where several do.
func (s *Shell) showReferring() {
	t, rs := s.referrals()
	switch len(rs) {
	case 0:
		return
	case 1:
		s.openFiltered(t.connID, rs[0].ref, rs[0].filters)
		return
	}
	labels := make([]string, len(rs))
	for i, r := range rs {
		labels[i] = r.label
	}
	pick := widget.NewRadioGroup(labels, nil)
	pick.SetSelected(labels[0])
	dialog.NewCustomConfirm("Show Referring Rows", "Show Rows", "Cancel", pick, func(ok bool) {
		if i := slices.Index(labels, pick.Selected); ok && i >= 0 {
			s.openFiltered(t.connID, rs[i].ref, rs[i].filters)
		}
	}, s.win).Show()
}
