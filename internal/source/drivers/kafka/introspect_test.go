package kafka

import (
	"testing"

	"github.com/twmb/franz-go/pkg/kadm"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// The cluster in the tree (T2.59).

func TestTheBrokersAreTheClustersOwnNodes(t *testing.T) {
	rack := "rack-a"
	seedRack := "ignored"
	got := brokersOf(kadm.BrokerDetails{
		{NodeID: 2, Host: "second", Port: 9092},
		{NodeID: -2147483647, Host: "seed", Port: 9092, Rack: &seedRack},
		{NodeID: 1, Host: "first", Port: 9093, Rack: &rack},
	})
	// In node order, whatever order they arrived in.
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 2 {
		t.Fatalf("the brokers read as %+v", got)
	}
	// A seed is an address somebody typed, not a node the cluster named.
	for _, b := range got {
		if b.Host == "seed" {
			t.Error("a seed broker was taken for a node of the cluster")
		}
	}
	if got[0].Host != "first" || got[0].Port != 9093 || got[0].Rack != rack {
		t.Errorf("the first broker reads as %+v", got[0])
	}
	// A broker in no rack says so by saying nothing, rather than by carrying
	// a pointer nobody may follow.
	if got[1].Rack != "" {
		t.Errorf("a broker with no rack reads as %q", got[1].Rack)
	}
	if brokersOf(nil) == nil {
		t.Error("a cluster of no brokers reads as no list at all")
	}
}

func TestWhatTheTreeCallsTheCluster(t *testing.T) {
	// The id it gives itself, which is what tells one cluster from another.
	if got := clusterName(&model.Cluster{ID: "abc123",
		Brokers: []model.Broker{{ID: 1, Host: "h", Port: 9092}}}); got != "abc123" {
		t.Errorf("a cluster with an id is called %q", got)
	}
	// Failing that, the broker it named: a label nobody can read is worse
	// than an address.
	if got := clusterName(&model.Cluster{
		Brokers: []model.Broker{{ID: 1, Host: "broker", Port: 9092}}}); got != "broker:9092" {
		t.Errorf("a cluster with no id is called %q", got)
	}
	if got := clusterName(&model.Cluster{}); got == "" {
		t.Error("a cluster with nothing at all is called nothing at all")
	}
}
