package shell

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/export"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/filedlg"
)

// fakeFiles is a file dialog a test answers: it keeps what each dialog was
// asked, and the last one's answer, waiting to be given.
type fakeFiles struct {
	saves, opens []filedlg.Options
	answer       filedlg.Done
}

func (f *fakeFiles) Save(_ fyne.Window, o filedlg.Options, done filedlg.Done) {
	f.saves, f.answer = append(f.saves, o), done
}

func (f *fakeFiles) Open(_ fyne.Window, o filedlg.Options, done filedlg.Done) {
	f.opens, f.answer = append(f.opens, o), done
}

// brokenRows fails at the first row, as a server going away does.
type brokenRows struct{}

func (brokenRows) Columns() []model.ColumnDef { return []model.ColumnDef{{Name: "n"}} }
func (brokenRows) Close() error               { return nil }
func (brokenRows) Next(context.Context) (model.Row, error) {
	return nil, errors.New("the server went away")
}

func TestTheShellUsesThePlatformsDialogsByDefault(t *testing.T) {
	fx := newFixture(t)
	d := fx.deps
	d.Files = nil
	s := New(fx.app, d)
	t.Cleanup(s.shutdown)
	if _, ok := s.d.Files.(filedlg.Native); !ok {
		t.Errorf("with no file dialogs given, the shell uses %T, want the platform's own", s.d.Files)
	}
}

// askExport opens the table's export form and asks for a file.
func askExport(t *testing.T) (*fixture, *tab) {
	t.Helper()
	fx, tb := openItems(t)
	fx.s.showExport()
	b := findButton(fx.s.win.Canvas().Overlays().Top(), "Choose File…")
	if b == nil {
		t.Fatal("the export form has no Choose File… button")
	}
	test.Tap(b)
	if len(fx.files.saves) != 1 {
		t.Fatalf("%d save dialogs asked, want 1", len(fx.files.saves))
	}
	return fx, tb
}

func TestExportAsksWhereInThePlatformsSaveDialog(t *testing.T) {
	fx, tb := askExport(t)
	o := fx.files.saves[0]
	if o.Name != tb.item.Text+".csv" || len(o.Extensions) != 1 || o.Extensions[0] != "csv" || o.Kind != "CSV" ||
		o.Accept != "Export" || o.Message != "Export “"+tb.item.Text+"” as CSV" {
		t.Errorf("the save dialog was asked %+v", o)
	}
	path := filepath.Join(t.TempDir(), "chosen.csv")
	fx.files.answer(path, nil)
	if len(fx.s.tasks) != 1 {
		t.Fatalf("%d tasks, want the export", len(fx.s.tasks))
	}
	k := fx.s.tasks[0]
	pump(t, fx.q, func() bool { return k.state != taskRunning })
	raw, err := os.ReadFile(path)
	if err != nil || k.state != taskDone || k.title != "Export to chosen.csv" {
		t.Fatalf("task %q ended %v (%s); file: %v", k.title, k.state, k.status, err)
	}
	if lines := strings.Count(string(raw), "\n"); lines != fakeRows+1 {
		t.Errorf("the file has %d lines, want a header and %d rows", lines, fakeRows)
	}
}

func TestACancelledSaveDialogExportsNothing(t *testing.T) {
	fx, _ := askExport(t)
	fx.files.answer("", nil)
	if len(fx.s.tasks) != 0 || fx.s.errors.shown() {
		t.Errorf("a cancel started %d tasks and showed an error: %v", len(fx.s.tasks), fx.s.errors.shown())
	}
}

func TestATabClosedWhileTheSaveDialogWasOpenMakesNoFile(t *testing.T) {
	fx, tb := askExport(t)
	fx.s.closeTab(tb.item)
	path := filepath.Join(t.TempDir(), "late.csv")
	fx.files.answer(path, nil)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a file was made for a tab that had gone: %v", err)
	}
	if len(fx.s.tasks) != 0 {
		t.Errorf("%d tasks started for a tab that had gone", len(fx.s.tasks))
	}
}

func TestAFileThatCannotBeMadeIsSaid(t *testing.T) {
	fx, _ := askExport(t)
	fx.files.answer(filepath.Join(t.TempDir(), "no such folder", "x.csv"), nil)
	if !fx.s.errors.shown() || !strings.Contains(fx.s.errors.message.Text, "could not create its file") {
		t.Errorf("error bar shown %v: %q", fx.s.errors.shown(), fx.s.errors.message.Text)
	}
	if len(fx.s.tasks) != 0 {
		t.Errorf("%d tasks started with no file to write", len(fx.s.tasks))
	}
	fx.s.errors.dismiss()
	fx.s.showExport()
	test.Tap(findButton(fx.s.win.Canvas().Overlays().Top(), "Choose File…"))
	fx.files.answer("", errors.New("the file dialog failed (code 0x3002)"))
	if !fx.s.errors.shown() || fx.s.errors.message.Text != "the file dialog failed (code 0x3002)" {
		t.Errorf("a dialog that failed is said: %q", fx.s.errors.message.Text)
	}
}

func TestAFailedExportRemovesTheFileItMade(t *testing.T) {
	fx, tb := openItems(t)
	path := filepath.Join(t.TempDir(), "broken.csv")
	src := &exportSrc{name: "broken", total: -1, rows: func() model.RowStream { return brokenRows{} }}
	j := fx.s.exportTo(tb, src, export.Options{Format: export.CSV}, path, nil)
	pump(t, fx.q, func() bool { return j.done })
	if j.err == nil {
		t.Fatal("the export should have failed")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a failed export left its file: %v", err)
	}
}

func TestAFileFieldIsChosenInThePlatformsOpenDialog(t *testing.T) {
	fx := newFixture(t)
	f := fx.s.showConnectionForm("")
	f.driver.SetSelected("PG Fake")
	if f.inputs["host"].choose != nil {
		t.Error("only a file field has Choose…")
	}
	f.driver.SetSelected("File Fake")
	in := f.inputs["database"]
	if in.choose == nil || findButton(in.obj, "Choose…") != in.choose {
		t.Fatal("a file field has no Choose… beside it")
	}
	test.Tap(in.choose)
	if len(fx.files.opens) != 1 {
		t.Fatalf("%d open dialogs asked, want 1", len(fx.files.opens))
	}
	if o := fx.files.opens[0]; o.Accept != "Choose" || o.Message != "Choose the File Fake database file" || o.Directory != "" {
		t.Errorf("the open dialog was asked %+v", o)
	}
	want := filepath.Join(t.TempDir(), "app.db")
	fx.files.answer(want, nil)
	if in.entry.Text != want {
		t.Fatalf("the field says %q, want the file chosen", in.entry.Text)
	}
	test.Tap(in.choose)
	if o := fx.files.opens[1]; o.Directory != filepath.Dir(want) {
		t.Errorf("the dialog starts in %q, want the folder of the file named (%q)", o.Directory, filepath.Dir(want))
	}
	fx.files.answer("", nil)
	if in.entry.Text != want {
		t.Errorf("a cancel changed the field to %q", in.entry.Text)
	}
	in.entry.SetText("relative.db")
	test.Tap(in.choose)
	if o := fx.files.opens[2]; o.Directory != "" {
		t.Errorf("a relative name has no folder to start in, got %q", o.Directory)
	}
	fx.files.answer("", errors.New("the file dialog failed"))
	if f.result.Text != "the file dialog failed" || f.result.Importance != widget.DangerImportance {
		t.Errorf("a dialog that failed is said: %q (%v)", f.result.Text, f.result.Importance)
	}
}
