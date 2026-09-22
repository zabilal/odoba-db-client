# ADR-0126: What a property finds that a test does not

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T3.15 · **Requirements:** RISK-8, FR-7.1, FR-7.2
**Packages:** `internal/diff`, `internal/model`, `internal/app`

## Context

RISK-8 names `internal/diff` by hand and asks for heavy testing of it,
because a sync script is generated from what it answers.

By this point the package had 39 tests and every mutation written against it
had been caught. More tests of the same kind would have said more about the
cases somebody had already thought of. The gap is the cases nobody thinks of.

## Decisions

1. **Properties over generated models, run as fuzz targets.** Six statements
   that must hold whatever two schemas are given: a model compared with
   itself never differs; the comparison is symmetric; every node reported is
   in one of the two models; the order two servers list things in is not a
   difference; a rule can only ever remove a difference; and every node can
   be found again by the name it was walked under, with the counts agreeing.

   This is the first fuzzing in the repository.

2. **The generator only makes models a server could have answered.** A real
   catalogue cannot hold two tables of one name in a schema, and a property
   that had to allow for one would be weaker everywhere else. What a
   duplicate name does is settled by a test of its own, written by hand.

3. **What fuzzing finds is written down as a test, by hand.** A corpus
   reproduces a defect only while the generator is unchanged: the bytes are a
   seed, not a schema, and the same bytes make a different model as soon as
   the generator does. The corpus is kept for the search; the proof is a test
   that says what the defect was.

   This was learnt the hard way — three saved inputs stopped reproducing the
   moment the generator was corrected, and the mutations aimed at them caught
   nothing.

## What it found

Three defects, none of which any of the 39 tests could see.

1. **A one-sided object was called after the list it was in.** A materialized
   view present in only one of the two models was reported as a view, because
   `match` took the kind from its parameter rather than from the object. A
   materialized view in *both* models was reported correctly, which is why no
   test had noticed.

2. **The three kinds of constraint shared one identity.** A primary key, a
   unique constraint and a check were all `constraint`, and a node's name in
   the tree is its kind and its name together. A unique constraint on one
   side and a check of the same name on the other — somebody turning one into
   the other — produced two different differences with one name between them:
   one hid the other, and a tick meant both.

   They are three kinds now. That also removed a guess from the sync script,
   which had been working out which list held a name.

3. **A view's kind depends on which way round the comparison was made**, and
   that is right rather than wrong: a comparison describes what an object is
   wanted as, and a sync script has to create that. It was not written down
   anywhere, and the property asking for symmetry is what asked the question.

## Consequences

- `model` has `KindPrimaryKey`, `KindUnique` and `KindCheck`. `KindConstraint`
  remains for the explorer, which lists a table's constraints together.
- A fuzz corpus is committed under `internal/diff/testdata`. It runs in an
  ordinary test run, which costs nothing and keeps the search going forward.
- The properties are mutation-tested like everything else, but three
  mutations are aimed at the hand-written regressions rather than at the
  properties, because a property with a stale corpus proves nothing.
- `precheck.py` counted only tests whose names start with Test, so a mutation
  aimed at a fuzz target was reported as matching nothing and went unchecked.
  It counts both now.
