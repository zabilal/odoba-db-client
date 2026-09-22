package shell

import (
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Making and unmaking a collection's indexes, from its structure tab
// (FR-6.3, FR-12.1, ADR-0069). Nothing is sent until the call it would make
// has been shown and agreed to (FR-6.4).

// indexFields is the placeholder that says how fields are written.
const indexFields = "name, score:-1, body:text"

// addIndex asks what index to make, then shows the call it would make.
func (s *Shell) addIndex(t *tab) {
	fields := widget.NewEntry()
	fields.SetPlaceHolder(indexFields)
	name := widget.NewEntry()
	name.SetPlaceHolder("optional; the server names it otherwise")
	unique := widget.NewCheck("", nil)
	sparse := widget.NewCheck("", nil)
	ttl := widget.NewEntry()
	ttl.SetPlaceHolder("optional; seconds after which a document goes")
	form := widget.NewForm(
		widget.NewFormItem("Fields", fields),
		widget.NewFormItem("Name", name),
		widget.NewFormItem("Unique", unique),
		widget.NewFormItem("Sparse", sparse),
		widget.NewFormItem("Expires after", ttl),
	)
	note := widget.NewLabel("")
	note.Importance, note.Wrapping = widget.DangerImportance, fyne.TextWrapWord
	note.Hide()
	body := container.NewVBox(form, note)

	d := dialog.NewCustomConfirm("Add Index", "Continue", "Cancel", body, func(ok bool) {
		if !ok {
			return
		}
		idx, err := indexFrom(fields.Text, name.Text, ttl.Text, unique.Checked, sparse.Checked)
		if err != nil {
			s.showError(err)
			return
		}
		s.reviewIndex(t, func(confirmed bool) (*source.WritePlan, error) {
			return app.PlanIndex(t.ctx, s.sourceFor(t), t.ref, idx, confirmed)
		})
	}, s.win)
	d.Resize(fyne.NewSize(520, 320))
	d.Show()
}

// dropIndex asks which index to drop, then shows the call it would make.
func (s *Shell) dropIndex(t *tab, names []string) {
	if len(names) == 0 {
		s.showError(fmt.Errorf("there is no index to drop but the one every document is found by"))
		return
	}
	choose := widget.NewSelect(names, nil)
	choose.SetSelectedIndex(0)
	d := dialog.NewCustomConfirm("Drop Index", "Continue", "Cancel",
		container.NewVBox(widget.NewLabel("Which index?"), choose), func(ok bool) {
			if !ok {
				return
			}
			s.reviewIndex(t, func(confirmed bool) (*source.WritePlan, error) {
				return app.PlanDropIndex(t.ctx, s.sourceFor(t), t.ref, choose.Selected, confirmed)
			})
		}, s.win)
	d.Show()
}

// reviewIndex shows the call a change would make, and runs it only if the
// person says so (FR-6.4). A connection marked Production asks again, with
// the consent carried into the plan it runs.
func (s *Shell) reviewIndex(t *tab, plan func(confirmed bool) (*source.WritePlan, error)) {
	p, err := plan(false)
	if err != nil {
		s.showError(err)
		return
	}
	d := dialog.NewCustomConfirm("Change the Index?", "Run", "Cancel", reviewBody(p), func(ok bool) {
		if !ok {
			return
		}
		if !p.Guarded {
			s.runIndex(t, p)
			return
		}
		s.askToType(t.connID, "Change Structure on Production?",
			productionBody("changes the structure of", s.connName(t.connID), "Nothing has been sent yet."),
			"Run",
			func() {
				consented, err := plan(true)
				if err != nil {
					s.showError(err)
					return
				}
				s.runIndex(t, consented)
			}, nil)
	}, s.win)
	d.SetConfirmImportance(widget.HighImportance)
	d.Resize(fyne.NewSize(560, 320))
	d.Show()
}

// runIndex applies a plan off the UI goroutine, and reads the structure
// again so the tab shows what is there now.
func (s *Shell) runIndex(t *tab, plan *source.WritePlan) {
	t.footer.SetText("Changing the index…")
	go func() {
		live, err := s.d.WS.Connect(t.ctx, t.connID)
		var out *source.WriteOutcome
		if err == nil {
			out, err = app.ApplyIndex(t.ctx, live.Source, plan)
		}
		s.d.Run(func() {
			if t.ctx.Err() != nil {
				return
			}
			t.footer.SetText("")
			switch {
			case err != nil:
				s.showError(fmt.Errorf("could not change the index: %w", err))
				s.crashed(t.connID, err)
			case out.Err != nil:
				s.showError(fmt.Errorf("could not change the index: %w", out.Err))
			default:
				s.reopenStructure(t)
			}
		})
	}()
}

// sourceFor is a tab's live source, or nil where it is not connected. The
// planning calls take it; a nil one fails in app, not here.
func (s *Shell) sourceFor(t *tab) source.Source {
	live, ok := s.d.WS.Get(t.connID)
	if !ok {
		return nil
	}
	return live.Source
}

// indexFrom reads what was typed into the index it describes.
func indexFrom(fields, name, ttl string, unique, sparse bool) (model.DocumentIndex, error) {
	keys, err := parseIndexKeys(fields)
	if err != nil {
		return model.DocumentIndex{}, err
	}
	idx := model.DocumentIndex{Name: strings.TrimSpace(name), Keys: keys, Unique: unique, Sparse: sparse}
	if t := strings.TrimSpace(ttl); t != "" {
		n, err := strconv.ParseInt(t, 10, 64)
		if err != nil || n <= 0 {
			return model.DocumentIndex{}, fmt.Errorf("%q is not a number of seconds", ttl)
		}
		idx.TTL = n
	}
	return idx, nil
}

// parseIndexKeys reads the fields an index is on: a name on its own is
// ascending, "name:-1" descending, and "name:text" an index of that kind.
func parseIndexKeys(text string) ([]model.IndexColumn, error) {
	var out []model.IndexColumn
	for _, term := range strings.Split(text, ",") {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		name, how, found := strings.Cut(term, ":")
		name, how = strings.TrimSpace(name), strings.TrimSpace(how)
		if name == "" {
			return nil, fmt.Errorf("%q names no field; write them as %s", term, indexFields)
		}
		col := model.IndexColumn{Name: name}
		switch {
		case !found || how == "1" || how == "asc":
		case how == "-1" || how == "desc":
			col.Descending = true
		case how == "":
			return nil, fmt.Errorf("%q says nothing after its colon; write them as %s", term, indexFields)
		default:
			// text, 2dsphere, hashed: a kind of index rather than an order.
			col.Expression = how
		}
		out = append(out, col)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("name the fields to index, as %s", indexFields)
	}
	return out, nil
}

// indexNames are the indexes of a described collection that can be dropped:
// every one but the identifier's, which is how a document is found.
func indexNames(coll *model.Collection) []string {
	var out []string
	for _, idx := range coll.Indexes {
		if idx.Name == "_id_" {
			continue
		}
		out = append(out, idx.Name)
	}
	return out
}
