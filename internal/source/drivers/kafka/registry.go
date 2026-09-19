package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/hamba/avro/v2"
	"github.com/twmb/franz-go/pkg/sr"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/tlsconf"
)

// The schema registry a cluster's records are described by (FR-13.7,
// ADR-0101).
//
// A registry is a second server, reached over HTTP and named separately: a
// broker knows nothing about it, and it knows nothing about the broker. What
// ties them together is five bytes at the front of a record — a zero, then the
// schema's id — which is how a record says which schema wrote it.
//
// Reading those five bytes is all this does with a record. What the rest of
// the bytes mean is the business of the decoder for the schema's own language,
// and those are written next (T2.70 onwards): claiming to decode Avro here
// would be claiming something nobody has written.

// registryOf builds a client for the registry a connection names, or nil where
// none is named. A registry is optional: most of what this driver does needs
// no schemas at all.
func registryOf(cfg source.ConnectionConfig) (*sr.Client, error) {
	raw := strings.TrimSpace(cfg.Params["registry"])
	if raw == "" {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "The schema registry address should be a URL, such as http://localhost:8081.",
			Err:  fmt.Errorf("kafka: %q is not a registry address", redactURL(raw))}
	}

	opts := []sr.ClientOpt{sr.URLs(raw), sr.UserAgent("ikigai")}
	if u.Scheme == "https" {
		// The same rules the broker's own connection follows: verification on
		// unless this connection says otherwise, and never quietly off.
		tlsCfg, err := tlsconf.Config(cfg.TLS, u.Hostname())
		if err != nil {
			return nil, err
		}
		if tlsCfg != nil {
			opts = append(opts, sr.DialTLSConfig(tlsCfg))
		}
	}
	user := strings.TrimSpace(cfg.Params["registryuser"])
	pass, err := secret(cfg, "registrypassword")
	if err != nil {
		return nil, err
	}
	if user != "" || pass != "" {
		opts = append(opts, sr.BasicAuth(user, pass))
	}
	cl, err := sr.NewClient(opts...)
	if err != nil {
		return nil, &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "These settings could not become a registry client.", Err: err}
	}
	return cl, nil
}

// redactURL keeps a registry address out of a message with its credentials in
// it (NFR-S2). A URL may carry a password in its own userinfo, and a hint is
// written where somebody can read it.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = url.User(u.User.Username())
	return u.String()
}

// Subjects lists what the registry holds, each with its versions (FR-13.14).
//
// A subject is usually a topic's name with -key or -value after it, which is
// how a record's key and its value are described separately.
func (s *kafkaSource) Subjects(ctx context.Context) (_ []model.SchemaSubject, err error) {
	defer panics.Recover(&err, "listing the schemas")

	if s.registry == nil {
		return nil, errNoRegistry
	}
	names, err := s.registry.Subjects(ctx)
	if err != nil {
		return nil, registryError(err)
	}
	out := make([]model.SchemaSubject, 0, len(names))
	for _, name := range names {
		// Versions are not read here: a registry may hold thousands of
		// subjects, and reading every version of each to draw a list is the
		// shape ADR-0092 refused for topics.
		out = append(out, model.SchemaSubject{Name: name})
	}
	return out, nil
}

// SubjectVersions is every version of one subject, newest last, with the
// schema text each was registered with (FR-13.14).
func (s *kafkaSource) SubjectVersions(ctx context.Context, subject string) (_ []model.SchemaVersion, err error) {
	defer panics.Recover(&err, "reading a schema's versions")

	if s.registry == nil {
		return nil, errNoRegistry
	}
	versions, err := s.registry.SubjectVersions(ctx, subject)
	if err != nil {
		return nil, registryError(err)
	}
	out := make([]model.SchemaVersion, 0, len(versions))
	for _, v := range versions {
		ss, err := s.registry.SchemaByVersion(ctx, subject, v)
		if err != nil {
			return nil, registryError(err)
		}
		out = append(out, versionOf(ss))
	}
	return out, nil
}

// versionOf is one registered schema as the model holds it.
func versionOf(ss sr.SubjectSchema) model.SchemaVersion {
	return model.SchemaVersion{
		Version:    int32(ss.Version),
		ID:         int32(ss.ID),
		Format:     ss.Type.String(), // already AVRO, PROTOBUF or JSON
		Definition: ss.Schema.Schema,
	}
}

// subject is one subject as the structure view shows it: every version it has
// had, the schema each was registered with, and the rule the next one will be
// checked against (FR-13.14).
func (s *kafkaSource) subject(ctx context.Context, name string) (*model.SchemaSubject, error) {
	versions, err := s.SubjectVersions(ctx, name)
	if err != nil {
		return nil, err
	}
	return &model.SchemaSubject{
		Name:          name,
		Versions:      versions,
		Compatibility: s.compatibility(ctx, name),
	}, nil
}

// compatibility is the rule this subject's next version will be checked
// against, or nothing.
//
// DefaultToGlobal asks the registry to resolve the inheritance itself. Most
// subjects set no rule of their own, and what somebody wants to know is the
// rule that applies, not that this subject is silent about it. A registry too
// old for the parameter ignores it and says nothing, which reaches the same
// place by a longer road.
//
// A rule that cannot be read leaves the field empty rather than failing the
// describe: the versions are what a subject is, and the rule is a remark
// about it. Reached only through subject, which has already found a registry.
func (s *kafkaSource) compatibility(ctx context.Context, name string) string {
	res := s.registry.Compatibility(sr.WithParams(ctx, sr.DefaultToGlobal), name)
	if len(res) == 0 || res[0].Err != nil {
		return ""
	}
	return res[0].Level.String()
}

// Decoder returns a decoder for a subject's records.
//
// What it does today is read the five bytes a record begins with and say which
// schema wrote it. It does not yet read the payload: Avro, Protobuf and JSON
// Schema are their own languages and are written next, and a decoder that
// returned a guess would be worse than one that says what it cannot do.
func (s *kafkaSource) Decoder(ctx context.Context, subject string) (_ source.Decoder, err error) {
	defer panics.Recover(&err, "reading a schema")

	if s.registry == nil {
		return nil, errNoRegistry
	}
	ss, err := s.registry.SchemaByVersion(ctx, subject, -1) // -1 is the latest
	if err != nil {
		return nil, registryError(err)
	}
	d := &schemaDecoder{subject: subject, schema: versionOf(ss)}
	// Read once, when the decoder is asked for: a schema this build cannot
	// read is worth saying now rather than on every record, and compiling a
	// .proto is work a topic's records should not each pay for.
	switch d.schema.Format {
	case "AVRO":
		parsed, err := avroSchema(d.schema.Definition)
		if err != nil {
			return nil, err
		}
		d.parsed = parsed
	case "PROTOBUF":
		file, err := protoFile(ctx, d.schema.Definition)
		if err != nil {
			return nil, err
		}
		d.proto = file
	}
	return d, nil
}

// errNoRegistry is what every registry call says when no registry was named.
// It is not a failure of the connection: a cluster is perfectly usable without
// one, and most are read without one.
var errNoRegistry = errors.New("kafka: this connection names no schema registry")

// registryError says what went wrong with a registry in the words a person can
// act on: the registry is a second server, and reaching it can fail in ways
// the broker never does.
func registryError(err error) error {
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "401") || strings.Contains(text, "unauthorized"):
		return &source.ConnectError{Kind: source.ConnectAuth,
			Hint: "The registry did not accept these credentials.", Err: err}
	case strings.Contains(text, "x509") || strings.Contains(text, "certificate"):
		return &source.ConnectError{Kind: source.ConnectTLS,
			Hint: "The registry's certificate could not be verified.", Err: err}
	case strings.Contains(text, "no such host"):
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "That registry host name could not be found.", Err: err}
	case strings.Contains(text, "connection refused"):
		return &source.ConnectError{Kind: source.ConnectRefused,
			Hint: "Nothing is listening at that registry address.", Err: err}
	}
	return err
}

// schemaDecoder reads the header a record carries, and says plainly that the
// payload is somebody else's business until T2.70.
type schemaDecoder struct {
	subject string
	schema  model.SchemaVersion

	// parsed is the schema itself, where this build can read the language it
	// is written in. Nil means the language is one nobody has written a
	// reader for yet, and Decode says so rather than guessing.
	parsed avro.Schema

	// proto is the compiled file a Protobuf schema describes. Only one of
	// these two is ever set: a schema is written in one language.
	proto protoreflect.FileDescriptor
}

// Name identifies the decoder in the UI: the language, the subject and the
// version, so that a person can see which schema was used.
func (d *schemaDecoder) Name() string {
	return fmt.Sprintf("%s (%s v%d)", title(d.schema.Format), d.subject, d.schema.Version)
}

// Decode reads the five bytes that say which schema wrote a record. The
// payload is returned undecoded, with what is known about it said rather than
// guessed at.
func (d *schemaDecoder) Decode(data []byte) (any, error) {
	var header sr.ConfluentHeader
	id, rest, err := header.DecodeID(data)
	if err != nil {
		return nil, fmt.Errorf("kafka: this record carries no schema id: %w", err)
	}
	if int32(id) != d.schema.ID {
		return nil, fmt.Errorf("kafka: this record was written by schema %d, and %s is version %d of %s, which is schema %d",
			id, title(d.schema.Format), d.schema.Version, d.subject, d.schema.ID)
	}
	if d.parsed != nil {
		return decodeAvro(d.parsed, rest)
	}
	if d.schema.Format == "JSON" {
		// A JSON Schema record is a JSON document with five bytes in front of
		// it. Those bytes are what keeps it from reading as JSON on its own:
		// they are valid text, so without them stripped the document shows
		// with junk glued to its front and never as JSON at all.
		if !json.Valid(rest) {
			return nil, fmt.Errorf("kafka: this record is %s, and what follows its schema id is not a JSON document",
				title(d.schema.Format))
		}
		return model.JSON(rest), nil
	}
	if d.proto != nil {
		// Protobuf says which of a file's messages this record is, in an
		// index after the header. Avro carries no such thing, which is why
		// this is read here rather than beside the id.
		index, body, err := header.DecodeIndex(rest, 0)
		if err != nil {
			return nil, fmt.Errorf("kafka: this record does not say which message it is: %w", err)
		}
		desc, err := messageAt(d.proto, index)
		if err != nil {
			return nil, err
		}
		return decodeProto(desc, body)
	}
	return nil, fmt.Errorf("kafka: this record is %s, written by schema %d; reading %s is not written yet (%d bytes after the header)",
		title(d.schema.Format), id, title(d.schema.Format), len(rest))
}

// title writes a schema language the way somebody reads it rather than the way
// a registry spells it.
func title(format string) string {
	switch strings.ToUpper(format) {
	case "AVRO":
		return "Avro"
	case "PROTOBUF":
		return "Protobuf"
	case "JSON":
		return "JSON Schema"
	}
	if format == "" {
		return "A schema"
	}
	return format
}
