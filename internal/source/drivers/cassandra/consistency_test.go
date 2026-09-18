package cassandra

import (
	"strings"
	"testing"

	"github.com/gocql/gocql"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// The level a connection reads and writes at (T2.52).

func at(level string) source.ConnectionConfig {
	return source.ConnectionConfig{Host: "db", Params: map[string]string{"consistency": level}}
}

func TestTheLevelsOfferedAreTheOnesAReadCanUse(t *testing.T) {
	var offered []string
	for _, f := range (Driver{}).Describe().Fields {
		if f.Key == "consistency" {
			offered = f.Options
			if f.Kind != source.FieldSelect || f.Default != defaultConsistency {
				t.Errorf("the level is chosen as %+v", f)
			}
		}
	}
	if len(offered) == 0 {
		t.Fatal("the form offers no consistency level")
	}
	// ANY is a write's level alone: a read at ANY is refused by the cluster,
	// so offering it would be offering a setting that stops reading working.
	for _, level := range offered {
		if level == "ANY" {
			t.Error("ANY is offered, and nothing can be read at it")
		}
		if _, err := gocql.ParseConsistencyWrapper(level); err != nil {
			t.Errorf("%s is offered and is no level: %v", level, err)
		}
	}
	// Every level a cluster answers reads at, offered: a person who needs
	// LOCAL_QUORUM must be able to say so.
	for _, level := range []string{"ONE", "LOCAL_ONE", "TWO", "THREE", "QUORUM", "LOCAL_QUORUM", "EACH_QUORUM", "ALL"} {
		if !strings.Contains(strings.Join(offered, " "), level) {
			t.Errorf("%s is not offered", level)
		}
	}
}

func TestTheLevelChosenIsTheOneAskedFor(t *testing.T) {
	for name, want := range map[string]gocql.Consistency{
		"ONE":          gocql.One,
		"one":          gocql.One,
		"  LOCAL_ONE ": gocql.LocalOne,
		"TWO":          gocql.Two,
		"THREE":        gocql.Three,
		"QUORUM":       gocql.Quorum,
		"LOCAL_QUORUM": gocql.LocalQuorum,
		"EACH_QUORUM":  gocql.EachQuorum,
		"ALL":          gocql.All,
	} {
		got, err := consistencyOf(at(name))
		if err != nil || got != want {
			t.Errorf("%q reads at %v: %v", name, got, err)
		}
	}
	// Nothing chosen is a majority of the replicas, which is what gocql
	// itself would have used.
	if got, err := consistencyOf(at("")); err != nil || got != gocql.Quorum {
		t.Errorf("nothing chosen reads at %v: %v", got, err)
	}
	if got, err := consistencyOf(source.ConnectionConfig{Host: "db"}); err != nil || got != gocql.Quorum {
		t.Errorf("no setting at all reads at %v: %v", got, err)
	}
}

func TestALevelNobodyOffersIsRefused(t *testing.T) {
	for _, name := range []string{"EACH_QUORM", "MOST", "1", "SERIAL", "LOCAL_SERIAL"} {
		_, err := consistencyOf(at(name))
		if err == nil {
			t.Errorf("%q was taken for a level", name)
			continue
		}
		if kind(err) != source.ConnectConfig {
			t.Errorf("%q: %v", name, err)
		}
		// The refusal says what there is to choose from.
		if !strings.Contains(err.Error(), "QUORUM") {
			t.Errorf("%q says %q", name, err)
		}
	}
	// ANY is a level, and is still refused: it is a write's alone.
	if _, err := consistencyOf(at("ANY")); err == nil {
		t.Error("a read at ANY was allowed")
	}
}

func TestTheClusterIsToldTheLevel(t *testing.T) {
	cluster, err := clusterOf(at("LOCAL_QUORUM"))
	if err != nil {
		t.Fatal(err)
	}
	// Set on the cluster, so that every statement carries it: gocql gives a
	// query the session's level.
	if cluster.Consistency != gocql.LocalQuorum {
		t.Errorf("the cluster reads at %v", cluster.Consistency)
	}
	if _, err := clusterOf(at("MOST")); err == nil {
		t.Error("a cluster was dialled at a level nobody offers")
	}
}
