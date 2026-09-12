# ADR-0065: A collection's documents, in the grid and as JSON

**Status:** Accepted · **Date:** 2026-09-12
**Tasks:** T2.33 · **Requirements:** FR-12.1, FR-12.4, FR-3.1 · **Packages:** `internal/source/drivers/mongo`, `internal/ui/shell`

## Context

The grid is the product's centre, and it draws rows of columns. A collection
has neither: a document holds what it holds. FR-12.1 asks for both views —
the table and the documents themselves.

## Decisions

1. **The columns are the fields a sample holds** (ADR-0064): a browse infers
   a shape over fifty documents and makes a column of each field, in
   inference's order, `_id` first. A field only some documents have is a
   column that may be empty; a field seen with two types is neither, and the
   column says what was seen, because no renderer is right for both and the
   grid draws what the value is.

2. **A document keeps what the columns do not show.** A field the sample
   missed is not lost — it is in the document, and the JSON view shows it.
   An empty collection still has `_id`, so the grid has a column rather than
   nothing at all.

3. **Values come back as themselves.** An embedded document is a map and an
   array a slice, which the grid already draws as compact JSON and the cell
   viewer opens in full; an ObjectID is its hex, a Decimal128 its digits
   kept as text, a date an instant, a UUID written as one. Nothing is
   rendered to a string on the way out of the driver that the UI would have
   to parse back.

4. **The server does the work.** Filters become a query document — ANDed,
   with values bound and never written into text (NFR-S6) — ordering becomes
   a sort, and a page becomes skip and limit. LIKE becomes an anchored
   regular expression whose own characters are quoted. A count is
   `countDocuments` over the same filter.

5. **A condition in a query language is refused.** MongoDB's own is the
   console's (T2.37); until that exists, the grid's WHERE box would be a
   language nothing else in the application knows (REQ-DRV-3).

6. **A document is told from another by `_id`**, which MongoDB gives every
   document. That is `IdentityDocumentID`, and it is what will make a
   collection's rows editable (T2.34).

7. **The JSON view shows the documents in the grid's place**, a page of fifty
   at a time, opening at the page the grid was looking at. It is read-only
   here — editing a document is T2.34 — and it renders fields in the columns'
   order, which a Go map would lose.

## Consequences

- A browse costs one sample before its first page. Fifty documents is the
  compromise: enough that the columns are not one document's, few enough to
  fit inside NFR-P3's two seconds with the page itself.
- The columns follow the sample, so two browses of a changing collection can
  differ in their columns. The alternative — a fixed union over everything —
  is a full scan before the first row.
- A nested document's own field order is lost in the JSON view, because its
  value is a Go map. The top level, which is what a person reads down, keeps
  the order the server gave.
