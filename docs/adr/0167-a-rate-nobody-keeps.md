# ADR-0167: A rate nobody keeps

**Status:** Accepted · **Date:** 2026-09-30
**Tasks:** T5.14 · **Requirements:** FR-13.17, NFR-P10, REQ-DB-1
**Packages:** `internal/model`, `internal/source`, `internal/source/drivers/kafka`, `internal/app`, `internal/ui/spark`, `internal/ui/shell`

## Context

"How busy is this topic" is the question somebody asks with a log open in front
of them, and Kafka will not answer it. The brokers know: they publish
`MessagesInPerSec` and `BytesInPerSec` per topic over JMX. JMX is not a protocol
this client speaks, and it is not a port a person browsing a topic has any reason
to have open — so as far as a Kafka client is concerned, a cluster keeps no rate
at all.

What it keeps is totals. The high watermark of every partition counts every
record ever written to it, and the log directories say how much room the
segments take. Two readings of those and the time between them are a rate, and
that is the only rate there is to have.

## Decisions

1. **The rate is measured here, and the measuring is what is drawn.** A window
   that has just opened knows nothing; one that has been open a minute knows the
   last minute. So the picture starts empty and fills, and the label says
   "measuring…" rather than showing a nought that would read as a quiet topic.
   This is not history — nothing is stored, and closing the tab forgets it.

2. **Records come from the watermarks and bytes from the log directories.** The
   sum of the watermarks only grows, so the difference between two readings is
   exactly what was produced in between. The log directories are counted on the
   broker leading each partition rather than on all of them: a topic kept in
   three copies takes three times the room, and none of that is what somebody
   means by how much it carries. `topicSize` in the same driver answers the other
   question — every replica summed — on purpose, because what a topic costs a
   cluster is every copy of it.

3. **A log that shrank is not a negative rate, and not a nought.** Retention
   deletes whole segments, so a byte reading can fall, and a fall says nothing at
   all about what was written in that interval. The bytes are unknown for it,
   which the rate carries as -1 and the label leaves out. A nought would be a
   claim, and a negative number a puzzle.

4. **Totals are the driver's, and the arithmetic is not.** `source.TopicMeter`
   answers `TopicTotals` and nothing else; `model.RateBetween` makes the rate.
   A driver that returned a rate would be a driver inventing the one thing its
   protocol will not tell it (REQ-DB-1).

5. **A cluster that will not say how big its logs are still says how many
   records.** Some managed Kafka refuses `DescribeLogDirs`. That is a `Sized`
   of false rather than a size of nothing, and the rate then carries the records
   alone — measuring half of something is worth more than measuring nothing.

6. **One picture, of the records, with the bytes as a number beside it.** Two
   sparklines on a line that already holds three buttons would be a line nobody
   can read. The picture is of what people watch, and both figures are written
   next to it.

7. **It is a sparkline, not a chart.** A chart is a statement about a result
   somebody chose to draw, with axes to read values off and a legend to say what
   is what. A sparkline is a shape, read at a glance, beside the number it is the
   history of — so `internal/ui/spark` has no axes, no ticks, no legend and no
   hover. What it does share is the drawing: the marks are rasterised by the same
   code the charts use, so a line here is the same line there.

8. **The picture is drawn against nought.** A topic going from a thousand to a
   thousand and one records a second is not as busy as one going from nothing to
   a thousand, and a line scaled to its own quietest moment would draw them the
   same.

9. **It sits in the bar above the records.** That is where somebody is when they
   wonder whether a topic is busy, and the bar is already a live thing (ADR-0164).
   A separate panel would be a second place to look at one topic.

10. **A window of fixed size, and a quiet topic costs nothing.** A minute of
    rates, dropped oldest-first, for the reason a tail's window is bounded
    (NFR-P10). The picture is drawn again only when a rate has arrived.

11. **A reading that failed is said, and is not the end of the measuring.** A
    broker that was busy is not a broker that has gone, and the next reading says
    which. While it is failing the label says so rather than drawing a rate of
    nothing; the picture stays as it was, because it is still true.

12. **A defect this found, which was not this task's.** Closing a tab cancelled
    its context and left everything else. That ends a following read — the
    goroutine returns — but it does not close the stream that was being read, so
    a window left a Kafka consumer open for every closed tab. The bar now lets go
    of both the tail and the meter when the tab goes, and neither is closed on
    the goroutine that draws the window.

13. **Three things came out for being unprovable.** A guard skipping the fill
    where there is none to draw, which the rasteriser already skips; a second
    switch for the drawing that the tab's own context had already thrown; and a
    package variable for the interval, replaced by a parameter — the caller
    already chooses how many rates to keep, so it can choose how often to read
    them, and a test then needs no shared state to fiddle with.

## Consequences

A Kafka topic's tab says how busy it is, in a picture and two numbers, from the
moment it is opened. `capability.Stream.Throughput` gates all of it, and the
conformance suite refuses a driver that claims it without implementing the
interface — through a check that returns what it finds, so that the check itself
is tested against a source built to break it.

What this does not do: it does not draw the bytes, which are a number only; it
does not answer "which of my forty topics is busy", because that wants a list
with a picture on every row and the tree draws text; and it does not read the
brokers' own rates, which would mean speaking JMX. It measures only while a tab
is open, so it is not monitoring and cannot be — which is the honest shape of
something a database client can offer.

## Alternatives

**Reading the brokers' JMX metrics.** Rejected: a second protocol, a second port
to open, a second thing to configure and fail, and in a managed cluster usually
not reachable at all. The totals are in the protocol the client already speaks.

**A Sparkline kind in the chart package.** Rejected: `chart.Kinds` is the list of
ways a person can choose to draw a result, and a sparkline is not one of them —
adding it there would have put it in that menu. The drawing is shared instead,
which is the part worth sharing.

**Storing the rates, so the picture survives a closed tab.** Deferred: it would
make this monitoring, which wants a story about retention, about what happens
while the window is shut, and about a hundred topics nobody is looking at. A
picture of what the window has watched needs none of that.
