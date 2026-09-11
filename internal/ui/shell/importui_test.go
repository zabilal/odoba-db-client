package shell

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/transfer"
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
	if n := len(fx.files.opens); n == 0 || !slices.Contains(fx.files.opens[n-1].Extensions, "xlsx") {
		t.Fatalf("Import asks for a file of the kinds it reads: %+v", fx.files.opens)
	}
	had := map[*tab]bool{}
	for _, o := range fx.s.open {
		had[o] = true
	}
	fx.files.answer(path, nil)
	var it *tab
	pump(t, fx.q, func() bool {
		for _, o := range fx.s.open {
			if o.imp != nil && !had[o] {
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
	if !strings.Contains(it.footer.Text, "1 of the first 2 rows has a value that would not go in; the first: id: not a whole number (x)") {
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
	if p.dryRun(); len(fx.s.tasks) != 0 {
		t.Error("nothing to write, nothing to try")
	}
	if p.startImport(); len(fx.s.tasks) != 0 {
		t.Error("nothing to import")
	}
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

// pickFor is the mapping's picker for a table column.
func pickFor(p *importPanel, name string) *widget.Select {
	for _, item := range p.mapping.Objects[0].(*widget.Form).Items {
		if item.Text == name {
			return item.Widget.(*widget.Select)
		}
	}
	return nil
}

// lastTaskEnds waits for the latest task to end.
func lastTaskEnds(t *testing.T, fx *fixture) *task {
	t.Helper()
	k := fx.s.tasks[len(fx.s.tasks)-1]
	pump(t, fx.q, func() bool { return k.state != taskRunning })
	return k
}

func TestADryRunReadsEveryRowAndListsWhatWouldNotGoIn(t *testing.T) {
	fx, _ := itemsDescribed(t)
	var b strings.Builder
	b.WriteString("name,id\n")
	for i := 1; i <= 30; i++ {
		switch i {
		case 5:
			b.WriteString("n5,x\n")
		case 25:
			b.WriteString("n25,2.5\n")
		default:
			fmt.Fprintf(&b, "n%d,%d\n", i, i)
		}
	}
	it, p := importing(t, fx, b.String())
	if p.tabs.SelectedIndex() != 0 || p.summary.Text != dryRunIntro {
		t.Errorf("the first rows are shown until a dry run: %q", p.summary.Text)
	}
	test.Tap(p.dry)
	if !p.dry.Disabled() {
		t.Error("one dry run at a time")
	}
	if p.startImport(); !p.load.Disabled() || len(fx.s.runningTasks(nil)) != 1 {
		t.Error("no import while a dry run runs")
	}
	k := lastTaskEnds(t, fx)
	if k.title != "Dry run of people.csv" || k.state != taskDone || k.status != "2 of 30 rows would not go in." {
		t.Errorf("task %q: %v %q", k.title, k.state, k.status)
	}
	if p.tabs.SelectedIndex() != 1 || p.summary.Text != k.status || p.dry.Disabled() || p.known != 30 {
		t.Errorf("the dry run answers in its tab: %q", p.summary.Text)
	}
	select {
	case <-k.finished:
	default:
		t.Error("a dry run says when it has stopped reading, as quitting waits for it")
	}
	pump(t, fx.q, func() bool { _, ok := p.checked.Model().Row(it.ctx, 1); return ok })
	first, _ := p.checked.Model().Row(it.ctx, 0)
	second, _ := p.checked.Model().Row(it.ctx, 1)
	if n, _ := p.checked.Model().Extent(); n != 2 || fmt.Sprint(first) != "[5 id x not a whole number]" || fmt.Sprint(second) != "[25 id 2.5 not a whole number]" {
		t.Errorf("each value that would not go in, by row: %v %v", first, second)
	}
	test.Tap(p.dry)
	if again := lastTaskEnds(t, fx); again == k || again.status != k.status {
		t.Error("a dry run can be run again")
	}
	pickFor(p, "id").SetSelected(notImported)
	if p.checked != nil || p.summary.Text != dryRunIntro || p.known != -1 {
		t.Error("a change to the mapping forgets the dry run, which was for the mapping as it was")
	}
	test.Tap(p.dry)
	if k := lastTaskEnds(t, fx); k.status != "All 30 rows would go in." {
		t.Errorf("status %q", k.status)
	}
}

func TestAChangeStopsADryRun(t *testing.T) {
	fx, _ := itemsDescribed(t)
	_, p := importing(t, fx, "name,id\nfirst,1\nsecond,x\n")
	p.dryRun()
	p.dryRun()
	if len(fx.s.runningTasks(nil)) != 1 {
		t.Errorf("one dry run at a time: %d", len(fx.s.runningTasks(nil)))
	}
	k := fx.s.tasks[len(fx.s.tasks)-1]
	pickFor(p, "name").SetSelected(notImported)
	if !k.stopping {
		t.Error("a change to the mapping stops the dry run, in the task centre too")
	}
	lastTaskEnds(t, fx)
	if k.state != taskCancelled || k.status != "Stopped, as the import changed" || p.dry.Disabled() || p.summary.Text != dryRunIntro {
		t.Errorf("a dry run of the import as it was stops: %v %q, summary %q", k.state, k.status, p.summary.Text)
	}
	p.dryRun()
	if len(fx.s.runningTasks(nil)) != 1 {
		t.Error("a dry run of the import as it is now starts")
	}
	lastTaskEnds(t, fx)
	p.header.SetChecked(false)
	if p.summary.Text != dryRunIntro {
		t.Errorf("a change to the options forgets the findings too: %q", p.summary.Text)
	}
}

func TestClosingAnImportStopsItsDryRun(t *testing.T) {
	fx, _ := itemsDescribed(t)
	it, p := importing(t, fx, "name,id\nfirst,1\n")
	p.dryRun()
	fx.s.closeTab(it.item)
	if k := lastTaskEnds(t, fx); k.state != taskCancelled {
		t.Errorf("closing the tab stops its dry run: %v %q", k.state, k.status)
	}
}

func TestADryRunsFindingsAreWorded(t *testing.T) {
	for _, c := range []struct {
		c    transfer.Checked
		want string
	}{
		{transfer.Checked{}, "The file has no rows."},
		{transfer.Checked{Rows: 1}, "The file's one row would go in."},
		{transfer.Checked{Rows: 1200}, "All 1,200 rows would go in."},
		{transfer.Checked{Rows: 1200, Bad: 2, Values: 2, Problems: make([]transfer.Problem, 2)}, "2 of 1,200 rows would not go in."},
		{transfer.Checked{Rows: 5000, Bad: 1500, Values: 1600, Problems: make([]transfer.Problem, 1000)},
			"1,500 of 5,000 rows would not go in. The first 1,000 of 1,600 values that would not are listed."},
	} {
		if got := checkedSummary(c.c); got != c.want {
			t.Errorf("%+v: %q", c.c, got)
		}
	}
	if got := checkedStatus(transfer.Checked{Rows: 1000}); got != "1,000 rows read" {
		t.Errorf("%q", got)
	}
	if got := checkedStatus(transfer.Checked{Rows: 1000, Bad: 3}); got != "1,000 rows read, 3 would not go in" {
		t.Errorf("%q", got)
	}
}

func TestACancelledOrFailedDryRunSaysSo(t *testing.T) {
	fx, _ := itemsDescribed(t)
	var b strings.Builder
	b.WriteString("name,id\n")
	for i := range 300000 {
		fmt.Fprintf(&b, "n,%d\n", i)
	}
	it, p := importing(t, fx, b.String())
	p.dryRun()
	k := fx.s.tasks[len(fx.s.tasks)-1]
	pump(t, fx.q, func() bool { return strings.HasSuffix(k.status, " rows read") })
	fx.s.stopTask(k)
	lastTaskEnds(t, fx)
	if k.state != taskCancelled || !strings.HasPrefix(k.status, "Cancelled after ") || !strings.HasSuffix(k.status, " rows; nothing was written.") ||
		p.summary.Text != k.status || p.dry.Disabled() {
		t.Errorf("cancelled: %v %q, summary %q", k.state, k.status, p.summary.Text)
	}
	p.format.SetSelected("Excel")
	pump(t, fx.q, func() bool { return strings.HasPrefix(it.footer.Text, "Could not read the file as Excel") })
	p.dryRun()
	if k := lastTaskEnds(t, fx); k.state != taskFailed || !strings.HasPrefix(k.status, "Could not read the file past 0 rows: ") || p.summary.Text != k.status {
		t.Errorf("failed: %v %q", k.state, k.status)
	}
}

func TestAColumnNoFileColumnFillsIsSaid(t *testing.T) {
	fx, tb := itemsDescribed(t)
	tb.table.Columns[1].Type.Nullable, tb.table.Columns[1].HasDefault = false, false
	tb.table.Columns = append(tb.table.Columns, model.Column{Name: "total", Type: model.DataType{Class: model.TypeInteger}, Generated: "id * 2"})
	it, p := importing(t, fx, "id\n1\n")
	if !strings.HasPrefix(it.footer.Text, "name needs a value, and no column of the file fills it. The first row goes in as shown.") {
		t.Errorf("footer %q", it.footer.Text)
	}
	if _, ok := p.pairs["total"]; ok || pickFor(p, "total") != nil {
		t.Error("a generated column is the database's to fill")
	}
	p.dryRun()
	if len(fx.s.tasks) != 0 || p.tabs.SelectedIndex() != 1 || p.summary.Text != "Every row would be refused: name needs a value, and no column of the file fills it." {
		t.Errorf("no dry run needed: %d tasks, %q", len(fx.s.tasks), p.summary.Text)
	}
	p.summary.SetText("")
	if p.startImport(); len(fx.s.tasks) != 0 || !strings.HasPrefix(p.summary.Text, "Every row would be refused: name needs a value") {
		t.Errorf("nor an import: %q", p.summary.Text)
	}
	pickFor(p, "id").SetSelected(notImported)
	p.dryRun()
	if len(fx.s.tasks) != 0 {
		t.Error("no column filled, no dry run")
	}
	if got := unfilledText([]string{"a", "b"}); got != "a, b need a value, and no column of the file fills them." {
		t.Errorf("%q", got)
	}
}

// browsesSoFar is how many times the fake source has been browsed.
func browsesSoFar() int {
	browses.Lock()
	defer browses.Unlock()
	return len(browses.opts)
}

func TestAnImportWritesTheRowsAndRereadsTheTable(t *testing.T) {
	fx, _ := itemsDescribed(t)
	it, p := importing(t, fx, "name,id\nfirst,1\n,2\n")
	if findButton(it.item.Content, "Dry Run") != p.dry || findButton(it.item.Content, "Import") != p.load || p.load.Importance != widget.HighImportance {
		t.Error("Dry Run and Import are the panel's, Import the one to press")
	}
	before, reads := len(writtenPlans()), browsesSoFar()
	test.Tap(p.load)
	if !p.load.Disabled() || !p.dry.Disabled() {
		t.Error("one thing at a time")
	}
	if p.dryRun(); len(fx.s.runningTasks(nil)) != 1 {
		t.Error("no dry run while importing")
	}
	if p.startImport(); len(fx.s.runningTasks(nil)) != 1 {
		t.Error("one import at a time")
	}
	k := lastTaskEnds(t, fx)
	if k.title != "Import people.csv into items" || k.state != taskDone || k.status != "Imported 2 rows into items." ||
		it.footer.Text != k.status || p.load.Disabled() || p.dry.Disabled() {
		t.Errorf("task %q: %v %q, footer %q", k.title, k.state, k.status, it.footer.Text)
	}
	select {
	case <-k.finished:
	default:
		t.Error("an import says when it has stopped writing, as quitting waits for it")
	}
	plans := writtenPlans()[before:]
	if len(plans) != 1 || len(plans[0].Statements) != 2 || plans[0].Statements[1].SQL != "0 [] map[id:2 name:<nil>]" || plans[0].Statements[0].Confirmed {
		t.Errorf("the rows written as new rows, a batch a transaction: %+v", plans)
	}
	pump(t, fx.q, func() bool { return browsesSoFar() > reads })
}

func TestProductionAsksBeforeImporting(t *testing.T) {
	fx := newFixture(t)
	c, err := fx.conns.Create(store.SavedConnection{Name: "prod", Driver: "postgres", Host: "db1", Environment: "production"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return tb.table != nil })
	_, p := importing(t, fx, "name,id\nfirst,1\n")
	before := len(writtenPlans())
	test.Tap(p.load)
	if text := labelText(fx.s.win.Canvas().Overlays().Top()); !strings.Contains(text, "“items” on “prod”, which is marked Production") || len(fx.s.tasks) != 0 {
		t.Fatalf("Import asks before writing to production: %q", text)
	}
	tapOnTop(t, fx, "Cancel")
	if len(fx.s.tasks) != 0 {
		t.Fatal("no to production writes nothing")
	}
	test.Tap(p.load)
	tapOnTop(t, fx, "Import")
	if k := lastTaskEnds(t, fx); k.state != taskDone || len(writtenPlans()) != before+1 || !writtenPlans()[before].Statements[0].Confirmed {
		t.Errorf("the rows written carry the consent: %v %q", k.state, k.status)
	}
}

func TestAnImportStoppedSaysWhereAndWhatWasWritten(t *testing.T) {
	fx, tb := itemsDescribed(t)
	it, p := importing(t, fx, "name,id\nfirst,1\nsecond,2\n")
	failWrite.Store(2)
	t.Cleanup(func() { failWrite.Store(0) })
	test.Tap(p.load)
	k := lastTaskEnds(t, fx)
	if k.state != taskFailed || k.status != "Stopped at row 2: fakesql: duplicate key. Nothing was written." || it.footer.Text != k.status ||
		!fx.s.errors.shown() || fx.s.errors.message.Text != k.status {
		t.Errorf("%v %q, footer %q", k.state, k.status, it.footer.Text)
	}
	failWrite.Store(0)
	fx.s.selectTab(tb)
	_, p2 := importing(t, fx, "name,id\nfirst,1\nsecond,x\n")
	test.Tap(p2.load)
	if k := lastTaskEnds(t, fx); k.status != "Stopped at row 2: id: not a whole number (x). Nothing was written." {
		t.Errorf("%q", k.status)
	}
}

func TestAnImportSaysHowLongIsLeftAndCanBeCancelled(t *testing.T) {
	fx, _ := itemsDescribed(t)
	var b strings.Builder
	b.WriteString("name,id\n")
	for i := range 300000 {
		fmt.Fprintf(&b, "n,%d\n", i)
	}
	_, p := importing(t, fx, b.String())
	test.Tap(p.dry)
	lastTaskEnds(t, fx)
	test.Tap(p.load)
	k := fx.s.tasks[len(fx.s.tasks)-1]
	pump(t, fx.q, func() bool { return k.frac > 0 })
	if !strings.Contains(k.status, " of 300,000 rows · ") {
		t.Errorf("the rows the dry run counted: %q", k.status)
	}
	fx.s.stopTask(k)
	lastTaskEnds(t, fx)
	if k.state != taskCancelled || !strings.HasPrefix(k.status, "Cancelled. The first ") || !strings.HasSuffix(k.status, " rows were written, and none after.") {
		t.Errorf("%v %q", k.state, k.status)
	}
}

func TestWhatAnImportLeftWrittenIsWorded(t *testing.T) {
	for _, c := range []struct {
		n      int64
		undone bool
		want   string
	}{
		{0, true, " Nothing was written."},
		{1, true, " The first row was written, and none after."},
		{1500, true, " The first 1,500 rows were written, and none after."},
		{0, false, " The server could not undo the rows before it, so some may have been written."},
		{500, false, " The first 500 rows were written; the server could not undo the rows after them, so some may have been too."},
	} {
		if got := wroteText(c.n, c.undone); got != c.want {
			t.Errorf("%d %v: %q", c.n, c.undone, got)
		}
	}
	if _, say := importEnd(transfer.Loaded{Written: 3}, errors.New("gone"), false, "items"); say != "Not imported: gone. The first 3 rows were written, and none after." {
		t.Errorf("%q", say)
	}
	if _, say := importEnd(transfer.Loaded{}, &transfer.LoadError{Row: 2, Err: errors.New("dup")}, false, "items"); say != "Stopped at row 2: dup. The server could not undo the rows before it, so some may have been written." {
		t.Errorf("%q", say)
	}
	if _, say := importEnd(transfer.Loaded{Written: 1}, nil, false, "items"); say != "Imported 1 row into items." {
		t.Errorf("%q", say)
	}
}
