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
		// And it has an account of itself — its brokers, and which of them
		// answers for the whole — which Describe has returned since T2.59
		// and nothing could reach until this was said out loud (ADR-0106).
		Describable: true,
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
		kind, ok := model.ClassOf(ref)
		if !ok {
			return nil, nil
		}
		switch kind {
		case model.KindTopic:
			return s.topicNodes(ctx, ref)
		case model.KindConsumerGroup:
			return s.groupNodes(ctx, ref)
		case model.KindSubject:
			return s.subjectNodes(ctx, ref)
		}
		return nil, nil
	case model.KindTopic:
		return s.partitionNodes(ctx, ref)
	}
	// A partition holds records rather than objects, and reading those is
	// T2.63.
	return nil, nil
}

// classes are what a cluster holds, by class.
//
// Opening a cluster costs two requests: the metadata that names its topics,
// and a listing of its groups. Both are bounded by the size of the cluster
// rather than by what it holds, which is the line ADR-0092 drew — what it
// refused was a cost that grows with the topics.
func (s *kafkaSource) classes(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	topics, err := s.topics(ctx)
	if err != nil {
		return nil, err
	}
	groups, err := s.admin.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	out := model.ClassNodes(ref, map[model.ObjectKind]int64{
		model.KindTopic:         int64(len(topics)),
		model.KindConsumerGroup: int64(len(groups)),
	})
	if len(out) == 0 {
		// A cluster whose topics are all Kafka's own still shows the class,
		// empty: a node opening onto nothing reads as a tree that failed
		// rather than as a cluster nobody has written to yet.
		out = []model.Node{model.ClassNode(ref, model.KindTopic, 0)}
	}
	// Schemas live on a second server (ADR-0101), and nothing is asked of it
	// here. Counting subjects would put a third round trip — to a host that
	// can be down while the cluster is perfectly well — in front of every
	// cluster somebody opens, and would stop them browsing topics when it
	// failed. So the class is shown wherever a registry was named, unbadged
	// for the reason a topic carries no size (ADR-0092), and what the
	// registry has to say is said when somebody opens it.
	if s.registry != nil {
		out = append(out, model.Node{
			Ref:         model.ClassRef(ref, model.KindSubject),
			Label:       model.ClassLabel(model.KindSubject),
			HasChildren: true,
		})
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
			// Its partitions are under it, and there is always at least one.
			HasChildren: len(d.Partitions) > 0,
			// And its records can be read (T2.63).
			Browsable: true,
			Badge:     &model.Badge{Text: strconv.Itoa(len(d.Partitions)), Exact: true},
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
	case model.KindSubject:
		return s.subject(ctx, ref.Name())
	case model.KindConsumerGroup:
		return s.group(ctx, ref.Name())
	}
	return nil, nil
}

// group is one consumer group as the structure view shows it: what it is
// doing, who is in it, and what each of them was given to read (FR-13.10).
//
// What any of them has fallen behind by is not read here. That is a request
// for the group's committed offsets and another for the ends of the logs, and
// the model says as much where it holds them: offsets are populated on
// demand, never while a group is being listed (T2.76).
func (s *kafkaSource) group(ctx context.Context, name string) (*model.ConsumerGroup, error) {
	described, err := s.admin.DescribeGroups(ctx, name)
	if err != nil {
		return nil, err
	}
	d, ok := described[name]
	if !ok {
		return nil, fmt.Errorf("kafka: this cluster has no consumer group %q", name)
	}
	if d.Err != nil {
		return nil, d.Err
	}
	g := &model.ConsumerGroup{ID: d.Group, State: d.State}
	for _, m := range d.Members {
		g.Members = append(g.Members, model.GroupMember{
			ID: m.MemberID, ClientID: m.ClientID, Host: m.ClientHost,
			Assignment: assignmentOf(m),
		})
	}
	return g, nil
}

// assignmentOf is what one member was given to read.
//
// A group need not be consuming a topic at all: Kafka Connect uses the same
// machinery for its own purposes, and an assignment written for something
// else is not partitions of a log. Reading one as though it were would invent
// an assignment nobody made, so anything that is not a consumer's assignment
// reads as none at all.
func assignmentOf(m kadm.DescribedGroupMember) []model.TopicPartition {
	c, ok := m.Assigned.AsConsumer()
	if !ok {
		return nil
	}
	var out []model.TopicPartition
	for _, t := range c.Topics {
		for _, p := range t.Partitions {
			out = append(out, model.TopicPartition{Topic: t.Topic, Partition: p})
		}
	}
	return out
}

// partitionNodes are a topic's logs, one node each. A partition is a leaf:
// what is under it is records, which is a grid's business rather than a
// tree's (T2.63).
//
// Listing them costs the metadata and the offsets, and not the log
// directories: what a topic occupies is asked for when a topic is described,
// not when somebody opens it in the tree.
func (s *kafkaSource) partitionNodes(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	if len(ref.Path) < 2 {
		return nil, nil
	}
	name := ref.Name()
	parts, _, err := s.logs(ctx, name)
	if err != nil {
		return nil, err
	}
	out := make([]model.Node, 0, len(parts))
	for _, p := range parts {
		attrs := map[string]string{"leader": leaderAttr(p.Leader)}
		if p.LowWatermark >= 0 && p.HighWatermark >= 0 {
			attrs["offsets"] = fmt.Sprintf("%d to %d", p.LowWatermark, p.HighWatermark)
		}
		if len(p.ISR) < len(p.Replicas) {
			// Said only when it is true: a partition keeping up needs no
			// remark, and one that is not is what somebody is looking for.
			attrs["in sync"] = fmt.Sprintf("%d of %d", len(p.ISR), len(p.Replicas))
		}
		out = append(out, model.Node{
			Ref:   model.NewRef(model.KindPartition, ref.Path[0], name, strconv.FormatInt(int64(p.ID), 10)),
			Label: fmt.Sprintf("partition %d", p.ID),
			Attrs: attrs,
		})
	}
	return out, nil
}

// leaderAttr names the broker a partition is led from, or says there is none:
// a partition without a leader cannot be used until one is elected, and -1 is
// a fact about the protocol rather than about the log.
func leaderAttr(id int32) string {
	if id < 0 {
		return "none"
	}
	return strconv.FormatInt(int64(id), 10)
}

// groupNodes are the cluster's consumer groups, in name order. What a group
// is doing comes with the listing, so a node needs nothing described.
func (s *kafkaSource) groupNodes(ctx context.Context, class model.ObjectRef) ([]model.Node, error) {
	groups, err := s.admin.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	cluster := class.Path[0]
	out := make([]model.Node, 0, len(groups))
	for _, g := range groups.Sorted() {
		attrs := map[string]string{}
		if g.State != "" {
			attrs["state"] = g.State
		}
		if g.ProtocolType != "" {
			attrs["protocol"] = g.ProtocolType
		}
		out = append(out, model.Node{
			Ref:   model.NewRef(model.KindConsumerGroup, cluster, g.Group),
			Label: g.Group,
			Attrs: attrs,
			// Who is in it and what each was given to read: a description,
			// and no rows at all (ADR-0106).
			Describable: true,
		})
	}
	return out, nil
}

// subjectNodes are what the registry holds, in the order it lists them
// (FR-13.14).
//
// A subject is a leaf. Its versions are revisions of one thing rather than
// things of their own, so they are shown when the subject is described and
// not hung under it as nodes somebody would have to open one at a time.
//
// There is no badge for the same reason: how many versions a subject has is a
// request per subject, which is the cost ADR-0092 refused when it left topics
// unbadged by size.
func (s *kafkaSource) subjectNodes(ctx context.Context, class model.ObjectRef) ([]model.Node, error) {
	subjects, err := s.Subjects(ctx)
	if err != nil {
		return nil, err
	}
	cluster := class.Path[0]
	out := make([]model.Node, 0, len(subjects))
	for _, sub := range subjects {
		out = append(out, model.Node{
			Ref:   model.NewRef(model.KindSubject, cluster, sub.Name),
			Label: sub.Name,
			// Its versions and their schemas are what it is, and they are
			// shown by describing it rather than by opening rows it has not
			// got (ADR-0106).
			Describable: true,
		})
	}
	return out, nil
}

// ConsumerGroups lists the cluster's groups and what each is doing, and does
// not work out how far behind any of them is: a cluster can hold thousands,
// and lag is a pair of requests apiece (FR-13.10).
func (s *kafkaSource) ConsumerGroups(ctx context.Context) (_ []model.ConsumerGroup, err error) {
	defer panics.Recover(&err, "listing the consumer groups")

	groups, err := s.admin.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.ConsumerGroup, 0, len(groups))
	for _, g := range groups.Sorted() {
		out = append(out, model.ConsumerGroup{ID: g.Group, State: g.State})
	}
	return out, nil
}

// GroupOffsets is how far a group has got on every partition it reads, and
// how far behind that leaves it (FR-13.10).
//
// This is the request the rest of the group view will not make on its own. It
// costs the group's committed offsets and the ends of every log those offsets
// are in, so it happens when somebody asks for it, and never as a side effect
// of describing a group (T2.75).
func (s *kafkaSource) GroupOffsets(ctx context.Context, groupID string) (_ []model.GroupOffset, err error) {
	defer panics.Recover(&err, "reading a group's progress")

	lags, err := s.admin.Lag(ctx, groupID)
	if err != nil {
		return nil, err
	}
	l, ok := lags[groupID]
	if !ok {
		return nil, fmt.Errorf("kafka: this cluster has no consumer group %q", groupID)
	}
	// Describing the group and fetching its commits are two requests, and
	// either can fail on its own; the lag is worth nothing if either did.
	if err := l.Error(); err != nil {
		return nil, err
	}
	return progressOf(l.Lag), nil
}

// progressOf is a group's lag as the model holds it, in topic and partition
// order so that the same group reads the same way twice.
//
// Every -1 that comes through is kept. A partition nothing has committed to,
// an end nobody could read, and a lag that could not be worked out from
// either are all -1 here, and the model says so for each of them — because
// "has not committed" and "committed at the beginning" are different facts
// about a group, and smoothing them together would hide the one somebody is
// looking for.
func progressOf(lag kadm.GroupLag) []model.GroupOffset {
	var out []model.GroupOffset
	for _, ps := range lag {
		for _, l := range ps {
			out = append(out, model.GroupOffset{
				Topic: l.Topic, Partition: l.Partition,
				Current: l.Commit.At, End: l.End.Offset, Lag: l.Lag,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Topic != out[j].Topic {
			return out[i].Topic < out[j].Topic
		}
		return out[i].Partition < out[j].Partition
	})
	return out
}

// logs are a topic's partitions and the metadata they came from, without
// asking the brokers what any of it occupies.
func (s *kafkaSource) logs(ctx context.Context, name string) ([]model.Partition, kadm.TopicDetail, error) {
	md, err := s.admin.Metadata(ctx, name)
	if err != nil {
		return nil, kadm.TopicDetail{}, err
	}
	d, ok := md.Topics[name]
	if !ok {
		return nil, kadm.TopicDetail{}, fmt.Errorf("kafka: this cluster has no topic %q", name)
	}
	if d.Err != nil {
		return nil, kadm.TopicDetail{}, d.Err
	}
	start, startErr := s.admin.ListStartOffsets(ctx, name)
	end, endErr := s.admin.ListEndOffsets(ctx, name)
	if startErr != nil {
		start = nil
	}
	if endErr != nil {
		end = nil
	}
	return partitionsOf(name, d.Partitions, start, end), d, nil
}

// topic is what one topic is: how it is cut up, where the pieces are, and how
// much of it there is to read. Reading that costs three requests beyond the
// metadata, which is why it happens for a topic somebody asked about rather
// than for every topic in a list (ADR-0092).
func (s *kafkaSource) topic(ctx context.Context, name string) (*model.Topic, error) {
	parts, d, err := s.logs(ctx, name)
	if err != nil {
		return nil, err
	}
	t := &model.Topic{
		Name:              name,
		Internal:          d.IsInternal,
		Partitions:        parts,
		ReplicationFactor: replicationOf(d.Partitions),
		Attrs:             map[string]string{},
	}
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
