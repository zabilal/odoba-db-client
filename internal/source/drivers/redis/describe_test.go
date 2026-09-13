package redis

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

func TestHowManyOfADatabasesKeysWillExpire(t *testing.T) {
	fields := infoFields("# Keyspace\r\ndb0:keys=3,expires=1,avg_ttl=0\r\ndb5:keys=9,expires=0,avg_ttl=0\r\n")
	if got := expiringIn(fields, 0); got != 1 {
		t.Errorf("db0 has %d expiring", got)
	}
	if got := expiringIn(fields, 5); got != 0 {
		t.Errorf("db5 has %d expiring", got)
	}
	// A database the server said nothing about holds nothing, and nothing is
	// known about what expires in it.
	if got := expiringIn(fields, 9); got != -1 {
		t.Errorf("db9 has %d expiring", got)
	}
	if got := expiringIn(map[string]string{"db0": "keys=3"}, 0); got != -1 {
		t.Errorf("a line that does not say: %d", got)
	}
}

func TestTheFiguresWorthShowing(t *testing.T) {
	fields := infoFields(strings.Join([]string{
		"# Server", "redis_version:7.4.1", "os:Linux", "tcp_port:6379",
		"# Memory", "used_memory_human:1.05M", "maxmemory_policy:noeviction", "mem_allocator:jemalloc",
		"# Stats", "keyspace_hits:42", "keyspace_misses:7",
		"# Nothing", "something_else:1",
	}, "\r\n"))
	groups := figures(fields)
	titles := make([]string, 0, len(groups))
	for _, g := range groups {
		titles = append(titles, g.Title)
	}
	if strings.Join(titles, " ") != "Server Memory Statistics" {
		t.Errorf("the headings are %v; a heading with nothing under it is not shown", titles)
	}
	// The server's own names are kept: what a person searches for is
	// used_memory_human, not "Memory used".
	memory := groups[1]
	if memory.Values[0].Name != "used_memory_human" || memory.Values[0].Value != "1.05M" {
		t.Errorf("memory %+v", memory.Values)
	}
	// The order is the one they are listed in here, not the one the server
	// happened to print.
	if memory.Values[1].Name != "maxmemory_policy" || memory.Values[2].Name != "mem_allocator" {
		t.Errorf("memory %+v", memory.Values)
	}
	// A figure the server did not report is left out rather than shown empty.
	for _, g := range groups {
		for _, f := range g.Values {
			if f.Value == "" {
				t.Errorf("%s is shown with nothing in it", f.Name)
			}
			if f.Name == "something_else" {
				t.Error("a figure nobody asked for is shown")
			}
		}
	}
	if len(figures(map[string]string{})) != 0 {
		t.Error("a server that says nothing is shown as saying something")
	}
}

func TestWhatIsCountedByKind(t *testing.T) {
	// Nothing counts a kind nobody here counts, and -1 is what says so.
	if got := lengthOf(nil, nil, "ReJSON-RL", "doc"); got != -1 {
		t.Errorf("a document is %d long", got)
	}
	if got := lengthOf(nil, nil, "timeseries", "ts"); got != -1 {
		t.Errorf("a time series is %d long", got)
	}
}

func TestADatabaseAndAKeyAreWhatThisDescribes(t *testing.T) {
	s := &redisSource{}
	for _, ref := range []model.ObjectRef{
		model.NewRef(model.KindTable, "db0", "people"),
		model.NewRef(model.KindCollection, "db0", "people"),
		{},
	} {
		if _, err := s.Describe(nil, ref); err == nil {
			t.Errorf("%v was described", ref)
		}
	}
}
