package kafka

import (
	"errors"
	"strings"
	"testing"

	"github.com/twmb/franz-go/pkg/sr"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// What a registry is asked, and what it is told, without a registry to ask
// (T2.69, FR-13.7).

func TestARegistryAddressKeepsItsPasswordOutOfWhatIsSaid(t *testing.T) {
	// A URL may carry credentials in its own userinfo, and a hint is written
	// where somebody can read it (NFR-S2).
	if got := redactURL("https://user:hunter2@registry.example:8081"); strings.Contains(got, "hunter2") {
		t.Errorf("a redacted address still reads %q", got)
	}
	if got := redactURL("https://user:hunter2@registry.example:8081"); !strings.Contains(got, "user") {
		t.Errorf("a redacted address lost the user too: %q", got)
	}
	// One with nothing to hide is left as it is.
	if got := redactURL("http://localhost:8081"); got != "http://localhost:8081" {
		t.Errorf("an address with no credentials became %q", got)
	}
}

func TestAnAddressThatIsNotOneIsRefusedNow(t *testing.T) {
	for _, raw := range []string{"localhost:8081", "ftp://registry:8081", "://nonsense"} {
		_, err := registryOf(source.ConnectionConfig{Params: map[string]string{"registry": raw}})
		var ce *source.ConnectError
		if !errors.As(err, &ce) || ce.Kind != source.ConnectConfig {
			t.Errorf("%q was taken for a registry address: %v", raw, err)
		}
	}
	// Naming no registry is not a fault: most clusters are read without one.
	cl, err := registryOf(source.ConnectionConfig{Params: map[string]string{}})
	if cl != nil || err != nil {
		t.Errorf("naming no registry gave %v, %v", cl, err)
	}
}

func TestASchemaIsCarriedAcrossAsTheModelHoldsIt(t *testing.T) {
	ss := sr.SubjectSchema{
		Subject: "orders-value", Version: 3, ID: 7,
		Schema: sr.Schema{Schema: `{"type":"record"}`, Type: sr.TypeProtobuf},
	}
	got := versionOf(ss)
	want := model.SchemaVersion{Version: 3, ID: 7, Format: "PROTOBUF", Definition: `{"type":"record"}`}
	if got != want {
		t.Errorf("a schema reads as %+v, want %+v", got, want)
	}
}

func TestASchemasLanguageIsNamedTheWayItIsRead(t *testing.T) {
	for _, c := range []struct{ format, want string }{
		{"AVRO", "Avro"}, {"PROTOBUF", "Protobuf"}, {"JSON", "JSON Schema"},
		{"", "A schema"}, {"SOMETHING", "SOMETHING"},
	} {
		if got := title(c.format); got != c.want {
			t.Errorf("%q reads as %q, want %q", c.format, got, c.want)
		}
	}
}

func TestWhatWentWrongWithARegistryIsSaidInItsOwnKind(t *testing.T) {
	for _, c := range []struct {
		text string
		want source.ConnectKind
	}{
		{"401 Unauthorized", source.ConnectAuth},
		{"x509: certificate signed by unknown authority", source.ConnectTLS},
		{"dial tcp: lookup registry.invalid: no such host", source.ConnectUnreachable},
		{"dial tcp 127.0.0.1:8081: connect: connection refused", source.ConnectRefused},
	} {
		var ce *source.ConnectError
		if err := registryError(errors.New(c.text)); !errors.As(err, &ce) || ce.Kind != c.want {
			t.Errorf("%q is kind %v, want %v", c.text, err, c.want)
		}
	}
	// Anything else is passed along as it came: inventing a kind for it would
	// send somebody looking in the wrong place.
	plain := errors.New("the registry said something unexpected")
	got := registryError(plain)
	if !errors.Is(got, plain) {
		t.Errorf("an unfamiliar failure became %v", got)
	}
	var ce *source.ConnectError
	if errors.As(got, &ce) {
		t.Errorf("an unfamiliar failure was given the kind %v, which nothing said it was", ce.Kind)
	}
}

func TestADecoderNamesTheSchemaItReadsBy(t *testing.T) {
	d := &schemaDecoder{subject: "orders-value",
		schema: model.SchemaVersion{Version: 3, ID: 7, Format: "AVRO"}}
	if got := d.Name(); got != "Avro (orders-value v3)" {
		t.Errorf("the decoder is called %q", got)
	}
	// A record whose header names another schema is refused by number rather
	// than read with the wrong one.
	other := []byte{0, 0, 0, 0, 9, 'x'}
	_, err := d.Decode(other)
	if err == nil || !strings.Contains(err.Error(), "written by schema 9") {
		t.Errorf("a record of another schema says %v", err)
	}
	// And it says which schema this decoder reads by, which is how somebody
	// sees that the two do not match rather than only that one exists.
	if err == nil || !strings.Contains(err.Error(), "which is schema 7") {
		t.Errorf("a record of another schema does not say what this decoder reads by: %v", err)
	}
	// Bytes with no header are not guessed at.
	if _, err := d.Decode([]byte("nope")); err == nil || !strings.Contains(err.Error(), "carries no schema id") {
		t.Errorf("bytes with no header say %v", err)
	}
	// And one written by this very schema says what is not written yet,
	// rather than returning a value nobody decoded.
	mine := []byte{0, 0, 0, 0, 7, 'x'}
	if _, err := d.Decode(mine); err == nil || !strings.Contains(err.Error(), "not written yet") {
		t.Errorf("a record of this schema says %v", err)
	}
}

func TestALanguageNobodyHasWrittenAReaderForIsRefused(t *testing.T) {
	// Protobuf and JSON Schema are their own languages and are written next.
	// Until then a decoder for one says so, rather than returning a guess:
	// the claim must not run ahead of the code (REQ-DRV-1).
	for _, format := range []string{"PROTOBUF", "JSON"} {
		d := &schemaDecoder{subject: "orders-value",
			schema: model.SchemaVersion{Version: 1, ID: 7, Format: format}}
		record := []byte{0, 0, 0, 0, 7, 'x'}
		_, err := d.Decode(record)
		if err == nil || !strings.Contains(err.Error(), "not written yet") {
			t.Errorf("a %s record says %v", format, err)
		}
	}
}

func TestASchemaThisBuildCannotReadIsSaidWhenItIsAskedFor(t *testing.T) {
	// A registry may hold a schema this build cannot parse. Saying so when
	// the decoder is asked for beats a decoder that fails on every record.
	if _, err := avroSchema(`{"type":"record"`); err == nil {
		t.Error("a schema that is not one parsed")
	} else if !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("an unreadable schema says %v", err)
	}
	if _, err := avroSchema(orderSchemaText); err != nil {
		t.Errorf("a schema that is one did not parse: %v", err)
	}
}

const orderSchemaText = `{"type":"record","name":"Order","fields":[{"name":"id","type":"string"}]}`
