# ADR-0106: A description is not rows

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T2.74, T2.75 · **Requirements:** FR-2.4, FR-13.1, FR-13.10, FR-13.14
**Packages:** `internal/model`, `internal/ui/shell`, `internal/source/drivers/kafka`, `internal/source/drivers/cassandra`

## Context

The explorer decides what to offer for the selected node from flags the driver
sets on it. `Browsable` says that opening the node yields rows, and it drove
both actions: opening the data, and opening the structure.

For a table those are the same object seen twice, so the two agreed everywhere
anybody looked. They stop agreeing for an object that has a description and no
rows. A Kafka cluster is one: it has brokers and a controller and no rows at
all. So is a consumer group, a schema registry subject, and a Cassandra
keyspace.

The result was that `Describe` returned a description for four kinds of
object that nothing in the tree could ask for. Four arms of the structure view
had been written against them — `*model.Cluster` since T2.59, `*model.Schema`
for a keyspace, and `*model.SchemaSubject` and `*model.ConsumerGroup` in the
two tasks just finished — and the command that would have shown any of them
was disabled whenever one was selected.

It failed quietly, which is why it lasted. The structure view's last arm says
"the structure of this kind of object cannot be shown yet", so nothing crashed
and no test went red; the menu item was simply grey, and a grey menu item
looks like a decision somebody made.

## Decisions

1. **A node says whether it can be described, separately from whether it can
   be read.** `Describable` sits beside `Browsable` on `model.Node` and drives
   the "open structure" action, as `Browsable` drives "open data".

2. **Being browsable implies being describable.** Everything that yields rows
   also has a structure, so the condition is either flag and no driver has to
   set both. That also means this change cannot take an action away from any
   node that had it.

3. **The shell asks the node, not the kind.** The alternative was a list of
   kinds in the shell that are worth describing. It would be wrong the moment
   a driver describes something the list does not mention, and it puts a
   judgement about one driver's objects in code that serves all of them.

4. **Capabilities were not the place either.** `Capabilities().Objects` says
   which kinds appear in the tree, not which have a description; and the
   answer is needed in a menu-enablement callback, which must be cheap,
   synchronous, and answerable without a round trip.

5. **Only kinds a driver really describes are marked.** Every driver's
   `Describe` was read rather than assumed. PostgreSQL, MySQL and SQLite
   describe tables and views; MongoDB describes collections; Redis describes
   its databases and keys — all of them browsable already, so all of them
   unaffected. What was marked is what was both described and unreachable:
   Kafka's cluster, subjects and consumer groups, and Cassandra's keyspaces.

## Consequences

Four kinds of object can be opened that could not be opened before, and two of
them are the views the two preceding tasks were written to produce. Nothing
that was reachable changes, because the new flag only ever adds.

The lesson is about what a proxy costs. `Browsable` was never meant to answer
"has this a structure"; it answered it correctly for every object anybody
tested, and wrongly for every object of a kind nobody could test that way. A
flag that stands in for another is a bet that the two never come apart, and
the bet is settled silently — here, by a menu item that stayed grey.

What this does not do is give the tree a way to say that a description is
empty. A driver that marks a node describable and then returns nothing gets a
tab saying so, which is the same answer the view has always given.
