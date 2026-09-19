package decode

import (
	"errors"
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

// stub is a decoder somebody else built — a registry's, in practice — which
// Applicable is asked to offer beside the local ones.
type stub struct {
	name string
	err  error
}

func (s stub) Name() string { return s.name }

func (s stub) Decode([]byte) (any, error) {
	if s.err != nil {
		return nil, s.err
	}
	return map[string]any{"read": "by a schema"}, nil
}

func TestWhatSomebodyElseKnowsIsOfferedFirst(t *testing.T) {
	// A decoder built from a schema knows more about these bytes than
	// anything worked out from the bytes alone, so it is the reading
	// somebody most likely wants (ADR-0102).
	got := Applicable([]byte(`{"a":1}`), stub{name: "Avro (orders-value v1)"})
	if len(got) != 4 || got[0].Name() != "Avro (orders-value v1)" {
		t.Fatalf("offered %v", names(t, []byte(`{"a":1}`)))
	}
	if got[1].Name() != NameJSON || got[3].Name() != NameHex {
		t.Errorf("what the bytes say for themselves is offered as %s, %s", got[1].Name(), got[3].Name())
	}
	// One that cannot read these bytes is not offered, exactly as a local
	// decoder that cannot is not.
	got = Applicable([]byte("plain"), stub{name: "Avro", err: errors.New("not Avro")})
	for _, d := range got {
		if d.Name() == "Avro" {
			t.Error("a decoder that refused the bytes was offered anyway")
		}
	}
}

func TestADecoderThatIsNotThereIsNotAsked(t *testing.T) {
	// A field with no subject has no decoder, and the picker passes what it
	// has. Nil must be stepped over rather than called.
	got := Applicable([]byte("plain"), nil)
	if len(got) != 2 || got[0].Name() != NameText {
		t.Errorf("with no decoder of its own, bytes are read by %v", got)
	}
}
