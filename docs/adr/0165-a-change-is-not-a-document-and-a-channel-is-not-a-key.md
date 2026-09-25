# ADR-0165: A change is not a document, and a channel is not a key

**Status:** Accepted · **Date:** 2026-09-25
**Tasks:** T5.12 · **Requirements:** FR-12.5, REQ-DB-1, REQ-DB-2, REQ-DB-4
**Packages:** `internal/source`, `internal/source/drivers/mongo`, `internal/source/drivers/redis`, `internal/ui/shell`

## Context

ADR-0164 gave a following read somewhere to go: the grid draws a tail, the window
turns one on, and a position can be asked for. It left the thing it was built
for undone. MongoDB and Redis both have something that answers when somebody
writes, and neither is a log: a change stream is a feed of events about
documents, and a pub-sub channel is a name that messages pass through and are
kept by nobody.

Kafka made the shape of the controls easy, because everything a Kafka connection
can open is a topic and every topic can be followed. Neither of these servers is
like that. A Redis connection browses keys, which hold values and are read rather
than waited for. A Mongo connection browses collections, but also databases and
indexes. A capability flag on the source says "this server can follow things"; it
cannot say which ones.

## Decisions

1. **A followed collection has a change's columns, not a document's.** What a
   change stream answers is an event: when, what happened, which document, what
   the document is now, and which fields moved. A deletion has no document at
   all, and drawing one for it would show a document that is not there. So the
   stream answers five fixed columns of its own, and the grid reads them from the
   fetcher as it reads any other — columns are asked for on every draw, so
   nothing else had to change.

2. **The document as it is after the change, where the server will give it.**
   `fullDocument: updateLookup`, because a person watching a collection is
   watching documents rather than field names. The lookup happens when the change
   is read, so a document deleted before its update was read arrives with no
   document: true of the server, and worth knowing rather than worth hiding.

3. **A standalone's refusal says what to do about it.** A change stream needs an
   oplog, which a standalone MongoDB has not got. The driver's own words are
   "The $changeStream stage is only supported on replica sets"; what this says is
   that the server keeps no oplog, so a collection cannot be followed, and that
   needs a replica set or a sharded cluster. The driver's error is kept
   underneath for anybody who wants it.

4. **Which objects can be followed is the source's answer, not the window's
   guess.** `source.Followable` is one method — `CanFollow(ref) bool` — and the
   window asks it before it offers the control. Without it, a source claiming
   `Stream.Follow` is taken to mean anything it can browse, which is what Kafka
   means. Redis answers yes to a channel and no to a key; Mongo to a collection
   and nothing else (REQ-DB-1, REQ-DB-2). The driver asks the same question
   before it answers a browse, which is one rule read twice rather than two
   rules.

5. **Channels are the server's, not a database's.** `SUBSCRIBE` is not scoped by
   `SELECT`: a message published on db0 reaches a listener on db7. So the
   Channels class sits beside the numbered databases at the root rather than
   under each of them, which is also the truth about pub-sub that a person needs
   before they use it. Keys stay rows in the grid (T2.40); channels are a tree
   node because a channel is not a row anywhere.

6. **A channel nobody is listening to is not in the tree, because it is not yet
   a channel.** `PUBSUB CHANNELS` names the channels that have subscribers, which
   is all the server knows: there is nowhere for a quiet name to be kept. An
   empty Channels folder is left out rather than shown, and a server that will
   not answer `PUBSUB` at all — some managed Redis will not — is a server with no
   channels rather than a server whose databases cannot be listed.

7. **Reading a channel without following answers nothing, rather than refusing.**
   Nothing is kept on one, so a page of it is a page of nothing. Answering no
   rows opens the tab with the Follow control above the empty grid, which is the
   whole of how a channel is read; refusing to open it would leave a person with
   nowhere to press.

8. **A stream that keeps nothing has one position, and it is where following
   begins.** `Stream.Consume` means records can be read, and neither of these can:
   a change stream is not a log this reads pages of. So the window offers "A
   time" alone where `SeekTimestamp` is claimed without `Consume`, and starting
   there begins a tail from that time instead of reading a page — a read would
   answer the same nothing and say it came from somewhere.

9. **A listener is let go of at once.** The socket read a Redis subscription
   waits on is not interrupted by a context, so a stream that read in `Next`
   would notice a cancel only when the next message arrived — never, on a quiet
   channel — and a tail's `Close`, which waits for its reader, would hold the
   window with it. So messages are read on the client's own goroutine and `Next`
   selects on the context. The Mongo driver's `Next` answers a cancel itself, and
   a test holds both to it.

10. **A listener that cannot keep up loses messages, and that is pub-sub.** There
    is nothing kept to catch up from, and a server whose output buffer for a
    subscriber fills disconnects it. A tail's pause is backpressure everywhere it
    can be (ADR-0097) and cannot be here; saying so is better than implying a
    guarantee Redis does not make.

11. **Two branches came out for being unprovable.** A message about an oplog that
    no longer reaches back to the time asked for: MongoDB 7 starts from the
    oplog's beginning instead of refusing, so nothing could reach it. And a
    cancelled change stream's own `ctx.Err()`, because the driver's error already
    carries the cancellation — the branch said the same thing twice.

## Consequences

T5.12 is done, and both halves arrive through the door ADR-0164 built: the
controls do not know what kind of server is behind them. Redis grows a new
object kind, so `model.Classes` grows a Channels class and the explorer an icon
for it — the class machinery meant nothing else had to change.

`source.Followable` is optional, so every driver that means "anything browsable"
keeps saying it by not implementing it. Kafka implements it anyway, because a
consumer group is something to look at rather than to read, and until now the
window would have offered a Follow on one.

What this does not do: it does not offer a channel that nobody is listening to,
so a person who wants to watch `__keyevent@0__:expired` on a quiet server cannot
find it in the tree. That is the server's limit rather than a choice made here,
but it is a gap: naming a channel by hand would close it, and nothing in the
window does that yet.

## Alternatives

**A Channels folder under every numbered database.** Rejected: it would be the
same sixteen listings of one thing, and it would teach that a channel belongs to
a database, which is the misunderstanding that makes pub-sub surprising.

**Following a Redis channel by pattern (`PSUBSCRIBE`).** Deferred: the tree lists
channels that exist, and a pattern is a different request — closer to a filter
than to an object. The message's own channel would have to be a column then,
which is why one is not there now.

**A kinds list in the window: topic, collection, channel.** Rejected for
`Followable`. The window would hold a list that every new driver has to be added
to, and getting it wrong shows up as a control that fails when it is pressed
rather than as a compile error. Which of a server's objects has a stream behind
it is the engine's business (REQ-DB-1).
