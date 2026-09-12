package shell

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"

	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Editing a document (FR-12.1, ADR-0067). A document store's row is a
// document, and a grid cell cannot hold one: what a person edits is the JSON
// itself. The editor knows the shape the collection was sampled for — the
// grid's own columns — so it can say what the rest of the collection holds
// that this document has not, and where a value's type is not the rest's.
//
// Saving writes one document through the same writer and the same guard as
// the grid's own changes (ADR-0066); the JSON is the review.

// editDocument opens the active row's document in the JSON view.
func (j *jsonView) editDocument() {
	c, ok := j.g.Selection().Active()
	if !ok {
		j.where.SetText("Choose a document in the grid first")
		return
	}
	j.seq++
	seq := j.seq
	row := int64(c.Row)
	j.where.SetText("Reading…")
	m := j.g.Model()
	go func() {
		rows, err := m.Read(j.t.ctx, row, row+1)
		j.s.d.Run(func() {
			if seq != j.seq || j.t.ctx.Err() != nil {
				return
			}
			switch {
			case err != nil:
				j.where.SetText("Could not read the document: " + err.Error())
			case len(rows) == 0:
				j.where.SetText("No document here")
			default:
				j.editing, j.row, j.at = true, rows[0], row
				j.text.SetText(documentJSON(j.g.Model().Columns(), rows[0]))
				j.note.SetText(alsoHolds(j.g.Model().Columns(), rows[0]))
				j.where.SetText(fmt.Sprintf("Document %d", row+1))
				j.buttons()
			}
		})
	}()
}

// stopEditing puts the page back, keeping nothing that was typed.
func (j *jsonView) stopEditing() {
	j.editing, j.row, j.warned, j.note.Text = false, nil, "", ""
	j.note.Refresh()
	j.buttons()
	j.read()
}

// saveDocument writes what was typed, as a change to the document it was
// read from. Nothing is written until the fields that differ are known: a
// document saved unchanged writes nothing at all.
func (j *jsonView) saveDocument() {
	e := editsFor(j.t, j.g)
	if e == nil || e.writes == nil {
		j.note.SetText("This connection cannot write.")
		return
	}
	cols := j.g.Model().Columns()
	values, err := documentChanges(cols, j.row, j.text.Text)
	if err != nil {
		j.note.SetText(err.Error())
		return
	}
	if len(values) == 0 {
		j.note.SetText("Nothing was changed.")
		return
	}
	// A document store takes a field of any type, so a type unlike the rest
	// of the collection's is said and not refused — once, and the next Save
	// writes it (FR-12.4).
	if w := typeWarnings(cols, values); w != "" && w != j.warned {
		j.warned = w
		j.note.SetText(w + ". Save again to write it.")
		return
	}
	if e.pending == nil {
		j.note.SetText("These documents have nothing to tell them apart, so they cannot be written.")
		return
	}
	id := e.pending.Identity()
	key := make([]any, 0, len(id.Columns))
	for _, name := range id.Columns {
		v, ok := fieldOf(cols, j.row, name)
		if !ok {
			j.note.SetText("This document has no " + name + ", so it cannot be addressed.")
			return
		}
		key = append(key, v)
	}
	cs := source.Changeset{Target: id.Target, Identity: id, Changes: []source.RowChange{
		{Kind: source.ChangeUpdate, Key: key, Values: values},
	}}
	plan, err := e.writes.Plan(e.ctx, cs)
	if err != nil {
		j.note.SetText("Could not plan the change: " + err.Error())
		return
	}
	j.stopEditing()
	j.s.commit(e, plan, cs)
}

// buttons shows the ones the mode has: reading a page, or editing one
// document.
func (j *jsonView) buttons() {
	for _, b := range []*widget.Button{j.prev, j.next, j.edit} {
		if j.editing {
			b.Hide()
		} else {
			b.Show()
		}
	}
	if j.canEdit() {
		j.edit.Enable()
	} else {
		j.edit.Disable()
	}
	for _, b := range []*widget.Button{j.save, j.cancel} {
		if j.editing {
			b.Show()
		} else {
			b.Hide()
		}
	}
	j.text.Enable()
	if !j.editing {
		// A page is read, not written: only one document at a time is
		// edited, and only Save writes it.
		j.text.Disable()
	}
}

// alsoHolds says which of the collection's fields are empty in this
// document. The columns came from a sample of the documents (ADR-0065), so
// they are the shape, and a field the rest hold and this one has not is
// worth seeing while it is edited.
func alsoHolds(cols []model.ColumnDef, row model.Row) string {
	var missing []string
	for i, c := range cols {
		if i < len(row) && row[i] != nil {
			continue
		}
		missing = append(missing, c.Name)
	}
	if len(missing) == 0 {
		return ""
	}
	return "Empty here, and held by other documents sampled: " + strings.Join(missing, ", ")
}

// fieldOf is a row's value for a named column.
func fieldOf(cols []model.ColumnDef, row model.Row, name string) (any, bool) {
	for i, c := range cols {
		if c.SourceName() == name || c.Name == name {
			if i < len(row) {
				return row[i], true
			}
			return nil, false
		}
	}
	return nil, false
}

// documentChanges reads the edited JSON and says what differs from the
// document it was read from: a field changed or added is its new value, and
// a field no longer there is model.Removed.
func documentChanges(cols []model.ColumnDef, row model.Row, text string) (map[string]any, error) {
	var doc map[string]any
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("This is not a JSON document: %v", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("This is more than one document; edit one at a time.")
	}
	was := make(map[string]any, len(cols))
	for i, c := range cols {
		if i < len(row) {
			was[c.Name] = row[i]
		}
	}
	values := map[string]any{}
	for name, v := range doc {
		old, had := was[name]
		now, err := jsonValue(v, old)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", name, err)
		}
		if had && same(old, now) {
			continue
		}
		values[name] = now
	}
	for name, old := range was {
		if _, there := doc[name]; there || old == nil {
			continue // never there, or there still
		}
		values[name] = model.Removed{}
	}
	if v, changed := values["_id"]; changed {
		return nil, fmt.Errorf("This document's _id would become %v, which is another document. Add a document instead.", v)
	}
	return values, nil
}

// jsonValue is a value read from the edited JSON, in the form the store
// holds it. A number stays whole where it was whole: a document store tells
// 42 from 42.0, and a person typing 43 over 42 means the number the field
// held, not a new kind of one.
func jsonValue(v any, old any) (any, error) {
	n, ok := v.(json.Number)
	if !ok {
		return v, nil
	}
	if i, err := n.Int64(); err == nil {
		if _, wasFloat := old.(float64); !wasFloat {
			return i, nil
		}
		return float64(i), nil
	}
	f, err := n.Float64()
	if err != nil {
		return nil, fmt.Errorf("%s is not a number this store can hold", n)
	}
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return nil, fmt.Errorf("%s is not a number this store can hold", n)
	}
	return f, nil
}

// same reports whether an edited value is the value that was there, read
// back through JSON: what the grid holds is not always what JSON gives, so
// they are compared as JSON.
func same(a, b any) bool {
	if reflect.DeepEqual(a, b) {
		return true
	}
	ja, ea := json.Marshal(a)
	jb, eb := json.Marshal(b)
	return ea == nil && eb == nil && string(ja) == string(jb)
}

// typeWarnings says where an edited value is not the type the rest of the
// collection holds for that field. A document store allows it, so it is said
// and not refused (FR-12.4).
func typeWarnings(cols []model.ColumnDef, values map[string]any) string {
	var out []string
	for _, c := range cols {
		v, changed := values[c.Name]
		if !changed || v == nil || c.Type.Class == model.TypeUnknown {
			continue
		}
		if !fits(v, c.Type.Class) {
			out = append(out, fmt.Sprintf("%s is %s here, and %s in the documents sampled",
				c.Name, jsonKind(v), c.Type.Class))
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "; ")
}

func fits(v any, class model.TypeClass) bool {
	switch v.(type) {
	case string:
		return class == model.TypeString || class == model.TypeUUID || class == model.TypeDate ||
			class == model.TypeTime || class == model.TypeTimestamp || class == model.TypeDecimal ||
			class == model.TypeBytes
	case bool:
		return class == model.TypeBool
	case int64:
		return class == model.TypeInteger || class == model.TypeFloat || class == model.TypeDecimal
	case float64:
		return class == model.TypeFloat || class == model.TypeDecimal || class == model.TypeInteger
	case map[string]any:
		return class == model.TypeStruct || class == model.TypeJSON
	case []any:
		return class == model.TypeArray || class == model.TypeJSON
	case model.Removed:
		return true
	}
	return true
}

// jsonKind names what a value is, as a person would say it.
func jsonKind(v any) string {
	switch v.(type) {
	case string:
		return "text"
	case bool:
		return "true or false"
	case int64, float64:
		return "a number"
	case map[string]any:
		return "a document"
	case []any:
		return "a list"
	}
	return "something else"
}

// canEdit reports whether a document can be edited here: the rows must be
// writable, and one must be chosen in the grid.
func (j *jsonView) canEdit() bool {
	e := editsFor(j.t, j.g)
	if e == nil || e.writes == nil || e.pending == nil {
		return false
	}
	_, ok := j.g.Selection().Active()
	return ok
}
