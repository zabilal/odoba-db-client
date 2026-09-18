package kafka

import (
	"context"
	"net"
	"sort"
	"strconv"

	"github.com/twmb/franz-go/pkg/kadm"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
)

// The tree of a stream source (T2.59).
//
// A connection is to one cluster, and a cluster has no databases to choose
// between, so the root is the cluster itself: one node, with everything else
// hanging beneath it when it is written. Until topics and consumer groups
// exist (T2.62) it says it has no children, because a node that claims
// children and then opens onto nothing is an expander that turns for ever
// (FR-2.2).

// Root is the cluster this connection is to.
func (s *kafkaSource) Root(ctx context.Context) (_ []model.Node, err error) {
	defer panics.Recover(&err, "reading the cluster")

	c, err := s.cluster(ctx)
	if err != nil {
		return nil, err
	}
	name := clusterName(c)
	return []model.Node{{
		Ref:   model.NewRef(model.KindCluster, name),
		Label: name,
	}}, nil
}

// Children is nothing yet: topics and consumer groups are T2.62.
func (s *kafkaSource) Children(context.Context, model.ObjectRef) ([]model.Node, error) {
	return nil, nil
}

// Describe says what a cluster is: who its brokers are, which of them answers
// for the whole, and what it calls itself (FR-13.1).
func (s *kafkaSource) Describe(ctx context.Context, ref model.ObjectRef) (_ any, err error) {
	defer panics.Recover(&err, "describing the cluster")

	if ref.Kind != model.KindCluster {
		return nil, nil
	}
	return s.cluster(ctx)
}

// Badge counts nothing. A broker count belongs in what the cluster says about
// itself rather than on the node, and topics are not listed yet.
func (s *kafkaSource) Badge(context.Context, model.ObjectRef) (model.Badge, bool, error) {
	return model.Badge{}, false, nil
}

// cluster is the cluster's own account of itself.
func (s *kafkaSource) cluster(ctx context.Context) (*model.Cluster, error) {
	md, err := s.admin.BrokerMetadata(ctx)
	if err != nil {
		return nil, err
	}
	c := &model.Cluster{
		ID:         md.Cluster,
		Controller: md.Controller,
		Brokers:    brokersOf(md.Brokers),
		Attrs:      map[string]string{},
	}
	if v := s.version(ctx); v != "" {
		// Read back from the API versions the broker offers, because it does
		// not say its release outright (ADR-0086).
		c.Attrs["version"] = v
	}
	return c, nil
}

// brokersOf are the cluster's nodes, by node id.
//
// The seeds this connection started from are not among them: a seed is an
// address somebody typed rather than a node the cluster told us about, and
// franz-go marks one by numbering it very negatively and giving it no rack.
func brokersOf(details kadm.BrokerDetails) []model.Broker {
	out := make([]model.Broker, 0, len(details))
	for _, b := range details {
		if b.NodeID < 0 {
			continue
		}
		broker := model.Broker{ID: b.NodeID, Host: b.Host, Port: b.Port}
		if b.Rack != nil {
			broker.Rack = *b.Rack
		}
		out = append(out, broker)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// clusterName is what the tree calls the cluster: the id it gives itself, or
// else the first broker it named, because a node whose label is empty is a
// row nobody can read.
func clusterName(c *model.Cluster) string {
	if c.ID != "" {
		return c.ID
	}
	if len(c.Brokers) > 0 {
		return net.JoinHostPort(c.Brokers[0].Host, strconv.FormatInt(int64(c.Brokers[0].Port), 10))
	}
	return "cluster"
}
