package shell

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// jsonOn opens a tab's JSON view over its grid.
func jsonOn(t *testing.T, fx *fixture, tb *tab, g *grid.TableGrid) *jsonView {
	t.Helper()
	fx.s.run(cmdJSONView)
	j := tb.jsons[g]
	if j == nil || !j.shown() {
		t.Fatal("the JSON view did not open")
	}
	pump(t, fx.q, func() bool { return strings.Contains(j.text.Text, "item") })
	return j
}

func TestTheJSONViewShowsTheDocumentsInTheGridsPlace(t *testing.T) {
	fx, tb := loadedItems(t)
	j := jsonOn(t, fx, tb, tb.grid)
	if tb.body.Objects[0] != j.box {
		t.Error("the documents take the grid's place")
	}
	text := j.text.Text
	if !strings.HasPrefix(text, "[\n  {") || !strings.HasSuffix(text, "]") {
		t.Errorf("the documents are not a JSON array:\n%s", text)
	}
	if !strings.Contains(text, `"id": 1`) || !strings.Contains(text, `"name": "item 1"`) {
		t.Errorf("a document does not hold its fields:\n%s", text)
	}
	if got := strings.Count(text, `"id":`); got != jsonPage {
		t.Errorf("%d documents shown, want a page of %d", got, jsonPage)
	}
	if j.where.Text != "Documents 1–50" {
		t.Errorf("it says %q", j.where.Text)
	}
	if !j.prev.Disabled() {
		t.Error("there is nothing before the first page")
	}
	if j.next.Disabled() {
		t.Error("there are more documents to read")
	}
	// Asked again, the grid comes back.
	fx.s.run(cmdJSONView)
	if j.shown() {
		t.Error("the JSON view did not close")
	}
	if tb.body.Objects[0] == j.box {
		t.Error("the grid did not come back")
	}
}

func TestTheJSONViewPagesThroughTheDocuments(t *testing.T) {
	fx, tb := loadedItems(t)
	j := jsonOn(t, fx, tb, tb.grid)
	j.page(1)
	pump(t, fx.q, func() bool { return strings.Contains(j.text.Text, `"id": 51`) })
	if j.where.Text != "Documents 51–100" {
		t.Errorf("the second page says %q", j.where.Text)
	}
	if j.prev.Disabled() {
		t.Error("there is a page to go back to")
	}
	j.page(-1)
	pump(t, fx.q, func() bool { return strings.Contains(j.text.Text, `"id": 1`) })
	if j.where.Text != "Documents 1–50" {
		t.Errorf("back to %q", j.where.Text)
	}
	// There is nothing before the first page, or after the last.
	j.page(-1)
	if j.from != 0 {
		t.Errorf("paged back to %d", j.from)
	}
	j.from = 200
	j.page(1)
	if j.from != 200 {
		t.Errorf("paged past the end to %d", j.from)
	}
}

func TestTheJSONViewOpensWhereTheGridWasLooking(t *testing.T) {
	fx, tb := loadedItems(t)
	tb.grid.Select(grid.CellID{Row: 120, Col: 0}, grid.CellID{Row: 120, Col: 0})
	j := jsonOn(t, fx, tb, tb.grid)
	if j.from != 100 {
		t.Errorf("opened at %d, want the page holding the active row", j.from)
	}
	if !strings.Contains(j.text.Text, `"id": 101`) {
		t.Errorf("shows:\n%s", j.text.Text[:200])
	}
}

func TestDocumentsJSONWritesValuesAsTheyAre(t *testing.T) {
	cols := []model.ColumnDef{
		{Name: "_id"}, {Name: "when"}, {Name: "raw"}, {Name: "dec"},
		{Name: "none"}, {Name: "address"}, {Name: "tags"}, {Name: "later"},
	}
	when := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	rows := []model.Row{{
		"64b1", when, []byte{1, 2}, model.Decimal("1.250"), nil,
		map[string]any{"city": "Kyoto"}, []any{"a", "b"}, model.Default{},
	}}
	got := documentsJSON(cols, rows)
	for _, want := range []string{
		`"_id": "64b1"`,
		`"when": "2026-09-12T10:00:00Z"`,
		`"raw": "AQI="`,
		`"dec": "1.250"`,
		`"none": null`,
		`"city": "Kyoto"`,
		`"tags": [`,
		`"later": null`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the documents do not hold %s:\n%s", want, got)
		}
	}
	// The fields come in the columns' order, which a map would lose.
	if i, k := strings.Index(got, `"_id"`), strings.Index(got, `"when"`); i > k {
		t.Errorf("the fields are out of order:\n%s", got)
	}
	// What is written is JSON, whatever the rows hold: a row shorter than
	// the columns writes them all, with nothing for what it has not got.
	for _, text := range []string{got, documentsJSON(cols, []model.Row{{"only"}})} {
		var back []map[string]any
		if err := json.Unmarshal([]byte(text), &back); err != nil {
			t.Errorf("does not read back as JSON (%v):\n%s", err, text)
			continue
		}
		if len(back) != 1 || len(back[0]) != len(cols) {
			t.Errorf("read back as %v, want a field for each column", back)
		}
	}
	// No rows is an empty array, not an empty string.
	if got := documentsJSON(cols, nil); got != "[\n]" {
		t.Errorf("no documents are %q", got)
	}
}
