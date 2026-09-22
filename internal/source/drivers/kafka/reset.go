package kafka

import (
	"context"
	"errors"
	"fmt"

	"github.com/twmb/franz-go/pkg/kadm"

	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Moving a consumer group's offsets (T2.80, FR-13.13).
//
// This decides what a running consumer reads next, and it is the most
// destructive thing here even though it deletes nothing: a group moved to the
// beginning of a log processes everything in it again, and one moved to the
// end never sees what it skipped. It is AccessAdmin and guarded without
// exception (FR-13.21).

// ResetOffsets moves where a group will read from.
func (s *kafkaSource) ResetOffsets(ctx context.Context, req source.ResetRequest) (err error) {
	defer panics.Recover(&err, "moving a group's offsets")

	if err := s.cfg.Guard.Allow(source.AccessAdmin, req.Confirmed); err != nil {
		return err
	}
	if req.GroupID == "" {
		return errors.New("kafka: there is no group to move without a name")
	}
	if err := s.idle(ctx, req.GroupID); err != nil {
		return err
	}

	wanted, err := s.resetting(ctx, req)
	if err != nil {
		return err
	}
	if len(wanted) == 0 {
		return fmt.Errorf("kafka: %s has committed nothing, so there is nowhere to move it from", req.GroupID)
	}

	offsets := kadm.Offsets{}
	for topic, partitions := range wanted {
		at, err := s.positions(ctx, topic, partitions, req.Seek)
		if err != nil {
			return err
		}
		for partition, o := range at {
			// -1 for the leader epoch: this offset was worked out from a
			// listing rather than read off a record, so there is no epoch to
			// carry and claiming one would be inventing it.
			offsets.AddOffset(topic, partition, o, -1)
		}
	}
	if len(offsets) == 0 {
		return errors.New("kafka: that position is in none of the group's partitions, so nothing was moved")
	}

	resps, err := s.admin.CommitOffsets(ctx, req.GroupID, offsets)
	if err != nil {
		return err
	}
	return resps.Error()
}

// idle refuses to move a group that something is reading through.
//
// Kafka will not take a commit from outside a live group, and what it says
// when it refuses is about membership rather than about what somebody was
// trying to do. Saying it here is the difference between a person stopping
// their consumers and a person guessing.
func (s *kafkaSource) idle(ctx context.Context, group string) error {
	described, err := s.admin.DescribeGroups(ctx, group)
	if err != nil {
		return err
	}
	d, ok := described[group]
	if !ok {
		return fmt.Errorf("kafka: this cluster has no consumer group %q", group)
	}
	if d.Err != nil {
		return d.Err
	}
	switch n := len(d.Members); {
	case n == 1:
		return fmt.Errorf("kafka: %s has one consumer reading through it, and a group's offsets "+
			"cannot be moved while anything is in it — stop it first", group)
	case n > 1:
		return fmt.Errorf("kafka: %s has %d consumers reading through it, and a group's offsets "+
			"cannot be moved while anything is in it — stop them first", group, n)
	}
	return nil
}

// resetting is which partitions to move: the ones named, or every one the
// group has committed to.
//
// A group is moved where it has been rather than everywhere it could go.
// Committing an offset for a partition it never read would add to what the
// group is doing rather than change it.
func (s *kafkaSource) resetting(ctx context.Context, req source.ResetRequest) (map[string][]int32, error) {
	out := map[string][]int32{}
	if len(req.Partitions) > 0 {
		for _, tp := range req.Partitions {
			out[tp.Topic] = append(out[tp.Topic], tp.Partition)
		}
		return out, nil
	}
	fetched, err := s.admin.FetchOffsets(ctx, req.GroupID)
	if err != nil {
		return nil, err
	}
	if err := fetched.Error(); err != nil {
		return nil, err
	}
	fetched.Each(func(o kadm.OffsetResponse) {
		out[o.Topic] = append(out[o.Topic], o.Partition)
	})
	return out, nil
}

// positions is where the seek lands in each of a topic's partitions.
//
// It asks the cluster the same questions reading a log asks, and works the
// answer out with the same code (positionOf, in browse.go), so that moving a
// group to a time puts it exactly where a read from that time would have
// begun. That is what the model asks for where it holds a reset's seek.
func (s *kafkaSource) positions(ctx context.Context, topic string, only []int32, seek source.Seek) (map[int32]int64, error) {
	starts, err := s.admin.ListStartOffsets(ctx, topic)
	if err != nil {
		return nil, err
	}
	ends, err := s.admin.ListEndOffsets(ctx, topic)
	if err != nil {
		return nil, err
	}
	begins, err := offsets(starts[topic])
	if err != nil {
		return nil, err
	}
	finish, err := offsets(ends[topic])
	if err != nil {
		return nil, err
	}

	timed := map[int32]int64{}
	if seek.Mode == source.SeekTimestamp {
		listed, err := s.admin.ListOffsetsAfterMilli(ctx, seek.Time.UnixMilli(), topic)
		if err != nil {
			return nil, err
		}
		for partition, o := range listed[topic] {
			if o.Err == nil {
				timed[partition] = o.Offset
			}
		}
	}

	out := map[int32]int64{}
	for _, p := range only {
		start, ok := begins[p]
		if !ok {
			continue
		}
		end, ok := finish[p]
		if !ok {
			continue
		}
		at, haveTime := timed[p]
		from, found, err := positionOf(start, end, at, haveTime, &seek, false)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		// Past the end is the end. A group committed beyond its own log would
		// read nothing until the log caught up, which is what none of these
		// positions means.
		if from > end {
			from = end
		}
		out[p] = from
	}
	return out, nil
}
