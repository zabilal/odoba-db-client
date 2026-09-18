//go:build conformance

package kafka

import (
	"context"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// The cluster overview against a real broker (T2.59, FR-13.1).

func TestLiveTheTreeIsTheCluster(t *testing.T) {
	src := live(t, liveConfig())
	ctx := context.Background()
	nodes, err := src.Root(ctx)
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("the root holds %d nodes", len(nodes))
	}
	cluster := nodes[0]
	if cluster.Ref.Kind != model.KindCluster || cluster.Label == "" {
		t.Errorf("the cluster node is %+v", cluster)
	}
	// It holds its topics and says so, and opening it yields the class —
	// never nothing, which would be an expander that turns for ever.
	if !cluster.HasChildren {
		t.Error("the cluster claims no children though it holds its topics")
	}
	kids, err := src.Children(ctx, cluster.Ref)
	if err != nil || len(kids) == 0 {
		t.Fatalf("the cluster holds %v: %v", kids, err)
	}
	for _, k := range kids {
		if kind, ok := model.ClassOf(k.Ref); !ok || kind != model.KindTopic {
			t.Errorf("the cluster holds %+v, which is no class of topics", k)
		}
	}
}

func TestLiveDescribesTheClusterAndItsBrokers(t *testing.T) {
	src := live(t, liveConfig())
	ctx := context.Background()
	nodes, err := src.Root(ctx)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("root: %v, %v", nodes, err)
	}
	desc, err := src.Describe(ctx, nodes[0].Ref)
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	c, ok := desc.(*model.Cluster)
	if !ok {
		t.Fatalf("a cluster is described as %T", desc)
	}
	// The id is what tells one cluster from another, and the tree uses it.
	if c.ID == "" {
		t.Error("the cluster gives itself no id")
	}
	if c.ID != "" && nodes[0].Label != c.ID {
		t.Errorf("the tree calls it %q and it calls itself %q", nodes[0].Label, c.ID)
	}
	// One node in this rig, and it answers for the whole.
	if len(c.Brokers) != 1 {
		t.Fatalf("the cluster has %d brokers", len(c.Brokers))
	}
	b := c.Brokers[0]
	if b.Host == "" || b.Port == 0 {
		t.Errorf("a broker with no address: %+v", b)
	}
	if c.Controller != b.ID {
		t.Errorf("broker %d answers for the cluster, and the brokers are %+v", c.Controller, c.Brokers)
	}
	// What it speaks, read back from the API versions it offers.
	if v := c.Attrs["version"]; !strings.Contains(v, "v") {
		t.Errorf("the cluster says its version is %q", v)
	}
}
