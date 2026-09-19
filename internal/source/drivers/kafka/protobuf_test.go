package kafka

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// Reading a record written in Protobuf, without a registry to ask (T2.71).

const orderProto = `syntax = "proto3";
package ikigai;
message Order {
  string id = 1;
  double total = 2;
  bytes raw = 3;
  Status status = 4;
  repeated string tags = 5;
  map<string, int64> counts = 6;
  message Line {
    string sku = 1;
  }
  Line first = 7;
}
enum Status {
  UNKNOWN = 0;
  PAID = 1;
}
`

func TestASchemaThatIsNotProtobufIsSaidWhenItIsAskedFor(t *testing.T) {
	// A registry may hold a .proto this build cannot read. Saying so when the
	// decoder is asked for beats failing on every record.
	if _, err := protoFile(context.Background(), "this is not a proto file"); err == nil {
		t.Error("nonsense compiled as a schema")
	} else if !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("an unreadable schema says %v", err)
	}
	if _, err := protoFile(context.Background(), orderProto); err != nil {
		t.Errorf("a schema that is one did not compile: %v", err)
	}
}

func TestTheIndexSaysWhichMessageWroteARecord(t *testing.T) {
	f, err := protoFile(context.Background(), orderProto)
	if err != nil {
		t.Fatal(err)
	}
	// No index at all means the first message declared, which is what a
	// record with the shortcut index means.
	msg, err := messageAt(f, nil)
	if err != nil || string(msg.Name()) != "Order" {
		t.Errorf("an empty index names %v (%v)", msg, err)
	}
	if msg, err := messageAt(f, []int{0}); err != nil || string(msg.Name()) != "Order" {
		t.Errorf("index 0 names %v (%v)", msg, err)
	}
	// A path descends into messages declared inside others — including the
	// ones nobody wrote. A map field generates an entry message, and it is
	// nested like any other, so the index counts what the descriptor holds
	// rather than what somebody typed.
	nested := f.Messages().Get(0).Messages()
	var lineAt = -1
	for i := 0; i < nested.Len(); i++ {
		if string(nested.Get(i).Name()) == "Line" {
			lineAt = i
		}
	}
	if lineAt < 0 {
		t.Fatalf("the message declared inside Order is not among its %d nested messages", nested.Len())
	}
	if msg, err := messageAt(f, []int{0, lineAt}); err != nil || string(msg.Name()) != "Line" {
		t.Errorf("index 0,%d names %v (%v)", lineAt, msg, err)
	}
	// And the synthetic one is reachable the same way, being a message like
	// any other as far as the wire is concerned.
	if msg, err := messageAt(f, []int{0, 0}); err != nil || !strings.HasSuffix(string(msg.Name()), "Entry") {
		t.Errorf("index 0,0 names %v (%v)", msg, err)
	}
	// And a path that leads nowhere is refused rather than read as something
	// it is not: the fields are numbered on the wire, so the wrong message
	// decodes into nonsense rather than an error.
	if _, err := messageAt(f, []int{9}); err == nil || !strings.Contains(err.Error(), "no such message") {
		t.Errorf("an index past the end says %v", err)
	}
	if _, err := messageAt(f, []int{0, 5}); err == nil {
		t.Error("a nested index past the end was accepted")
	}
}

func TestAMessageReadsAsThePlainValuesItHolds(t *testing.T) {
	f, err := protoFile(context.Background(), orderProto)
	if err != nil {
		t.Fatal(err)
	}
	desc, err := messageAt(f, []int{0})
	if err != nil {
		t.Fatal(err)
	}

	// Built by hand on the wire: field 1 a string, field 3 bytes, field 4 an
	// enum, field 5 repeated.
	var b []byte
	b = append(b, 0x0a, 0x07)             // field 1, length 7
	b = append(b, []byte("order-1")...)   //
	b = append(b, 0x1a, 0x02, 0xff, 0x00) // field 3, two bytes
	b = append(b, 0x20, 0x01)             // field 4, enum 1
	b = append(b, 0x2a, 0x01, 'a')        // field 5, "a"

	v, err := decodeProto(desc, b)
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	fields, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("a message decoded to %T", v)
	}
	if fields["id"] != "order-1" {
		t.Errorf("id read as %#v", fields["id"])
	}
	// Bytes stay bytes, so that what is shown is text where it reads as text
	// and hex where it does not (ADR-0102).
	if got, ok := fields["raw"].([]byte); !ok || !reflect.DeepEqual(got, []byte{0xff, 0x00}) {
		t.Errorf("raw read as %#v", fields["raw"])
	}
	// An enum reads as the name it was written as: a number says nothing.
	if fields["status"] != "PAID" {
		t.Errorf("status read as %#v", fields["status"])
	}
	if got, ok := fields["tags"].([]any); !ok || len(got) != 1 || got[0] != "a" {
		t.Errorf("tags read as %#v", fields["tags"])
	}
	// Only what was written is shown: a field the record says nothing about
	// is absent rather than zero, which would put words in the producer's
	// mouth.
	if _, there := fields["total"]; there {
		t.Errorf("a field never written is present as %#v", fields["total"])
	}
	if _, there := fields["first"]; there {
		t.Error("a message never written is present")
	}

	// Bytes that are not this message say so.
	if _, err := decodeProto(desc, []byte{0xff, 0xff, 0xff}); err == nil {
		t.Error("bytes that are not the message decoded anyway")
	}
}

func TestASchemaMayImportWhatProtocWouldHave(t *testing.T) {
	// A registry's schema is free to import the well-known types and expect
	// them to be there, exactly as it would when protoc compiled it.
	const withImport = `syntax = "proto3";
package ikigai;
import "google/protobuf/timestamp.proto";
message Event {
  string id = 1;
  google.protobuf.Timestamp when = 2;
}
`
	f, err := protoFile(context.Background(), withImport)
	if err != nil {
		t.Fatalf("a schema importing a well-known type: %v", err)
	}
	desc, err := messageAt(f, nil)
	if err != nil || string(desc.Name()) != "Event" {
		t.Fatalf("it holds %v (%v)", desc, err)
	}
	// And the imported message reads as its own fields, nested.
	var b []byte
	b = append(b, 0x0a, 0x02, 'i', 'd')   // field 1, "id"
	b = append(b, 0x12, 0x02, 0x08, 0x2a) // field 2, a Timestamp with seconds=42
	v, err := decodeProto(desc, b)
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	fields := v.(map[string]any)
	when, ok := fields["when"].(map[string]any)
	if !ok {
		t.Fatalf("the imported message read as %#v", fields["when"])
	}
	if when["seconds"] != int64(42) {
		t.Errorf("its seconds read as %#v", when["seconds"])
	}
}
