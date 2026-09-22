package kafka

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// The cluster in the tree (T2.59).

func TestTheBrokersAreTheClustersOwnNodes(t *testing.T) {
	rack := "rack-a"
	seedRack := "ignored"
	got := brokersOf(kadm.BrokerDetails{
		{NodeID: 2, Host: "second", Port: 9092},
		{NodeID: -2147483647, Host: "seed", Port: 9092, Rack: &seedRack},
		{NodeID: 1, Host: "first", Port: 9093, Rack: &rack},
	})
	// In node order, whatever order they arrived in.
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 2 {
		t.Fatalf("the brokers read as %+v", got)
	}
	// A seed is an address somebody typed, not a node the cluster named.
	for _, b := range got {
		if b.Host == "seed" {
			t.Error("a seed broker was taken for a node of the cluster")
		}
	}
	if got[0].Host != "first" || got[0].Port != 9093 || got[0].Rack != rack {
		t.Errorf("the first broker reads as %+v", got[0])
	}
	// A broker in no rack says so by saying nothing, rather than by carrying
	// a pointer nobody may follow.
	if got[1].Rack != "" {
		t.Errorf("a broker with no rack reads as %q", got[1].Rack)
	}
	if brokersOf(nil) == nil {
		t.Error("a cluster of no brokers reads as no list at all")
	}
}

func TestKafkasOwnTopicsAreNotListed(t *testing.T) {
	got := ordinary(kadm.TopicDetails{
		"orders":             {Topic: "orders"},
		"__consumer_offsets": {Topic: "__consumer_offsets", IsInternal: true},
		"audit":              {Topic: "audit"},
	})
	// What somebody put there, in name order; the cluster's own workings are
	// not anybody's data.
	if len(got) != 2 || got[0].Topic != "audit" || got[1].Topic != "orders" {
		t.Fatalf("the topics read as %+v", got)
	}
	for _, d := range got {
		if d.IsInternal {
			t.Errorf("%s is Kafka's own and is listed", d.Topic)
		}
	}
	if ordinary(kadm.TopicDetails{}) == nil {
		t.Error("a cluster with no topics of its own reads as no list at all")
	}
}

func TestARecordAsTheGridHoldsIt(t *testing.T) {
	when := time.Date(2026, 9, 18, 22, 0, 0, 0, time.UTC)
	row := rowOf(&kgo.Record{
		Partition: 2, Offset: 41, Timestamp: when,
		Key: []byte("k"), Value: []byte("v"),
		Headers: []kgo.RecordHeader{
			{Key: "trace", Value: []byte("one")},
			{Key: "trace", Value: []byte("two")},
		},
	})
	if len(row) != len(recordColumns) {
		t.Fatalf("a record reads as %d values and there are %d columns", len(row), len(recordColumns))
	}
	// Where it was, when it arrived, and what it carried — in that order.
	if row[0] != int64(2) || row[1] != int64(41) || row[2] != when {
		t.Errorf("a record reads as %v", row[:3])
	}
	if string(row[3].([]byte)) != "k" || string(row[4].([]byte)) != "v" {
		t.Errorf("its key and value read as %v, %v", row[3], row[4])
	}
	// Kafka lets a header name repeat, so both are kept: a map would have
	// quietly dropped one of them.
	headers, ok := row[5].([]any)
	if !ok || len(headers) != 2 {
		t.Fatalf("its headers read as %v", row[5])
	}
	for i, want := range []string{"one", "two"} {
		h := headers[i].(map[string]any)
		if h["key"] != "trace" || string(h["value"].([]byte)) != want {
			t.Errorf("header %d reads as %v", i, h)
		}
	}

	// A record with no key has none, rather than an empty one: the difference
	// is a fact about how it was written.
	bare := rowOf(&kgo.Record{Partition: 0, Offset: 0, Timestamp: when, Value: []byte("v")})
	if bare[3] != nil {
		t.Errorf("a record with no key reads as %#v", bare[3])
	}
	if bare[5] != nil {
		t.Errorf("a record with no headers reads as %#v", bare[5])
	}
}

func TestAPartitionWithNoLeaderSaysSo(t *testing.T) {
	// A live cluster elects a leader for every partition, so this is the
	// case no broker here will produce: -1 is what Kafka means by "none",
	// and it is a fact about the protocol rather than about the log.
	if got := leaderAttr(-1); got != "none" {
		t.Errorf("a partition with no leader is led by %q", got)
	}
	if got := leaderAttr(0); got != "0" {
		t.Errorf("broker zero reads as %q", got)
	}
	if got := leaderAttr(7); got != "7" {
		t.Errorf("broker seven reads as %q", got)
	}
}

func TestWhereALogBeginsAndEnds(t *testing.T) {
	ps := kadm.PartitionDetails{
		0: {Partition: 0, Leader: 1, Replicas: []int32{1}, ISR: []int32{1}},
		1: {Partition: 1, Leader: 1, Replicas: []int32{1}, ISR: []int32{1}},
	}
	start := kadm.ListedOffsets{"orders": {0: {Offset: 10}, 1: {Offset: 0}}}
	end := kadm.ListedOffsets{"orders": {0: {Offset: 42}, 1: {Offset: 0}}}

	got := partitionsOf("orders", ps, start, end)
	if len(got) != 2 || got[0].ID != 0 || got[1].ID != 1 {
		t.Fatalf("the partitions read as %+v", got)
	}
	if got[0].LowWatermark != 10 || got[0].HighWatermark != 42 {
		t.Errorf("a log running from 10 to 42 reads as %+v", got[0])
	}
	// An empty log begins and ends in the same place, and that is a fact
	// about it rather than an absence of one.
	if got[1].LowWatermark != 0 || got[1].HighWatermark != 0 {
		t.Errorf("an empty log reads as %+v", got[1])
	}

	// Offsets nobody could read leave the watermarks unknown: not knowing and
	// being empty are different things, and only one is about the data.
	for _, p := range partitionsOf("orders", ps, nil, nil) {
		if p.LowWatermark != -1 || p.HighWatermark != -1 {
			t.Errorf("a log whose offsets could not be read reads as %+v", p)
		}
	}
	// An offset that came back carrying a failure of its own is no offset.
	failed := kadm.ListedOffsets{"orders": {0: {Offset: 7, Err: errors.New("no")}}}
	if got := partitionsOf("orders", ps, failed, failed); got[0].LowWatermark != -1 {
		t.Errorf("an offset that failed reads as %+v", got[0])
	}
}

func TestHowManyCopiesOfEachPartition(t *testing.T) {
	of := func(replicas ...int) kadm.PartitionDetails {
		ps := kadm.PartitionDetails{}
		for i, n := range replicas {
			d := kadm.PartitionDetail{Partition: int32(i)}
			for r := 0; r < n; r++ {
				d.Replicas = append(d.Replicas, int32(r))
			}
			ps[int32(i)] = d
		}
		return ps
	}
	if got := replicationOf(of(3, 3, 3)); got != 3 {
		t.Errorf("three partitions kept three times each read as %d", got)
	}
	if got := replicationOf(of(1)); got != 1 {
		t.Errorf("one partition kept once reads as %d", got)
	}
	// Partitions that disagree are said to disagree, rather than averaged
	// into a number true of none of them.
	if got := replicationOf(of(3, 2)); got != -1 {
		t.Errorf("partitions replicated unevenly read as %d", got)
	}
	if got := replicationOf(kadm.PartitionDetails{}); got != -1 {
		t.Errorf("a topic with no partitions reads as %d", got)
	}
}

func TestASizeSaysWhatItIsASizeOf(t *testing.T) {
	// A topic kept three times occupies three times its own size, and the
	// words have to carry that or the number is read as the data's.
	got := sizeText(12 * 1024 * 1024)
	if !strings.Contains(got, "12.0 MB") || !strings.Contains(got, "across all replicas") {
		t.Errorf("twelve megabytes of replicas read as %q", got)
	}
	for n, want := range map[int64]string{
		0: "0 B", 512: "512 B", 2048: "2.0 KB", 5 * 1024 * 1024 * 1024: "5.0 GB",
	} {
		if got := humanBytes(n); got != want {
			t.Errorf("%d bytes read as %q, want %q", n, got, want)
		}
	}
}

func TestWhatTheTreeCallsTheCluster(t *testing.T) {
	// The id it gives itself, which is what tells one cluster from another.
	if got := clusterName(&model.Cluster{ID: "abc123",
		Brokers: []model.Broker{{ID: 1, Host: "h", Port: 9092}}}); got != "abc123" {
		t.Errorf("a cluster with an id is called %q", got)
	}
	// Failing that, the broker it named: a label nobody can read is worse
	// than an address.
	if got := clusterName(&model.Cluster{
		Brokers: []model.Broker{{ID: 1, Host: "broker", Port: 9092}}}); got != "broker:9092" {
		t.Errorf("a cluster with no id is called %q", got)
	}
	if got := clusterName(&model.Cluster{}); got == "" {
		t.Error("a cluster with nothing at all is called nothing at all")
	}
}

// A group's progress as the model holds it (T2.76, FR-13.10).

func TestAGroupsProgressIsCarriedAcrossInOrder(t *testing.T) {
	lag := kadm.GroupLag{
		"orders": {
			2: {Topic: "orders", Partition: 2, Commit: kadm.Offset{At: 90},
				End: kadm.ListedOffset{Offset: 100}, Lag: 10},
			0: {Topic: "orders", Partition: 0, Commit: kadm.Offset{At: 50},
				End: kadm.ListedOffset{Offset: 50}, Lag: 0},
		},
		"audit": {
			1: {Topic: "audit", Partition: 1, Commit: kadm.Offset{At: 7},
				End: kadm.ListedOffset{Offset: 9}, Lag: 2},
		},
	}
	got := progressOf(lag)
	if len(got) != 3 {
		t.Fatalf("a group reading three partitions has %d: %+v", len(got), got)
	}
	// By topic and then by partition: a map has no order, and the same group
	// has to read the same way twice.
	want := []model.GroupOffset{
		{Topic: "audit", Partition: 1, Current: 7, End: 9, Lag: 2},
		{Topic: "orders", Partition: 0, Current: 50, End: 50, Lag: 0},
		{Topic: "orders", Partition: 2, Current: 90, End: 100, Lag: 10},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("partition %d reads as %+v, not %+v", i, got[i], want[i])
		}
	}
}

func TestWhatCouldNotBeReadOfAGroupSaysSoRatherThanReadingAsZero(t *testing.T) {
	// Nothing committed yet, and an end nobody could read: kadm says -1 for
	// each, the model documents -1 for each, and the difference between "has
	// not committed" and "committed at the beginning" is exactly what
	// somebody is looking for.
	lag := kadm.GroupLag{
		"orders": {
			0: {Topic: "orders", Partition: 0, Commit: kadm.Offset{At: -1},
				End: kadm.ListedOffset{Offset: 100}, Lag: -1},
			1: {Topic: "orders", Partition: 1, Commit: kadm.Offset{At: 5},
				End: kadm.ListedOffset{Offset: -1}, Lag: -1},
		},
	}
	got := progressOf(lag)
	if len(got) != 2 {
		t.Fatalf("two partitions read as %+v", got)
	}
	if got[0].Current != -1 || got[0].End != 100 || got[0].Lag != -1 {
		t.Errorf("a partition nothing has committed to reads as %+v", got[0])
	}
	if got[1].Current != 5 || got[1].End != -1 || got[1].Lag != -1 {
		t.Errorf("a partition whose end could not be read reads as %+v", got[1])
	}
}

func TestAGroupReadingNothingHasNoProgressToShow(t *testing.T) {
	if got := progressOf(nil); len(got) != 0 {
		t.Errorf("a group reading nothing has progress %+v", got)
	}
	if got := progressOf(kadm.GroupLag{"orders": {}}); len(got) != 0 {
		t.Errorf("a topic with no partitions read has progress %+v", got)
	}
}
