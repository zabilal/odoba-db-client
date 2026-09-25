package redis

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Listening to a channel (FR-12.5). What needs a server to prove is in
// pubsub_live_test.go; this is what a channel is on its own.

// A channel can be followed and nothing else here can: a key holds a value to
// be read, and a database holds keys.
func TestOnlyAChannelIsFollowed(t *testing.T) {
	s := &redisSource{}
	for _, c := range []struct {
		ref  model.ObjectRef
		want bool
	}{
		{model.NewRef(model.KindChannel, "news"), true},
		{model.NewRef(model.KindKey, "db0", "user:1"), false},
		{model.NewRef(model.KindDatabase, "db0"), false},
		// A channel with no name, and a channel named as if it were under a
		// database: neither addresses a channel this server has.
		{model.NewRef(model.KindChannel), false},
		{model.NewRef(model.KindChannel, "db0", "news"), false},
		{model.NewRef(model.KindChannel, ""), false},
	} {
		if got := s.CanFollow(c.ref); got != c.want {
			t.Errorf("CanFollow(%s) is %v", c.ref, got)
		}
	}
}

// Nothing is kept on a channel, so reading one without following answers
// nothing at all — rather than refusing to open, which would leave a person
// with no way to reach the Follow beside the empty grid.
func TestNothingIsKeptOnAChannel(t *testing.T) {
	s := &redisSource{}
	ref := model.NewRef(model.KindChannel, "news")
	rs, err := s.Browse(context.Background(), ref, source.BrowseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cols := rs.Columns()
	want := []string{"at", "message"}
	if len(cols) != len(want) {
		t.Fatalf("a message has %d columns: %+v", len(cols), cols)
	}
	for i, name := range want {
		if cols[i].Name != name {
			t.Errorf("column %d is %q, want %q", i, cols[i].Name, name)
		}
	}
	if _, err := rs.Next(context.Background()); !errors.Is(err, io.EOF) {
		t.Errorf("it read %v", err)
	}
	// Closed twice, because a stream's Close is allowed to be.
	if err := rs.Close(); err != nil {
		t.Error(err)
	}
	if err := rs.Close(); err != nil {
		t.Error(err)
	}
	// And a count of it is nothing, which is an answer rather than a failure:
	// the grid draws an empty log and a real scrollbar over it.
	n, err := s.Count(context.Background(), ref, source.BrowseOptions{})
	if err != nil || n != 0 {
		t.Errorf("it counts %d, %v", n, err)
	}
}

// A browse that is not of a channel at all is refused rather than listened to:
// a name is what SUBSCRIBE takes, and there is none.
func TestAChannelBrowseNeedsAChannel(t *testing.T) {
	s := &redisSource{}
	for _, ref := range []model.ObjectRef{
		model.NewRef(model.KindChannel),
		model.NewRef(model.KindChannel, "db0", "news"),
	} {
		if _, err := s.listen(context.Background(), ref, source.BrowseOptions{Follow: true}); err == nil {
			t.Errorf("%s was listened to", ref)
		}
	}
}

// The command a channel's browse shows is what a person would type to see the
// same thing, which is the whole point of showing it (FR-5.13).
func TestTheCommandAChannelSends(t *testing.T) {
	s := &redisSource{}
	stmt, err := s.BuildBrowse(model.NewRef(model.KindChannel, "news"), source.BrowseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if stmt.SQL != "SUBSCRIBE news" {
		t.Errorf("it sends %q", stmt.SQL)
	}
	// A name that is not plain is quoted, as every other name this driver
	// writes is: a channel may be called anything at all.
	stmt, err = s.BuildBrowse(model.NewRef(model.KindChannel, "news feed"), source.BrowseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if stmt.SQL != `SUBSCRIBE "news feed"` {
		t.Errorf("it sends %q", stmt.SQL)
	}
}

// The tree holds channels under one folder of the model's own naming, beside
// the databases rather than inside one: a channel is the server's, and a
// message published on db0 reaches a listener on db7.
func TestChannelsAreTheServersAndNotADatabases(t *testing.T) {
	nodes := channelNodes([]string{"news", "alerts"})
	if len(nodes) != 2 {
		t.Fatalf("it holds %d channels", len(nodes))
	}
	for i, name := range []string{"news", "alerts"} {
		n := nodes[i]
		if n.Label != name || n.Ref.Kind != model.KindChannel {
			t.Errorf("channel %d is %+v", i, n)
		}
		if len(n.Ref.Path) != 1 {
			t.Errorf("%s is addressed as %v, which is under something", name, n.Ref.Path)
		}
		if !n.Browsable {
			t.Errorf("%s cannot be opened", name)
		}
	}
	// And the folder they are under is the class the model knows, which is how
	// its label and its place are the same on every engine (REQ-DB-4).
	class := model.ClassRef(model.ObjectRef{}, model.KindChannel)
	if k, ok := model.ClassOf(class); !ok || k != model.KindChannel {
		t.Errorf("the folder %v is no class the model knows", class)
	}
	if model.ClassLabel(model.KindChannel) != "Channels" {
		t.Errorf("the class is called %q", model.ClassLabel(model.KindChannel))
	}
}
