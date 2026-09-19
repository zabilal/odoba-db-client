package kafka

import (
	"context"
	"fmt"

	"github.com/bufbuild/protocompile"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

// Reading a record written in Protobuf (FR-13.7, T2.71).
//
// A registry holds Protobuf as the .proto source somebody wrote, not as a
// compiled descriptor, so reading a record means compiling that text first.
// It is compiled once, when the decoder is asked for, because compiling is
// work and a topic's records all share a schema.
//
// Protobuf also differs from Avro in what a record says about itself: a file
// may declare several messages, so Confluent's framing carries an index after
// the five-byte header saying which one this record is. Reading a record as
// the wrong message would decode silently into nonsense — the fields are
// numbered, not named, on the wire — so the index is followed exactly and a
// path that leads nowhere is refused.

// schemaFile is the name a registry's schema is compiled under. A .proto
// needs a file name and a registry does not give one.
const schemaFile = "schema.proto"

// protoFile compiles a schema's text into descriptors, or says why it could
// not be read. Standard imports are available, since a schema may well import
// google/protobuf/timestamp.proto and expect it to be there.
func protoFile(ctx context.Context, text string) (protoreflect.FileDescriptor, error) {
	c := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
			Accessor: protocompile.SourceAccessorFromMap(map[string]string{schemaFile: text}),
		}),
	}
	files, err := c.Compile(ctx, schemaFile)
	if err != nil {
		return nil, fmt.Errorf("kafka: this schema could not be read: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("kafka: this schema holds no file")
	}
	return files[0], nil
}

// messageAt follows the index a record carries to the message that wrote it.
//
// The path walks declarations: the first number picks a message declared in
// the file, and each after it picks one declared inside that message. An empty
// path means the first message, which is what a record with no index means.
func messageAt(file protoreflect.FileDescriptor, index []int) (protoreflect.MessageDescriptor, error) {
	if len(index) == 0 {
		index = []int{0}
	}
	msgs := file.Messages()
	var msg protoreflect.MessageDescriptor
	for depth, at := range index {
		if at < 0 || at >= msgs.Len() {
			return nil, fmt.Errorf("kafka: this record names message %v, and there is no such message in its schema", index)
		}
		msg = msgs.Get(at)
		if depth < len(index)-1 {
			msgs = msg.Messages()
		}
	}
	return msg, nil
}

// decodeProto reads a record's payload as a message, into the plain values the
// grid and the record view already show.
func decodeProto(desc protoreflect.MessageDescriptor, payload []byte) (any, error) {
	msg := dynamicpb.NewMessage(desc)
	if err := proto.Unmarshal(payload, msg); err != nil {
		return nil, fmt.Errorf("kafka: this record does not match its schema: %w", err)
	}
	return protoFields(msg), nil
}

// protoFields is a message as a map of its populated fields.
//
// Only what was written is shown. Protobuf gives an unset field its type's
// zero value, and a record that says nothing about a field is not the same as
// one that says zero — showing every field would put words in the producer's
// mouth.
func protoFields(msg protoreflect.Message) map[string]any {
	out := map[string]any{}
	msg.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		out[string(fd.Name())] = protoValue(fd, v)
		return true
	})
	return out
}

// protoValue is one field's value as a plain Go value. Bytes stay bytes, so
// that what is shown is text where it reads as text and hex where it does not
// (ADR-0102); numbers stay numbers, rather than the strings JSON would make
// of the 64-bit ones.
func protoValue(fd protoreflect.FieldDescriptor, v protoreflect.Value) any {
	switch {
	case fd.IsMap():
		out := map[string]any{}
		v.Map().Range(func(k protoreflect.MapKey, mv protoreflect.Value) bool {
			out[k.String()] = protoScalar(fd.MapValue(), mv)
			return true
		})
		return out
	case fd.IsList():
		list := v.List()
		out := make([]any, list.Len())
		for i := range out {
			out[i] = protoScalar(fd, list.Get(i))
		}
		return out
	}
	return protoScalar(fd, v)
}

// protoScalar is one value of a field's own kind.
func protoScalar(fd protoreflect.FieldDescriptor, v protoreflect.Value) any {
	switch fd.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return protoFields(v.Message())
	case protoreflect.EnumKind:
		// The name it was written as, where the schema has one: a number says
		// nothing to somebody reading a record.
		if e := fd.Enum().Values().ByNumber(v.Enum()); e != nil {
			return string(e.Name())
		}
		return int32(v.Enum())
	}
	return v.Interface()
}
