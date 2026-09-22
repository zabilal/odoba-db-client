//go:build conformance

package kafka

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Moving a consumer group's offsets (T2.80, FR-13.13).

// emptied waits for a group to have nobody in it, which is what lets its
// offsets be moved at all: leaving a group is a rebalance rather than an
// instant, and waiting for the condition beats pausing and hoping.
func emptied(t *testing.T, src source.Source, group string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		described, err := src.(*kafkaSource).admin.DescribeGroups(context.Background(), group)
		if err == nil {
			if d, ok := described[group]; ok && d.Err == nil && len(d.Members) == 0 {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s still has something reading through it", group)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// committed is where a group has got to on one partition.
func committed(t *testing.T, src source.Source, group, topic string, partition int32) int64 {
	t.Helper()
	fetched, err := src.(*kafkaSource).admin.FetchOffsets(context.Background(), group)
	if err != nil {
		t.Fatalf("reading %s's offsets: %v", group, err)
	}
	o, ok := fetched.Lookup(topic, partition)
	if !ok {
		t.Fatalf("%s has committed nothing for %s/%d", group, topic, partition)
	}
	if o.Err != nil {
		t.Fatalf("%s/%d: %v", topic, partition, o.Err)
	}
	return o.At
}

// consumed makes a group that has read a log and left it, which is the only
// state in which its offsets can be moved.
func consumed(t *testing.T, src source.Source, group, topic string, records int) {
	t.Helper()
	cl := reading(t, group, topic)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cl.PollRecords(ctx, records)
	if err := cl.CommitUncommittedOffsets(context.Background()); err != nil {
		t.Fatalf("committing for %s: %v", group, err)
	}
	cl.Close()
	emptied(t, src, group)
}

func TestLiveAGroupsOffsetsAreMovedWhereItIsTold(t *testing.T) {
	src := producing(t, source.Guard{})
	a := administers(t, src)
	r, ok := src.(source.OffsetResetter)
	if !ok {
		t.Fatal("claims Stream.ResetOffsets but does not implement OffsetResetter")
	}
	ctx := context.Background()
	const topic = "ikigai_it_reset"
	const group = "ikigai_it_reset_group"

	_ = a.DeleteTopic(ctx, topic, false)
	if err := a.CreateTopic(ctx, source.TopicSpec{Name: topic, Partitions: 1, ReplicationFactor: 1}); err != nil {
		t.Fatalf("creating %s: %v", topic, err)
	}
	t.Cleanup(func() { _ = a.DeleteTopic(context.Background(), topic, false) })
	settled(t, src, topic, 1)
	written(t, src, topic, 5)
	consumed(t, src, group, topic, 5)

	// To the beginning: everything in the log is read again.
	if err := r.ResetOffsets(ctx, source.ResetRequest{GroupID: group,
		Seek: source.Seek{Mode: source.SeekBeginning}}); err != nil {
		t.Fatalf("moving %s to the beginning: %v", group, err)
	}
	if at := committed(t, src, group, topic, 0); at != 0 {
		t.Errorf("moved to the beginning, %s is at %d", group, at)
	}

	// And to the end, which is the commonest move of all — and the one an
	// implementation borrowing the browser's rules wholesale would have
	// dropped, since reading asks whether there is anything there and this
	// does not.
	if err := r.ResetOffsets(ctx, source.ResetRequest{GroupID: group,
		Seek: source.Seek{Mode: source.SeekEnd}}); err != nil {
		t.Fatalf("moving %s to the end: %v", group, err)
	}
	if at := committed(t, src, group, topic, 0); at != 5 {
		t.Errorf("moved to the end of a log of five, %s is at %d", group, at)
	}

	// A named offset lands exactly there.
	if err := r.ResetOffsets(ctx, source.ResetRequest{GroupID: group,
		Seek: source.Seek{Mode: source.SeekOffset, Offset: 2}}); err != nil {
		t.Fatalf("moving %s to offset 2: %v", group, err)
	}
	if at := committed(t, src, group, topic, 0); at != 2 {
		t.Errorf("moved to offset 2, %s is at %d", group, at)
	}

	// And one past the end lands at the end rather than beyond it: a group
	// committed past its own log would read nothing until the log caught up.
	if err := r.ResetOffsets(ctx, source.ResetRequest{GroupID: group,
		Seek: source.Seek{Mode: source.SeekOffset, Offset: 9999}}); err != nil {
		t.Fatalf("moving %s past the end: %v", group, err)
	}
	if at := committed(t, src, group, topic, 0); at != 5 {
		t.Errorf("moved past the end of a log of five, %s is at %d", group, at)
	}
}

func TestLiveAGroupWithSomethingReadingThroughItIsNotMoved(t *testing.T) {
	src := producing(t, source.Guard{})
	a := administers(t, src)
	r := src.(source.OffsetResetter)
	ctx := context.Background()
	const topic = "ikigai_it_reset_live"
	const group = "ikigai_it_reset_live_group"

	_ = a.DeleteTopic(ctx, topic, false)
	if err := a.CreateTopic(ctx, source.TopicSpec{Name: topic, Partitions: 1, ReplicationFactor: 1}); err != nil {
		t.Fatalf("creating %s: %v", topic, err)
	}
	t.Cleanup(func() { _ = a.DeleteTopic(context.Background(), topic, false) })
	settled(t, src, topic, 1)
	written(t, src, topic, 2)

	// Still reading, and still in the group.
	cl := reading(t, group, topic)
	defer cl.Close()

	err := r.ResetOffsets(ctx, source.ResetRequest{GroupID: group,
		Seek: source.Seek{Mode: source.SeekBeginning}})
	if err == nil {
		t.Fatal("a group with something reading through it was moved")
	}
	// Kafka refuses this in words about group membership. What is said here
	// has to be about what somebody was trying to do, and what to do next.
	if !strings.Contains(err.Error(), "reading through it") {
		t.Errorf("a live group is refused with %q", err)
	}
}

func TestLiveAGroupThatIsNotThereIsNotMoved(t *testing.T) {
	src := producing(t, source.Guard{})
	r := src.(source.OffsetResetter)
	err := r.ResetOffsets(context.Background(), source.ResetRequest{
		GroupID: "ikigai_it_no_such_group_at_all",
		Seek:    source.Seek{Mode: source.SeekBeginning}})
	if err == nil {
		t.Fatal("a group that is not there was moved")
	}
}
