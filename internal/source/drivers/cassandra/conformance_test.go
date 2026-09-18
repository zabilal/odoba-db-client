//go:build conformance

package cassandra

import (
	"context"
	"fmt"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/conformance"
)

// The shared driver suite (REQ-DRV-1), against a real cluster. What this
// driver claims, it is held to; what it does not claim — writing, counting,
// distinct values, bulk loading — the suite skips.

func TestConformance(t *testing.T) {
	fixed := live(t, liveConfig(fixture))
	seeded(t, fixed)
	pagesSeeded(t, fixed)
	conformance.Run(t, suiteTarget("cassandra", liveConfig(fixture)))
}

// TestConformanceWithoutAKeyspace runs the same suite over a connection that
// named none, which is how a person connects to look around before choosing
// one. Every name the driver writes must then carry the keyspace it is in,
// and the tree must still reach the same tables.
func TestConformanceWithoutAKeyspace(t *testing.T) {
	fixed := live(t, liveConfig(fixture))
	seeded(t, fixed)
	pagesSeeded(t, fixed)
	conformance.Run(t, suiteTarget("cassandra-no-keyspace", liveConfig("")))
}

// suiteTarget is a connection the suite opens afresh for every check.
func suiteTarget(name string, cfg source.ConnectionConfig) conformance.Target {
	open := func(ctx context.Context, t *testing.T, g source.Guard) source.Source {
		c := cfg
		c.Guard = g
		s, err := Driver{}.Open(ctx, c)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	return conformance.Target{
		Name:      name,
		Open:      func(ctx context.Context, t *testing.T) source.Source { return open(ctx, t, source.Guard{}) },
		Browsable: model.NewRef(model.KindTable, fixture, "pages"),
		// Nothing is written: this driver claims no Writer, so every check
		// that changes a row has nothing to hold it to. Reading is the whole
		// of what it promises so far.
		OpenGuarded: open,
		// A condition in CQL names what the rows are partitioned by: a
		// cluster reads a partition, not a table, and this fixture's rows are
		// all in one. There is no condition true of every row without naming
		// it, which is the difference from SQL the suite is told about here.
		Conditions: &conformance.Conditions{
			True: "bucket = 1", False: "bucket = 2", Trailing: "bucket = 1 -- a note",
			Refused: []string{"bucket = 1; SELECT 1", "(bucket = 1", "bucket = 1)", "'open = 1", "bucket = 1 /* open"},
		},
	}
}

// TestConformanceOnAMaterializedView holds the claim that a view is browsable
// like any other object: a cluster keeps a view as a table of its own, with a
// key of its own, and this driver reads it through the same contract.
func TestConformanceOnAMaterializedView(t *testing.T) {
	fixed := live(t, liveConfig(fixture))
	if !seeded(t, fixed) {
		t.Skip("materialized views are disabled on this cluster")
	}
	peopleSeeded(t, fixed)
	target := suiteTarget("cassandra-view", liveConfig(fixture))
	target.Browsable = model.NewRef(model.KindMaterializedView, fixture, "people_by_score")
	target.Conditions = &conformance.Conditions{
		True: "country = 'FR'", False: "country = 'nowhere'", Trailing: "country = 'FR' -- a note",
		Refused: []string{"country = 'FR'; SELECT 1", "(country = 'FR'", "country = 'FR')",
			"'open = 1", "country = 'FR' /* open"},
	}
	conformance.Run(t, target)
}

// peopleSeeded gives the people table rows under a country of its own, so
// that a view of them has something to be read through. Other tests write to
// this table under other keys; these rows are nobody else's.
func peopleSeeded(t *testing.T, src source.Source) {
	t.Helper()
	ctx := context.Background()
	s := src.(*cassandraSource)
	for i := 1; i <= 5; i++ {
		if err := s.session.Query(fmt.Sprintf(
			`INSERT INTO %s.people (country, id, name, score) VALUES ('FR', %d, 'person %d', %d.5)`,
			fixture, i, i, i)).WithContext(ctx).Exec(); err != nil {
			t.Fatalf("seeding people: %v", err)
		}
	}
}
