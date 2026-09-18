# ADR-0091: The cluster in the tree

**Status:** Accepted · **Date:** 2026-09-18
**Tasks:** T2.59 · **Requirements:** FR-2.2, FR-13.1 · **Packages:** `internal/source/drivers/kafka`, `internal/ui/shell`

## Context

Every source in this application so far has had something to choose between at
the top of its tree: databases, keyspaces, a numbered Redis database. A Kafka
connection has none. It is a connection to one cluster, and what a person wants
to know first is what that cluster is — which brokers it has, which of them
answers for the whole, and what it calls itself (FR-13.1).

## Decisions

1. **The root is the cluster itself**, one node, with everything else hanging
   beneath it as it is written. A tree whose root is empty would be a tree that
   says a connected source has nothing in it.

2. **Brokers are not nodes.** The model has no object kind for a broker, and
   that is deliberate: it was designed against Kafka before any driver existed
   (T0.29), and `model.Cluster` already carries `Brokers` as a field. So the
   brokers are part of what a cluster *is*, and they arrive through `Describe`
   into the structure tab — beside how a Cassandra keyspace shows its
   replication (ADR-0081), which is the same shape of answer.

3. **The cluster claims no children until it has some.** Topics and consumer
   groups are T2.62; until then the node says it has none. This is the rule the
   conformance suite was taught to hold at every depth in T2.53, and it applies
   to the driver that added the rule as much as to the one it caught.

4. **A seed is not a broker.** The addresses a connection started from are not
   nodes of the cluster: franz-go marks a seed by numbering it very negatively
   and giving it no rack, and they are dropped rather than shown as brokers
   nobody can find.

5. **The version is an attribute, not a field.** A broker does not say its
   release; it says which API versions it speaks, and a release is read back
   out of that (ADR-0086). It goes in `Attrs`, where what a server says about
   itself belongs, rather than in a field that would imply the cluster stated
   it.

## Consequences

A Kafka connection opens onto something worth seeing: the cluster, named by the
id it gives itself, with its brokers, their addresses and racks, and the
controller marked on its own row rather than explained in a legend elsewhere.

What is not there is as deliberate: no topics, no groups, no records, and no
counts. Each arrives with the task that writes it, and the node will say it has
children on the day it does.
