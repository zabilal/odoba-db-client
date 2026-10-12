//go:build conformance

package nats

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/conformance"
)

// The live suite, against one NATS server with JetStream on and nobody asked
// who they are (T5.16):
//
//	docker run -d --name ikigai-nats -p 54222:4222 -p 58222:8222 --memory 256m \
//	  nats:2.12 -js -m 8222
//
// IKIGAI_REQUIRE_NATS=1 turns a skip into a failure, which is what the gate
// sets. What it cannot prove is a server that checks a password, a token or a
// certificate: what the settings become is held by tests of their own
// (source_test.go), and that is said in ADR-0176.

func required() bool { return os.Getenv("IKIGAI_REQUIRE_NATS") != "" }

func port() int {
	if v, err := strconv.Atoi(os.Getenv("IKIGAI_NATS_PORT")); err == nil {
		return v
	}
	return 54222
}

func settings(g source.Guard) source.ConnectionConfig {
	return source.ConnectionConfig{DriverID: driverID, Host: "127.0.0.1", Port: port(),
		TLS: source.TLSConfig{Mode: "disable"}, Guard: g}
}

func open(t *testing.T, g source.Guard) *natsSource {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	src, err := Driver{}.Open(ctx, settings(g))
	if err != nil {
		if required() {
			t.Fatalf("NATS required but unavailable: %v", err)
		}
		t.Skipf("no NATS on port %d: %v", port(), err)
	}
	t.Cleanup(func() { _ = src.Close() })
	return src.(*natsSource)
}

func topic(name string) model.ObjectRef { return model.NewRef(model.KindTopic, "server", name) }

const orders = "ikigai_it_orders"

// seeded makes a stream afresh, listening on subjects, with n messages in it.
// Each message says which it is, on a subject that alternates, and every third
// carries headers.
func seeded(t *testing.T, s *natsSource, name string, n int, subjects ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.js.DeleteStream(ctx, name); err != nil && !errors.Is(err, jetstream.ErrStreamNotFound) {
		t.Fatalf("removing %s: %v", name, err)
	}
	if len(subjects) == 0 {
		subjects = []string{name + ".>"}
	}
	if _, err := s.js.CreateStream(ctx, jetstream.StreamConfig{Name: name, Subjects: subjects,
		Storage: jetstream.MemoryStorage, Description: "made by the tests"}); err != nil {
		t.Fatalf("making %s: %v", name, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.js.DeleteStream(ctx, name)
	})
	for i := 1; i <= n; i++ {
		msg := &natsgo.Msg{Subject: fmt.Sprintf("%s.%s", name, []string{"new", "paid"}[i%2]),
			Data: []byte(fmt.Sprintf("order %d", i))}
		if i%3 == 0 {
			msg.Header = natsgo.Header{"trace": {"t" + strconv.Itoa(i)}, "a": {"1", "2"}}
		}
		if _, err := s.js.PublishMsg(ctx, msg); err != nil {
			t.Fatalf("writing message %d: %v", i, err)
		}
	}
}

func TestConformance(t *testing.T) {
	s := open(t, source.Guard{})
	seeded(t, s, "ikigai_it_conformance", 20)
	conformance.Run(t, conformance.Target{
		Name:      "nats",
		Open:      func(_ context.Context, t *testing.T) source.Source { return open(t, source.Guard{}) },
		Browsable: topic("ikigai_it_conformance"),
		// Nothing is written through a grid: this driver claims no Writer.
		// One message at a time is written by Produce, which has its own
		// tests.
	})
}

// read is every row a browse answers.
func read(t *testing.T, s *natsSource, name string, opt source.BrowseOptions) []model.Row {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := s.Browse(ctx, topic(name), opt)
	if err != nil {
		t.Fatalf("Browse(%+v): %v", opt, err)
	}
	defer st.Close()
	var out []model.Row
	for {
		row, err := st.Next(ctx)
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("Next after %d rows: %v", len(out), err)
		}
		out = append(out, row)
	}
}

func seqs(rows []model.Row) []int64 {
	out := make([]int64, len(rows))
	for i, r := range rows {
		out[i], _ = r[1].(int64)
	}
	return out
}

func span(from, to int64) []int64 {
	var out []int64
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

// A message is read as what it is: where it was, when, on which subject, what
// it carried and what was said about it.
func TestLiveAMessageIsWhatWasWritten(t *testing.T) {
	s := open(t, source.Guard{})
	before := time.Now().Add(-time.Minute)
	seeded(t, s, orders, 6)
	rows := read(t, s, orders, source.BrowseOptions{})
	if len(rows) != 6 {
		t.Fatalf("read %d messages", len(rows))
	}
	st, _ := s.Browse(context.Background(), topic(orders), source.BrowseOptions{})
	var names []string
	for _, c := range st.Columns() {
		names = append(names, c.Name)
		if !c.ReadOnly {
			t.Errorf("%s can be edited", c.Name)
		}
	}
	st.Close()
	// The columns a Kafka record has, so the same window reads both.
	if !reflect.DeepEqual(names, []string{"partition", "offset", "timestamp", "key", "value", "headers"}) {
		t.Fatalf("the columns are %v", names)
	}
	first, third := rows[0], rows[2]
	if first[0] != int64(0) || first[1] != int64(1) || first[3] != orders+".paid" ||
		!reflect.DeepEqual(first[4], []byte("order 1")) || first[5] != nil {
		t.Errorf("the first message is %#v", first)
	}
	when, ok := first[2].(time.Time)
	if !ok || when.Before(before) || when.After(time.Now().Add(time.Minute)) || when.Location() != time.UTC {
		t.Errorf("it was written at %v", first[2])
	}
	// Headers in name order, each value of a name on its own line: a header
	// may be said more than once.
	want := []any{
		map[string]any{"key": "a", "value": []byte("1")},
		map[string]any{"key": "a", "value": []byte("2")},
		map[string]any{"key": "trace", "value": []byte("t3")},
	}
	if third[3] != orders+".paid" || !reflect.DeepEqual(third[5], want) {
		t.Errorf("the third message is on %v with headers %#v", third[3], third[5])
	}
	if rows[1][3] != orders+".new" {
		t.Errorf("the second message is on %v", rows[1][3])
	}
}

// A stream is read from a position, to where it ended when the read began.
func TestLiveAStreamIsReadFromAPosition(t *testing.T) {
	s := open(t, source.Guard{})
	seeded(t, s, orders, 30)
	// The time between the tenth message and the eleventh, for reading by time.
	ctx := context.Background()
	stream, _ := s.js.Stream(ctx, orders)
	tenth, err := stream.GetMsg(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		opt  source.BrowseOptions
		want []int64
	}{
		"all of it":                {source.BrowseOptions{}, span(1, 30)},
		"a bound":                  {source.BrowseOptions{Limit: 7}, span(1, 7)},
		"from the beginning":       {source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekBeginning}, Limit: 3}, span(1, 3)},
		"from a sequence":          {source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekOffset, Offset: 25}}, span(25, 30)},
		"from before the first":    {source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekOffset, Offset: -4}, Limit: 2}, span(1, 2)},
		"from past the end":        {source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekOffset, Offset: 31}}, nil},
		"the last five":            {source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekLast, Count: 5}}, span(26, 30)},
		"the last, more than is":   {source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekLast, Count: 500}}, span(1, 30)},
		"the last none":            {source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekLast, Count: 0}}, nil},
		"from a time":              {source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekTimestamp, Time: tenth.Time.Add(time.Nanosecond)}}, span(11, 30)},
		"from a time, bounded":     {source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekTimestamp, Time: tenth.Time}, Limit: 2}, span(10, 11)},
		"from a time after it all": {source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekTimestamp, Time: time.Now().Add(time.Hour)}}, nil},
		"its one partition":        {source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekBeginning, Partitions: []int32{0}}, Limit: 2}, span(1, 2)},
		"a partition it has not":   {source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekBeginning, Partitions: []int32{3}}}, nil},
	} {
		got := seqs(read(t, s, orders, c.opt))
		if len(got)+len(c.want) > 0 && !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: read %v, want %v", name, got, c.want)
		}
	}

	// What is written after a read began is not part of it.
	st, err := s.Browse(ctx, topic(orders), source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekOffset, Offset: 29}})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := s.js.Publish(ctx, orders+".new", []byte("late")); err != nil {
		t.Fatal(err)
	}
	n := 0
	for {
		if _, err := st.Next(ctx); err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatal(err)
			}
			break
		}
		n++
	}
	if n != 2 {
		t.Errorf("a read begun at thirty messages read %d from the twenty-ninth", n)
	}
}

// An empty stream, and one whose messages have gone, are read as nothing rather
// than waited on.
func TestLiveNothingIsReadAsNothing(t *testing.T) {
	s := open(t, source.Guard{})
	seeded(t, s, orders, 0)
	if rows := read(t, s, orders, source.BrowseOptions{}); len(rows) != 0 {
		t.Errorf("an empty stream held %d messages", len(rows))
	}
	seeded(t, s, orders, 5)
	ctx := context.Background()
	stream, _ := s.js.Stream(ctx, orders)
	if err := stream.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	if rows := read(t, s, orders, source.BrowseOptions{}); len(rows) != 0 {
		t.Errorf("a purged stream held %d messages", len(rows))
	}
	// And one with a hole in it is read round the hole, to its end.
	seeded(t, s, orders, 6)
	stream, _ = s.js.Stream(ctx, orders)
	for _, seq := range []uint64{3, 6} {
		if err := stream.DeleteMsg(ctx, seq); err != nil {
			t.Fatal(err)
		}
	}
	if got := seqs(read(t, s, orders, source.BrowseOptions{})); !reflect.DeepEqual(got, []int64{1, 2, 4, 5}) {
		t.Errorf("a stream with two messages removed read %v", got)
	}
}

// Following reads what comes next, and stops when it is told to.
func TestLiveAStreamIsFollowed(t *testing.T) {
	s := open(t, source.Guard{})
	seeded(t, s, orders, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// With nowhere named, only what is written from now on.
	st, err := s.Browse(ctx, topic(orders), source.BrowseOptions{Follow: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got := make(chan model.Row, 4)
	ended := make(chan error, 1)
	go func() {
		for {
			row, err := st.Next(ctx)
			if err != nil {
				ended <- err
				close(got)
				return
			}
			got <- row
		}
	}()
	select {
	case row := <-got:
		t.Fatalf("following from now read what was already there: %v", row)
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := s.js.Publish(ctx, orders+".new", []byte("fresh")); err != nil {
		t.Fatal(err)
	}
	select {
	case row := <-got:
		if row[1] != int64(5) || !reflect.DeepEqual(row[4], []byte("fresh")) {
			t.Errorf("the next message read as %v", row)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a message written while following never arrived")
	}
	// Closing it ends the read rather than breaking it.
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case _, open := <-got:
		if open {
			t.Error("a closed read handed over another message")
		}
		if err := <-ended; !errors.Is(err, io.EOF) {
			t.Errorf("a read closed while it waited ended with %v; it is over, not broken", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a closed read is still waiting")
	}

	// From somewhere named, what is there and then what comes.
	back, err := s.Browse(ctx, topic(orders), source.BrowseOptions{Follow: true,
		Seek: &source.Seek{Mode: source.SeekLast, Count: 2}})
	if err != nil {
		t.Fatal(err)
	}
	defer back.Close()
	for _, want := range []int64{4, 5} {
		row, err := back.Next(ctx)
		if err != nil || row[1] != want {
			t.Fatalf("following from the last two read %v, %v; want %d", row, err, want)
		}
	}
	// And a follow given up on says so, promptly.
	stop, halt := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { _, err := back.Next(stop); done <- err }()
	time.Sleep(100 * time.Millisecond)
	halt()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("a stopped follow answered %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a follow did not stop when it was told to")
	}
}

// Looking at a stream leaves nothing behind and moves nobody's place: the
// reader it is read through is this application's own, is not listed, and is
// gone when the read ends (FR-13.19).
func TestLiveReadingAStreamPerturbsNobody(t *testing.T) {
	s := open(t, source.Guard{})
	seeded(t, s, orders, 10)
	ctx := context.Background()
	stream, _ := s.js.Stream(ctx, orders)
	if _, err := stream.CreateConsumer(ctx, jetstream.ConsumerConfig{Durable: "billing", AckPolicy: jetstream.AckExplicitPolicy}); err != nil {
		t.Fatal(err)
	}
	before, err := s.GroupOffsets(ctx, orders+"/billing")
	if err != nil {
		t.Fatal(err)
	}

	st, err := s.Browse(ctx, topic(orders), source.BrowseOptions{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Next(ctx); err != nil {
		t.Fatal(err)
	}
	// The reader is one the server will remove by itself if this cannot.
	lister := stream.ListConsumers(ctx)
	for c := range lister.Info() {
		if _, mine := c.Config.Metadata[ours]; mine && (c.Config.InactiveThreshold != idle || c.Config.Durable != "" ||
			c.Config.AckPolicy != jetstream.AckNonePolicy) {
			t.Errorf("the reader is %+v", c.Config)
		}
	}
	// While it is being read there are two consumers at the server, and one
	// is shown.
	if info, _ := stream.Info(ctx); info.State.Consumers != 2 {
		t.Errorf("while reading, the stream has %d consumers", info.State.Consumers)
	}
	groups, err := s.ConsumerGroups(ctx)
	if err != nil || len(groups) != 1 || groups[0].ID != orders+"/billing" {
		t.Errorf("while reading, the consumers shown are %+v, %v", groups, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Errorf("closing it twice: %v", err)
	}
	if info, _ := stream.Info(ctx); info.State.Consumers != 1 {
		t.Errorf("after reading, the stream has %d consumers", info.State.Consumers)
	}
	// A read that found nothing made nothing.
	read(t, s, orders, source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekOffset, Offset: 99}})
	read(t, s, orders, source.BrowseOptions{})
	// Nor did one that had to ask the server before it knew there was nothing.
	read(t, s, orders, source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekTimestamp, Time: time.Now().Add(time.Hour)}})
	if info, _ := stream.Info(ctx); info.State.Consumers != 1 {
		t.Errorf("after three reads, the stream has %d consumers", info.State.Consumers)
	}
	after, err := s.GroupOffsets(ctx, orders+"/billing")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Errorf("billing was at %+v and is at %+v, %v", before, after, err)
	}
}

func TestLiveWhatCannotBeReadIsRefused(t *testing.T) {
	s := open(t, source.Guard{})
	seeded(t, s, orders, 3)
	ctx := context.Background()
	for name, c := range map[string]struct {
		ref  model.ObjectRef
		opt  source.BrowseOptions
		says string
	}{
		"a consumer":                 {model.NewRef(model.KindConsumerGroup, "server", orders+"/x"), source.BrowseOptions{}, "holds no messages"},
		"a condition":                {topic(orders), source.BrowseOptions{Where: "x"}, "no condition"},
		"a filter":                   {topic(orders), source.BrowseOptions{Filters: []source.Filter{{Column: "key", Op: source.OpEqual, Values: []any{"x"}}}}, "does not filter"},
		"a sort":                     {topic(orders), source.BrowseOptions{Sorts: []source.Sort{{Column: "offset"}}}, "only order"},
		"skipping rows":              {topic(orders), source.BrowseOptions{Offset: 5}, "from a position"},
		"the end, not followed":      {topic(orders), source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekEnd}}, "unless the stream is being followed"},
		"a way nobody knows":         {topic(orders), source.BrowseOptions{Seek: &source.Seek{Mode: 99}}, "no way to read"},
		"a stream that is not there": {topic("ikigai_it_nosuch"), source.BrowseOptions{}, `no stream "ikigai_it_nosuch"`},
	} {
		st, err := s.Browse(ctx, c.ref, c.opt)
		if err == nil {
			st.Close()
			t.Errorf("%s was read", name)
		} else if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v, want it to mention %q", name, err, c.says)
		}
	}
	if !s.CanFollow(topic(orders)) || s.CanFollow(model.NewRef(model.KindConsumerGroup, "server", "a/b")) {
		t.Error("what can be followed is a stream, and only a stream")
	}
}

func TestLiveTheTree(t *testing.T) {
	s := open(t, source.Guard{})
	seeded(t, s, orders, 4)
	seeded(t, s, "ikigai_it_audit", 0)
	ctx := context.Background()
	stream, _ := s.js.Stream(ctx, orders)
	for _, name := range []string{"shipping", "billing"} {
		if _, err := stream.CreateConsumer(ctx, jetstream.ConsumerConfig{Durable: name}); err != nil {
			t.Fatal(err)
		}
	}

	roots, err := s.Root(ctx)
	if err != nil || len(roots) != 1 || roots[0].Ref.Kind != model.KindCluster || !roots[0].HasChildren || roots[0].Label == "" {
		t.Fatalf("Root: %+v, %v", roots, err)
	}
	classes, err := s.Children(ctx, roots[0].Ref)
	if err != nil || len(classes) != 2 {
		t.Fatalf("the server holds %+v, %v", classes, err)
	}
	if k, _ := model.ClassOf(classes[0].Ref); k != model.KindTopic {
		t.Errorf("the first class is of %v", k)
	}
	if k, _ := model.ClassOf(classes[1].Ref); k != model.KindConsumerGroup || classes[1].Badge.Text != "2" {
		t.Errorf("the second class is of %v, counted %s", k, classes[1].Badge.Text)
	}
	streams, err := s.Children(ctx, classes[0].Ref)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := strconv.Atoi(classes[0].Badge.Text); n != len(streams) {
		t.Errorf("the class says %s and holds %d", classes[0].Badge.Text, len(streams))
	}
	byName := map[string]model.Node{}
	var names []string
	for _, n := range streams {
		byName[n.Label] = n
		names = append(names, n.Label)
	}
	for i := 1; i < len(names); i++ {
		if names[i] < names[i-1] {
			t.Errorf("the streams are not in order: %v", names)
		}
	}
	o := byName[orders]
	if o.Ref.String() != model.NewRef(model.KindTopic, roots[0].Label, orders).String() || !o.Browsable ||
		!o.Describable || o.HasChildren || o.Badge == nil || o.Badge.Text != "4" || !o.Badge.Exact {
		t.Errorf("%s is %+v (badge %+v)", orders, o, o.Badge)
	}
	if a := byName["ikigai_it_audit"]; a.Badge == nil || a.Badge.Text != "0" {
		t.Errorf("an empty stream is badged %+v", a.Badge)
	}
	consumers, err := s.Children(ctx, classes[1].Ref)
	if err != nil || len(consumers) != 2 {
		t.Fatalf("the consumers are %+v, %v", consumers, err)
	}
	if c := consumers[0]; c.Label != orders+"/billing" || c.Ref.Kind != model.KindConsumerGroup || !c.Describable ||
		c.Attrs["stream"] != orders || c.Attrs["state"] != "durable, idle" {
		t.Errorf("the first consumer is %+v", c)
	}
	if consumers[1].Label != orders+"/shipping" {
		t.Errorf("the second consumer is %s", consumers[1].Label)
	}
	// Nothing under a stream or a consumer, and nothing under a class the
	// tree does not have.
	for _, leaf := range []model.ObjectRef{o.Ref, consumers[0].Ref, model.ClassRef(roots[0].Ref, model.KindTable)} {
		if kids, err := s.Children(ctx, leaf); err != nil || len(kids) != 0 {
			t.Errorf("under %s: %v, %v", leaf, kids, err)
		}
	}
	b, ok, err := s.Badge(ctx, o.Ref)
	if err != nil || !ok || b.Text != "4" || !b.Exact {
		t.Errorf("counted: %+v, %v, %v", b, ok, err)
	}
	if _, ok, err := s.Badge(ctx, consumers[0].Ref); ok || err != nil {
		t.Errorf("a consumer was counted: %v, %v", ok, err)
	}
	if _, _, err := s.Badge(ctx, topic("ikigai_it_nosuch")); err == nil {
		t.Error("a stream that is not there was counted")
	}

	// With no consumers there is no class of them.
	for _, name := range []string{"shipping", "billing"} {
		if err := stream.DeleteConsumer(ctx, name); err != nil {
			t.Fatal(err)
		}
	}
	if classes, err := s.Children(ctx, roots[0].Ref); err != nil || len(classes) != 1 {
		t.Errorf("with nobody reading, the server holds %+v, %v", classes, err)
	}
}

// A key-value bucket is a stream underneath, and is listed as the stream it is
// kept in: this does not read buckets as keys and values, and does not pretend
// they are not there.
func TestLiveABucketIsListedAsItsStream(t *testing.T) {
	s := open(t, source.Guard{})
	ctx := context.Background()
	_ = s.js.DeleteKeyValue(ctx, "ikigai_it_bucket")
	kv, err := s.js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: "ikigai_it_bucket", Storage: jetstream.MemoryStorage})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.js.DeleteKeyValue(context.Background(), "ikigai_it_bucket") })
	if _, err := kv.Put(ctx, "colour", []byte("green")); err != nil {
		t.Fatal(err)
	}
	rows := read(t, s, "KV_ikigai_it_bucket", source.BrowseOptions{})
	if len(rows) != 1 || rows[0][3] != "$KV.ikigai_it_bucket.colour" || !reflect.DeepEqual(rows[0][4], []byte("green")) {
		t.Errorf("the bucket's stream reads %#v", rows)
	}
}

func TestLiveAStreamIsDescribed(t *testing.T) {
	s := open(t, source.Guard{})
	seeded(t, s, orders, 5)
	ctx := context.Background()
	stream, _ := s.js.Stream(ctx, orders)
	if err := stream.DeleteMsg(ctx, 1); err != nil {
		t.Fatal(err)
	}
	got, err := s.Describe(ctx, topic(orders))
	if err != nil {
		t.Fatal(err)
	}
	tp, ok := got.(*model.Topic)
	if !ok || tp.Name != orders || len(tp.Partitions) != 1 || tp.ReplicationFactor != 1 {
		t.Fatalf("described as %#v", got)
	}
	// One log, from the first message it still has to where the next will go.
	if p := tp.Partitions[0]; p.ID != 0 || p.LowWatermark != 2 || p.HighWatermark != 6 || tp.MessageCount() != 4 {
		t.Errorf("its log is %+v, counted %d", p, tp.MessageCount())
	}
	if tp.Attrs["messages"] != "4" || tp.Attrs["description"] != "made by the tests" {
		t.Errorf("about it: %v", tp.Attrs)
	}
	conf := map[string]model.ConfigEntry{}
	for _, c := range tp.Config {
		conf[c.Name] = c
		if !c.ReadOnly || c.Source != "stream" {
			t.Errorf("%s is %+v", c.Name, c)
		}
	}
	for name, want := range map[string]string{
		"subjects": orders + ".>", "retention": "Limits", "storage": "Memory", "discard": "DiscardOld",
		"max messages": "unlimited", "max bytes": "unlimited", "max age": "unlimited", "replicas": "1",
	} {
		if conf[name].Value != want {
			t.Errorf("%s is %q, want %q", name, conf[name].Value, want)
		}
	}
	// An empty stream begins where it ends.
	seeded(t, s, "ikigai_it_audit", 0)
	empty, err := s.Describe(ctx, topic("ikigai_it_audit"))
	if e, _ := empty.(*model.Topic); err != nil || e == nil || e.MessageCount() != 0 || e.Partitions[0].LowWatermark != 1 || e.Partitions[0].HighWatermark != 1 {
		t.Errorf("an empty stream: %#v, %v", empty, err)
	}
	if _, err := s.Describe(ctx, topic("ikigai_it_nosuch")); err == nil || !strings.Contains(err.Error(), "no stream") {
		t.Errorf("a stream that is not there: %v", err)
	}
	if _, err := s.Describe(ctx, model.NewRef(model.KindCluster, "server")); err == nil {
		t.Error("the server was described as a stream")
	}
}

// A consumer's place is where everything before it is dealt with, and how far
// behind it is, is everything it has not yet acknowledged.
func TestLiveAConsumersProgress(t *testing.T) {
	s := open(t, source.Guard{})
	seeded(t, s, orders, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stream, _ := s.js.Stream(ctx, orders)
	cons, err := stream.CreateConsumer(ctx, jetstream.ConsumerConfig{Durable: "billing", AckPolicy: jetstream.AckExplicitPolicy})
	if err != nil {
		t.Fatal(err)
	}
	id := orders + "/billing"
	progress := func() model.GroupOffset {
		t.Helper()
		got, err := s.GroupOffsets(ctx, id)
		if err != nil || len(got) != 1 {
			t.Fatalf("progress: %+v, %v", got, err)
		}
		return got[0]
	}
	// Never read through: no place yet, and everything still to do.
	if p := progress(); p != (model.GroupOffset{Topic: orders, Partition: 0, Current: -1, End: 11, Lag: 10}) {
		t.Errorf("before anything was read: %+v", p)
	}
	// Four sent, the first three acknowledged.
	batch, err := cons.Fetch(4)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for msg := range batch.Messages() {
		if n++; n <= 3 {
			if err := msg.DoubleAck(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	if p := progress(); p != (model.GroupOffset{Topic: orders, Partition: 0, Current: 4, End: 11, Lag: 7}) {
		t.Errorf("after four sent and three acknowledged: %+v", p)
	}

	groups, err := s.ConsumerGroups(ctx)
	if err != nil || len(groups) != 1 || groups[0].ID != id || groups[0].State != "durable, idle" || groups[0].Offsets != nil {
		t.Errorf("the consumers are %+v, %v", groups, err)
	}
	got, err := s.Describe(ctx, model.NewRef(model.KindConsumerGroup, "server", id))
	g, ok := got.(*model.ConsumerGroup)
	if err != nil || !ok || g.ID != id || g.State != "durable, idle" || len(g.Offsets) != 1 || g.TotalLag() != 7 {
		t.Errorf("described as %#v, %v", got, err)
	}

	// A consumer of some of a stream's subjects is behind by what is its to
	// read, not by the distance to the end.
	if _, err := stream.CreateConsumer(ctx, jetstream.ConsumerConfig{Durable: "paid", FilterSubject: orders + ".paid"}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GroupOffsets(ctx, orders+"/paid"); err != nil || got[0].Lag != 5 || got[0].End != 11 {
		t.Errorf("a consumer of half the subjects: %+v, %v", got, err)
	}

	for bad, says := range map[string]string{
		orders + "/nobody":       `no consumer "nobody"`,
		"ikigai_it_nosuch/x":     `no stream "ikigai_it_nosuch"`,
		"billing":                "not a consumer",
		orders + "/":             "not a consumer",
		"/billing":               "not a consumer",
		orders + "/billing/more": "not a consumer",
	} {
		if got, err := s.GroupOffsets(ctx, bad); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("progress of %q: %+v, %v; want it to mention %q", bad, got, err, says)
		}
	}
}

// A message is written on the subject given, into the stream named and no
// other.
func TestLiveAMessageIsWritten(t *testing.T) {
	s := open(t, source.Guard{})
	seeded(t, s, orders, 2)
	seeded(t, s, "ikigai_it_audit", 0, "ikigai_it_audit")
	ctx := context.Background()

	at, seq, err := s.Produce(ctx, source.ProduceRequest{Topic: orders, Partition: -1, Key: []byte(orders + ".refunded"),
		Value: []byte("order 9"), Headers: []model.RecordHeader{{Key: "trace", Value: []byte("t9")}, {Key: "trace", Value: []byte("again")}}})
	if err != nil || at != (model.TopicPartition{Topic: orders, Partition: 0}) || seq != 3 {
		t.Fatalf("written at %+v, %d, %v", at, seq, err)
	}
	rows := read(t, s, orders, source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekLast, Count: 1}})
	want := []any{map[string]any{"key": "trace", "value": []byte("t9")}, map[string]any{"key": "trace", "value": []byte("again")}}
	if len(rows) != 1 || rows[0][3] != orders+".refunded" || !reflect.DeepEqual(rows[0][4], []byte("order 9")) || !reflect.DeepEqual(rows[0][5], want) {
		t.Errorf("it reads back as %#v", rows)
	}
	// Nothing in it is nothing, and is still a message.
	if _, seq, err := s.Produce(ctx, source.ProduceRequest{Topic: orders, Key: []byte(orders + ".new")}); err != nil || seq != 4 {
		t.Errorf("an empty message: %d, %v", seq, err)
	}
	if rows := read(t, s, orders, source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekLast, Count: 1}}); len(rows) != 1 ||
		!reflect.DeepEqual(rows[0][4], []byte{}) || rows[0][5] != nil {
		t.Errorf("it reads back as %#v", rows)
	}
	// A stream with one subject needs none given.
	if at, seq, err := s.Produce(ctx, source.ProduceRequest{Topic: "ikigai_it_audit", Value: []byte("x")}); err != nil || at.Topic != "ikigai_it_audit" || seq != 1 {
		t.Errorf("with the stream's own subject: %+v, %d, %v", at, seq, err)
	}

	for name, c := range map[string]struct {
		rec  source.ProduceRequest
		says string
	}{
		"no stream":                     {source.ProduceRequest{Key: []byte("x")}, "needs a stream"},
		"a partition it has not":        {source.ProduceRequest{Topic: orders, Partition: 2, Key: []byte(orders + ".new")}, "no partition 2"},
		"a schema to check against":     {source.ProduceRequest{Topic: orders, Key: []byte(orders + ".new"), Subject: "orders-value"}, "no schema registry"},
		"no subject, where it has many": {source.ProduceRequest{Topic: orders}, "has to be given as the key"},
		"a pattern":                     {source.ProduceRequest{Topic: orders, Key: []byte(orders + ".*")}, "a pattern"},
		"another stream's subject":      {source.ProduceRequest{Topic: orders, Key: []byte("ikigai_it_audit")}, "is not a subject stream " + orders + " keeps"},
		"a subject nothing keeps":       {source.ProduceRequest{Topic: orders, Key: []byte("ikigai_it_nowhere")}, "so nothing was written"},
		"a stream that is not there":    {source.ProduceRequest{Topic: "ikigai_it_nosuch", Key: []byte("x")}, "no stream"},
	} {
		if at, seq, err := s.Produce(ctx, c.rec); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %+v, %d, %v; want it to mention %q", name, at, seq, err, c.says)
		}
	}
	// And none of those wrote anything anywhere.
	if n := len(read(t, s, orders, source.BrowseOptions{})); n != 4 {
		t.Errorf("after the refusals the stream holds %d messages", n)
	}
	if n := len(read(t, s, "ikigai_it_audit", source.BrowseOptions{})); n != 1 {
		t.Errorf("after the refusals the other stream holds %d messages", n)
	}
}

// Writing is guarded like any other write (FR-13.21).
func TestLiveWritingIsGuarded(t *testing.T) {
	plain := open(t, source.Guard{})
	seeded(t, plain, orders, 0)
	ctx := context.Background()
	rec := source.ProduceRequest{Topic: orders, Key: []byte(orders + ".new"), Value: []byte("x")}

	if _, _, err := open(t, source.Guard{ReadOnly: true}).Produce(ctx, rec); !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("on a read-only connection: %v", err)
	}
	prod := open(t, source.Guard{Environment: source.EnvProduction})
	if _, _, err := prod.Produce(ctx, rec); !errors.Is(err, source.ErrConfirmationRequired) {
		t.Errorf("on a production connection, unasked: %v", err)
	}
	if n := len(read(t, plain, orders, source.BrowseOptions{})); n != 0 {
		t.Errorf("the refused writes left %d messages", n)
	}
	rec.Confirmed = true
	if _, seq, err := prod.Produce(ctx, rec); err != nil || seq != 1 {
		t.Errorf("on a production connection, with consent: %d, %v", seq, err)
	}
}

// How much a stream has carried: every message ever written, kept or not, and
// the room what is kept takes.
func TestLiveAStreamIsMeasured(t *testing.T) {
	s := open(t, source.Guard{})
	seeded(t, s, orders, 8)
	ctx := context.Background()
	first, err := s.TopicTotals(ctx, orders)
	if err != nil || first.Records != 8 || first.Bytes <= 0 || !first.Sized || time.Since(first.At) > time.Minute {
		t.Fatalf("measured %+v, %v", first, err)
	}
	stream, _ := s.js.Stream(ctx, orders)
	if err := stream.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	// Removing them does not unwrite them.
	if after, err := s.TopicTotals(ctx, orders); err != nil || after.Records != 8 || after.Bytes != 0 {
		t.Errorf("after a purge: %+v, %v", after, err)
	}
	if _, err := s.TopicTotals(ctx, "ikigai_it_nosuch"); err == nil || !strings.Contains(err.Error(), "no stream") {
		t.Errorf("a stream that is not there: %v", err)
	}
}

func TestLiveInfoSaysWhatItIs(t *testing.T) {
	s := open(t, source.Guard{})
	seeded(t, s, orders, 1)
	ctx := context.Background()
	info, err := s.Info(ctx)
	if err != nil || info.Product != "NATS" || !strings.HasPrefix(info.Version, "2.") || info.Latency <= 0 {
		t.Fatalf("%+v, %v", info, err)
	}
	if n, err := strconv.Atoi(info.Attrs["streams"]); err != nil || n < 1 || info.Attrs["consumers"] == "" || info.Attrs["server"] == "" {
		t.Errorf("about itself: %v", info.Attrs)
	}
	if err := s.Ping(ctx); err != nil {
		t.Errorf("Ping: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("closing it twice: %v", err)
	}
	if err := s.Ping(ctx); err == nil {
		t.Error("a closed connection answered a ping")
	}
	if _, err := s.Info(ctx); err == nil {
		t.Error("a closed connection said what it is")
	}
}

func TestLiveAWrongAddressSaysSo(t *testing.T) {
	open(t, source.Guard{}) // or skip
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	began := time.Now()
	cfg := settings(source.Guard{})
	cfg.Port = 59
	if _, err := (Driver{}).Open(ctx, cfg); kindOf(err) != source.ConnectUnreachable {
		t.Errorf("a wrong port: %v", err)
	}
	cfg = settings(source.Guard{})
	cfg.Host = "no-such-host.invalid"
	if _, err := (Driver{}).Open(ctx, cfg); kindOf(err) != source.ConnectUnreachable {
		t.Errorf("no such host: %v", err)
	}
	// Told to encrypt, against a server that does not: about encryption.
	cfg = settings(source.Guard{})
	cfg.TLS.Mode = "require"
	if _, err := (Driver{}).Open(ctx, cfg); kindOf(err) != source.ConnectTLS {
		t.Errorf("encrypting to a server that does not: %v", err)
	}
	if took := time.Since(began); took > 15*time.Second {
		t.Errorf("three refusals took %v", took)
	}
}
