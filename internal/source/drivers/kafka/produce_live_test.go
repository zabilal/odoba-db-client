//go:build conformance

package kafka

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Writing a record (T2.77, FR-13.11, FR-13.21).

// producing is a connection whose guard is the one given, so that what a
// write is allowed to do can be varied without varying anything else.
func producing(t *testing.T, g source.Guard) source.Source {
	t.Helper()
	cfg := liveConfig()
	cfg.Guard = g
	return live(t, cfg)
}

func TestLiveARecordIsWrittenAndReadBack(t *testing.T) {
	src := producing(t, source.Guard{})
	seededTopic(t, src, "ikigai_it_produce", 3)
	p, ok := src.(source.StreamProducer)
	if !ok {
		t.Fatal("claims Stream.Produce but does not implement StreamProducer")
	}
	ctx := context.Background()

	value := []byte(`{"written":"by the produce test"}`)
	at, offset, err := p.Produce(ctx, source.ProduceRequest{
		Topic: "ikigai_it_produce", Partition: -1,
		Key: []byte("k1"), Value: value,
		Headers: []model.RecordHeader{{Key: "trace-id", Value: []byte("abc123")}},
	})
	if err != nil {
		t.Fatalf("writing a record: %v", err)
	}
	if at.Topic != "ikigai_it_produce" {
		t.Errorf("the record went to the topic %q", at.Topic)
	}
	// Addressed to no partition, it still landed in one, and says which:
	// where a record went is the first thing anybody needs of it.
	if at.Partition < 0 || at.Partition > 2 {
		t.Errorf("the record landed in partition %d of three", at.Partition)
	}
	if offset < 0 {
		t.Errorf("the record was written at offset %d", offset)
	}

	// And it is there to be read, which is the only proof that counts.
	found := false
	for _, row := range from(t, src, "ikigai_it_produce", nil, 10_000) {
		for _, cell := range row {
			if b, ok := cell.([]byte); ok && string(b) == string(value) {
				found = true
			}
		}
	}
	if !found {
		t.Error("the record that was written was not read back")
	}
}

func TestLiveARecordGoesWhereItIsAddressed(t *testing.T) {
	src := producing(t, source.Guard{})
	seededTopic(t, src, "ikigai_it_produce", 3)
	p := src.(source.StreamProducer)
	ctx := context.Background()

	for _, want := range []int32{0, 1, 2} {
		at, _, err := p.Produce(ctx, source.ProduceRequest{
			Topic: "ikigai_it_produce", Partition: want, Value: []byte("addressed")})
		if err != nil {
			t.Fatalf("writing to partition %d: %v", want, err)
		}
		if at.Partition != want {
			t.Errorf("a record addressed to partition %d landed in %d", want, at.Partition)
		}
	}

	// A partition the topic has not got is refused, and the refusal says what
	// the topic actually has: a record in the wrong log is worse than one
	// that was never written.
	_, _, err := p.Produce(ctx, source.ProduceRequest{
		Topic: "ikigai_it_produce", Partition: 99, Value: []byte("nowhere")})
	if err == nil || !strings.Contains(err.Error(), "no partition 99") {
		t.Errorf("a record addressed to a partition that is not there: %v", err)
	}
}

func TestLiveTheSameKeyGoesToTheSamePartition(t *testing.T) {
	// Addressed to no partition, a record is hashed by its key, which is what
	// keeps one key's records in the order they were written.
	src := producing(t, source.Guard{})
	seededTopic(t, src, "ikigai_it_produce", 3)
	p := src.(source.StreamProducer)
	ctx := context.Background()

	first, _, err := p.Produce(ctx, source.ProduceRequest{
		Topic: "ikigai_it_produce", Partition: -1, Key: []byte("same-key"), Value: []byte("one")})
	if err != nil {
		t.Fatalf("writing the first: %v", err)
	}
	second, _, err := p.Produce(ctx, source.ProduceRequest{
		Topic: "ikigai_it_produce", Partition: -1, Key: []byte("same-key"), Value: []byte("two")})
	if err != nil {
		t.Fatalf("writing the second: %v", err)
	}
	if first.Partition != second.Partition {
		t.Errorf("one key's records went to partitions %d and %d", first.Partition, second.Partition)
	}
}

func TestLiveAReadOnlyConnectionWritesNothing(t *testing.T) {
	src := producing(t, source.Guard{ReadOnly: true})
	seededTopic(t, src, "ikigai_it_produce", 3)
	p := src.(source.StreamProducer)

	_, _, err := p.Produce(context.Background(), source.ProduceRequest{
		Topic: "ikigai_it_produce", Partition: -1, Value: []byte("should not be written")})
	if !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("a read-only connection wrote a record, or refused in other words: %v", err)
	}
	// Consent cannot buy its way past read-only: that is a setting about the
	// connection, not a question about this record.
	_, _, err = p.Produce(context.Background(), source.ProduceRequest{
		Topic: "ikigai_it_produce", Partition: -1, Value: []byte("nor this"), Confirmed: true})
	if !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("a confirmed write got past read-only: %v", err)
	}
}

func TestLiveAProductionConnectionAsksBeforeItWrites(t *testing.T) {
	src := producing(t, source.Guard{Environment: source.EnvProduction})
	seededTopic(t, src, "ikigai_it_produce", 3)
	p := src.(source.StreamProducer)
	ctx := context.Background()
	req := source.ProduceRequest{Topic: "ikigai_it_produce", Partition: -1, Value: []byte("guarded")}

	if _, _, err := p.Produce(ctx, req); !errors.Is(err, source.ErrConfirmationRequired) {
		t.Errorf("a production connection wrote without being asked: %v", err)
	}
	// And consent given for this record lets this record through.
	req.Confirmed = true
	if _, _, err := p.Produce(ctx, req); err != nil {
		t.Errorf("a confirmed write to a production connection was refused: %v", err)
	}
}

func TestLiveARecordThatItsSchemaCannotReadIsNotWritten(t *testing.T) {
	src := described(t)
	registered(t, "ikigai_it_orders-value", orderSchema)
	seededTopic(t, src, "ikigai_it_produce_schema", 1)
	p := src.(source.StreamProducer)
	ctx := context.Background()

	// Bytes with no schema header at all: a reader would not know what wrote
	// them, so this will not write them under a subject that says it does.
	_, _, err := p.Produce(ctx, source.ProduceRequest{
		Topic: "ikigai_it_produce_schema", Partition: -1,
		Value: []byte("just some text"), Subject: "ikigai_it_orders-value"})
	if err == nil {
		t.Fatal("a record its subject cannot read was written")
	}
	if !strings.Contains(err.Error(), "ikigai_it_orders-value") {
		t.Errorf("the refusal does not name the subject it is refusing against: %v", err)
	}

	// Naming no subject writes whatever it is given: most topics have no
	// schema, and a connection that names a registry must still write to them.
	if _, _, err := p.Produce(ctx, source.ProduceRequest{
		Topic: "ikigai_it_produce_schema", Partition: -1, Value: []byte("just some text")}); err != nil {
		t.Errorf("a record naming no subject was refused: %v", err)
	}
}
