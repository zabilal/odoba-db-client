# ADR-0125: A rule about what not to compare

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T3.14 · **Requirements:** FR-7.5, FR-7.6, RISK-8
**Packages:** `internal/diff`, `internal/schemafile`, `internal/app`, `internal/ui/shell`

## Context

`internal/diff` compares text as text and every attribute it is given, and
says so in its own first paragraph: normalising quietly would be that package
deciding two different things are the same, somewhere nobody can see or turn
off (ADR-0119). It has pointed at FR-7.5 for that since it was written.

FR-7.5 names five rules: schemas, name patterns, whitespace, collation,
comments. What it does not say is where they live, and that is the decision
worth making carefully, because a rule can hide a dropped column.

## Decisions

1. **A rule is applied while the comparison is made, not as a filter over the
   tree.** A difference left out is not a difference: it does not make its
   table read as changed, it is not counted in the summary, and it cannot be
   chosen for a sync script. Hiding it afterwards would leave all three
   wrong, and the third is the dangerous one — a script offering to close a
   difference somebody believed was being ignored.

2. **The rules live with the saved model.** They are part of the same
   agreement the model is: a team decides once that the audit schema is
   nobody's to deploy, and the decision travels in version control beside the
   objects it is about. Kept with the connection instead, a comparison would
   mean something different on each person's machine — and the difference
   would be invisible to everybody but its owner.

   They are written into the model's root file, so a commit that changes a
   rule touches one file and says so. Saving the model again keeps them:
   reading the database is no reason to throw away an agreement about it.

3. **What is left out is always said.** The line above the tree carries it
   whether or not anything differs, because "nothing differs" over a rule
   that hides a dropped column is a sentence that is true and misleading at
   once.

4. **A rule can be tried without being agreed.** The dialog's tick — keep
   these with the saved model — is off by default, so somebody can see what a
   rule would do to this comparison before writing it down for everybody.

5. **A name pattern names objects, not the parts they are made of.** A rule
   ignoring a table called `audit` must not ignore a column of that name in
   every table there is. The patterns are shell patterns — `*` and `?` — and
   the same rules apply to tables, views, routines, sequences and types.

6. **A pattern that is not one is refused before a comparison is made with
   it, and never written into a model.** And should a bad pattern reach a
   comparison anyway, it leaves objects in rather than dropping them: the
   failure this feature has to avoid is silently comparing less than somebody
   thinks.

7. **Collation covers the engine's own words for it.** No two engines call it
   the same thing, so the rule matches attributes whose name holds collate,
   charset, encoding or ctype. What is not one of those is still compared.

## Consequences

- `diff.Compare` is `diff.CompareWith` with no rules, so nothing changes for
  anybody who sets none, and the package's promise about comparing text as
  text is unchanged by default.
- Every comparison inside the package is now a method on a value carrying the
  rules, so a rule cannot be applied in one place and forgotten in another.
- Rules in a model file are read by any version of this program; a version
  that predates them would ignore the field and report differences the rules
  say to leave out. That is noisy rather than wrong, which is the right way
  round for a forwards-compatible change.
- Whitespace is applied to definitions, expressions, predicates and
  generation expressions — everything this compares that is the engine's own
  language. It is not applied to names, where a space is part of the name.
