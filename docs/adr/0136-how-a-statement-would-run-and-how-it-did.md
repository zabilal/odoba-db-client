# ADR-0136: How a statement would run, and how it did

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T3.25 · **Requirements:** FR-5.13, NFR-S4, FR-4.9
**Packages:** `internal/source`, `internal/source/drivers/{postgres,mysql,sqlite}`, `internal/app`, `internal/ui/shell`

## Context

The `Explainer` contract has been in `internal/source/query.go` since the
driver interfaces were written, and nothing implemented it. FR-5.13 asks for
`EXPLAIN` and `EXPLAIN ANALYZE` rendered as a readable plan tree with cost
heat. A plan is the one thing a database tells you about its own behaviour,
and it is read for one reason: to find the step to change.

## Decisions

1. **Asking the planner is a read; measuring is whatever the statement is.**
   `EXPLAIN` does not run anything, so it is allowed on a read-only
   connection whatever it is asked about. `EXPLAIN ANALYZE` runs the
   statement, so it goes through the same guard as running it: a read-only
   connection refuses it here rather than at the server, and a production
   connection asks first. The guard comes before anything about what the
   server can report, because whether a statement may run is not conditional
   on how its plan would be written.

2. **A plan is asked for once.** PostgreSQL will write a plan as text, as
   JSON, as XML or as YAML, and asking twice to have both would mean running
   an analysed statement twice — which for anything that writes is not a
   second reading of the same thing. So the tree is read from the JSON and
   the JSON is kept as the server's own words, shown beside the tree.

3. **Three engines, in their own words.** PostgreSQL's JSON is a tree of
   uniform nodes. MySQL's and MariaDB's is nested objects whose keys name
   what they are, and the two name the same things differently — MySQL puts
   costs in a `cost_info` object and counts `rows_examined_per_scan`,
   MariaDB puts `cost` and `rows` beside each other. One reader takes what
   it recognises from either and keeps the document whole for the rest. A
   key a server adds in a later release is not a step until it is listed as
   one, so a plan never invents a step out of a field.

4. **Only what a server really reports.** SQLite publishes no estimates at
   all, so every figure there says "not reported" rather than standing at a
   number nobody gave. A figure of −1 is what that looks like, throughout.

5. **Measuring is claimed separately from planning.** SQLite has no form
   that runs a statement and measures it; MySQL's `EXPLAIN ANALYZE` answers
   text in a shape of its own, which is a plan to read but not one to draw a
   tree from; MariaDB's `ANALYZE FORMAT=JSON` writes what happened into the
   same document. So `Query.ExplainAnalyze` is its own capability, and the
   window offers the button only where it would work.

6. **A step's own work is what its children did not do.** A parent's figure
   includes everything under it, so sharing the work out by the figures as
   given would make the root always the hottest step and say nothing. Never
   less than nothing: a parallel plan can report children that together took
   longer than their parent, which is real.

7. **Time where it was measured, cost where it was not.** What a plan cost
   is a guess and what it took is not, so a measured plan is shared out by
   time. The footer says which, because a bar standing for a guess and a bar
   standing for a measurement would otherwise look the same.

8. **The heat is a length, a number and a weight — not a colour alone.**
   Each step's share is a bar in a track so the rows can be compared down
   the column, a percentage beside it, and the hottest step is emboldened.
   About one man in twelve cannot rely on colour (NFR-A1).

9. **The step whose estimate was furthest out is named.** A plan goes wrong
   where the estimate was wrong, and being ten times out is the threshold
   worth saying. A row either way in the arithmetic, so that a step which
   expected none and returned none is not infinitely wrong.

10. **A row of a plan is cut, not wrapped.** A condition can be a page long,
    and a step that took three lines to describe would push the next step
    off the screen. The whole of it is in the server's own words below.

11. **What is planned is what is pointed at**: the selection, or else the
    statement the caret is in. A plan of a whole script would be a plan of
    something nobody asked about.

## Consequences

- PostgreSQL, MySQL, MariaDB and SQLite all read plans; MySQL is the one
  that plans but does not measure. Cassandra, Mongo, Redis and Kafka do not,
  and the window does not offer to.
- The plan readers are pure functions over a document, tested against both
  servers' own words written out by hand as well as against the servers
  themselves. A server is needed to prove the words are what the server
  says; it is not needed to prove they are read correctly.
- MySQL and MariaDB refuse `EXPLAIN` of a write inside a read-only
  transaction. That is their rule, not this program's, and a read-only
  connection there cannot plan a write — only read one.
- Nothing re-plans a statement as it is edited, and nothing compares two
  plans. Both would be worth having and neither is claimed by a task.
