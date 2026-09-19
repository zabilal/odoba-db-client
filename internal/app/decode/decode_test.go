package decode

import (
	"reflect"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// The decoders that need nothing but the bytes (T2.68, FR-13.7).

func names(t *testing.T, b []byte) []string {
	t.Helper()
	ds := Applicable(b)
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.Name()
	}
	return out
}

func TestOnlyTheDecodersThatCanReadTheBytesAreOffered(t *testing.T) {
	// Most decoded first: somebody opening a record wants to see what it says.
	if got, want := names(t, []byte(`{"a":1}`)), []string{NameJSON, NameText, NameHex}; !reflect.DeepEqual(got, want) {
		t.Errorf("JSON bytes are read by %v", got)
	}
	// Text that is not JSON is not offered as JSON.
	if got, want := names(t, []byte("hello")), []string{NameText, NameHex}; !reflect.DeepEqual(got, want) {
		t.Errorf("plain text is read by %v", got)
	}
	// Bytes that are not text at all are only ever bytes.
	if got, want := names(t, []byte{0xff, 0xfe, 0x00}), []string{NameHex}; !reflect.DeepEqual(got, want) {
		t.Errorf("bytes that are not text are read by %v", got)
	}
	// Empty bytes are not JSON, there being no document there; they are the
	// empty string, which somebody may well have written, and they are bytes.
	if got, want := names(t, []byte{}), []string{NameText, NameHex}; !reflect.DeepEqual(got, want) {
		t.Errorf("empty bytes are read by %v", got)
	}
}

func TestEachDecoderSaysWhatItMakesOfTheBytes(t *testing.T) {
	json, _ := ByName(NameJSON)
	v, err := json.Decode([]byte(`{"a":1}`))
	if err != nil {
		t.Fatalf("decoding JSON: %v", err)
	}
	// Its own text, not a re-encoding: every number keeps its digits.
	if got, ok := v.(model.JSON); !ok || string(got) != `{"a":1}` {
		t.Errorf("JSON decoded to %#v", v)
	}
	if _, err := json.Decode([]byte("hello")); err == nil {
		t.Error("bytes that are not JSON decoded as JSON")
	}

	text, _ := ByName(NameText)
	if v, err := text.Decode([]byte("hello")); err != nil || v != "hello" {
		t.Errorf("text decoded to %#v, %v", v, err)
	}
	if _, err := text.Decode([]byte{0xff}); err == nil {
		t.Error("bytes that are not UTF-8 decoded as text")
	}

	// Bytes are always bytes, so this one never fails: it is what everything
	// else falls back to.
	hex, _ := ByName(NameHex)
	v, err = hex.Decode([]byte{0xff, 0x00})
	if err != nil {
		t.Fatalf("decoding bytes: %v", err)
	}
	if got, ok := v.([]byte); !ok || !reflect.DeepEqual(got, []byte{0xff, 0x00}) {
		t.Errorf("bytes decoded to %#v", v)
	}
}

func TestADecoderIsFoundByTheNameItGoesBy(t *testing.T) {
	for _, name := range []string{NameJSON, NameText, NameHex} {
		d, ok := ByName(name)
		if !ok || d.Name() != name {
			t.Errorf("%s was not found by its name", name)
		}
	}
	// A name from a registry's decoder, or from a build that no longer offers
	// it, is not found here — and that is not an error.
	if _, ok := ByName("Avro"); ok {
		t.Error("a decoder that needs a registry was found among the local ones")
	}
}
