package kafka

import (
	"context"
	"fmt"
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
		// It holds its topics, and always says so: the class is shown even
		// when empty, so this never opens onto nothing.
		HasChildren: true,
	}}, nil
}

// Children lists what a node holds: a cluster holds classes, and the topics
// class holds the topics. A topic's partitions are T2.62 and its records
// T2.63, so a topic holds nothing the tree can open yet.
func (s *kafkaSource) Children(ctx context.Context, ref model.ObjectRef) (_ []model.Node, err error) {
	defer panics.Recover(&err, "listing what is in a "+string(ref.Kind))

	switch ref.Kind {
	case model.KindCluster:
		return s.classes(ctx, ref)
	case model.KindFolder:
		if kind, ok := model.ClassOf(ref); !ok || kind != model.KindTopic {
			return nil, nil
		}
		return s.topicNodes(ctx, ref)
	}
	return nil, nil
}

// classes are what a cluster holds, by class.
func (s *kafkaSource) classes(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	topics, err := s.topics(ctx)
	if err != nil {
		return nil, err
	}
	out := model.ClassNodes(ref, map[model.ObjectKind]int64{model.KindTopic: int64(len(topics))})
	if len(out) == 0 {
		// A cluster whose topics are all Kafka's own still shows the class,
		// empty: a node opening onto nothing reads as a tree that failed
		// rather than as a cluster nobody has written to yet.
		out = []model.Node{model.ClassNode(ref, model.KindTopic, 0)}
	}
	return out, nil
}

// topics are the cluster's, in name order, without Kafka's own.
func (s *kafkaSource) topics(ctx context.Context) ([]kadm.TopicDetail, error) {
	md, err := s.admin.Metadata(ctx)
	if err != nil {
		return nil, err
	}
	return ordinary(md.Topics), nil
}

// ordinary are the topics somebody put there, in name order.
//
// __consumer_offsets and its like are the cluster's workings rather than
// anybody's data, and are hidden as PostgreSQL's system schemas and
// Cassandra's system keyspaces are (FR-13.2). A topic that is internal still
// says so when it is described: what is hidden is where it appears, not what
// it is.
func ordinary(topics kadm.TopicDetails) []kadm.TopicDetail {
	out := make([]kadm.TopicDetail, 0, len(topics))
	for _, d := range topics.Sorted() {
		if d.IsInternal {
			continue
		}
		out = append(out, d)
	}
	return out
}

// topicNodes are the topics of a cluster, badged with how many logs each is
// cut into — which the metadata that named them has already said, so the
// badge costs nothing to draw (ADR-0092).
func (s *kafkaSource) topicNodes(ctx context.Context, class model.ObjectRef) ([]model.Node, error) {
	topics, err := s.topics(ctx)
	if err != nil {
		return nil, err
	}
	cluster := class.Path[0]
	out := make([]model.Node, 0, len(topics))
	for _, d := range topics {
		out = append(out, model.Node{
			Ref:   model.NewRef(model.KindTopic, cluster, d.Topic),
			Label: d.Topic,
			Badge: &model.Badge{Text: strconv.Itoa(len(d.Partitions)), Exact: true},
		})
	}
	return out, nil
}

// Describe says what a cluster is: who its brokers are, which of them answers
// for the whole, and what it calls itself (FR-13.1).
func (s *kafkaSource) Describe(ctx context.Context, ref model.ObjectRef) (_ any, err error) {
	defer panics.Recover(&err, "describing the cluster")

	switch ref.Kind {
	case model.KindCluster:
		return s.cluster(ctx)
	case model.KindTopic:
		return s.topic(ctx, ref.Name())
	}
	return nil, nil
}

// topic is what one topic is: how it is cut up, where the pieces are, and how
// much of it there is to read. Reading that costs three requests beyond the
// metadata, which is why it happens for a topic somebody asked about rather
// than for every topic in a list (ADR-0092).
func (s *kafkaSource) topic(ctx context.Context, name string) (*model.Topic, error) {
	md, err := s.admin.Metadata(ctx, name)
	if err != nil {
		return nil, err
	}
	d, ok := md.Topics[name]
	if !ok {
		return nil, fmt.Errorf("kafka: this cluster has no topic %q", name)
	}
	if d.Err != nil {
		return nil, d.Err
	}

	t := &model.Topic{
		Name:              name,
		Internal:          d.IsInternal,
		ReplicationFactor: replicationOf(d.Partitions),
		Attrs:             map[string]string{},
	}
	start, startErr := s.admin.ListStartOffsets(ctx, name)
	end, endErr := s.admin.ListEndOffsets(ctx, name)
	if startErr != nil {
		start = nil
	}
	if endErr != nil {
		end = nil
	}
	t.Partitions = partitionsOf(name, d.Partitions, start, end)
	if size, ok := s.topicSize(ctx, name, d.Partitions.Numbers()); ok {
		t.Attrs["size on disk"] = sizeText(size)
	}
	return t, nil
}

// partitionsOf are a topic's logs: where each is led from, who keeps a copy,
// and where it begins and ends.
//
// Offsets nobody could read leave the watermarks unknown rather than reading
// as a log with nothing in it. Not knowing and being empty are different
// things, and only one of them is a fact about the data.
func partitionsOf(topic string, ps kadm.PartitionDetails, start, end kadm.ListedOffsets) []model.Partition {
	out := make([]model.Partition, 0, len(ps))
	for _, p := range ps.Sorted() {
		part := model.Partition{ID: p.Partition, Leader: p.Leader, Replicas: p.Replicas, ISR: p.ISR,
			LowWatermark: -1, HighWatermark: -1}
		if o, ok := start.Lookup(topic, p.Partition); ok && o.Err == nil {
			part.LowWatermark = o.Offset
		}
		if o, ok := end.Lookup(topic, p.Partition); ok && o.Err == nil {
			part.HighWatermark = o.Offset
		}
		out = append(out, part)
	}
	return out
}

// replicationOf is how many copies there are of each partition, or -1 where
// the partitions do not agree — which is what the model asks for, rather than
// an average true of none of them.
func replicationOf(ps kadm.PartitionDetails) int32 {
	sorted := ps.Sorted()
	if len(sorted) == 0 {
		return -1
	}
	rf := int32(len(sorted[0].Replicas))
	for _, p := range sorted[1:] {
		if int32(len(p.Replicas)) != rf {
			return -1
		}
	}
	return rf
}

// topicSize is what a topic occupies on the brokers: every replica of every
// partition, summed. The request names the partitions, because a topic asked
// for with none is a topic asked about with nothing to answer.
func (s *kafkaSource) topicSize(ctx context.Context, name string, partitions []int32) (int64, bool) {
	if len(partitions) == 0 {
		return 0, false
	}
	set := kadm.TopicsSet{name: map[int32]struct{}{}}
	for _, p := range partitions {
		set[name][p] = struct{}{}
	}
	dirs, err := s.admin.DescribeAllLogDirs(ctx, set)
	if err != nil {
		return 0, false
	}
	var total int64
	var found bool
	dirs.Each(func(d kadm.DescribedLogDir) {
		if d.Err != nil {
			return
		}
		d.Topics.Each(func(p kadm.DescribedLogDirPartition) {
			if p.Topic == name && p.Size >= 0 {
				total += p.Size
				found = true
			}
		})
	})
	return total, found
}

// sizeText says what the number is as well as what it says. A topic kept
// three times occupies three times its own size, and calling that "size"
// without saying so would be read as the size of the data.
func sizeText(bytes int64) string {
	return fmt.Sprintf("%s across all replicas", humanBytes(bytes))
}

// humanBytes is a size somebody can read at a glance.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
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
