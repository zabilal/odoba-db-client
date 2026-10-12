package pulsar

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/apache/pulsar-client-go/pulsaradmin/pkg/utils"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
)

// The explorer tree (FR-2.1, FR-13.1):
//
//	cluster        [cluster]
//	  namespace    [cluster, tenant/namespace]
//	    Topics     model.ClassRef
//	      topic    [cluster, tenant/namespace, persistent://tenant/namespace/name]
//
// A namespace is shown with its tenant, as Pulsar writes it, and in one level
// rather than two: a tenant holds nothing but namespaces, and a level with
// nothing in it but another level is a click nobody needed.
//
// A topic is named in full in what addresses it and by its own name in what is
// shown. A topic cut into partitions is shown once, as itself; its partitions
// are topics of their own underneath, and listing them beside it would show
// one thing several times over.

func (s *pulsarSource) Root(context.Context) (_ []model.Node, err error) {
	defer panics.Recover(&err, "reading the cluster")
	return []model.Node{{Ref: model.NewRef(model.KindCluster, s.cluster), Label: s.cluster, HasChildren: true}}, nil
}

func (s *pulsarSource) Children(ctx context.Context, ref model.ObjectRef) (_ []model.Node, err error) {
	defer panics.Recover(&err, "listing what is in a "+string(ref.Kind))

	switch ref.Kind {
	case model.KindCluster:
		return s.namespaces(ctx)
	case model.KindSchema:
		if len(ref.Path) != 2 {
			return nil, fmt.Errorf("pulsar: incomplete reference %s", ref)
		}
		topics, err := s.topics(ctx, ref.Path[1])
		if err != nil {
			return nil, err
		}
		// The class even where it holds nothing: a namespace says it has
		// children, and one that then lists none draws an expander that turns
		// and never opens (FR-2.2).
		return []model.Node{model.ClassNode(ref, model.KindTopic, int64(len(topics)))}, nil
	case model.KindFolder:
		kind, ok := model.ClassOf(ref)
		if !ok || kind != model.KindTopic || len(ref.Path) < 2 {
			return nil, nil
		}
		topics, err := s.topics(ctx, ref.Path[1])
		if err != nil {
			return nil, err
		}
		out := make([]model.Node, 0, len(topics))
		for _, t := range topics {
			n := model.Node{Ref: model.NewRef(model.KindTopic, ref.Path[0], ref.Path[1], t.name),
				Label: short(t.name), Browsable: true, Describable: true}
			if t.partitions > 0 {
				n.Badge = &model.Badge{Text: strconv.Itoa(t.partitions), Exact: true}
				n.Attrs = map[string]string{"partitions": strconv.Itoa(t.partitions)}
			}
			out = append(out, n)
		}
		return out, nil
	}
	return nil, nil
}

// namespaces are every tenant's, as tenant/namespace, in name order.
func (s *pulsarSource) namespaces(ctx context.Context) ([]model.Node, error) {
	tenants, err := s.admin.Tenants().ListWithContext(ctx)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, tenant := range tenants {
		spaces, err := s.admin.Namespaces().GetNamespacesWithContext(ctx, tenant)
		if err != nil {
			return nil, err
		}
		names = append(names, spaces...)
	}
	sort.Strings(names)
	out := make([]model.Node, 0, len(names))
	for _, name := range names {
		n := model.Node{Ref: model.NewRef(model.KindSchema, s.cluster, name), Label: name, HasChildren: true}
		if strings.HasPrefix(name, "pulsar/") {
			// Pulsar's own: where it keeps what it knows about itself.
			n.Attrs = map[string]string{"system": "true"}
		}
		out = append(out, n)
	}
	return out, nil
}

// listed is one topic of a namespace: its full name, and how many partitions
// it is cut into, or none where it is one log.
type listed struct {
	name       string
	partitions int
}

// partition matches the name of a partition of a topic, which is a topic of
// its own: the topic's name, and which piece.
var partition = regexp.MustCompile(`-partition-\d+$`)

// topics are a namespace's, in name order.
func (s *pulsarSource) topics(ctx context.Context, namespace string) ([]listed, error) {
	ns, err := utils.GetNamespaceName(namespace)
	if err != nil {
		return nil, fmt.Errorf("pulsar: %q is not a namespace: one is written tenant/namespace", namespace)
	}
	partitioned, single, err := s.admin.Topics().ListWithContext(ctx, *ns)
	if err != nil {
		return nil, err
	}
	var out []listed
	for _, name := range partitioned {
		t := listed{name: name}
		if tn, err := utils.GetTopicName(name); err == nil {
			if md, err := s.admin.Topics().GetMetadataWithContext(ctx, *tn); err == nil {
				t.partitions = md.Partitions
			}
		}
		out = append(out, t)
	}
	for _, name := range single {
		if !partition.MatchString(name) {
			out = append(out, listed{name: name})
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].name < out[b].name })
	return out, nil
}

// short is a topic's own name, without where it is: the last part of
// persistent://tenant/namespace/name.
func short(full string) string {
	if i := strings.LastIndexByte(full, '/'); i >= 0 {
		return full[i+1:]
	}
	return full
}

// ours begins the name of every reader this application makes, so that a
// reader is not shown among a topic's subscriptions: somebody looking at who is
// subscribed did not ask to see themselves looking.
const ours = "ikigai-reader"

// Describe is what one topic is: its partitions, how much it holds, and who is
// subscribed to it and how far behind each is.
func (s *pulsarSource) Describe(ctx context.Context, ref model.ObjectRef) (_ any, err error) {
	defer panics.Recover(&err, "reading the structure")
	if ref.Kind != model.KindTopic {
		return nil, fmt.Errorf("pulsar: a %s has no structure to read", ref.Kind)
	}
	tn, err := utils.GetTopicName(ref.Name())
	if err != nil {
		return nil, fmt.Errorf("pulsar: %q is not a topic: %w", ref.Name(), err)
	}
	md, err := s.admin.Topics().GetMetadataWithContext(ctx, *tn)
	if err != nil {
		return nil, err
	}
	t := &model.Topic{Name: ref.Name(), ReplicationFactor: -1, Attrs: map[string]string{}}
	var subs map[string]utils.SubscriptionStats
	if md.Partitions > 0 {
		stats, err := s.admin.Topics().GetPartitionedStatsWithContext(ctx, *tn, true)
		if err != nil {
			return nil, err
		}
		subs = stats.Subscriptions
		for i := 0; i < md.Partitions; i++ {
			part := stats.Partitions[ref.Name()+"-partition-"+strconv.Itoa(i)]
			t.Partitions = append(t.Partitions, model.Partition{ID: int32(i), Leader: -1, HighWatermark: part.MsgCounterIn})
		}
		t.Attrs["storage"] = strconv.FormatInt(stats.StorageSize, 10) + " bytes"
	} else {
		stats, err := s.admin.Topics().GetStatsWithContext(ctx, *tn)
		if err != nil {
			return nil, err
		}
		subs = stats.Subscriptions
		t.Partitions = []model.Partition{{ID: 0, Leader: -1, HighWatermark: stats.MsgCounterIn}}
		t.Attrs["storage"] = strconv.FormatInt(stats.StorageSize, 10) + " bytes"
	}
	if said := subscriptions(subs); said != "" {
		t.Attrs["subscriptions"] = said
	}
	return t, nil
}

// subscriptions are who is subscribed to a topic and how many messages each has
// still to acknowledge, in name order and without this application's own
// readers.
func subscriptions(subs map[string]utils.SubscriptionStats) string {
	names := make([]string, 0, len(subs))
	for name := range subs {
		if !strings.HasPrefix(name, ours) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = name + " (" + strconv.FormatInt(subs[name].MsgBacklog, 10) + " behind)"
	}
	return strings.Join(parts, ", ")
}

// Badge is nothing: how many messages a topic holds is not something a cluster
// keeps a figure for that means what a badge says.
func (s *pulsarSource) Badge(context.Context, model.ObjectRef) (model.Badge, bool, error) {
	return model.Badge{}, false, nil
}

// exists says whether a topic is there, and says so before anything is read
// from it or written to it.
//
// It has to be asked, because Pulsar does not refuse: a cluster as it is
// installed makes a topic the first time one is named, so reading a topic that
// is misspelt creates it, and writing to one puts a message somewhere nobody
// is listening. Looking at a cluster must not add to it.
func (s *pulsarSource) exists(ctx context.Context, topic string) error {
	tn, err := utils.GetTopicName(topic)
	if err != nil {
		return fmt.Errorf("pulsar: %q is not a topic: %w", topic, err)
	}
	// A topic in pieces is there if it has pieces. The count is asked for and
	// not just the absence of an error: the broker this was written against
	// refuses the question for a topic that is not there, and an older one
	// answers nought.
	if md, err := s.admin.Topics().GetMetadataWithContext(ctx, *tn); err == nil && md.Partitions > 0 {
		return nil
	}
	if _, err := s.admin.Topics().GetStatsWithContext(ctx, *tn); err != nil {
		if strings.Contains(err.Error(), "code: 404") {
			return fmt.Errorf("pulsar: this cluster has no topic %s", topic)
		}
		return err
	}
	return nil
}
