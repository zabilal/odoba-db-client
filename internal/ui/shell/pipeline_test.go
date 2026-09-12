package shell

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/store"
)

// openDocuments opens a data tab over the document fake's collection.
func openDocuments(t *testing.T, fx *fixture, host string) *tab {
	t.Helper()
	conn := store.SavedConnection{Name: host, Driver: "docfake", Host: host}
	if host == "prod" {
		conn.Environment = "production"
	}
	c, err := fx.conns.Create(conn, nil)
	if err != nil {
		t.Fatal(err)
	}
	fx.s.OpenObject(c.ID, model.Node{Ref: peopleRef, Label: "people", Browsable: true})
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool {
		if tb.model == nil {
			return false
		}
		_, ok := tb.model.Row(tb.ctx, 1)
		return ok
	})
	return tb
}

// pipelineOn opens a tab's pipeline editor.
func pipelineOn(t *testing.T, fx *fixture, tb *tab) *pipelineBar {
	t.Helper()
	fx.s.run(cmdPipeline)
	if tb.pipeline == nil || !tb.pipeline.shown() {
		t.Fatal("the pipeline editor did not open")
	}
	return tb.pipeline
}

func TestAPipelineIsOfferedWhereOneCanBeRun(t *testing.T) {
	fx := newFixture(t)
	// A table's rows are not read by a pipeline, and nothing offers one.
	selectItems(t, fx)
	fx.s.run(cmdOpen)
	pump(t, fx.q, func() bool { return fx.onlyTab(t).model != nil })
	if fx.s.canPipeline() {
		t.Error("a table offers a pipeline")
	}
	if c, _ := fx.s.reg.Get(cmdPipeline); c.Title != "Aggregation Pipeline…" {
		t.Errorf("the command is called %q", c.Title)
	}
}

func TestAPipelineRunsAndPutsWhatItMadeInTheGrid(t *testing.T) {
	fx := newFixture(t)
	tb := openDocuments(t, fx, "docs")
	if !fx.s.canPipeline() {
		t.Fatal("a collection does not offer a pipeline")
	}
	b := pipelineOn(t, fx, tb)

	// Nothing runs until there are stages to run.
	b.run()
	if !strings.Contains(b.note.Text, "stages") {
		t.Errorf("an empty pipeline says %q", b.note.Text)
	}

	b.entry.SetText(`[{"$group": {"_id": "$name", "n": {"$sum": 1}}}]`)
	b.run()
	pump(t, fx.q, func() bool { return len(tb.model.Columns()) == 2 })
	if got := tb.model.Columns(); got[0].Name != "_id" || got[1].Name != "n" {
		t.Errorf("the grid's columns are %v, want what the pipeline made", got)
	}
	pump(t, fx.q, func() bool { _, ok := tb.model.Row(tb.ctx, 1); return ok })
	row, _ := tb.model.Row(tb.ctx, 1)
	if len(row) != 2 || row[0] != "Grace" {
		t.Errorf("a row of the pipeline is %v", row)
	}
	if text, confirmed := lastPipeline(); !strings.Contains(text, "$group") || confirmed {
		t.Errorf("what ran was %q (consented %v)", text, confirmed)
	}
	// Show Documents puts the collection back.
	if !b.back.Visible() {
		t.Error("there is no way back to the documents")
	}
	b.restore()
	pump(t, fx.q, func() bool { return len(tb.model.Columns()) == 3 })
	if got := tb.model.Columns(); got[1].Name != "name" {
		t.Errorf("the grid's columns are %v, want the collection's", got)
	}
	if b.back.Visible() {
		t.Error("the way back is still offered with nothing to go back to")
	}
}

func TestAPipelineTheServerRefusesSaysSo(t *testing.T) {
	fx := newFixture(t)
	tb := openDocuments(t, fx, "docs")
	b := pipelineOn(t, fx, tb)
	before := tb.model.Columns()
	b.entry.SetText(`[{"$bad": 1}]`)
	b.run()
	pump(t, fx.q, func() bool { return strings.Contains(b.note.Text, "$bad") })
	if !strings.Contains(b.note.Text, "Could not run") {
		t.Errorf("it says %q", b.note.Text)
	}
	// What was shown is still shown: a pipeline that failed changes nothing.
	if got := tb.model.Columns(); len(got) != len(before) {
		t.Errorf("the grid's columns are %v, want the collection's", got)
	}
	if b.back.Visible() {
		t.Error("a pipeline that failed offers a way back")
	}
}

func TestAPipelineThatWritesOnProductionAsksFirst(t *testing.T) {
	fx := newFixture(t)
	tb := openDocuments(t, fx, "prod")
	b := pipelineOn(t, fx, tb)
	b.entry.SetText(`[{"$match": {}}, {"$out": "copies"}]`)
	b.run()
	pump(t, fx.q, func() bool {
		return strings.Contains(labelText(fx.s.win.Canvas().Overlays().Top()), "marked Production")
	})
	if _, confirmed := lastPipeline(); confirmed {
		t.Error("it ran with consent before consent was given")
	}
	tapOnTop(t, fx, "Cancel")
	if b.note.Text != "Not run" {
		t.Errorf("saying no says %q", b.note.Text)
	}
	b.run()
	pump(t, fx.q, func() bool {
		return strings.Contains(labelText(fx.s.win.Canvas().Overlays().Top()), "marked Production")
	})
	tapOnTop(t, fx, "Run")
	pump(t, fx.q, func() bool { _, confirmed := lastPipeline(); return confirmed })
	if text, _ := lastPipeline(); !strings.Contains(text, "$out") {
		t.Errorf("what ran with consent was %q", text)
	}
}

func TestThePipelineEditorOpensAndCloses(t *testing.T) {
	fx := newFixture(t)
	tb := openDocuments(t, fx, "docs")
	b := pipelineOn(t, fx, tb)
	if len(tb.top.Objects) != 1 {
		t.Fatal("the editor is not above the grid")
	}
	fx.s.run(cmdPipeline)
	if b.shown() {
		t.Error("the editor did not close")
	}
	// It keeps what was typed while the tab is open: the editor is the
	// tab's, not one made afresh each time it is shown.
	b.entry.SetText(`[{"$match": {}}]`)
	fx.s.run(cmdPipeline)
	if !tb.pipeline.shown() || tb.pipeline.entry.Text == "" {
		t.Error("it forgot the stages while it was hidden")
	}
}
