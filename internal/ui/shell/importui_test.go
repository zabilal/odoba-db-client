package shell

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/store"
)

// itemsDescribed is the items table open, its rows loaded and its columns known.
func itemsDescribed(t *testing.T) (*fixture, *tab) {
	t.Helper()
	fx, tb := loadedItems(t)
	pump(t, fx.q, func() bool { return tb.table != nil })
	return fx, tb
}

// importing answers Import… with a file holding text, and waits for its
// panel's first rows.
func importing(t *testing.T, fx *fixture, text string) (*tab, *importPanel) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "people.csv")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if !fx.s.canImport() {
		t.Fatal("a table whose columns are known takes an import")
	}
	fx.s.run(cmdImport)
	if len(fx.files.opens) != 1 || !slices.Contains(fx.files.opens[0].Extensions, "xlsx") {
		t.Fatalf("Import asks for a file of the kinds it reads: %+v", fx.files.opens)
	}
	fx.files.answer(path, nil)
	var it *tab
	pump(t, fx.q, func() bool {
		for _, o := range fx.s.open {
			if o.imp != nil {
				it = o
			}
		}
		return it != nil && it.imp.grid != nil
	})
	return it, it.imp
}

func TestAFileIsReadAndItsColumnsPairedForAnImport(t *testing.T) {
	fx, tb := itemsDescribed(t)
	it, p := importing(t, fx, "name,id\nfirst,1\nsecond,x\n")
	if fx.s.activeTab() != it || !strings.HasPrefix(it.item.Text, "Import people.csv into items") {
		t.Errorf("the import is a tab of its own, in front: %q", it.item.Text)
	}
	if p.format.Selected != "CSV" || p.comma.Selected != "Comma" || !p.header.Checked || p.encoding.Selected != "UTF-8" {
		t.Errorf("how the file is read, as found from it: %v %v %v %v", p.format.Selected, p.comma.Selected, p.header.Checked, p.encoding.Selected)
	}
	if p.pairs["id"] != 1 || p.pairs["name"] != 0 {
		t.Errorf("each table column is filled by the file column of its name: %v", p.pairs)
	}
	if !strings.Contains(it.footer.Text, "1 of the first 2 rows have a value that would not go in; the first: id: not a whole number (x)") {
		t.Errorf("footer %q", it.footer.Text)
	}
	if n, _ := p.grid.Model().Extent(); n != 2 {
		t.Errorf("the first rows are shown, as the table would take them: %d", n)
	}
	pump(t, fx.q, func() bool { _, ok := p.grid.Model().Row(it.ctx, 1); return ok })
	if row, _ := p.grid.Model().Row(it.ctx, 1); len(row) != 2 || row[0] != "x" {
		t.Errorf("a value that would not go in is shown as the file has it: %v", row)
	}
	if tb.imp != nil || fx.s.canImport() {
		t.Error("an import's own tab takes no import")
	}
	fx.s.selectTab(tb)
	fx.s.run(cmdImport)
	fx.files.answer(p.f.Name(), nil)
	pump(t, fx.q, func() bool { return fx.s.activeTab() == it })
	imports := 0
	for _, o := range fx.s.open {
		if o.imp != nil {
			imports++
		}
	}
	if imports != 1 {
		t.Errorf("the same file into the same table brings its tab forward: %d imports", imports)
	}
}

func TestACancelledOrFailedDialogOpensNoImport(t *testing.T) {
	fx, tb := itemsDescribed(t)
	fx.s.importFrom(tb, "", nil) // cancelled
	time.Sleep(20 * time.Millisecond)
	fx.q.Flush()
	if fx.s.errors.shown() || len(fx.s.open) != 1 {
		t.Error("a cancelled dialog does nothing")
	}
	fx.s.importFrom(tb, "", errors.New("the dialog failed"))
	if !fx.s.errors.shown() || fx.s.errors.message.Text != "the dialog failed" || len(fx.s.open) != 1 {
		t.Errorf("a dialog that failed says so: %q", fx.s.errors.message.Text)
	}
}

func TestAnImportIsNotKeptInTheSession(t *testing.T) {
	fx, _ := itemsDescribed(t)
	importing(t, fx, "name\nx\n")
	if ss := fx.s.sessionOf(); len(ss.Tabs) != 1 || ss.Tabs[0].Label != "items" {
		t.Errorf("only the table's tab is kept: %+v", ss.Tabs)
	}
}

func TestAnImportsMappingAndOptionsAreChanged(t *testing.T) {
	fx, _ := itemsDescribed(t)
	it, p := importing(t, fx, "name,id\nfirst,1\nsecond,x\n")
	var idPick *widget.Select
	for _, item := range p.mapping.Objects[0].(*widget.Form).Items {
		if item.Text == "id" {
			idPick = item.Widget.(*widget.Select)
		}
	}
	idPick.SetSelected(notImported)
	if p.pairs["id"] != -1 || !strings.Contains(it.footer.Text, "The first 2 rows go in as shown.") {
		t.Errorf("without id, every value goes in: %q", it.footer.Text)
	}
	p.header.SetChecked(false) // the first row is a row, and the columns are numbered
	pump(t, fx.q, func() bool { return strings.Contains(it.footer.Text, "No column of the file fills one of the table's") })
	if len(p.rows) != 3 || p.from[0].Name != "column 1" {
		t.Errorf("read again as the options say: %d rows, %v", len(p.rows), p.from)
	}
	p.format.SetSelected("Excel")
	pump(t, fx.q, func() bool { return strings.HasPrefix(it.footer.Text, "Could not read the file as Excel") })
	if !p.comma.Disabled() || p.sheet.Disabled() {
		t.Error("a workbook has a sheet, not a delimiter")
	}
}

func TestClosingAnImportClosesItsFile(t *testing.T) {
	fx, _ := itemsDescribed(t)
	it, p := importing(t, fx, "name\nx\n")
	fx.s.closeTab(it.item)
	pump(t, fx.q, func() bool { _, err := p.f.Stat(); return err != nil })
}

func TestAFileThatCannotBeReadIsSaid(t *testing.T) {
	fx, tb := itemsDescribed(t)
	fx.s.importFrom(tb, filepath.Join(t.TempDir(), "gone.csv"), nil)
	pump(t, fx.q, func() bool {
		return fx.s.errors.shown() && strings.Contains(fx.s.errors.message.Text, "could not read gone.csv")
	})
	if len(fx.s.open) != 1 {
		t.Error("a file that cannot be read opens no import")
	}
}

func TestImportIsOfferedOnlyWhereATableTakesIt(t *testing.T) {
	fx := newFixture(t)
	c, err := fx.conns.Create(store.SavedConnection{Name: "ro", Driver: "postgres", Host: "db1", ReadOnly: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return tb.table != nil })
	if fx.s.canImport() {
		t.Error("a read-only connection takes no import")
	}
	fx2 := newFixture(t)
	openQuery(t, fx2, "")
	if fx2.s.canImport() {
		t.Error("a query tab takes no import")
	}
}
