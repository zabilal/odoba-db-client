package localdb

import (
	"context"
	"encoding/json"
)

// DecoderChoice is the decoder a topic's key or value is read with (FR-13.7):
// the name it goes by, so that a choice outlives the build that made it. A
// name no longer offered is read back as a name nobody knows, which is how a
// decoder that needed a registry stops applying when the registry goes.
type DecoderChoice struct {
	Name string `json:"name,omitempty"`
}

// Decoder choices live in the key-value table under this prefix, one key to a
// connection, a field and a topic. A connection's ID is hex and a field is
// "key" or "value"; the topic goes last because it is the only part a person
// names, and nothing after it has to be told apart from it.
const decoderPrefix = "decoder/"

func decoderKey(connID, field, topic string) string {
	return decoderPrefix + connID + "/" + field + "/" + topic
}

// Decoder is the decoder last chosen for a topic's key or value, and whether
// one was.
func (d *DB) Decoder(ctx context.Context, connID, field, topic string) (DecoderChoice, bool, error) {
	b, ok, err := d.Get(ctx, decoderKey(connID, field, topic))
	if err != nil || !ok {
		return DecoderChoice{}, false, err
	}
	var c DecoderChoice
	if err := json.Unmarshal(b, &c); err != nil {
		return DecoderChoice{}, false, err
	}
	return c, true, nil
}

// PutDecoder remembers the decoder chosen for a topic's key or value.
func (d *DB) PutDecoder(ctx context.Context, connID, field, topic string, c DecoderChoice) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return d.Put(ctx, decoderKey(connID, field, topic), b)
}
