# ADR-0166: A permission is a sentence

**Status:** Accepted · **Date:** 2026-09-30
**Tasks:** T5.13 · **Requirements:** FR-13.15, FR-4.9, FR-13.21, REQ-DB-1, REQ-DB-2
**Packages:** `internal/model`, `internal/source`, `internal/source/drivers/kafka`, `internal/ui/shell`

## Context

A Kafka cluster's permissions are the thing that stands between it and everybody
who is not supposed to be reading it. There is no hierarchy to them and no
inheritance: each one says that a principal may, or may not, do one thing to one
resource from one host, and the only precedence is that a refusal beats a
permission wherever both reach. So the list of them is the whole truth about who
can do what, which makes reading it worth as much as changing it.

They also barely exist. Kafka answers every question about them with
`SECURITY_DISABLED` unless a broker has an authorizer configured, and a cluster
without one allows everything to everybody who can reach it. Neither of this
project's test brokers had one, which is why the plain broker is now the case
that proves the refusal and `ikigai-kafka-sasl` was recreated with
`StandardAuthorizer` — `User:ikigai` and `User:ANONYMOUS` as super users, so
that everything already proven against it still passes.

## Decisions

1. **Reading them and changing them are two claims.** `source.ACLInspector`
   reads, `source.ACLAdmin` grants and revokes, and `capability.Stream` has a
   flag for each. A connection may be allowed to see who may do what without
   being allowed to change it, and one interface for both would make offering
   the first a promise to do the second (ADR-0107). Claiming the second without
   the first is a contradiction the conformance suite refuses: a permission
   granted where none can be read is one nobody can check or take back.

2. **A permission is a sentence, and the sentence is what is agreed to.**
   "User:alice may read the topic orders, from anywhere" is how one is listed,
   how a grant is confirmed and how a revoke is confirmed. A grid of five
   columns would be the same facts with the meaning taken out, and the one
   moment that matters is the moment somebody says yes to a change — so what
   they say yes to is a sentence they can read, not a row they have to assemble.

3. **They are read where the thing they are about is.** On a topic, on a
   consumer group, on the class either hangs under, and on the cluster, which is
   the only place the whole list can be seen. A permissions browser somewhere
   else in the window would be a second place to look for something that is
   already about an object in the tree.

4. **Asking about one object answers everything that reaches it.** The
   permission written about its name, the one written about every name of its
   kind, and any prefix of it: all three let somebody in, so a list showing only
   the first would lie about who can. That is Kafka's `MATCH` rather than
   `LITERAL`, and it is why the filter has a name in it at all.

5. **A cluster that keeps no permissions says so, in place of the list.** An
   empty list reads as "nobody may do anything", and the truth is the opposite.
   So `SECURITY_DISABLED` becomes "this cluster keeps no permissions: no
   authorizer is configured on the brokers, so everything is allowed to
   everybody who can reach it" — on reading, on granting and on revoking alike.

6. **An empty list says what it does not mean.** Where a cluster has an
   authorizer and nothing written for this object, the line under the list says
   that what that means is the brokers' setting rather than anything shown here:
   a cluster either refuses everybody it has not been told about or allows them,
   and no client can tell which from the outside.

7. **A permission nobody here has a word for is still shown.** A resource type
   or an operation this application does not know reads as the protocol's own
   word with its underscores taken out, because a permission nobody can see is
   worse than one named oddly — it is also one nobody can take away. Writing one
   is refused instead: granting the wrong operation because the right one had no
   name would be granting something nobody asked for.

8. **The window offers only what it can write.** A permission about a user
   principal — who may mint a delegation token for whom — is shown where a
   cluster holds one and is not among the kinds the form offers, because the
   client cannot build that request. A choice that failed when it was taken
   would be worse than a choice that is not there (ADR-0165).

9. **A grant or a revoke is exactly what it names.** Never a pattern: a revoke
   by pattern would take away permissions nobody named, and a grant by one would
   give more than was asked for. A prefix is still possible — it is asked for by
   ticking a box, and it is then part of what the sentence says.

10. **Revoking nothing says so.** Kafka's delete answers how many it matched,
    and none means the permission was not there — somebody else took it away, or
    it never existed. Reporting success for that would be reporting that
    something was done.

11. **Both changes are AccessAdmin, and the driver refuses before it dials.** A
    read-only connection refuses outright, and a production one refuses without
    consent given for this act and no other (FR-4.9, FR-13.21). Reading is not a
    change, so a read-only connection reads them.

12. **What is shown after a change is what the cluster says, read again.** Not
    the list with the change applied to it in the window: a broker's authorizer
    hears about a change after the controller has accepted it, so the answer can
    lag, and the tests wait for the condition rather than pausing and hoping
    (`settled` does the same for topics). The selection is dropped when the list
    is read again, because the rows have moved.

13. **The order is fixed.** By what the permission is about, then its name, then
    whether it is a prefix, then who, what, allow before deny, and finally where
    from. A list that reordered itself between two readings of the same cluster
    would be a list nobody could point at.

14. **The new conformance check returns what it finds.** Every real driver passes
    the capability-claim checks, so running them against real drivers proves
    nothing about the checks themselves. `permissionClaims` answers a list of
    problems, and a test holds it against sources built to break it.

15. **Two lines came out for being unprovable.** The pattern choice's second half
    — "or the resource is the cluster" — is unreachable, because the cluster has
    no name to give and the first half already covers it. And the list's chosen
    row was put back by hand as well as by unselecting it, which is the same
    thing said twice.

## Consequences

Permissions can be read and changed from the window, on the four things they can
be about, and the whole of a cluster's can be seen at once. `ikigai-kafka-sasl`
is a properly secured broker now, which is where ACLs matter and which the SASL
and TLS tests were already using.

What this does not do: it does not show what a permission *means* for a
principal — "can alice read orders?" is a question about every permission that
reaches the topic, the brokers' `allow.everyone.if.no.acl.found`, and whether
alice is a super user, and only the first of those is something a client can
read. It does not offer delegation-token permissions about users, and it does
not write a permission about anything the client cannot name. Kafka is also the
only paradigm with any of this: the interfaces are optional, so nothing else has
to answer them.

## Alternatives

**A grid of permissions, edited like rows of a table.** Rejected: Kafka has no
way to alter a permission, only to create and delete one, so the grid's update
would have to be a delete and a create — two changes behind one edit, on the
most dangerous thing in a cluster. And the five columns would take the meaning
out of what somebody is agreeing to.

**One interface for reading and changing.** Rejected for the reason ADR-0107
gives: offering to show permissions would become a promise to change them, and
the two are not the same claim on any cluster.

**Showing a cluster's `allow.everyone.if.no.acl.found` beside an empty list.**
Deferred: it is a broker config, readable through DescribeConfigs, and worth
having — but per-broker rather than per-cluster, and an answer assembled from
several brokers that disagree would be worse than the honest sentence that is
there now.
