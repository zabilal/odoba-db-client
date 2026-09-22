package app

import (
	"context"
	"errors"

	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Making, unmaking and reshaping topics (FR-13.12).
//
// Each of these is AccessDDL and each is refused by the driver before it
// dials where the connection is read-only or wants asking (FR-13.21). None of
// these relaxes that; they are the calls, not the guard.

// CreateTopic makes a topic.
func CreateTopic(ctx context.Context, src source.Source, spec source.TopicSpec) (err error) {
	defer panics.Recover(&err, "creating a topic")
	a, ok := src.(source.TopicAdmin)
	if !ok {
		return errNoTopicAdmin
	}
	return a.CreateTopic(ctx, spec)
}

// DeleteTopic unmakes one, and everything written to it.
func DeleteTopic(ctx context.Context, src source.Source, topic string, confirmed bool) (err error) {
	defer panics.Recover(&err, "deleting a topic")
	a, ok := src.(source.TopicAdmin)
	if !ok {
		return errNoTopicAdmin
	}
	return a.DeleteTopic(ctx, topic, confirmed)
}

// AddPartitions cuts a topic into more logs than it had.
func AddPartitions(ctx context.Context, src source.Source, topic string, count int32, confirmed bool) (err error) {
	defer panics.Recover(&err, "adding partitions to a topic")
	a, ok := src.(source.TopicAdmin)
	if !ok {
		return errNoTopicAdmin
	}
	return a.AddPartitions(ctx, topic, count, confirmed)
}

// AlterTopicConfig changes the settings it is given and leaves the others
// where they were.
func AlterTopicConfig(ctx context.Context, src source.Source, topic string, set map[string]string, confirmed bool) (err error) {
	defer panics.Recover(&err, "changing a topic's configuration")
	a, ok := src.(source.TopicAdmin)
	if !ok {
		return errNoTopicAdmin
	}
	return a.AlterTopicConfig(ctx, topic, set, confirmed)
}

var errNoTopicAdmin = errors.New("this source cannot make or unmake topics")
