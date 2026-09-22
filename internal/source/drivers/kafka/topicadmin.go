package kafka

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/twmb/franz-go/pkg/kadm"

	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Making, unmaking and reshaping topics (T2.78, FR-13.12).
//
// All of it is AccessDDL, and all of it passes the guard before a broker is
// asked anything: a read-only connection refuses outright, and a production
// one refuses without consent given for this act and no other (FR-4.9).
//
// Kafka answers these as batch requests with a result per topic, so a failure
// can arrive in either of two places: the request itself failed, or the
// request succeeded and the one topic in it did not. Both are returned. A
// call that looked only at the first would report success for a topic that
// was never made.

// CreateTopic makes a topic.
func (s *kafkaSource) CreateTopic(ctx context.Context, spec source.TopicSpec) (err error) {
	defer panics.Recover(&err, "creating a topic")

	if err := s.cfg.Guard.Allow(source.AccessDDL, spec.Confirmed); err != nil {
		return err
	}
	if spec.Name == "" {
		return errors.New("kafka: a topic needs a name")
	}
	if spec.Partitions < 1 {
		return fmt.Errorf("kafka: a topic is cut into at least one partition, and %d was asked for", spec.Partitions)
	}
	if spec.ReplicationFactor < 1 {
		return fmt.Errorf("kafka: a topic is kept in at least one copy, and %d was asked for", spec.ReplicationFactor)
	}

	resps, err := s.admin.CreateTopics(ctx, spec.Partitions, spec.ReplicationFactor, configOf(spec.Config), spec.Name)
	if err != nil {
		return err
	}
	for _, r := range resps.Sorted() {
		if r.Err != nil {
			return said(r.Err, r.ErrMessage)
		}
	}
	return nil
}

// DeleteTopic unmakes a topic, and everything written to it.
func (s *kafkaSource) DeleteTopic(ctx context.Context, topic string, confirmed bool) (err error) {
	defer panics.Recover(&err, "deleting a topic")

	if err := s.cfg.Guard.Allow(source.AccessDDL, confirmed); err != nil {
		return err
	}
	if topic == "" {
		return errors.New("kafka: there is no topic to delete without a name")
	}

	resps, err := s.admin.DeleteTopics(ctx, topic)
	if err != nil {
		return err
	}
	for _, r := range resps.Sorted() {
		if r.Err != nil {
			return said(r.Err, r.ErrMessage)
		}
	}
	return nil
}

// AddPartitions cuts a topic into more logs than it had.
//
// It cannot be undone, and it changes which partition a key lands in for
// every record written afterwards — so records already written stay where
// they are and new ones with the same key may not join them. Saying that is
// the caller's job, and it has to be said before this is reached.
func (s *kafkaSource) AddPartitions(ctx context.Context, topic string, count int32, confirmed bool) (err error) {
	defer panics.Recover(&err, "adding partitions to a topic")

	if err := s.cfg.Guard.Allow(source.AccessDDL, confirmed); err != nil {
		return err
	}
	if topic == "" {
		return errors.New("kafka: there is no topic to add partitions to without a name")
	}
	if count < 1 {
		return fmt.Errorf("kafka: adding partitions adds at least one, and %d was asked for", count)
	}

	resps, err := s.admin.CreatePartitions(ctx, int(count), topic)
	if err != nil {
		return err
	}
	for _, r := range resps.Sorted() {
		if r.Err != nil {
			return said(r.Err, r.ErrMessage)
		}
	}
	return nil
}

// AlterTopicConfig changes the settings it is given and leaves the rest of
// them alone.
//
// The request is the incremental one on purpose: the whole-state form would
// clear every setting somebody did not mention, which turns changing one
// thing into an undeclared reset of everything else.
func (s *kafkaSource) AlterTopicConfig(ctx context.Context, topic string, set map[string]string, confirmed bool) (err error) {
	defer panics.Recover(&err, "changing a topic's configuration")

	if err := s.cfg.Guard.Allow(source.AccessDDL, confirmed); err != nil {
		return err
	}
	if topic == "" {
		return errors.New("kafka: there is no topic to configure without a name")
	}
	if len(set) == 0 {
		return errors.New("kafka: nothing was given to change")
	}

	alters := make([]kadm.AlterConfig, 0, len(set))
	for _, name := range sortedNames(set) {
		value := set[name]
		alters = append(alters, kadm.AlterConfig{Op: kadm.SetConfig, Name: name, Value: &value})
	}
	resps, err := s.admin.AlterTopicConfigs(ctx, alters, topic)
	if err != nil {
		return err
	}
	for _, r := range resps {
		if r.Err != nil {
			return said(r.Err, r.ErrMessage)
		}
	}
	return nil
}

// configOf is a topic's configuration as kadm takes it.
func configOf(set map[string]string) map[string]*string {
	if len(set) == 0 {
		return nil
	}
	out := make(map[string]*string, len(set))
	for _, name := range sortedNames(set) {
		value := set[name]
		out[name] = &value
	}
	return out
}

// sortedNames is a map's keys in name order, so that the same change asked
// for twice is made the same way twice.
func sortedNames(set map[string]string) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// said is a broker's error with whatever it said about it.
//
// kerr names the condition and the message says which topic and why; dropping
// the message leaves somebody holding "policy violation" and no idea which
// policy.
func said(err error, message string) error {
	if message == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, message)
}
