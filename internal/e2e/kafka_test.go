//go:build conformance

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/hamba/avro/v2"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/store"

	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/kafka"
)

// J8 runs against the schema registry's own broker rather than the plain
// one, because decoding a record needs both and they are a pair (ADR-0101).
// Neither has an in-process stand-in the way SQLite does, so unlike every
// other journey this one exists only under the conformance tag.

const j8Schema = `{"type":"record","name":"Order","fields":[
	{"name":"id","type":"string"},{"name":"total","type":"double"}]}`

// j8Records is how many records the topic holds. Small: this journey is
// about what the window does with them, not about throughput.
const j8Records = 6

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

// kafkaJourney fills a topic with records written by a schema the registry
// knows, and leaves a consumer group behind the end of it so that there is
// lag to read.
func kafkaJourney(t *testing.T) (journey, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cfg := source.ConnectionConfig{
		DriverID: "kafka", Host: "127.0.0.1", Port: registryBroker(),
		TLS:    source.TLSConfig{Mode: "disable"},
		Params: map[string]string{"registry": registryURL()},
	}
	drv, err := source.Lookup("kafka")
	if err != nil {
		t.Fatal(err)
	}
	src, err := drv.Open(ctx, cfg)
	if err != nil {
		if os.Getenv("IKIGAI_REQUIRE_SCHEMA_REGISTRY") != "" {
			t.Fatalf("a schema registry is required but unavailable: %v", err)
		}
		t.Skipf("no broker on %d with a registry on %d (make kafka-up): %v",
			registryBroker(), registryPort(), err)
	}
	defer src.Close()

	topic := fmt.Sprintf("ikigai_j8_%d", time.Now().UnixNano())
	group := topic + "_readers"
	admin, ok := src.(source.TopicAdmin)
	if !ok {
		t.Fatal("this driver cannot make a topic")
	}
	if err := admin.CreateTopic(ctx, source.TopicSpec{
		Name: topic, Partitions: 1, ReplicationFactor: 1, Confirmed: true}); err != nil {
		t.Fatalf("making %s: %v", topic, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin.DeleteTopic(ctx, topic, true)
	})

	// The schema the records are written by, and its id, which goes on the
	// wire in front of every one of them.
	id := registerSchema(t, topic+"-value", j8Schema)
	schema, err := avro.Parse(j8Schema)
	if err != nil {
		t.Fatal(err)
	}
	producer, ok := src.(source.StreamProducer)
	if !ok {
		t.Fatal("this driver cannot write a record")
	}
	for i := range j8Records {
		body, err := avro.Marshal(schema, map[string]any{
			"id": fmt.Sprintf("order-%d", i), "total": float64(i) + 0.5})
		if err != nil {
			t.Fatal(err)
		}
		value := append([]byte{0, byte(id >> 24), byte(id >> 16), byte(id >> 8), byte(id)}, body...)
		if _, _, err := producer.Produce(ctx, source.ProduceRequest{
			Topic: topic, Partition: -1, Key: []byte(fmt.Sprintf("k%d", i)),
			Value: value, Confirmed: true}); err != nil {
			t.Fatalf("writing record %d: %v", i, err)
		}
	}

	// A group left at the beginning, so it is every record behind. It is
	// made by committing an offset for it rather than through the driver,
	// which will not move a group that does not exist yet — this is the
	// fixture standing in for whatever had been reading the topic.
	seedGroup(t, ctx, group, topic)

	// The cluster calls itself something; the sidebar path starts there.
	roots, err := src.Root(ctx)
	if err != nil || len(roots) == 0 {
		t.Fatalf("the cluster has no name: %v", err)
	}
	cluster := roots[0].Ref

	return journey{
		conn: store.SavedConnection{
			Name: "stream", Driver: "kafka", Host: "127.0.0.1", Port: registryBroker(),
			Params: map[string]string{"registry": registryURL()},
			TLS:    store.TLS{Mode: "disable"},
		},
		path: []model.ObjectRef{
			cluster,
			model.ClassRef(cluster, model.KindTopic),
			model.NewRef(model.KindTopic, cluster.Name(), topic),
		},
		table: topic,
	}, group
}

// registerSchema puts a schema in the registry and answers its id.
func registerSchema(t *testing.T, subject, schema string) int {
	t.Helper()
	body := strings.NewReader(`{"schemaType":"AVRO","schema":` + strconv.Quote(schema) + `}`)
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
	var out struct {
		ID int `json:"id"`
	}
	if err := jsonDecode(res.Body, &out); err != nil {
		t.Fatalf("reading the id of %s: %v", subject, err)
	}
	return out.ID
}

// runJ8 is journey J8: debug a stream. It walks the part of the journey the
// window can drive — a topic opened, its records decoded against the schema
// registry, and a consumer group's lag read — and stops where the window
// does. Seeking to a timestamp and tailing live messages have no way in from
// the window: source.Seek and app.NewTail exist and are tested, and nothing
// reaches them. TASKS.md records that, and this journey is [~] until it does.
func runJ8(t *testing.T, j journey, group string) {
	h := start(t, j)

	// A topic opens onto its records.
	walkTo(t, h, j.path)
	if err := h.s.Commands().Run("object.open"); err != nil {
		t.Fatalf("Open Data: %v", err)
	}
	if h.tabs.Selected() == nil || h.tabs.Selected().Text != j.table {
		t.Fatalf("no tab for %q opened", j.table)
	}
	waitFor(t, h.q, "the topic's records", func() bool {
		h.w.Canvas().Capture()
		g := h.s.ActiveGrid()
		if g == nil {
			return false
		}
		n, _ := g.Model().Extent()
		return n == j8Records
	})
	g := h.s.ActiveGrid()

	// Decoded against the registry, without being asked: the subject is the
	// topic's name and the record says which schema wrote it.
	g.Select(gridCell(0, 0), gridCell(0, 0))
	if err := h.s.Commands().Run("grid.record"); err != nil {
		t.Fatalf("Show Record: %v", err)
	}
	// The registry's schema is offered as a way of reading the value, named
	// after the subject and version it came from. It is offered rather than
	// imposed: the bytes are shown as they are until somebody says what they
	// mean. Choosing it is the journey's step.
	view := h.tabs.Selected().Content
	waitFor(t, h.q, "the schema to be offered", func() bool {
		return schemaForm(view) != ""
	})
	form := schemaForm(view)
	if !strings.HasPrefix(form, "Avro (") || !strings.Contains(form, j.table+"-value") {
		t.Errorf("the schema is offered as %q", form)
	}
	for _, sel := range find[*widget.Select](view) {
		if slices.Contains(sel.Options, form) {
			sel.SetSelected(form)
		}
	}
	waitFor(t, h.q, "the record to be decoded", func() bool {
		said := strings.Join(labelsIn(h.tabs.Selected().Content), " ")
		return strings.Contains(said, "order-0") && strings.Contains(said, "0.5")
	})

	// And the window exports as it reads it: the file holds what the
	// schema says the records are, not the bytes they arrived as
	// (FR-10.8, FR-13.16). The grid is what is exported, so the record
	// view goes away first, the way somebody closes it.
	test.Tap(button(t, view, "Show Grid"))
	if err := h.s.Commands().Run("data.export"); err != nil {
		t.Fatalf("Export: %v", err)
	}
	sheet := h.w.Canvas().Overlays().Top()
	if sheet == nil {
		t.Fatal("the export form did not open")
	}
	for _, sel := range find[*widget.Select](sheet) {
		if slices.Contains(sel.Options, "NDJSON") {
			sel.SetSelected("NDJSON")
		}
	}
	test.Tap(button(t, sheet, "Choose File…"))
	path := filepath.Join(t.TempDir(), "records.ndjson")
	h.files.picks(t, h.q, path)
	waitFor(t, h.q, "the exported records", func() bool {
		b, err := os.ReadFile(path)
		return err == nil && strings.Contains(string(b), `"order-0"`) &&
			!strings.Contains(string(b), `"value":"`) // not base64, and not hex
	})

	// And a consumer group says how far behind it is.
	groupPath := []model.ObjectRef{
		j.path[0],
		model.ClassRef(j.path[0], model.KindConsumerGroup),
		model.NewRef(model.KindConsumerGroup, j.path[0].Name(), group),
	}
	walkTo(t, h, groupPath)
	if err := h.s.Commands().Run("object.structure"); err != nil {
		t.Fatalf("Open Structure: %v", err)
	}
	// How far a group has got is read when somebody asks for it and not
	// before, because it costs the cluster two requests (ADR-0107). So the
	// journey asks.
	structure := h.tabs.Selected().Content
	waitFor(t, h.q, "the group's structure", func() bool {
		return strings.Contains(strings.Join(labelsIn(structure), " "), "read when you ask for it")
	})
	test.Tap(button(t, structure, "Read how far it has got"))

	// It is every record behind, and the view says so in records rather than
	// in offsets: a number somebody can act on.
	behind := fmt.Sprintf("%d records behind", j8Records)
	waitFor(t, h.q, "the group's lag", func() bool {
		h.w.Canvas().Capture()
		return strings.Contains(strings.Join(labelsIn(h.tabs.Selected().Content), " "), behind)
	})
}

func TestJ8Kafka(t *testing.T) {
	j, group := kafkaJourney(t)
	runJ8(t, j, group)
}

// seedGroup leaves a consumer group at the start of a topic, which is what
// a reader that has fallen every record behind looks like.
func seedGroup(t *testing.T, ctx context.Context, group, topic string) {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers("127.0.0.1:" + strconv.Itoa(registryBroker())))
	if err != nil {
		t.Fatalf("reaching the broker: %v", err)
	}
	defer cl.Close()
	offsets := kadm.Offsets{}
	offsets.AddOffset(topic, 0, 0, -1)
	if _, err := kadm.NewClient(cl).CommitOffsets(ctx, group, offsets); err != nil {
		t.Fatalf("leaving %s behind: %v", group, err)
	}
}

// schemaForm is the way of reading a value that came from the registry, or
// "" until one is offered. It is named for the subject and version that
// produced it, which is how one schema is told from another.
func schemaForm(o fyne.CanvasObject) string {
	for _, sel := range find[*widget.Select](o) {
		for _, name := range sel.Options {
			if strings.HasPrefix(name, "Avro (") {
				return name
			}
		}
	}
	return ""
}
