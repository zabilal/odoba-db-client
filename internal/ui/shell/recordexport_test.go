package shell

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/export"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// Exporting a consumed window of a topic (FR-10.8, FR-13.16).

// A topic's window exports as the window reads it: JSON as JSON, text as
// text, and bytes nothing will read as bytes.
func TestExportingATopicWritesWhatTheWindowShows(t *testing.T) {
	fx, tb := openRecords(t)
	out := &sink{}
	j := fx.s.runExport(tb, fx.s.exportSource(), export.Options{Format: export.NDJSON}, out, "events.ndjson", nil)
	pump(t, fx.q, func() bool { return j.done })
	if j.err != nil {
		t.Fatal(j.err)
	}
	got := out.String()
	// The JSON value is JSON in the file, not a string of digits and not
	// base64: a record's value is what it says.
	if !strings.Contains(got, `"value":{"total":12.50}`) {
		t.Errorf("the JSON record reads:\n%s", got)
	}
	if !strings.Contains(got, `"value":"plain words"`) {
		t.Errorf("the text record reads:\n%s", got)
	}
	// Bytes no decoder will read are still there, as bytes.
	if !strings.Contains(got, `"value":"//4A"`) {
		t.Errorf("the record of bare bytes reads:\n%s", got)
	}
	if !strings.Contains(got, `"key":"order-1"`) {
		t.Errorf("the key reads:\n%s", got)
	}
}

// The decoder somebody chose for the topic is the one the export uses:
// the file is what the window was showing.
func TestExportingATopicUsesTheDecoderChosen(t *testing.T) {
	fx, tb := openRecords(t)
	ctx := context.Background()
	if err := fx.hist.PutDecoder(ctx, tb.connID, app.DecodeValue, "events",
		localdb.DecoderChoice{Name: "Text"}); err != nil {
		t.Fatal(err)
	}
	out := &sink{}
	j := fx.s.runExport(tb, fx.s.exportSource(), export.Options{Format: export.NDJSON}, out, "events.ndjson", nil)
	pump(t, fx.q, func() bool { return j.done })
	if j.err != nil {
		t.Fatal(j.err)
	}
	// Read as text, the JSON value is a string in the file rather than an
	// object: that is what reading it as text means.
	if got := out.String(); !strings.Contains(got, `"value":"{\"total\":12.50}"`) {
		t.Errorf("the file reads:\n%s", got)
	}
}

// A table's rows are not records, and nothing decodes them.
func TestExportingATableIsUnchanged(t *testing.T) {
	fx, tb := openItems(t)
	out := &sink{}
	j := fx.s.runExport(tb, fx.s.exportSource(), export.Options{Format: export.NDJSON}, out, "items.ndjson", nil)
	pump(t, fx.q, func() bool { return j.done })
	if j.err != nil {
		t.Fatal(j.err)
	}
	if got := out.String(); !strings.Contains(got, `"name":"item 0"`) {
		t.Errorf("the file reads:\n%.200s", got)
	}
}

// A topic exported from the explorer, with the rest of a batch, is
// decoded too: where it was exported from is not what it holds.
func TestExportingAMarkedTopicDecodesItToo(t *testing.T) {
	fx, tb := openRecords(t)
	fx.s.Explorer.Refresh(explorer.RootID)
	loaded(t, fx, explorer.RootID)
	loaded(t, fx, view.ConnectionID(tb.connID))
	loaded(t, fx, view.NodeID(tb.connID, model.NewRef(model.KindCluster, "cluster")))
	id := view.NodeID(tb.connID, topicRef)
	if !fx.s.Explorer.ToggleMark(id) {
		t.Fatalf("the topic could not be marked: %v", fx.s.Explorer.Marks())
	}
	fx.s.sync()
	if !fx.s.canExportMarked() {
		t.Fatal("a marked topic was not offered an export")
	}
	dir := t.TempDir()
	fx.s.run(cmdMarksExport)
	test.Tap(findButton(fx.s.win.Canvas().Overlays().Top(), "Choose Where…"))
	if len(fx.files.saves) != 1 {
		t.Fatalf("it asked for %d places to save", len(fx.files.saves))
	}
	fx.files.answer(filepath.Join(dir, "events.csv"), nil)
	var body string
	pump(t, fx.q, func() bool {
		b, err := os.ReadFile(filepath.Join(dir, "events.csv"))
		body = string(b)
		return err == nil && strings.Contains(body, "order-1")
	})
	if strings.Contains(body, `\x6f72646572`) {
		t.Errorf("the key was written as bytes:\n%s", body)
	}
	if !strings.Contains(body, `"{""total"":12.50}"`) { // CSV doubles the quotes
		t.Errorf("the value was not written as it reads:\n%s", body)
	}
}

// shoutingDecoder stands in for a schema registry's decoder.
type shoutingDecoder struct{}

func (shoutingDecoder) Name() string               { return "Avro (events-value v1)" }
func (shoutingDecoder) Decode([]byte) (any, error) { return "SHOUTED", nil }

// A choice that names the registry's decoder is the registry's decoder,
// and one that names a local decoder is that: the name is what somebody
// picked from, and both lists are picked from in the same window.
func TestTheDecoderIsChosenByName(t *testing.T) {
	fx, tb := openRecords(t)
	ctx := context.Background()
	reg := shoutingDecoder{}
	for _, c := range []struct{ name, want string }{
		{"Avro (events-value v1)", "Avro (events-value v1)"},
		{"Text", "Text"},
		{"Nothing anybody offers", ""},
		{"", ""},
	} {
		if err := fx.hist.PutDecoder(ctx, tb.connID, app.DecodeValue, "events",
			localdb.DecoderChoice{Name: c.name}); err != nil {
			t.Fatal(err)
		}
		r := fx.s.readerFor(tb.connID, app.DecodeValue, "events", reg)
		got := ""
		if r.Chosen != nil {
			got = r.Chosen.Name()
		}
		if got != c.want {
			t.Errorf("%q was read with %q, want %q", c.name, got, c.want)
		}
		if r.Extra != reg {
			t.Errorf("%q lost the registry's decoder", c.name)
		}
	}
}
