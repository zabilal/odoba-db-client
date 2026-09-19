// Package decode reads a record's bytes as something a person can read
// (FR-13.7).
//
// A record's key and value are bytes, and bytes are not one thing: the same
// bytes are text to whoever wrote them, JSON to the service that parses them,
// and a dump to whoever has to know exactly what went over the wire. These are
// the decoders that need nothing but the bytes themselves. Avro, Protobuf and
// JSON Schema need a registry to ask (T2.69 onwards) and arrive as further
// decoders through the same interface, so that what changes is the list rather
// than everything that reads from it.
//
// A decoder that cannot read the bytes says so rather than guessing. What the
// UI does with that is show the fault beside the bytes rather than in place of
// them — an undecodable record is still worth seeing (source.Decoder).
package decode

import (
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// The names decoders go by, which are what a person chooses between.
const (
	NameJSON = "JSON"
	NameText = "Text"
	NameHex  = "Hex"
)

// Local are the decoders that need nothing but the bytes, most decoded first:
// somebody opening a record wants to see what it says, and descends towards
// the bytes only when the meaning is in doubt.
func Local() []source.Decoder {
	return []source.Decoder{jsonDecoder{}, textDecoder{}, hexDecoder{}}
}

// ByName is the local decoder that goes by a name, and whether there is one.
// A name that is not among them is not an error here: it may belong to a
// registry's decoder, or to a build that no longer offers it.
func ByName(name string) (source.Decoder, bool) {
	for _, d := range Local() {
		if d.Name() == name {
			return d, true
		}
	}
	return nil, false
}

// Applicable are the decoders that can read these bytes, most decoded first.
// Only what a value admits is offered: a form that cannot be read is worse
// than one that is not there (ADR-0098).
//
// Extra decoders come first, before the local ones. A decoder that needed a
// registry to build knows more about these bytes than anything here can work
// out from the bytes alone, so it is the reading somebody most likely wants
// (T2.70).
func Applicable(b []byte, extra ...source.Decoder) []source.Decoder {
	var out []source.Decoder
	for _, d := range append(append([]source.Decoder{}, extra...), Local()...) {
		if d == nil {
			continue
		}
		if _, err := d.Decode(b); err == nil {
			out = append(out, d)
		}
	}
	return out
}

// jsonDecoder reads bytes that parse as JSON. The value keeps its own text
// rather than being re-encoded, so that every number keeps its digits.
type jsonDecoder struct{}

func (jsonDecoder) Name() string { return NameJSON }

func (jsonDecoder) Decode(b []byte) (any, error) {
	if !json.Valid(b) {
		return nil, errors.New("these bytes are not JSON")
	}
	return model.JSON(b), nil
}

// textDecoder reads bytes that are text. Bytes that are not valid UTF-8 are
// refused rather than shown as replacement characters, which would be a lie
// about what was written.
type textDecoder struct{}

func (textDecoder) Name() string { return NameText }

func (textDecoder) Decode(b []byte) (any, error) {
	if !utf8.Valid(b) {
		return nil, errors.New("these bytes are not UTF-8 text")
	}
	return string(b), nil
}

// hexDecoder reads bytes as themselves. It never fails, because bytes are
// always bytes: it is what everything else falls back to.
type hexDecoder struct{}

func (hexDecoder) Name() string { return NameHex }

func (hexDecoder) Decode(b []byte) (any, error) { return b, nil }
