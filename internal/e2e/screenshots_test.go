package e2e

import (
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/editor/view"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
	explorerview "github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// TestScreenshots renders the application's main states to PNG files, for
// reviewing the interface without a window server: the test driver draws the
// real widget tree with the real theme. Opt in with IKIGAI_SCREENSHOTS=dir;
// the ordinary test run writes nothing.
// gridTable is what the grid's table can be found by: it selects cells,
// and hears the modifiers of a click.
type gridTable interface {
	fyne.CanvasObject
	Select(widget.TableCellID)
	MouseDown(*desktop.MouseEvent)
}

func TestScreenshots(t *testing.T) {
	dir := os.Getenv("IKIGAI_SCREENSHOTS")
	if dir == "" {
		t.Skip("set IKIGAI_SCREENSHOTS to a directory to render screenshots")
	}
	h := start(t, sqliteJourney(t))
	shot := func(name string) {
		t.Helper()
		h.q.Flush()
		img := h.w.Canvas().Capture()
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img.(image.Image)); err != nil {
			t.Fatal(err)
		}
	}

	shot("1-empty")

	h.s.Commands().Run("connection.new")
	shot("2-connection-form")
	for h.w.Canvas().Overlays().Top() != nil {
		h.w.Canvas().Overlays().Remove(h.w.Canvas().Overlays().Top())
	}

	// Walk the sidebar to the table and open it.
	ids := []string{explorerview.ConnectionID(h.conn.ID),
		explorerview.NodeID(h.conn.ID, model.NewRef(model.KindFolder, "main", "tables")),
		explorerview.NodeID(h.conn.ID, model.NewRef(model.KindTable, "main", "people"))}
	parent := explorer.RootID
	for _, id := range ids {
		expand(t, h.q, h.s.Explorer.Model, parent, id)
		h.s.Explorer.Tree.OpenBranch(parent)
		parent = id
	}
	h.s.Explorer.Tree.Select(parent)
	h.s.Commands().Run("object.open")
	waitFor(t, h.q, "rows", func() bool {
		h.w.Canvas().Capture()
		return hasLabel(h.tabs.Selected().Content, "42 rows")
	})
	shot("3-table-rows")

	// The grid's table is its own subclass of Fyne's, found by what it does.
	tbl := find[gridTable](h.tabs.Selected().Content)[0]
	tbl.MouseDown(&desktop.MouseEvent{})
	tbl.Select(widget.TableCellID{Row: 1, Col: 0})
	tbl.MouseDown(&desktop.MouseEvent{Modifier: fyne.KeyModifierShift})
	tbl.Select(widget.TableCellID{Row: 4, Col: 1})
	shot("3a-selection")

	tbl.MouseDown(&desktop.MouseEvent{})
	tbl.Select(widget.TableCellID{Row: 0, Col: 1})
	h.s.Commands().Run("data.filterValues")
	values := find[*widget.List](h.w.Canvas().Overlays().Top())[0]
	waitFor(t, h.q, "the value list", func() bool { return values.Length() > 0 })
	shot("3b-picklist")
	for h.w.Canvas().Overlays().Top() != nil {
		h.w.Canvas().Overlays().Remove(h.w.Canvas().Overlays().Top())
	}

	h.s.Commands().Run("data.where")
	test.Type(h.w.Canvas().Focused(), "id > 30")
	h.w.Canvas().Focused().TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	waitFor(t, h.q, "the WHERE clause", func() bool {
		h.w.Canvas().Capture()
		for _, l := range find[*widget.Label](h.tabs.Selected().Content) {
			if strings.HasPrefix(l.Text, "12 rows") {
				return true
			}
		}
		return false
	})
	shot("3c-where")
	h.s.Commands().Run("data.where")

	tbl.MouseDown(&desktop.MouseEvent{})
	tbl.Select(widget.TableCellID{Row: 2, Col: 1})
	h.s.Commands().Run("grid.viewer")
	shot("3d-cell-viewer")
	h.s.Commands().Run("grid.viewer")

	h.s.Commands().Run("grid.moveRight") // the active cell is in name
	tbl.MouseDown(&desktop.MouseEvent{})
	tbl.Select(widget.TableCellID{Row: 2, Col: 0})
	h.s.Commands().Run("grid.moveRight")
	shot("3e-columns")

	h.s.OpenQuery(h.conn.ID)
	ed := find[*view.Editor](h.tabs.Selected().Content)[0]
	test.Type(ed.Focusable(), "-- people with a long id\nSELECT id, name, length(name) AS n\nFROM people\nWHERE id > 30\nORDER BY id DESC;")
	runWhenReady(t, h.q, h.s, "query.runAll")
	waitFor(t, h.q, "result", func() bool { return hasLabel(h.tabs.Selected().Content, "12 rows") })
	h.w.Canvas().Capture()
	shot("4-query-results")

	h.s.Commands().Run("edit.findReplace")
	test.Type(h.w.Canvas().Focused(), "id")
	shot("5-find-bar")
	h.w.Canvas().Focused().TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})

	h.s.Commands().Run("query.history")
	history := find[*widget.List](h.w.Canvas().Overlays().Top())[0]
	waitFor(t, h.q, "the history list", func() bool { return history.Length() > 0 })
	shot("6-history")
	for h.w.Canvas().Overlays().Top() != nil {
		h.w.Canvas().Overlays().Remove(h.w.Canvas().Overlays().Top())
	}

	h.s.Commands().Run("appearance.dark")
	h.q.Flush()
	h.w.Canvas().Capture()
	shot("7-dark")

	h.s.ShowError(errors.New("The export failed, and the partial file was removed: open /exports/people.csv: permission denied"))
	shot("8-error")

	h.s.Commands().Run("appearance.light")
	h.s.Commands().Run("appearance.accent.orange")
	h.q.Flush()
	h.w.Canvas().Capture()
	shot("9-accent")
}
