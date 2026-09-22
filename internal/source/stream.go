package source

import (
	"context"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// This file holds the optional interfaces specific to the stream/log paradigm.
//
// Note what is NOT here: consuming records. That happens through Browser like
// every other paradigm's data path (see browse.go), with Seek and Follow in
// BrowseOptions. Only operations with no analogue elsewhere live here.

// GroupInspector reads consumer groups and how far behind they are.
//
// It is the half of stream administration that changes nothing, and it is an
// interface of its own for that reason: showing how far a group has got is
// worth offering long before anything here can reset an offset or create a
// topic, and one interface for both would make offering the first a promise
// to do the second (ADR-0107).
type GroupInspector interface {
	// ConsumerGroups lists groups without computing lag, which would be
	// prohibitive on a cluster with thousands of them (FR-13.10).
	ConsumerGroups(ctx context.Context) ([]model.ConsumerGroup, error)

	// GroupOffsets loads one group's per-partition progress and lag. Called
	// only when the user opens a group.
	GroupOffsets(ctx context.Context, groupID string) ([]model.GroupOffset, error)
}

// StreamAdmin inspects and manages consumer groups and topics.
type StreamAdmin interface {
	GroupInspector

	// ResetOffsets moves a group's committed offsets.
	//
	// This is AccessAdmin: it changes what a running consumer will read next,
	// and is one of the most destructive operations in the product. It is
	// guarded without exception (FR-13.13, FR-13.21).
	ResetOffsets(ctx context.Context, req ResetRequest) error

	// CreateTopic, DeleteTopic and AlterTopicConfig are AccessDDL (FR-13.12).
	CreateTopic(ctx context.Context, spec TopicSpec) error
	DeleteTopic(ctx context.Context, topic string, confirmed bool) error
	AlterTopicConfig(ctx context.Context, topic string, set map[string]string, confirmed bool) error

	// AddPartitions increases a topic's partition count. It cannot be undone,
	// and it changes key-to-partition mapping for future records, so the UI
	// must say so before calling.
	AddPartitions(ctx context.Context, topic string, count int32, confirmed bool) error
}

// ResetRequest describes a consumer-group offset reset.
type ResetRequest struct {
	GroupID string

	// Partitions restricts the reset. Empty means every partition the group
	// has committed offsets for.
	Partitions []model.TopicPartition

	// Seek determines the new position, reusing the same seek semantics as
	// browsing so that "reset to a timestamp" means exactly what the user saw
	// when they seeked to that timestamp in the message browser.
	Seek Seek

	Confirmed bool
}

// TopicSpec describes a topic to create.
type TopicSpec struct {
	Name              string
	Partitions        int32
	ReplicationFactor int16
	Config            map[string]string
	Confirmed         bool
}

// StreamProducer writes records (FR-13.11).
type StreamProducer interface {
	// Produce writes one record and returns its assigned partition and offset.
	//
	// This is AccessWrite and inherits read-only mode and production
	// guardrails without exception (FR-13.21).
	Produce(ctx context.Context, rec ProduceRequest) (model.TopicPartition, int64, error)
}

// ProduceRequest is one record to write.
type ProduceRequest struct {
	Topic string

	// Partition selects an explicit partition; -1 lets the source choose by
	// key hashing.
	Partition int32

	Key     []byte
	Value   []byte
	Headers []model.RecordHeader

	// Subject names the schema-registry subject to validate against before
	// writing. Empty skips validation. When set and validation fails, nothing
	// is written — producing a record that consumers cannot decode is a
	// production incident, not a warning.
	Subject string

	Confirmed bool
}

// SchemaRegistry browses and resolves record schemas (FR-13.7, FR-13.14).
type SchemaRegistry interface {
	// Subjects lists registered subjects.
	Subjects(ctx context.Context) ([]model.SchemaSubject, error)

	// SubjectVersions loads every version of a subject, for the version diff.
	SubjectVersions(ctx context.Context, subject string) ([]model.SchemaVersion, error)

	// Decoder returns a decoder for a subject's records. The returned decoder
	// is used per record by the message browser, so it must be cheap to call
	// and safe for concurrent use.
	Decoder(ctx context.Context, subject string) (Decoder, error)
}

// Decoder turns raw record bytes into a canonical value (FR-13.7).
type Decoder interface {
	// Name identifies the decoder in the UI, e.g. "Avro (orders-value v3)".
	Name() string

	// Decode returns the decoded value, or an error the UI shows alongside the
	// raw bytes rather than in place of them — an undecodable record is still
	// worth seeing.
	Decode(data []byte) (any, error)
}
