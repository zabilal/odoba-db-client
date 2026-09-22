package shell

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// Making, unmaking and reshaping topics (T2.78, FR-13.12).

func TestATopicIsReadFromWhatWasTyped(t *testing.T) {
	spec, err := topicFrom(" orders ", "3", "2", "retention.ms: 600000\ncleanup.policy: compact")
	if err != nil {
		t.Fatalf("reading the form: %v", err)
	}
	if spec.Name != "orders" {
		t.Errorf("the name reads as %q, with its spaces still on it", spec.Name)
	}
	if spec.Partitions != 3 || spec.ReplicationFactor != 2 {
		t.Errorf("the shape reads as %d partitions in %d copies", spec.Partitions, spec.ReplicationFactor)
	}
	if spec.Config["retention.ms"] != "600000" || spec.Config["cleanup.policy"] != "compact" {
		t.Errorf("the settings read as %v", spec.Config)
	}
	// Nothing is confirmed by typing it: consent is asked for afterwards, and
	// only where the connection calls for it (FR-4.9).
	if spec.Confirmed {
		t.Error("filling in the form counted as consent to change the cluster")
	}
}

func TestATopicThatCouldNotExistIsRefusedInTheForm(t *testing.T) {
	for name, args := range map[string][4]string{
		"a topic with no name":          {"  ", "1", "1", ""},
		"a topic cut into none":         {"orders", "0", "1", ""},
		"partitions written in words":   {"orders", "three", "1", ""},
		"a topic kept in no copies":     {"orders", "1", "0", ""},
		"a setting that is not one":     {"orders", "1", "1", "retention.ms 600000"},
		"a setting with nothing to set": {"orders", "1", "1", ": 600000"},
	} {
		if _, err := topicFrom(args[0], args[1], args[2], args[3]); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	// A topic with no settings at all is a topic, not a mistake.
	spec, err := topicFrom("orders", "1", "1", "")
	if err != nil || spec.Config != nil {
		t.Errorf("a topic with no settings reads as %+v: %v", spec, err)
	}
}

func TestPartitionsToAddAreAtLeastOne(t *testing.T) {
	// Kafka cannot take partitions away, so asking for none or fewer is a
	// mistake to catch here rather than a request to send.
	for _, bad := range []string{"0", "-1", "", "some", "1.5"} {
		if _, err := partitionsFrom(bad); err == nil {
			t.Errorf("adding %q partitions was accepted", bad)
		}
	}
	if n, err := partitionsFrom(" 4 "); err != nil || n != 4 {
		t.Errorf("adding four partitions reads as %d: %v", n, err)
	}
}

func TestATopicsMenuOffersWhatCanBeDoneToIt(t *testing.T) {
	fx := newFixture(t)
	c, err := fx.conns.Create(store.SavedConnection{Name: "kafka1", Driver: "recordfake", Host: "kafka1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fx.s.Explorer.Refresh(explorer.RootID)
	loaded(t, fx, explorer.RootID)
	loaded(t, fx, view.ConnectionID(c.ID))
	loaded(t, fx, view.NodeID(c.ID, model.NewRef(model.KindCluster, "cluster")))

	got := labels(fx.s.explorerMenu(view.NodeID(c.ID, topicRef)))
	for _, id := range []string{cmdProduce, cmdTopicNew, cmdTopicPartition, cmdTopicDelete} {
		if !slices.Contains(got, titleOf(fx, id)) {
			t.Errorf("a topic's menu does not offer %q: %q", titleOf(fx, id), got)
		}
	}

	// A table is none of those things, and its menu says none of them.
	ft := newFixture(t)
	ct := selectItems(t, ft)
	tm := labels(ft.s.explorerMenu(view.NodeID(ct.ID, itemsNode.Ref)))
	for _, id := range []string{cmdTopicNew, cmdTopicPartition, cmdTopicDelete} {
		if slices.Contains(tm, titleOf(ft, id)) {
			t.Errorf("a table's menu offers %q: %q", titleOf(ft, id), tm)
		}
	}
}

func TestChangingTopicsOnProductionAsksAndDoesNothingUntilAnswered(t *testing.T) {
	fx, tb := openRecords(t)
	live, ok := fx.s.d.WS.Get(tb.connID)
	if !ok {
		t.Fatal("the connection is not open")
	}
	src := live.Source.(*recordSource)
	src.refuse = fmt.Errorf("%w: a structural change on a production connection", source.ErrConfirmationRequired)

	deleting := func(s source.Source, confirmed bool) error {
		return app.DeleteTopic(context.Background(), s, "events", confirmed)
	}
	fx.s.changeCluster(tb.connID, "delete events", deleting)
	pump(t, fx.q, func() bool { return fx.s.win.Canvas().Overlays().Top() != nil })

	if text := labelText(fx.s.win.Canvas().Overlays().Top()); !strings.Contains(text, "marked Production") ||
		!strings.Contains(text, "Nothing has happened yet") {
		t.Errorf("a structural change on production asks first, and says nothing has happened: %q", text)
	}
	if len(src.changed) != 0 {
		t.Fatalf("something changed before the question was answered: %v", src.changed)
	}

	// Saying no changes nothing at all.
	tapOnTop(t, fx, "Cancel")
	if len(src.changed) != 0 {
		t.Errorf("saying no changed %v", src.changed)
	}

	// Saying yes does it once, with the consent given for it.
	fx.s.changeCluster(tb.connID, "delete events", deleting)
	pump(t, fx.q, func() bool { return fx.s.win.Canvas().Overlays().Top() != nil })
	typeOnTop(t, fx, "kafka1")
	tapOnTop(t, fx, "Continue")
	pump(t, fx.q, func() bool { return len(src.changed) == 1 })
	if src.changed[0] != "delete events" {
		t.Errorf("what happened was %v", src.changed)
	}
}

func TestAReadOnlyConnectionChangesNoTopics(t *testing.T) {
	fx, tb := openRecords(t)
	live, ok := fx.s.d.WS.Get(tb.connID)
	if !ok {
		t.Fatal("the connection is not open")
	}
	src := live.Source.(*recordSource)
	src.refuse = fmt.Errorf("%w: structural change refused", source.ErrReadOnly)

	fx.s.changeCluster(tb.connID, "delete events", func(s source.Source, confirmed bool) error {
		return app.DeleteTopic(context.Background(), s, "events", confirmed)
	})
	pump(t, fx.q, func() bool { return strings.Contains(fx.s.errors.text, "read-only") })
	if len(src.changed) != 0 {
		t.Errorf("a read-only connection changed %v", src.changed)
	}
	// And it does not ask: read-only is a decision already made.
	if fx.s.win.Canvas().Overlays().Top() != nil {
		t.Error("a read-only connection asked whether to change the cluster anyway")
	}
}
