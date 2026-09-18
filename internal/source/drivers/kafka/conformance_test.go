//go:build conformance

package kafka

import (
	"context"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/conformance"
)

// The shared driver suite (REQ-DRV-1), against a real broker.
//
// This is the first stream source the suite has met. What it holds this driver
// to is what every driver is held to — a tree whose nodes are addressable and
// declared, a browse that returns columns and rows and closes twice without
// complaint, a read that stops when it is given up on — and what this driver
// does not claim, it is not asked for: no writing, no counting, no distinct
// values, no query language.

func TestConformance(t *testing.T) {
	src := live(t, liveConfig())
	seededTopic(t, src, "ikigai_it_conformance", 2)
	written(t, src, "ikigai_it_conformance", 20)

	conformance.Run(t, conformance.Target{
		Name: "kafka",
		Open: func(ctx context.Context, t *testing.T) source.Source {
			s, err := Driver{}.Open(ctx, liveConfig())
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			t.Cleanup(func() { s.Close() })
			return s
		},
		// A topic's records are what there is to read. The cluster name in
		// the ref is not looked at: a connection is to one cluster, and the
		// topic is what is addressed.
		Browsable: model.NewRef(model.KindTopic, "cluster", "ikigai_it_conformance"),
		// Nothing is written: this driver claims no Writer, so the checks
		// that change a record, guard one, or edit a result skip rather than
		// pretend. Producing is T2.77.
	})
}
