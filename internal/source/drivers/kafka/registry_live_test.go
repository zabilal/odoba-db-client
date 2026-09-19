//go:build conformance

package kafka

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hamba/avro/v2"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Against a real schema registry (T2.69, FR-13.7). These expect the
// ikigai-schema-registry container on 58081 beside ikigai-kafka-sr on 59093,
// and skip unless IKIGAI_REQUIRE_SCHEMA_REGISTRY says otherwise.

func registryPort() int {
	if v := os.Getenv("IKIGAI_SCHEMA_REGISTRY_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			return p
		}
	}
	return 58081
}

func registryBroker() int {
	if v := os.Getenv("IKIGAI_SCHEMA_REGISTRY_KAFKA_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			return p
		}
	}
	return 59093
}

func registryURL() string { return "http://127.0.0.1:" + strconv.Itoa(registryPort()) }

// described connects to the broker the registry sits beside, naming the
// registry, or skips where neither is running.
func described(t *testing.T) source.Source {
	t.Helper()
	cfg := source.ConnectionConfig{DriverID: driverID, Host: "127.0.0.1", Port: registryBroker(),
		TLS:    source.TLSConfig{Mode: "disable"},
		Params: map[string]string{"registry": registryURL()},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	src, err := Driver{}.Open(ctx, cfg)
	if err != nil {
		if os.Getenv("IKIGAI_REQUIRE_SCHEMA_REGISTRY") != "" {
			t.Fatalf("a schema registry is required but unavailable: %v", err)
		}
		t.Skipf("no registry on port %d (docker start ikigai-schema-registry): %v", registryPort(), err)
	}
	t.Cleanup(func() { src.Close() })
	return src
}

// registered puts a schema in the registry, so that a test has one to read.
func registered(t *testing.T, subject, schema string) {
	t.Helper()
	registeredAs(t, subject, "AVRO", schema)
}

// registeredAs puts a schema of a named language in the registry.
func registeredAs(t *testing.T, subject, language, schema string) {
	t.Helper()
	body := strings.NewReader(`{"schemaType":"` + language + `","schema":` + strconv.Quote(schema) + `}`)
	req, err := http.NewRequest(http.MethodPost, registryURL()+"/subjects/"+subject+"/versions", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/vnd.schemaregistry.v1+json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("registering %s: %v", subject, err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		t.Fatalf("registering %s: %s", subject, res.Status)
	}
}

const orderSchema = `{"type":"record","name":"Order","fields":[{"name":"id","type":"string"},{"name":"total","type":"double"}]}`

func TestLiveTheRegistrySaysWhatItHolds(t *testing.T) {
	src := described(t)
	registered(t, "ikigai_it_orders-value", orderSchema)

	reg, ok := src.(source.SchemaRegistry)
	if !ok {
		t.Fatal("a connection naming a registry does not implement one")
	}
	subjects, err := reg.Subjects(context.Background())
	if err != nil {
		t.Fatalf("listing subjects: %v", err)
	}
	found := false
	for _, s := range subjects {
		if s.Name == "ikigai_it_orders-value" {
			found = true
		}
	}
	if !found {
		t.Errorf("the subject just registered is not among %v", subjects)
	}
}

func TestLiveASubjectsVersionsCarryTheirSchemas(t *testing.T) {
	src := described(t)
	registered(t, "ikigai_it_orders-value", orderSchema)

	reg := src.(source.SchemaRegistry)
	versions, err := reg.SubjectVersions(context.Background(), "ikigai_it_orders-value")
	if err != nil {
		t.Fatalf("reading versions: %v", err)
	}
	if len(versions) == 0 {
		t.Fatal("a registered subject has no versions")
	}
	v := versions[0]
	if v.Version != 1 || v.ID < 1 {
		t.Errorf("the first version is %+v", v)
	}
	// The language it is written in, and the text it was registered with:
	// both are what the browser shows and diffs (FR-13.14).
	if v.Format != "AVRO" {
		t.Errorf("an Avro schema says it is %q", v.Format)
	}
	if !strings.Contains(v.Definition, `"name":"Order"`) {
		t.Errorf("the schema text reads %q", v.Definition)
	}
}

func TestLiveADecoderSaysWhichSchemaWroteARecord(t *testing.T) {
	src := described(t)
	registered(t, "ikigai_it_orders-value", orderSchema)

	reg := src.(source.SchemaRegistry)
	dec, err := reg.Decoder(context.Background(), "ikigai_it_orders-value")
	if err != nil {
		t.Fatalf("a decoder for a registered subject: %v", err)
	}
	// It names the language, the subject and the version, so that somebody
	// can see which schema was used.
	if got := dec.Name(); !strings.HasPrefix(got, "Avro (ikigai_it_orders-value v") {
		t.Errorf("the decoder is called %q", got)
	}

	versions, err := reg.SubjectVersions(context.Background(), "ikigai_it_orders-value")
	if err != nil {
		t.Fatal(err)
	}
	id := versions[len(versions)-1].ID

	// A record genuinely written by that schema, read back as the values it
	// was written from.
	schema, err := avro.Parse(orderSchema)
	if err != nil {
		t.Fatal(err)
	}
	body, err := avro.Marshal(schema, map[string]any{"id": "order-1", "total": 12.5})
	if err != nil {
		t.Fatal(err)
	}
	record := append([]byte{0, byte(id >> 24), byte(id >> 16), byte(id >> 8), byte(id)}, body...)
	v, err := dec.Decode(record)
	if err != nil {
		t.Fatalf("decoding a record of that schema: %v", err)
	}
	fields, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("a record decoded to %T", v)
	}
	if fields["id"] != "order-1" || fields["total"] != 12.5 {
		t.Errorf("a record decoded to %v", fields)
	}

	// Bytes that are not what the schema describes are said to disagree with
	// it, rather than read as something they are not.
	nonsense := append([]byte{0, byte(id >> 24), byte(id >> 16), byte(id >> 8), byte(id)}, []byte("payload")...)
	if _, err := dec.Decode(nonsense); err == nil || !strings.Contains(err.Error(), "does not match its schema") {
		t.Errorf("bytes that are not the schema's say %v", err)
	}

	// A record written by another schema is refused by number rather than
	// decoded with the wrong one.
	other := append([]byte{0, 0, 0, 0, byte(id + 9)}, []byte("payload")...)
	if _, err := dec.Decode(other); err == nil || !strings.Contains(err.Error(), "written by schema") {
		t.Errorf("decoding a record of another schema says %v", err)
	}

	// And bytes that carry no header at all are not guessed at.
	if _, err := dec.Decode([]byte("no header here")); err == nil ||
		!strings.Contains(err.Error(), "carries no schema id") {
		t.Errorf("decoding bytes with no header says %v", err)
	}
}

func TestLiveAConnectionWithNoRegistryRefusesInItsOwnWords(t *testing.T) {
	// The ordinary case: a cluster read without a registry at all.
	src := live(t, liveConfig())
	reg, ok := src.(source.SchemaRegistry)
	if !ok {
		t.Fatal("the driver implements SchemaRegistry whether or not one is named")
	}
	if got := src.Capabilities().Stream.SchemaRegistry; got {
		t.Error("a connection naming no registry claims to have one")
	}
	for _, err := range []error{
		func() error { _, err := reg.Subjects(context.Background()); return err }(),
		func() error { _, err := reg.SubjectVersions(context.Background(), "x"); return err }(),
		func() error { _, err := reg.Decoder(context.Background(), "x"); return err }(),
	} {
		if err == nil || !strings.Contains(err.Error(), "names no schema registry") {
			t.Errorf("without a registry: %v", err)
		}
	}
}

func TestLiveClaimingARegistryMeansImplementingOne(t *testing.T) {
	// The shared suite holds every driver to this (REQ-DRV-1), but it can
	// only ask where the claim is made, and the suite's own connection names
	// no registry. This is that check, on a connection that does.
	src := described(t)
	if !src.Capabilities().Stream.SchemaRegistry {
		t.Fatal("a connection naming a registry does not claim one")
	}
	if _, ok := src.(source.SchemaRegistry); !ok {
		t.Error("claims Stream.SchemaRegistry but does not implement SchemaRegistry")
	}
}

func TestLiveARecordUnderAJSONSchemaReadsAsItsDocument(t *testing.T) {
	src := described(t)
	// The last of the three languages. A JSON Schema record is a JSON
	// document with five bytes in front of it, and those five bytes are what
	// keep it from reading as JSON on its own (T2.72).
	registeredAs(t, "ikigai_it_shape-value", "JSON", `{"type":"object","properties":{"id":{"type":"string"}}}`)

	reg := src.(source.SchemaRegistry)
	dec, err := reg.Decoder(context.Background(), "ikigai_it_shape-value")
	if err != nil {
		t.Fatalf("a decoder for a JSON Schema subject: %v", err)
	}
	if got := dec.Name(); !strings.HasPrefix(got, "JSON Schema (") {
		t.Errorf("the decoder is called %q", got)
	}

	versions, err := reg.SubjectVersions(context.Background(), "ikigai_it_shape-value")
	if err != nil {
		t.Fatal(err)
	}
	id := versions[len(versions)-1].ID
	record := append([]byte{0, byte(id >> 24), byte(id >> 16), byte(id >> 8), byte(id)}, []byte(`{"id":"x"}`)...)
	v, err := dec.Decode(record)
	if err != nil {
		t.Fatalf("decoding a JSON Schema record: %v", err)
	}
	// The document itself, with its own text kept: every number keeps its
	// digits, as it does for a JSON value from anywhere else (ADR-0100).
	doc, ok := v.(model.JSON)
	if !ok {
		t.Fatalf("a JSON Schema record decoded to %T", v)
	}
	if string(doc) != `{"id":"x"}` {
		t.Errorf("it decoded to %s", doc)
	}

	// And what follows the header has to be a document: bytes that are not
	// say so, rather than being handed over as though they were.
	nonsense := append([]byte{0, byte(id >> 24), byte(id >> 16), byte(id >> 8), byte(id)}, []byte("not json")...)
	if _, err := dec.Decode(nonsense); err == nil || !strings.Contains(err.Error(), "not a JSON document") {
		t.Errorf("bytes that are not a document say %v", err)
	}
}

func TestLiveARecordWrittenInProtobufIsReadByItsSchema(t *testing.T) {
	src := described(t)
	registeredAs(t, "ikigai_it_proto-value", "PROTOBUF", liveOrderProto)

	reg := src.(source.SchemaRegistry)
	dec, err := reg.Decoder(context.Background(), "ikigai_it_proto-value")
	if err != nil {
		t.Fatalf("a decoder for a Protobuf subject: %v", err)
	}
	if got := dec.Name(); !strings.HasPrefix(got, "Protobuf (ikigai_it_proto-value v") {
		t.Errorf("the decoder is called %q", got)
	}

	versions, err := reg.SubjectVersions(context.Background(), "ikigai_it_proto-value")
	if err != nil {
		t.Fatal(err)
	}
	id := versions[len(versions)-1].ID

	// Protobuf's framing carries an index after the header saying which of a
	// file's messages the record is. A single zero is the shortcut for the
	// first one, which is what a producer writes for a file like this.
	body := []byte{0x0a, 0x07}
	body = append(body, []byte("order-1")...)
	body = append(body, 0x11)                                           // field 2, 64-bit
	body = append(body, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x29, 0x40) // 12.5
	record := []byte{0, byte(id >> 24), byte(id >> 16), byte(id >> 8), byte(id), 0x00}
	record = append(record, body...)

	v, err := dec.Decode(record)
	if err != nil {
		t.Fatalf("decoding a Protobuf record: %v", err)
	}
	fields, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("a record decoded to %T", v)
	}
	if fields["id"] != "order-1" || fields["total"] != 12.5 {
		t.Errorf("a record decoded to %v", fields)
	}

	// A record whose index names no message is refused rather than read as
	// one it is not: on the wire the fields are numbered, so the wrong
	// message decodes into nonsense instead of an error.
	lost := []byte{0, byte(id >> 24), byte(id >> 16), byte(id >> 8), byte(id), 0x02, 0x12, 0x00}
	if _, err := dec.Decode(lost); err == nil || !strings.Contains(err.Error(), "no such message") {
		t.Errorf("a record naming a message that is not there says %v", err)
	}
}

const liveOrderProto = `syntax = "proto3";
package ikigai;
message Order {
  string id = 1;
  double total = 2;
}
`
