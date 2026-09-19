package kafka

import (
	"fmt"

	"github.com/hamba/avro/v2"
)

// Reading a record written in Avro (FR-13.7, T2.70).
//
// A schema registry says which schema wrote a record; this reads the record by
// it. Avro carries no field names or types in the payload — the schema is what
// makes the bytes mean anything at all — so a record cannot be read without
// the registry, and a record read by the wrong schema would be nonsense rather
// than an error. That is why the id is checked before any of this runs.

// avroSchema parses a schema's text, or says why it could not be read. A
// registry may hold a schema this build cannot parse — a language it does not
// know, or one written for a newer Avro — and saying so is better than a
// decoder that fails on every record.
func avroSchema(text string) (avro.Schema, error) {
	s, err := avro.Parse(text)
	if err != nil {
		return nil, fmt.Errorf("kafka: this schema could not be read: %w", err)
	}
	return s, nil
}

// decodeAvro reads a record's payload by its schema, into the plain values the
// grid and the record view already know how to show: a record becomes a map, a
// union becomes the value it holds, and a timestamp becomes an instant.
func decodeAvro(schema avro.Schema, payload []byte) (any, error) {
	var into any
	if err := avro.Unmarshal(schema, payload, &into); err != nil {
		// The bytes and the schema disagree. That is worth saying plainly:
		// the record is still shown as bytes beside this, and somebody
		// looking at both can usually see which of the two is wrong.
		return nil, fmt.Errorf("kafka: this record does not match its schema: %w", err)
	}
	return into, nil
}
