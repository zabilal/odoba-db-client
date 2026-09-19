package shell

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sync/atomic"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
)

// A stream source whose records are described by a registry (T2.70). It is
// the record fake with a schema: what changes is that a decoder is offered
// for the topic's values, and nothing else.

type schemaFake struct{}

func init() { source.Register(schemaFake{}) }

func (schemaFake) Describe() source.Descriptor {
	return source.Descriptor{ID: "schemafake", Name: "Schema Fake", Paradigm: model.ParadigmStream,
		Fields: []source.Field{{Key: "host", Label: "Host", Kind: source.FieldText, Required: true}}}
}

func (schemaFake) Open(context.Context, source.ConnectionConfig) (source.Source, error) {
	return &schemaSource{}, nil
}

// schemaSource is the record source, and says what its records mean.
type schemaSource struct{ recordSource }

// schemaSilent makes the registry hold nothing for this topic, which is what
// most topics look like: a registry that answers, about subjects that are not
// there.
var schemaSilent bool

// schemaUnclaimed makes the source implement SchemaRegistry without claiming
// it. A driver in that state has written the methods and not promised them,
// and nothing should ask it for schemas: a claim is what says the promise is
// kept (REQ-DRV-1).
var schemaUnclaimed bool

// asked counts the times a decoder was asked for, so that a test can say
// whether a source was consulted at all.
var asked atomic.Int64

func (*schemaSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Paradigm: model.ParadigmStream,
		Stream: capability.Stream{Consume: true, SeekTimestamp: true, Follow: true,
			SchemaRegistry: !schemaUnclaimed},
		Objects: map[model.ObjectKind]bool{model.KindCluster: true, model.KindTopic: true},
	}
}

func (*schemaSource) Subjects(context.Context) ([]model.SchemaSubject, error) {
	return []model.SchemaSubject{{Name: "events-value"}}, nil
}

func (*schemaSource) SubjectVersions(_ context.Context, subject string) ([]model.SchemaVersion, error) {
	if subject != "events-value" {
		return nil, errors.New("schemafake: no such subject")
	}
	return []model.SchemaVersion{{Version: 1, ID: 1, Format: "AVRO", Definition: `"string"`}}, nil
}

// Decoder describes the values, and nothing else: a key with no subject is
// the ordinary case, and it must stay readable as what its bytes are.
func (*schemaSource) Decoder(_ context.Context, subject string) (source.Decoder, error) {
	asked.Add(1)
	if schemaSilent || subject != "events-value" {
		return nil, errors.New("schemafake: no such subject")
	}
	return orderDecoder{}, nil
}

// orderDecoder reads the fake's values as though a schema said what they are.
type orderDecoder struct{}

func (orderDecoder) Name() string { return "Avro (events-value v1)" }

func (orderDecoder) Decode(b []byte) (any, error) {
	if !strings.HasPrefix(string(b), "{") {
		return nil, errors.New("schemafake: this record does not match its schema")
	}
	return map[string]any{"total": 12.5, "raw": []byte{0xff, 0x00}}, nil
}

// openDescribedRecords opens the fake topic's records on a connection whose
// records a registry describes.
func openDescribedRecords(t *testing.T) (*fixture, *tab) {
	t.Helper()
	fx := newFixture(t)
	c, err := fx.conns.Create(store.SavedConnection{Name: "kafka2", Driver: "schemafake", Host: "kafka2"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fx.s.OpenObject(c.ID, model.Node{Ref: topicRef, Label: "events", Browsable: true})
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return tb.browse != nil })
	return fx, tb
}

func TestASchemasReadingIsOfferedFirst(t *testing.T) {
	fx, tb := openDescribedRecords(t)
	r := theRecord(t, fx, tb, 0)

	// The registry knows more about these bytes than anything worked out from
	// the bytes alone, so its reading comes first (ADR-0102).
	pump(t, fx.q, func() bool {
		return len(r.value.pick.Options) > 0 && r.value.pick.Options[0] == "Avro (events-value v1)"
	})
	if got := r.value.pick.Options[0]; got != "Avro (events-value v1)" {
		t.Errorf("the value is offered first as %q", got)
	}
	// And what the bytes say for themselves is still offered behind it.
	if got := strings.Join(r.value.pick.Options, ","); !strings.Contains(got, "Hex") {
		t.Errorf("the value is offered as %q", got)
	}
}

func TestAKeyWithNoSubjectIsStillReadable(t *testing.T) {
	fx, tb := openDescribedRecords(t)
	r := theRecord(t, fx, tb, 0)

	// Most topics describe their values and not their keys. A key with no
	// subject reads as what its bytes are, which is what it did before any
	// of this.
	if got := r.key.pick.Options; len(got) == 0 || got[0] != "Text" {
		t.Errorf("a key with no schema is offered as %v", got)
	}
}

func TestATopicTheRegistryKnowsNothingAboutReadsAsItsBytes(t *testing.T) {
	// A registry that answers, about a topic it holds no schema for. That is
	// what most topics look like, and it must leave the record exactly as
	// readable as it was without any registry at all (ADR-0102).
	schemaSilent = true
	t.Cleanup(func() { schemaSilent = false })

	fx, tb := openDescribedRecords(t)
	r := theRecord(t, fx, tb, 0)
	if got := strings.Join(r.value.pick.Options, ","); got != "JSON,Text,Hex" {
		t.Errorf("a value with no schema is offered as %q, which is what its bytes say and nothing more", got)
	}
	for _, name := range r.value.pick.Options {
		if strings.HasPrefix(name, "Avro") {
			t.Errorf("a topic with no schema was given one: %v", r.value.pick.Options)
		}
	}
}

func TestASourceThatHasNotPromisedSchemasIsNotAskedForThem(t *testing.T) {
	// The methods are written and the promise is not made. A claim is what
	// says a promise is kept (REQ-DRV-1), so nothing should go asking.
	schemaUnclaimed = true
	t.Cleanup(func() { schemaUnclaimed = false })
	asked.Store(0)

	fx, tb := openDescribedRecords(t)
	r := theRecord(t, fx, tb, 0)
	// Waiting for the forms says nothing: the local ones are there at once.
	// A claimed source is asked while a record is drawn, so this waits the
	// same way and then asks whether anybody went looking.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && asked.Load() == 0 {
		fx.q.Flush()
		time.Sleep(time.Millisecond)
	}
	if n := asked.Load(); n != 0 {
		t.Errorf("a source that claims no registry was asked for decoders %d times", n)
	}
	if got := strings.Join(r.value.pick.Options, ","); got != "JSON,Text,Hex" {
		t.Errorf("its value is offered as %q", got)
	}
}

func TestASchemasReadingIsPutBackWhenTheRegistryAnswers(t *testing.T) {
	fx, tb := openDescribedRecords(t)
	// Somebody read this topic's values by its schema before.
	if err := fx.hist.PutDecoder(context.Background(), tb.connID, "value", tb.ref.Name(),
		localdb.DecoderChoice{Name: "Avro (events-value v1)"}); err != nil {
		t.Fatal(err)
	}

	r := theRecord(t, fx, tb, 0)
	// The decoder is resolved after the bytes arrive, so the form somebody
	// chose does not exist when the view first draws. It must still be put
	// back once it does.
	pump(t, fx.q, func() bool { return r.value.pick.Selected == "Avro (events-value v1)" })
	if got := r.value.pick.Selected; got != "Avro (events-value v1)" {
		t.Errorf("a remembered schema reading came back as %q", got)
	}
}

func TestAChoiceMadeHereSurvivesTheRegistryAnswering(t *testing.T) {
	fx, tb := openDescribedRecords(t)
	r := theRecord(t, fx, tb, 0)

	// Chosen in this view, before the registry has answered.
	r.value.pick.SetSelected("Hex")
	pump(t, fx.q, func() bool {
		for _, n := range r.value.pick.Options {
			if strings.HasPrefix(n, "Avro") {
				return true
			}
		}
		return false
	})
	// What somebody picked here is not overruled by what arrives afterwards.
	if got := r.value.pick.Selected; got != "Hex" {
		t.Errorf("a choice made here became %q when the registry answered", got)
	}
}

func TestARememberedLocalReadingIsStillPutBack(t *testing.T) {
	fx, tb := openDescribedRecords(t)
	if err := fx.hist.PutDecoder(context.Background(), tb.connID, "value", tb.ref.Name(),
		localdb.DecoderChoice{Name: "Hex"}); err != nil {
		t.Fatal(err)
	}
	r := theRecord(t, fx, tb, 0)
	// The local forms are there from the first draw, so this was already
	// working; it keeps working.
	if got := r.value.pick.Selected; got != "Hex" {
		t.Errorf("a remembered local reading came back as %q", got)
	}
}
