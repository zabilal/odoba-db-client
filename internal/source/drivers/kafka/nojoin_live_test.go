//go:build conformance

package kafka

import (
	"context"
	"slices"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Reading never joins a consumer group and never commits an offset (T2.81,
// FR-13.19).
//
// The point of the requirement is not tidiness. Somebody inspecting a topic
// on a live cluster must not move production consumers, and the only way to
// be sure is that a read is never a member of anything.

// groupNames is what the cluster calls its consumer groups.
func groupNames(t *testing.T, s *kafkaSource) []string {
	t.Helper()
	groups, err := s.admin.ListGroups(context.Background())
	if err != nil {
		t.Fatalf("listing the groups: %v", err)
	}
	var out []string
	for _, g := range groups.Sorted() {
		out = append(out, g.Group)
	}
	return out
}

func TestLiveReadingJoinsNoGroupAndDisturbsNone(t *testing.T) {
	src := live(t, liveConfig())
	a := administers(t, src)
	s := src.(*kafkaSource)
	ctx := context.Background()
	const topic = "ikigai_it_nojoin"
	const group = "ikigai_it_nojoin_group"

	_ = a.DeleteTopic(ctx, topic, false)
	if err := a.CreateTopic(ctx, source.TopicSpec{
		Name: topic, Partitions: 2, ReplicationFactor: 1}); err != nil {
		t.Fatalf("creating %s: %v", topic, err)
	}
	t.Cleanup(func() { _ = a.DeleteTopic(context.Background(), topic, false) })
	settled(t, src, topic, 2)
	written(t, src, topic, 6)

	// A group that has read this topic and left, sitting where it got to.
	consumed(t, src, group, topic, 6)
	was := committed(t, src, group, topic, 0)
	before := groupNames(t, s)

	// Now somebody looks at the topic, the way the grid does.
	if rows := from(t, src, topic, nil, 100); len(rows) == 0 {
		t.Fatal("a topic that was written to read as empty")
	}
	// And again from a named position, which is the other way in.
	from(t, src, topic, &source.Seek{Mode: source.SeekBeginning}, 100)

	// No group came into being because somebody looked.
	for _, name := range groupNames(t, s) {
		if !slices.Contains(before, name) {
			t.Errorf("reading the topic brought the group %q into being", name)
		}
	}

	// And the group that was already there is exactly where it was. This is
	// the whole of what the requirement protects: inspecting a topic must
	// not move anybody's consumers.
	if now := committed(t, src, group, topic, 0); now != was {
		t.Errorf("reading the topic moved %s from %d to %d", group, was, now)
	}
}
