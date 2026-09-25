package redis

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Listening to a channel (FR-12.5).
//
// Pub/sub is the other half of what a Redis server does, and it is nothing like
// the keyspace. A channel holds no value, has no type and no expiry; what is
// published on one goes to whoever is listening at that moment and is kept by
// nobody. So a channel cannot be browsed — there is no page of it to read — and
// the only way to see what is on one is to listen while somebody speaks, which
// is what a following read is (ADR-0164).
//
// Channels are the server's, not a database's: SUBSCRIBE is not scoped by
// SELECT, and a message published on db0 reaches a listener on db7. So they sit
// beside the databases in the tree rather than under one, which is also the
// truth about them a person needs to know before they use them.
//
// The server can only name the channels somebody is listening to (PUBSUB
// CHANNELS). A channel nobody has subscribed to does not exist as far as Redis
// is concerned — there is nowhere for it to be kept — so a quiet name is not
// missing from the list, it is not yet a channel.

// channelColumns are what a message shows.
//
// The time is when the message arrived here. Redis sends no timestamp of its
// own and keeps no record to stamp, so this is the only time there is, and a
// tail with no time in it could not be read at all.
var channelColumns = []model.ColumnDef{
	{Name: "at", Type: model.DataType{Class: model.TypeTimestamp, Native: "received", Length: -1}, ReadOnly: true},
	{Name: "message", Type: model.DataType{Class: model.TypeString, Native: "payload", Length: -1}, ReadOnly: true},
}

// channelOf reads the channel a ref names.
func channelOf(ref model.ObjectRef) (string, error) {
	if ref.Kind != model.KindChannel || len(ref.Path) != 1 || ref.Path[0] == "" {
		return "", fmt.Errorf("redis: %s is not a channel", ref)
	}
	return ref.Path[0], nil
}

// CanFollow is a channel and nothing else: a key holds a value, which is read
// rather than waited for, and a database holds keys.
func (s *redisSource) CanFollow(ref model.ObjectRef) bool {
	_, err := channelOf(ref)
	return err == nil
}

// channels are the channels somebody is listening to, in name order.
//
// On a cluster this is the node this connection talks to. Ordinary pub/sub is
// forwarded across a cluster, so a message reaches every listener wherever they
// are, but each node only knows the subscribers of its own clients.
func (s *redisSource) channels(ctx context.Context) ([]string, error) {
	names, err := s.client.PubSubChannels(ctx, "*").Result()
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

// channelNodes are the channels as the tree holds them.
func channelNodes(names []string) []model.Node {
	out := make([]model.Node, 0, len(names))
	for _, name := range names {
		out = append(out, model.Node{Ref: model.NewRef(model.KindChannel, name),
			Label: name, Browsable: true})
	}
	return out
}

// channelClass is the Channels folder, or nothing where no channel has a
// listener: an empty class is a folder that is always there and always empty,
// which says less than its absence does (model.ClassNodes leaves it out).
//
// A server that will not answer PUBSUB — some managed Redis will not — is a
// server with no channels to show rather than a server whose databases cannot
// be listed. The keyspace is what the tree is for.
func (s *redisSource) channelClass(ctx context.Context) []model.Node {
	names, _ := s.channels(ctx)
	return model.ClassNodes(model.ObjectRef{}, map[model.ObjectKind]int64{
		model.KindChannel: int64(len(names))})
}

// childChannels are the channels under the Channels folder, and nothing under
// anything else: a database's keys are rows, not a tree (T2.40).
func (s *redisSource) childChannels(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	if k, ok := model.ClassOf(ref); !ok || k != model.KindChannel {
		return nil, nil
	}
	names, err := s.channels(ctx)
	return channelNodes(names), err
}

// listen reads a channel: nothing, unless somebody is following it.
func (s *redisSource) listen(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (model.RowStream, error) {
	name, err := channelOf(ref)
	if err != nil {
		return nil, err
	}
	if !opt.Follow {
		// Nothing is kept on a channel, so a page of one is a page of nothing.
		// Saying so by answering no rows is better than refusing to open it:
		// the window's Follow control is right there above the empty grid, and
		// that is the whole of how a channel is read.
		return &channelStream{}, nil
	}
	sub := s.client.Subscribe(ctx, name)
	// The subscription is waited for rather than assumed: until the server has
	// confirmed it, a message published now is a message nobody hears, and a
	// stream that quietly missed the beginning would be the one thing a person
	// following a channel cannot check.
	if _, err := sub.Receive(ctx); err != nil {
		sub.Close()
		return nil, err
	}
	// Read on a goroutine of the client's own rather than in Next. The socket
	// read a subscription waits on is not interrupted by a context — a cancel
	// is noticed when the next message arrives, which on a quiet channel is
	// never — and a window letting a tail go must not wait that long.
	//
	// A listener that cannot keep up loses messages. That is what pub/sub is
	// rather than a choice made here: there is nothing kept to catch up from,
	// and a server whose output buffer for a subscriber fills disconnects it.
	return &channelStream{sub: sub, msgs: sub.Channel()}, nil
}

// channelStream is a channel being listened to — or, with no subscription
// behind it, the nothing that is kept on one.
type channelStream struct {
	sub    *goredis.PubSub
	msgs   <-chan *goredis.Message
	closed bool
}

func (*channelStream) Columns() []model.ColumnDef { return channelColumns }

// Next waits for the next message. It never ends of its own accord: a channel
// nobody is speaking on has not ended, and saying it had would tell the grid
// that a live channel was a finished read.
func (c *channelStream) Next(ctx context.Context) (_ model.Row, err error) {
	defer panics.Recover(&err, "listening to a channel")
	if c.msgs == nil {
		return nil, io.EOF
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg, ok := <-c.msgs:
		if !ok {
			// The subscription has been closed, which is the end of it and not
			// a failure.
			return nil, io.EOF
		}
		return model.Row{time.Now().UTC(), msg.Payload}, nil
	}
}

func (c *channelStream) Close() error {
	if c.sub == nil || c.closed {
		return nil
	}
	c.closed = true
	return c.sub.Close()
}
