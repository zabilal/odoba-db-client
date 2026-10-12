//go:build conformance

package pulsar

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	pulsargo "github.com/apache/pulsar-client-go/pulsar"
	"github.com/apache/pulsar-client-go/pulsaradmin/pkg/utils"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/conformance"
)

// The live suite, against Pulsar standalone — one process that is broker,
// bookie and metadata store at once — with nobody asked who they are (T5.16):
//
//	docker run -d --name ikigai-pulsar -p 56650:6650 -p 58088:8080 --memory 1400m \
//	  -e PULSAR_MEM="-Xms256m -Xmx512m -XX:MaxDirectMemorySize=384m" \
//	  -e PULSAR_STANDALONE_USE_ZOOKEEPER=0 \
//	  apachepulsar/pulsar:4.0.6 bin/pulsar standalone --no-functions-worker --no-stream-storage
//
// IKIGAI_REQUIRE_PULSAR=1 turns a skip into a failure, which is what the gate
// sets. What it cannot prove is a cluster that checks a token or a certificate,
// or one of more than one broker: what the settings become is held by tests of
// their own (source_test.go), and that is said in ADR-0177.

func required() bool { return os.Getenv("IKIGAI_REQUIRE_PULSAR") != "" }

func envPort(name string, otherwise int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil {
		return v
	}
	return otherwise
}

func conf(g source.Guard) source.ConnectionConfig {
	return source.ConnectionConfig{DriverID: driverID, Host: "127.0.0.1", Port: envPort("IKIGAI_PULSAR_PORT", 56650),
		Params: map[string]string{"admin_port": strconv.Itoa(envPort("IKIGAI_PULSAR_ADMIN_PORT", 58088))},
		TLS:    source.TLSConfig{Mode: "disable"}, Guard: g}
}

func open(t *testing.T, g source.Guard) *pulsarSource {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	src, err := Driver{}.Open(ctx, conf(g))
	if err != nil {
		if required() {
			t.Fatalf("Pulsar required but unavailable: %v", err)
		}
		t.Skipf("no Pulsar: %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })
	return src.(*pulsarSource)
}

const namespace = "public/default"

func full(name string) string { return "persistent://" + namespace + "/" + name }

func topic(name string) model.ObjectRef {
	return model.NewRef(model.KindTopic, "standalone", namespace, full(name))
}

// seeded makes a topic afresh — in partitions pieces, or as one log where that
// is nought — with n messages in it. Each says which it is; its key alternates;
// every third has properties and every fifth has no key at all.
func seeded(t *testing.T, s *pulsarSource, name string, partitions, n int) {
	t.Helper()
	tn, err := utils.GetTopicName(full(name))
	if err != nil {
		t.Fatal(err)
	}
	remove := func() {
		// Whichever kind it was; one of the two is the wrong question.
		_ = s.admin.Topics().Delete(*tn, true, false)
		_ = s.admin.Topics().Delete(*tn, true, true)
	}
	remove()
	if err := s.admin.Topics().Create(*tn, partitions); err != nil {
		t.Fatalf("making %s: %v", name, err)
	}
	t.Cleanup(remove)
	if n == 0 {
		return
	}
	producer, err := s.client.CreateProducer(pulsargo.ProducerOptions{Topic: full(name), DisableBatching: true})
	if err != nil {
		t.Fatalf("writing to %s: %v", name, err)
	}
	defer producer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for i := 1; i <= n; i++ {
		msg := &pulsargo.ProducerMessage{Payload: []byte(fmt.Sprintf("order %d", i)), Key: []string{"eu", "us"}[i%2]}
		if i%5 == 0 {
			msg.Key = ""
		}
		if i%3 == 0 {
			msg.Properties = map[string]string{"trace": "t" + strconv.Itoa(i), "a": "1"}
		}
		if _, err := producer.Send(ctx, msg); err != nil {
			t.Fatalf("writing message %d: %v", i, err)
		}
	}
}

func TestConformance(t *testing.T) {
	s := open(t, source.Guard{})
	seeded(t, s, "ikigai_it_conformance", 0, 20)
	conformance.Run(t, conformance.Target{
		Name:      "pulsar",
		Open:      func(_ context.Context, t *testing.T) source.Source { return open(t, source.Guard{}) },
		Browsable: topic("ikigai_it_conformance"),
		// Nothing is written through a grid: this driver claims no Writer.
	})
}

// read is every row a browse answers.
func read(t *testing.T, s *pulsarSource, name string, opt source.BrowseOptions) []model.Row {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

func values(rows []model.Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		b, _ := r[4].([]byte)
		out[i] = string(b)
	}
	return out
}

func orders(from, to int) []string {
	var out []string
	for i := from; i <= to; i++ {
		out = append(out, fmt.Sprintf("order %d", i))
	}
	return out
}

const stream = "ikigai_it_orders"

var addressed = regexp.MustCompile(`^\d+:\d+$`)

// A message is read as what it is: where it was, when, under which key, what it
// carried and what was said about it.
func TestLiveAMessageIsWhatWasWritten(t *testing.T) {
	s := open(t, source.Guard{})
	before := time.Now().Add(-time.Minute)
	seeded(t, s, stream, 0, 6)
	rows := read(t, s, stream, source.BrowseOptions{})
	if !reflect.DeepEqual(values(rows), orders(1, 6)) {
		t.Fatalf("read %v", values(rows))
	}
	st, _ := s.Browse(context.Background(), topic(stream), source.BrowseOptions{})
	var names []string
	for _, c := range st.Columns() {
		names = append(names, c.Name)
		if !c.ReadOnly {
			t.Errorf("%s can be edited", c.Name)
		}
	}
	st.Close()
	if !reflect.DeepEqual(names, []string{"partition", "offset", "timestamp", "key", "value", "headers"}) {
		t.Fatalf("the columns are %v", names)
	}
	first, third, fifth := rows[0], rows[2], rows[4]
	where, _ := first[1].(string)
	if first[0] != int64(0) || !addressed.MatchString(where) || first[3] != "us" || first[5] != nil {
		t.Errorf("the first message is %#v", first)
	}
	when, ok := first[2].(time.Time)
	if !ok || when.Before(before) || when.After(time.Now().Add(time.Minute)) || when.Location() != time.UTC {
		t.Errorf("it was written at %v", first[2])
	}
	// Each message is somewhere of its own, and later ones are further on.
	seen := map[any]bool{}
	for _, r := range rows {
		if seen[r[1]] {
			t.Errorf("two messages are at %v", r[1])
		}
		seen[r[1]] = true
	}
	want := []any{map[string]any{"key": "a", "value": []byte("1")}, map[string]any{"key": "trace", "value": []byte("t3")}}
	if !reflect.DeepEqual(third[5], want) {
		t.Errorf("the third message's properties are %#v", third[5])
	}
	// No key is no key, which is not an empty one.
	if fifth[3] != nil {
		t.Errorf("a message written with no key has %#v", fifth[3])
	}
}

func TestLiveATopicIsReadFromAPosition(t *testing.T) {
	s := open(t, source.Guard{})
	seeded(t, s, stream, 0, 12)
	all := read(t, s, stream, source.BrowseOptions{})
	if len(all) != 12 {
		t.Fatalf("read %d messages", len(all))
	}
	fifth, _ := all[4][2].(time.Time)
	for name, c := range map[string]struct {
		opt  source.BrowseOptions
		want []string
	}{
		"a bound":                  {source.BrowseOptions{Limit: 4}, orders(1, 4)},
		"from the beginning":       {source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekBeginning}, Limit: 2}, orders(1, 2)},
		"from a time":              {source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekTimestamp, Time: fifth}}, orders(5, 12)},
		"from a time, bounded":     {source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekTimestamp, Time: fifth}, Limit: 3}, orders(5, 7)},
		"from a time after it all": {source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekTimestamp, Time: time.Now().Add(time.Hour)}}, nil},
	} {
		got := values(read(t, s, stream, c.opt))
		if len(got)+len(c.want) > 0 && !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: read %v, want %v", name, got, c.want)
		}
	}
	// Nobody saying how many is not everything (NFR-P11).
	seeded(t, s, "ikigai_it_many", 0, records+5)
	if n := len(read(t, s, "ikigai_it_many", source.BrowseOptions{})); n != records {
		t.Errorf("with no bound, %d messages", n)
	}
	// And a topic with nothing in it is read as nothing rather than waited on.
	seeded(t, s, "ikigai_it_empty", 0, 0)
	if n := len(read(t, s, "ikigai_it_empty", source.BrowseOptions{})); n != 0 {
		t.Errorf("an empty topic held %d messages", n)
	}
}

// A topic in pieces is read whole, each message saying which piece it was in.
func TestLiveATopicInPartitionsIsReadWhole(t *testing.T) {
	s := open(t, source.Guard{})
	seeded(t, s, "ikigai_it_parts", 3, 30)
	rows := read(t, s, "ikigai_it_parts", source.BrowseOptions{})
	got := map[string]bool{}
	pieces := map[int64]int{}
	for _, r := range rows {
		b, _ := r[4].([]byte)
		got[string(b)] = true
		p, _ := r[0].(int64)
		pieces[p]++
	}
	if len(rows) != 30 || len(got) != 30 {
		t.Fatalf("read %d messages, %d of them different", len(rows), len(got))
	}
	if len(pieces) < 2 {
		t.Errorf("thirty messages under three keys are all in %v", pieces)
	}
	for p := range pieces {
		if p < 0 || p > 2 {
			t.Errorf("a message says it was in partition %d of three", p)
		}
	}
}

func TestLiveATopicIsFollowed(t *testing.T) {
	s := open(t, source.Guard{})
	seeded(t, s, stream, 0, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := s.Browse(ctx, topic(stream), source.BrowseOptions{Follow: true})
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
	case <-time.After(500 * time.Millisecond):
	}
	if _, _, err := s.Produce(ctx, source.ProduceRequest{Topic: full(stream), Partition: -1, Value: []byte("fresh")}); err != nil {
		t.Fatal(err)
	}
	select {
	case row := <-got:
		if !reflect.DeepEqual(row[4], []byte("fresh")) {
			t.Errorf("the next message read as %v", row)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a message written while following never arrived")
	}
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
	case <-time.After(10 * time.Second):
		t.Fatal("a closed read is still waiting")
	}

	// A follow given up on says so, promptly.
	again, err := s.Browse(ctx, topic(stream), source.BrowseOptions{Follow: true, Seek: &source.Seek{Mode: source.SeekEnd}})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	stop, halt := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { _, err := again.Next(stop); done <- err }()
	time.Sleep(200 * time.Millisecond)
	halt()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("a stopped follow answered %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a follow did not stop when it was told to")
	}
}

func subscribed(t *testing.T, s *pulsarSource, name string) []string {
	t.Helper()
	tn, _ := utils.GetTopicName(full(name))
	subs, err := s.admin.Subscriptions().List(*tn)
	if err != nil {
		t.Fatal(err)
	}
	return subs
}

// Looking at a topic leaves nothing behind and moves nobody's place, and looking
// for one that is not there does not make it (FR-13.19).
func TestLiveReadingATopicPerturbsNobody(t *testing.T) {
	s := open(t, source.Guard{})
	seeded(t, s, stream, 0, 10)
	tn, _ := utils.GetTopicName(full(stream))
	if err := s.admin.Subscriptions().Create(*tn, "billing", utils.Earliest); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	describe := func() string {
		t.Helper()
		got, err := s.Describe(ctx, topic(stream))
		if err != nil {
			t.Fatal(err)
		}
		return got.(*model.Topic).Attrs["subscriptions"]
	}
	if got := describe(); got != "billing (10 behind)" {
		t.Fatalf("before reading, subscribed: %q", got)
	}
	st, err := s.Browse(ctx, topic(stream), source.BrowseOptions{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Next(ctx); err != nil {
		t.Fatal(err)
	}
	// While it is being read there are two subscriptions at the cluster, and
	// one is shown.
	if subs := subscribed(t, s, stream); len(subs) != 2 {
		t.Errorf("while reading, the cluster has %v", subs)
	}
	if got := describe(); got != "billing (10 behind)" {
		t.Errorf("while reading, subscribed: %q", got)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Errorf("closing it twice: %v", err)
	}
	read(t, s, stream, source.BrowseOptions{})
	for i := 0; i < 50 && len(subscribed(t, s, stream)) != 1; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if subs := subscribed(t, s, stream); !reflect.DeepEqual(subs, []string{"billing"}) {
		t.Errorf("after reading, the cluster has %v", subs)
	}
	if got := describe(); got != "billing (10 behind)" {
		t.Errorf("after reading, subscribed: %q", got)
	}

	// A topic that is not there is said not to be, and is still not there.
	gone := "ikigai_it_never_made"
	if st, err := s.Browse(ctx, topic(gone), source.BrowseOptions{}); err == nil || !strings.Contains(err.Error(), "no topic") {
		if st != nil {
			st.Close()
		}
		t.Errorf("reading a topic that is not there: %v", err)
	}
	if _, _, err := s.Produce(ctx, source.ProduceRequest{Topic: full(gone), Partition: -1, Value: []byte("x")}); err == nil ||
		!strings.Contains(err.Error(), "no topic") {
		t.Errorf("writing to a topic that is not there: %v", err)
	}
	if _, err := s.Describe(ctx, topic(gone)); err == nil {
		t.Error("a topic that is not there was described")
	}
	topics, err := s.topics(ctx, namespace)
	if err != nil {
		t.Fatal(err)
	}
	for _, tp := range topics {
		if tp.name == full(gone) {
			t.Error("asking after a topic made it")
		}
	}
}

func TestLiveWhatCannotBeReadIsRefused(t *testing.T) {
	s := open(t, source.Guard{})
	seeded(t, s, stream, 0, 3)
	ctx := context.Background()
	for name, c := range map[string]struct {
		ref  model.ObjectRef
		opt  source.BrowseOptions
		says string
	}{
		"a namespace":                  {model.NewRef(model.KindSchema, "standalone", namespace), source.BrowseOptions{}, "holds no messages"},
		"a condition":                  {topic(stream), source.BrowseOptions{Where: "x"}, "no condition"},
		"a filter":                     {topic(stream), source.BrowseOptions{Filters: []source.Filter{{Column: "key", Op: source.OpEqual, Values: []any{"x"}}}}, "does not filter"},
		"a sort":                       {topic(stream), source.BrowseOptions{Sorts: []source.Sort{{Column: "offset"}}}, "only order"},
		"skipping rows":                {topic(stream), source.BrowseOptions{Offset: 5}, "from a position"},
		"the end, not followed":        {topic(stream), source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekEnd}}, "unless the topic is being followed"},
		"an offset":                    {topic(stream), source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekOffset, Offset: 3}}, "no offset to read from"},
		"the last few":                 {topic(stream), source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekLast, Count: 3}}, "no counting back"},
		"one of its partitions":        {topic(stream), source.BrowseOptions{Seek: &source.Seek{Mode: source.SeekTimestamp, Partitions: []int32{0}}}, "a topic of its own"},
		"a way nobody knows":           {topic(stream), source.BrowseOptions{Seek: &source.Seek{Mode: 99}}, "no way to read"},
		"a name that is not a topic's": {model.NewRef(model.KindTopic, "standalone", namespace, "persistent://only-two/parts"), source.BrowseOptions{}, "is not a topic"},
	} {
		st, err := s.Browse(ctx, c.ref, c.opt)
		if err == nil {
			st.Close()
			t.Errorf("%s was read", name)
		} else if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v, want it to mention %q", name, err, c.says)
		}
	}
	if !s.CanFollow(topic(stream)) || s.CanFollow(model.NewRef(model.KindSchema, "standalone", namespace)) {
		t.Error("what can be followed is a topic, and only a topic")
	}
	// Refusals made no readers.
	if subs := subscribed(t, s, stream); len(subs) != 0 {
		t.Errorf("after the refusals the topic has %v", subs)
	}
}

func TestLiveTheTree(t *testing.T) {
	s := open(t, source.Guard{})
	seeded(t, s, stream, 0, 2)
	seeded(t, s, "ikigai_it_parts", 3, 0)
	ctx := context.Background()

	roots, err := s.Root(ctx)
	if err != nil || len(roots) != 1 || roots[0].Ref.Kind != model.KindCluster || roots[0].Label != "standalone" || !roots[0].HasChildren {
		t.Fatalf("Root: %+v, %v", roots, err)
	}
	spaces, err := s.Children(ctx, roots[0].Ref)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]model.Node{}
	var names []string
	for _, n := range spaces {
		byName[n.Label] = n
		names = append(names, n.Label)
	}
	for i := 1; i < len(names); i++ {
		if names[i] < names[i-1] {
			t.Errorf("the namespaces are not in order: %v", names)
		}
	}
	ns, ok := byName[namespace]
	if !ok || ns.Ref.String() != model.NewRef(model.KindSchema, "standalone", namespace).String() || !ns.HasChildren || len(ns.Attrs) != 0 {
		t.Fatalf("%s is %+v among %v", namespace, ns, names)
	}
	for name, n := range byName {
		if strings.HasPrefix(name, "pulsar/") != (n.Attrs["system"] == "true") {
			t.Errorf("%s is marked %v", name, n.Attrs)
		}
	}
	classes, err := s.Children(ctx, ns.Ref)
	if err != nil || len(classes) != 1 {
		t.Fatalf("the namespace holds %+v, %v", classes, err)
	}
	if k, _ := model.ClassOf(classes[0].Ref); k != model.KindTopic || !classes[0].HasChildren {
		t.Errorf("its class is %+v", classes[0])
	}
	topics, err := s.Children(ctx, classes[0].Ref)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := strconv.Atoi(classes[0].Badge.Text); n != len(topics) {
		t.Errorf("the class says %s and holds %d", classes[0].Badge.Text, len(topics))
	}
	tops := map[string]model.Node{}
	names = nil
	for _, n := range topics {
		tops[n.Label] = n
		names = append(names, n.Label)
		// A partition is a topic underneath, and is not shown beside the
		// topic it is a piece of.
		if strings.Contains(n.Label, "-partition-") {
			t.Errorf("%s is listed as a topic of its own", n.Label)
		}
	}
	for i := 1; i < len(names); i++ {
		if names[i] < names[i-1] {
			t.Errorf("the topics are not in order: %v", names)
		}
	}
	o := tops[stream]
	if o.Ref.String() != topic(stream).String() || !o.Browsable || !o.Describable || o.HasChildren || o.Badge != nil {
		t.Errorf("%s is %+v", stream, o)
	}
	if p := tops["ikigai_it_parts"]; p.Badge == nil || p.Badge.Text != "3" || p.Attrs["partitions"] != "3" || !p.Browsable {
		t.Errorf("the topic in three pieces is %+v (badge %+v)", p, p.Badge)
	}
	// Nothing under a topic, nothing under a class the tree has not got, and a
	// namespace named by half its name is refused.
	for _, leaf := range []model.ObjectRef{o.Ref, model.ClassRef(ns.Ref, model.KindConsumerGroup)} {
		if kids, err := s.Children(ctx, leaf); err != nil || len(kids) != 0 {
			t.Errorf("under %s: %v, %v", leaf, kids, err)
		}
	}
	for _, bad := range []model.ObjectRef{model.NewRef(model.KindSchema, "standalone"), model.NewRef(model.KindSchema, "standalone", "public"),
		model.NewRef(model.KindSchema, "standalone", namespace, "more")} {
		if kids, err := s.Children(ctx, bad); err == nil {
			t.Errorf("under %s: %v", bad, kids)
		}
	}
	if _, ok, err := s.Badge(ctx, o.Ref); ok || err != nil {
		t.Errorf("a topic was counted: %v, %v", ok, err)
	}

	// A namespace with nothing in it still has somewhere to open.
	nsName, _ := utils.GetNamespaceName("public/ikigai_it_empty_ns")
	_ = s.admin.Namespaces().DeleteNamespace("public/ikigai_it_empty_ns")
	if err := s.admin.Namespaces().CreateNamespace("public/ikigai_it_empty_ns"); err != nil {
		t.Fatalf("making a namespace: %v (%v)", err, nsName)
	}
	t.Cleanup(func() { _ = s.admin.Namespaces().DeleteNamespace("public/ikigai_it_empty_ns") })
	empty, err := s.Children(ctx, model.NewRef(model.KindSchema, "standalone", "public/ikigai_it_empty_ns"))
	if err != nil || len(empty) != 1 || empty[0].HasChildren || empty[0].Badge.Text != "0" {
		t.Errorf("an empty namespace: %+v, %v", empty, err)
	}
}

func TestLiveATopicIsDescribed(t *testing.T) {
	s := open(t, source.Guard{})
	seeded(t, s, stream, 0, 5)
	ctx := context.Background()
	got, err := s.Describe(ctx, topic(stream))
	if err != nil {
		t.Fatal(err)
	}
	tp, ok := got.(*model.Topic)
	if !ok || tp.Name != full(stream) || len(tp.Partitions) != 1 || tp.ReplicationFactor != -1 {
		t.Fatalf("described as %#v", got)
	}
	if p := tp.Partitions[0]; p.ID != 0 || p.HighWatermark != 5 || p.LowWatermark != 0 || p.Leader != -1 || tp.MessageCount() != 5 {
		t.Errorf("its log is %+v", p)
	}
	if !strings.HasSuffix(tp.Attrs["storage"], " bytes") || tp.Attrs["storage"] == "0 bytes" {
		t.Errorf("it takes %q", tp.Attrs["storage"])
	}
	if _, said := tp.Attrs["subscriptions"]; said {
		t.Errorf("with nobody subscribed, subscribed: %q", tp.Attrs["subscriptions"])
	}
	tn, _ := utils.GetTopicName(full(stream))
	for _, name := range []string{"shipping", "billing"} {
		if err := s.admin.Subscriptions().Create(*tn, name, utils.Latest); err != nil {
			t.Fatal(err)
		}
	}
	got, _ = s.Describe(ctx, topic(stream))
	if said := got.(*model.Topic).Attrs["subscriptions"]; said != "billing (0 behind), shipping (0 behind)" {
		t.Errorf("subscribed: %q", said)
	}

	seeded(t, s, "ikigai_it_parts", 3, 9)
	got, err = s.Describe(ctx, topic("ikigai_it_parts"))
	if err != nil {
		t.Fatal(err)
	}
	parts := got.(*model.Topic)
	if len(parts.Partitions) != 3 || parts.MessageCount() != 9 || parts.Attrs["storage"] == "" {
		t.Fatalf("the topic in three pieces is %+v", parts)
	}
	for i, p := range parts.Partitions {
		if p.ID != int32(i) {
			t.Errorf("its pieces are numbered %d at %d", p.ID, i)
		}
	}
	if _, err := s.Describe(ctx, model.NewRef(model.KindSchema, "standalone", namespace)); err == nil {
		t.Error("a namespace was described as a topic")
	}
	// Whatever it is called: what is described is a topic, not anything that
	// happens to end in a topic's name.
	if got, err := s.Describe(ctx, model.NewRef(model.KindSchema, "standalone", namespace, full(stream))); err == nil {
		t.Errorf("a namespace named like a topic was described: %#v", got)
	}
	if _, err := s.Describe(ctx, model.NewRef(model.KindTopic, "standalone", namespace, "persistent://x")); err == nil {
		t.Error("half a name was described as a topic")
	}
}

func TestLiveAMessageIsWritten(t *testing.T) {
	s := open(t, source.Guard{})
	seeded(t, s, stream, 0, 2)
	ctx := context.Background()
	at, entry, err := s.Produce(ctx, source.ProduceRequest{Topic: full(stream), Partition: -1, Key: []byte("eu"),
		Value: []byte("order 9"), Headers: []model.RecordHeader{{Key: "trace", Value: []byte("t9")}, {Key: "a", Value: []byte("1")}}})
	if err != nil || at != (model.TopicPartition{Topic: full(stream), Partition: 0}) || entry < 0 {
		t.Fatalf("written at %+v, %d, %v", at, entry, err)
	}
	rows := read(t, s, stream, source.BrowseOptions{})
	last := rows[len(rows)-1]
	want := []any{map[string]any{"key": "a", "value": []byte("1")}, map[string]any{"key": "trace", "value": []byte("t9")}}
	if len(rows) != 3 || last[3] != "eu" || !reflect.DeepEqual(last[4], []byte("order 9")) || !reflect.DeepEqual(last[5], want) {
		t.Errorf("it reads back as %#v", last)
	}
	// And where it says it went is where it is.
	if where, _ := last[1].(string); !strings.HasSuffix(where, ":"+strconv.FormatInt(entry, 10)) {
		t.Errorf("written at entry %d and read at %q", entry, where)
	}
	// No value is no value — what a compacted topic reads as a key removed —
	// and an empty one is empty: the two are written as what they are and read
	// back as that.
	if _, _, err := s.Produce(ctx, source.ProduceRequest{Topic: full(stream), Partition: -1}); err != nil {
		t.Errorf("a message with no value: %v", err)
	}
	if _, _, err := s.Produce(ctx, source.ProduceRequest{Topic: full(stream), Partition: -1, Value: []byte{}}); err != nil {
		t.Errorf("a message with an empty value: %v", err)
	}
	rows = read(t, s, stream, source.BrowseOptions{})
	if len(rows) != 5 {
		t.Fatalf("the topic holds %d messages", len(rows))
	}
	if none := rows[3]; none[3] != nil || none[4] != nil || none[5] != nil {
		t.Errorf("no value reads back as %#v", none)
	}
	if empty := rows[4]; !reflect.DeepEqual(empty[4], []byte{}) {
		t.Errorf("an empty value reads back as %#v", empty[4])
	}
	// Nothing is left publishing.
	tn, _ := utils.GetTopicName(full(stream))
	if stats, err := s.admin.Topics().GetStats(*tn); err != nil || len(stats.Publishers) != 0 {
		t.Errorf("after writing, the topic has %d publishers, %v", len(stats.Publishers), err)
	}

	for name, c := range map[string]struct {
		rec  source.ProduceRequest
		says string
	}{
		"no topic":                  {source.ProduceRequest{Partition: -1, Value: []byte("x")}, "needs a topic"},
		"a partition":               {source.ProduceRequest{Topic: full(stream), Partition: 0, Value: []byte("x")}, "not sent to partition 0"},
		"a schema to check against": {source.ProduceRequest{Topic: full(stream), Partition: -1, Subject: "orders"}, "schema"},
		"a property given twice": {source.ProduceRequest{Topic: full(stream), Partition: -1,
			Headers: []model.RecordHeader{{Key: "a", Value: []byte("1")}, {Key: "a", Value: []byte("2")}}}, `"a" is given twice`},
		"a name that is not a topic's": {source.ProduceRequest{Topic: "persistent://x", Partition: -1}, "is not a topic"},
	} {
		if at, entry, err := s.Produce(ctx, c.rec); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %+v, %d, %v; want it to mention %q", name, at, entry, err, c.says)
		}
	}
	if n := len(read(t, s, stream, source.BrowseOptions{})); n != 5 {
		t.Errorf("after the refusals the topic holds %d messages", n)
	}
}

// Writing is guarded like any other write (FR-13.21).
func TestLiveWritingIsGuarded(t *testing.T) {
	plain := open(t, source.Guard{})
	seeded(t, plain, stream, 0, 0)
	ctx := context.Background()
	rec := source.ProduceRequest{Topic: full(stream), Partition: -1, Value: []byte("x")}
	if _, _, err := open(t, source.Guard{ReadOnly: true}).Produce(ctx, rec); !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("on a read-only connection: %v", err)
	}
	prod := open(t, source.Guard{Environment: source.EnvProduction})
	if _, _, err := prod.Produce(ctx, rec); !errors.Is(err, source.ErrConfirmationRequired) {
		t.Errorf("on a production connection, unasked: %v", err)
	}
	if n := len(read(t, plain, stream, source.BrowseOptions{})); n != 0 {
		t.Errorf("the refused writes left %d messages", n)
	}
	rec.Confirmed = true
	if _, _, err := prod.Produce(ctx, rec); err != nil {
		t.Errorf("on a production connection, with consent: %v", err)
	}
	if n := len(read(t, plain, stream, source.BrowseOptions{})); n != 1 {
		t.Errorf("after one allowed write the topic holds %d messages", n)
	}
}

func TestLiveInfoSaysWhatItIs(t *testing.T) {
	s := open(t, source.Guard{})
	ctx := context.Background()
	info, err := s.Info(ctx)
	if err != nil || info.Product != "Pulsar" || info.Latency <= 0 || info.Attrs["cluster"] != "standalone" || info.Attrs["brokers"] != "1" {
		t.Fatalf("%+v, %v", info, err)
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
	// A closed connection reads and writes nothing, and says that is why.
	if _, err := s.Browse(ctx, topic(stream), source.BrowseOptions{}); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("reading on a closed connection: %v", err)
	}
	if _, _, err := s.Produce(ctx, source.ProduceRequest{Topic: full(stream), Partition: -1}); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("writing on a closed connection: %v", err)
	}
}

func TestLiveAWrongAddressSaysSo(t *testing.T) {
	open(t, source.Guard{}) // or skip
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	began := time.Now()
	cfg := conf(source.Guard{})
	cfg.Params = map[string]string{"admin_port": "59"}
	if _, err := (Driver{}).Open(ctx, cfg); kindOf(err) != source.ConnectUnreachable {
		t.Errorf("a wrong admin port: %v", err)
	}
	cfg = conf(source.Guard{})
	cfg.Host = "no-such-host.invalid"
	if _, err := (Driver{}).Open(ctx, cfg); kindOf(err) != source.ConnectUnreachable {
		t.Errorf("no such host: %v", err)
	}
	cfg = conf(source.Guard{})
	cfg.TLS.Mode = "require"
	if _, err := (Driver{}).Open(ctx, cfg); kindOf(err) != source.ConnectTLS {
		t.Errorf("encrypting to a cluster that does not: %v", err)
	}
	if took := time.Since(began); took > 15*time.Second {
		t.Errorf("three refusals took %v", took)
	}
	// The admin service answering does not mean a broker does: a wrong broker
	// port is found when something is read, and is said then, in time.
	cfg = conf(source.Guard{})
	cfg.Port = 59
	src, err := Driver{}.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("with a wrong broker port, opening: %v", err)
	}
	defer src.Close()
	seeded(t, open(t, source.Guard{}), stream, 0, 1)
	began = time.Now()
	if st, err := src.(*pulsarSource).Browse(ctx, topic(stream), source.BrowseOptions{}); err == nil {
		st.Close()
		t.Error("with a wrong broker port, a topic was read")
	} else if took := time.Since(began); took > 25*time.Second {
		t.Errorf("saying so took %v: %v", took, err)
	}
}
