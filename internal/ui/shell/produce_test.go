package shell

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// Writing a record to a topic (T2.77, FR-13.11).

func TestARecordIsReadFromWhatWasTyped(t *testing.T) {
	req, err := produceFrom("orders", "k1", `{"total":1}`, "2",
		"trace-id: abc\nsource: ikigai", "orders-value")
	if err != nil {
		t.Fatalf("reading the form: %v", err)
	}
	if req.Topic != "orders" || string(req.Key) != "k1" || string(req.Value) != `{"total":1}` {
		t.Errorf("the record reads as %+v", req)
	}
	if req.Partition != 2 {
		t.Errorf("a record addressed to partition 2 reads as %d", req.Partition)
	}
	if req.Subject != "orders-value" {
		t.Errorf("the subject to check it against reads as %q", req.Subject)
	}
	if len(req.Headers) != 2 || req.Headers[0].Key != "trace-id" || string(req.Headers[0].Value) != "abc" {
		t.Errorf("the headers read as %+v", req.Headers)
	}
	// Nothing is confirmed by typing it: consent is asked for afterwards and
	// only where the connection calls for it (FR-4.9).
	if req.Confirmed {
		t.Error("filling in the form counted as consent to write")
	}
}

func TestARecordWithNoKeyIsNotARecordWithAnEmptyKey(t *testing.T) {
	// A record with no key is spread across the partitions; one with an empty
	// key always hashes to the same partition. An empty box must not quietly
	// become the second of those.
	req, err := produceFrom("orders", "", "v", "", "", "")
	if err != nil {
		t.Fatalf("reading the form: %v", err)
	}
	if req.Key != nil {
		t.Errorf("an empty key box became the key %q", req.Key)
	}
	// And naming no partition means any of them, which is -1: 0 is a
	// partition, so it cannot also mean "wherever".
	if req.Partition != -1 {
		t.Errorf("a record addressed to no partition reads as partition %d", req.Partition)
	}
	if req.Subject != "" {
		t.Errorf("a record checked against nothing names the subject %q", req.Subject)
	}
}

func TestAPartitionThatIsNotOneIsRefusedBeforeAnythingIsSent(t *testing.T) {
	for _, bad := range []string{"first", "-1", "1.5", "0x2"} {
		if _, err := produceFrom("orders", "", "v", bad, "", ""); err == nil {
			t.Errorf("a partition written as %q was accepted", bad)
		}
	}
	// "any" is how the box says it when it is empty, and means the same.
	req, err := produceFrom("orders", "", "v", "any", "", "")
	if err != nil || req.Partition != -1 {
		t.Errorf("a partition of \"any\" reads as %d: %v", req.Partition, err)
	}
}

func TestHeadersAreReadOnePerLine(t *testing.T) {
	// A header's value may hold a colon — a trace parent does — and a name
	// may not, so the first colon is the one that separates.
	hs, err := headersFrom("traceparent: 00-abc-def-01\n\n  spaced  :  value  ")
	if err != nil {
		t.Fatalf("reading the headers: %v", err)
	}
	if len(hs) != 2 {
		t.Fatalf("two headers and a blank line between them read as %+v", hs)
	}
	if hs[0].Key != "traceparent" || string(hs[0].Value) != "00-abc-def-01" {
		t.Errorf("a header whose value holds colons reads as %+v", hs[0])
	}
	if hs[1].Key != "spaced" || string(hs[1].Value) != "value" {
		t.Errorf("a header written with spaces around it reads as %+v", hs[1])
	}
	if got := headersOf(t, ""); got != nil {
		t.Errorf("no headers at all read as %+v", got)
	}

	// A line that is not a header says which line it was.
	if _, err := headersFrom("trace-id abc"); err == nil || !strings.Contains(err.Error(), "header 1") {
		t.Errorf("a line with no colon in it: %v", err)
	}
	if _, err := headersFrom("ok: yes\n: nameless"); err == nil || !strings.Contains(err.Error(), "header 2") {
		t.Errorf("a header with no name: %v", err)
	}
}

// headersOf is headersFrom where the text is known to be good.
func headersOf(t *testing.T, text string) []model.RecordHeader {
	t.Helper()
	hs, err := headersFrom(text)
	if err != nil {
		t.Fatalf("reading %q: %v", text, err)
	}
	return hs
}

func TestOnlyATopicOffersToBeWrittenTo(t *testing.T) {
	fx := newFixture(t)
	c, err := fx.conns.Create(store.SavedConnection{Name: "kafka1", Driver: "recordfake", Host: "kafka1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fx.s.Explorer.Refresh(explorer.RootID)
	loaded(t, fx, explorer.RootID)
	loaded(t, fx, view.ConnectionID(c.ID))
	loaded(t, fx, view.NodeID(c.ID, model.NewRef(model.KindCluster, "cluster")))

	m := fx.s.explorerMenu(view.NodeID(c.ID, topicRef))
	if !slices.Contains(labels(m), titleOf(fx, cmdProduce)) {
		t.Errorf("a topic's menu does not offer to write a record: %q", labels(m))
	}

	// A table is not written to this way, and its menu does not suggest it
	// is: an item greyed out on every table would be noise about something
	// tables cannot do at all.
	ft := newFixture(t)
	ct := selectItems(t, ft)
	tm := ft.s.explorerMenu(view.NodeID(ct.ID, itemsNode.Ref))
	if slices.Contains(labels(tm), titleOf(ft, cmdProduce)) {
		t.Errorf("a table's menu offers to write a record: %q", labels(tm))
	}
}

func TestWritingToProductionAsksAndSendsNothingUntilItIsAnswered(t *testing.T) {
	fx, tb := openRecords(t)
	live, ok := fx.s.d.WS.Get(tb.connID)
	if !ok {
		t.Fatal("the connection is not open")
	}
	src := live.Source.(*recordSource)
	src.refuse = fmt.Errorf("%w: writing on a production connection", source.ErrConfirmationRequired)

	req := source.ProduceRequest{Topic: "events", Partition: -1, Value: []byte("a record")}
	fx.s.writeRecord(tb.connID, req)
	pump(t, fx.q, func() bool { return fx.s.win.Canvas().Overlays().Top() != nil })

	if text := labelText(fx.s.win.Canvas().Overlays().Top()); !strings.Contains(text, "marked Production") ||
		!strings.Contains(text, "Nothing has been sent yet") {
		t.Errorf("writing to production asks first, and says nothing has gone: %q", text)
	}
	if len(src.produced) != 0 {
		t.Fatalf("a record was sent before the question was answered: %+v", src.produced)
	}

	// Saying no sends nothing at all.
	tapOnTop(t, fx, "Cancel")
	if len(src.produced) != 0 {
		t.Errorf("saying no sent %d records", len(src.produced))
	}

	// Saying yes sends exactly one, with the consent that was given for it.
	fx.s.writeRecord(tb.connID, req)
	pump(t, fx.q, func() bool { return fx.s.win.Canvas().Overlays().Top() != nil })
	tapOnTop(t, fx, "Write")
	pump(t, fx.q, func() bool { return len(src.produced) == 1 })
	if !src.produced[0].Confirmed {
		t.Error("the record went without the consent that had just been given for it")
	}
	// And that consent belongs to that record: it is not left behind in what
	// the caller still holds, to be sent with the next one (FR-4.9).
	if req.Confirmed {
		t.Error("consent given once stayed behind in the request")
	}
}

func TestAReadOnlyConnectionSaysSoAndWritesNothing(t *testing.T) {
	fx, tb := openRecords(t)
	live, ok := fx.s.d.WS.Get(tb.connID)
	if !ok {
		t.Fatal("the connection is not open")
	}
	src := live.Source.(*recordSource)
	src.refuse = fmt.Errorf("%w: write operation refused", source.ErrReadOnly)

	fx.s.writeRecord(tb.connID, source.ProduceRequest{Topic: "events", Partition: -1, Value: []byte("x")})
	pump(t, fx.q, func() bool { return strings.Contains(fx.s.errors.text, "read-only") })
	if len(src.produced) != 0 {
		t.Errorf("a read-only connection wrote %d records", len(src.produced))
	}
	// And it does not ask. Read-only is a decision already made, not a
	// question about this record.
	if fx.s.win.Canvas().Overlays().Top() != nil {
		t.Error("a read-only connection asked whether to write anyway")
	}
}
