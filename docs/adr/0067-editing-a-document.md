# ADR-0067: Editing a document

**Status:** Accepted · **Date:** 2026-09-12
**Tasks:** T2.34 · **Requirements:** FR-12.1, FR-12.4, FR-4.4 · **Packages:** `internal/ui/shell`

## Context

A document store's row is a document, and a grid cell cannot hold one: a
nested document renders as compact JSON and cannot be typed into. FR-12.1
asks for a schema-aware JSON document editor, and ADR-0066 gave the writer
it needs.

## Decisions

1. **The JSON view edits one document at a time.** The page (ADR-0065) is a
   view: Edit Document opens the grid's active row alone, as a JSON object,
   and Save writes it. A page of fifty documents diffed as one array would
   be a worse thing to reason about than a document, and a worse thing to
   get wrong.

2. **What is saved is what differs.** The edited JSON is read against the
   document it came from: a field changed or added is its new value, a field
   taken out of the text is `model.Removed`, and an untouched field is not
   written at all — an edit to one field must not write over what another
   person changed in another (ADR-0031). A field this document never had is
   not removed from it: the column is the collection's, and there is nothing
   here to go.

3. **A whole number stays whole.** JSON has one kind of number and a
   document store tells `43` from `43.0`, so a number is written back as the
   kind the field held. A field that held a fraction keeps one.

4. **The `_id` cannot be edited.** Changing it means another document, and
   the editor says so rather than writing one document over another.

5. **It is schema-aware in two ways, and neither refuses anything.** The
   columns came from a sample of the collection (ADR-0065), so they are its
   shape: the editor says which of them are empty in this document, and where
   an edited value's type is not the type the rest hold it says that too —
   once. A second Save writes it, because a document store allows it and the
   person may mean it (FR-12.4).

6. **Saving goes through the same writer, guard and commit as the grid's own
   changes.** The JSON is the review, so there is no second review panel, but
   a production connection still asks before anything is written, and the
   consent is given to the change that was shown: `commit` now takes the
   changeset a plan came from and plans it again with consent, rather than
   assuming the pending changes are the only source of one.

## Consequences

- A document is written as a single update, so two people editing different
  fields of one document do not overwrite each other; two editing the same
  field, the last save wins, as everywhere else in the application.
- The page view's text is not read: only the document being edited is saved.
  The entry is disabled while a page is shown, so what cannot be saved cannot
  be typed.
- A field added in the editor reaches the server under its own name even
  though the grid has no column for it; it appears as a column when the
  collection is sampled again.
