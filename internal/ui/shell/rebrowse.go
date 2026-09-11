package shell

import (
	"fmt"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/app/filterexpr"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// resort applies a header click: the object is browsed again in the new
// order, on the server (FR-3.3, ADR-0016).
func (s *Shell) resort(t *tab, keys []grid.SortKey) {
	cols := t.model.Columns()
	opt := t.want
	opt.Sorts = nil
	for _, k := range keys {
		if k.Column >= 0 && k.Column < len(cols) {
			opt.Sorts = append(opt.Sorts, source.Sort{Column: cols[k.Column].Name, Descending: k.Descending})
		}
	}
	s.rebrowse(t, opt, keys, "Sorting…", "Could not sort: ")
}

// refilter applies the filter row (FR-3.5). Every column's text is parsed
// first. A column whose text does not parse is marked and explained, and
// nothing is sent: half a filter would show rows as filtered that are not.
func (s *Shell) refilter(t *tab, texts []string) {
	cols := t.model.Columns()
	var filters []source.Filter
	var bad []int
	problem := ""
	for i, text := range texts {
		if i >= len(cols) {
			break
		}
		if p, ok := t.picked[i]; ok {
			if strings.TrimSpace(text) == p.text {
				op := source.OpIn
				if p.negate {
					op = source.OpNotIn
				}
				filters = append(filters, source.Filter{Column: cols[i].Name, Op: op, Values: p.values})
				continue
			}
			delete(t.picked, i) // edited by hand: the text is the filter now
		}
		fs, err := filterexpr.Parse(cols[i].Name, text, cols[i].Type)
		if err != nil {
			bad = append(bad, i)
			if problem == "" {
				problem = fmt.Sprintf("Filter on %s: %v", cols[i].Name, err)
			}
			continue
		}
		filters = append(filters, fs...)
	}
	if problem != "" {
		t.grid.SetFilterErrors(bad...)
		t.problem = problem
		s.showCount(t)
		s.browsed(t)
		return
	}
	opt := t.want
	opt.Filters = filters
	s.rebrowse(t, opt, t.grid.Sorts(), "Filtering…", "Could not filter: ")
}

// rebrowse browses the tab's object again with new options. Only the latest
// request is applied. One that fails leaves the rows as they were, puts the
// sort header back, and marks the filters that were not applied.
func (s *Shell) rebrowse(t *tab, opt source.BrowseOptions, keys []grid.SortKey, doing, failed string) {
	t.browseSeq++
	t.want = opt
	seq, bs, texts := t.browseSeq, t.browse, t.grid.FilterTexts()
	t.footer.SetText(doing)
	go func() {
		next, err := bs.With(t.ctx, opt)
		s.d.Run(func() {
			if t.ctx.Err() != nil || seq != t.browseSeq {
				return
			}
			if err != nil {
				t.want = t.browse.Options()
				t.grid.SetSorts(t.applied)
				t.grid.SetFilterErrors(changed(texts, t.filtered)...)
				t.problem = failed + err.Error()
				s.showCount(t)
				s.browsed(t)
				return
			}
			t.browse, t.applied, t.filtered, t.problem = next, keys, texts, ""
			t.grid.SetFilterErrors()
			t.model.SetFetcher(next)
			t.grid.ScheduleRefresh()
			s.count(t)
			s.browsed(t)
		})
	}()
}

// changed lists the columns whose filter text differs from the text the rows
// are filtered by.
func changed(texts, applied []string) []int {
	var out []int
	for i, text := range texts {
		was := ""
		if i < len(applied) {
			was = applied[i]
		}
		if strings.TrimSpace(text) != strings.TrimSpace(was) {
			out = append(out, i)
		}
	}
	return out
}
