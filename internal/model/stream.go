package model

import "time"

// Types in this file describe stream/log structure — Kafka and engines like it.
//
// This paradigm is the reason the canonical model is shaped the way it is. A
// log has no rows, no server-owned schema and no primary key; records are
// addressed by partition and offset, and are immutable once written. Any
// abstraction that assumes otherwise is wrong, and designing against Kafka
// before writing a single driver (T0.29) is what surfaced that.

// Cluster describes a stream cluster (FR-13.1).
type Cluster struct {
	ID         string
	Brokers    []Broker
	Controller int32 // broker ID of the controller, -1 when unknown
	Attrs      map[string]string
}

// Broker is one node of a stream cluster.
type Broker struct {
	ID   int32
	Host string
	Port int32
	Rack string
}

// Topic is a named, partitioned log (FR-13.2).
type Topic struct {
	Name       string
	Internal   bool
	Partitions []Partition

	// ReplicationFactor is -1 when it varies across partitions.
	ReplicationFactor int32

	// Config holds topic configuration. Overrides are distinguished from
	// inherited defaults so the UI can render them differently (FR-13.12).
	Config []ConfigEntry

	Attrs map[string]string
}

// MessageCount returns the sum of (high - low) across partitions. This is an
// upper bound on retained records, not a count of records ever written, and
// compaction or retention can make it exceed what a consumer will actually
// read. The UI must label it accordingly.
func (t Topic) MessageCount() int64 {
	var n int64
	for _, p := range t.Partitions {
		if p.HighWatermark > p.LowWatermark {
			n += p.HighWatermark - p.LowWatermark
		}
	}
	return n
}

// Partition is one ordered log within a topic (FR-13.3).
type Partition struct {
	ID       int32
	Leader   int32
	Replicas []int32
	ISR      []int32 // in-sync replicas

	LowWatermark  int64 // earliest retained offset
	HighWatermark int64 // next offset to be written
}

// ConfigEntry is one configuration key of a topic or broker.
type ConfigEntry struct {
	Name  string
	Value string

	// Source records where the value came from — a topic-level override, a
	// broker default, or the static default. FR-13.12 requires showing this.
	Source string

	// Sensitive values are returned redacted by the server and must never be
	// logged or displayed in full.
	Sensitive bool

	ReadOnly bool
}

// IsOverride reports whether the entry was explicitly set rather than inherited.
func (c ConfigEntry) IsOverride() bool {
	return c.Source != "" && c.Source != "DEFAULT_CONFIG" && c.Source != "STATIC_BROKER_CONFIG"
}

// ConsumerGroup describes a group of cooperating consumers (FR-13.10).
type ConsumerGroup struct {
	ID      string
	State   string
	Members []GroupMember

	// Offsets carries per-partition progress. Populated on demand: a cluster
	// can have thousands of groups, so lag is never computed during listing.
	Offsets []GroupOffset
}

// TotalLag sums lag across all known partitions.
func (g ConsumerGroup) TotalLag() int64 {
	var n int64
	for _, o := range g.Offsets {
		if o.Lag > 0 {
			n += o.Lag
		}
	}
	return n
}

// GroupMember is one consumer instance in a group.
type GroupMember struct {
	ID         string
	ClientID   string
	Host       string
	Assignment []TopicPartition
}

// GroupOffset is a group's progress on one partition.
type GroupOffset struct {
	Topic     string
	Partition int32

	// Current is the group's committed offset, -1 when it has never committed.
	Current int64

	// End is the partition's high watermark at the time of measurement.
	End int64

	// Lag is End-Current, or -1 when Current is unknown. Because Current and
	// End are read at slightly different instants, lag is a close estimate
	// rather than an exact figure, and may briefly read negative on a busy
	// partition; consumers of this field must clamp rather than trust the sign.
	Lag int64
}

// TopicPartition addresses one partition of one topic.
type TopicPartition struct {
	Topic     string
	Partition int32
}

// Record is one message read from a log.
//
// Records reach the grid as Rows via a RowStream like any other data, but this
// struct is what the message detail view (FR-13.8) renders.
type Record struct {
	Topic     string
	Partition int32
	Offset    int64
	Timestamp time.Time

	Key   []byte
	Value []byte

	Headers []RecordHeader

	// KeyDecoded and ValueDecoded hold the results of applying the topic's
	// configured decoders (FR-13.7). Nil when raw or when decoding failed.
	KeyDecoded   any
	ValueDecoded any

	// DecodeErr records why decoding failed, so the UI can show the raw bytes
	// alongside an explanation instead of an empty cell.
	DecodeErr string
}

// RecordHeader is one message header.
type RecordHeader struct {
	Key   string
	Value []byte
}

// SchemaSubject is a schema-registry subject (FR-13.14).
type SchemaSubject struct {
	Name     string
	Versions []SchemaVersion

	// Compatibility is the subject's compatibility mode, empty when it
	// inherits the registry default.
	Compatibility string
}

// SchemaVersion is one registered version of a subject.
type SchemaVersion struct {
	Version int32
	ID      int32

	// Format is the schema language: AVRO, PROTOBUF or JSON.
	Format string

	// Definition is the schema text, shown and diffed in the UI.
	Definition string
}
