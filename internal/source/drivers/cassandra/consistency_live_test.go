//go:build conformance

package cassandra

import (
	"context"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// The level a connection reads at, against a real cluster (T2.52).

// atLevel is a connection to the fixture that reads at one named level.
func atLevel(level string) source.ConnectionConfig {
	cfg := liveConfig(fixture)
	cfg.Params = map[string]string{"consistency": level}
	return cfg
}

func TestLiveReadsAtTheLevelChosen(t *testing.T) {
	// A table with rows in it, so that a level that answers is seen to answer
	// with the data and not merely without complaint.
	pages := model.NewRef(model.KindTable, fixture, "pages")
	fixed := live(t, liveConfig(fixture))
	seeded(t, fixed)
	pagesSeeded(t, fixed)
	for _, level := range []string{"ONE", "LOCAL_ONE", "QUORUM", "LOCAL_QUORUM", "ALL"} {
		// One node holding every replica of this keyspace answers all of
		// these: a majority of one, and all of one, are that node.
		src := live(t, atLevel(level))
		rows, err := pageOrError(src, pages, 0, 5)
		if err != nil {
			t.Errorf("reading at %s: %v", level, err)
			continue
		}
		if len(rows) == 0 {
			t.Errorf("reading at %s read nothing", level)
		}
	}
}

func TestLiveALevelTheClusterCannotReachIsTheClustersToRefuse(t *testing.T) {
	// THREE replicas, of a keyspace that has one. The level is the cluster's
	// to judge, and this is what says the chosen one is really being sent:
	// were it dropped, or replaced by the default, this read would succeed.
	fixed := live(t, liveConfig(fixture))
	seeded(t, fixed)
	pagesSeeded(t, fixed)
	src := live(t, atLevel("THREE"))
	_, err := pageOrError(src, model.NewRef(model.KindTable, fixture, "pages"), 0, 5)
	if err == nil {
		t.Fatal("a read at THREE was answered by a cluster of one node")
	}
	// The cluster's own words, carried rather than swallowed.
	if !strings.Contains(strings.ToUpper(err.Error()), "THREE") &&
		!strings.Contains(strings.ToLower(err.Error()), "consistency") {
		t.Errorf("the refusal says %q", err)
	}
	// And the tree, read through the same session, is refused for the same
	// reason: a person who asked for three replicas asked about all of it.
	if _, err := src.Children(context.Background(),
		model.NewRef(model.KindDatabase, fixture)); err == nil {
		t.Error("the tree was read at a level the cluster cannot reach")
	}
}

func TestLiveALevelNobodyOffersNeverConnects(t *testing.T) {
	ctx := context.Background()
	d := Driver{}
	src, err := d.Open(ctx, atLevel("EACH_QUORM"))
	if err == nil {
		src.Close()
		t.Fatal("a connection was opened at a level nobody offers")
	}
	if kind(err) != source.ConnectConfig {
		t.Fatalf("the refusal is %#v", err)
	}
	// It is refused before a node is dialled, and says what to choose.
	if !strings.Contains(err.Error(), "QUORUM") {
		t.Errorf("the refusal says %q", err)
	}
}
