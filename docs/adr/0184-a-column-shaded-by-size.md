# ADR-0184: A column shaded by size

**Status:** Accepted · **Date:** 2026-10-12
**Tasks:** T5.21 · **Requirements:** FR-3.17, NFR-A2
**Packages:** `internal/ui/theme`, `internal/ui/grid`, `internal/ui/shell`, `internal/app`

## Context

FR-3.17 asks for conditional formatting, a heat map, on numeric columns: a way
to see where the large values are without reading every one.

## Decisions

1. **Heat Map is a command on the column in front, and a toggle.** View ▸
   Columns ▸ Heat Map, and in the palette. It is offered on a column of whole
   numbers, floats or exact decimals, on a table whose source measures
   columns. Asked again, the column is unshaded.

2. **The range is the server's.** A cell is shaded by where its value falls
   between the column's smallest and largest — and the grid does not know
   those: it holds the pages somebody has scrolled through, and lets go of
   them. A picture drawn from what is in hand would change as a person
   scrolled, and the darkest cell on screen would not be the largest in the
   table. So the two ends are asked of the source, the way a column is
   measured (FR-3.14), over the rows the tab's own filters select, and the
   footer says what they were.

3. **Narrowing measures again.** When the rows a tab shows change — a filter,
   a typed condition — each shaded column is measured again, so the shading
   is always of what is being looked at. An answer to a question nobody is
   asking any more — the tab narrowed again, or the column unshaded, while the
   server was counting — is dropped.

4. **The text wins.** The smallest value is left as the grid is and the
   largest goes thirty-five hundredths of the way to the accent colour. That
   figure is held by a test: on every accent, light and dark, a number on the
   hottest cell is at AA contrast, the least of them 5.9 to 1. A stronger
   colour would make a better picture and worse text (NFR-A2).

5. **Colour is not the only sign.** The number is printed in every cell as it
   was. The shading lets an eye find the large ones; somebody who cannot tell
   the tints apart has lost a convenience and no information.

6. **What has no size is not shaded.** A cell holding nothing, or not a
   number, or not-a-number, is drawn as any other. A column whose values are
   all one value is not shaded at all, and that is said: a column tinted one
   colour would look like a finding.

7. **A change and the selection are drawn over it.** A cell about to be
   written has its own tint, and what is about to be written matters more
   than how big it is.

8. **A value past the range is drawn as the nearer end.** After a commit or a
   refresh the rows may hold a value larger than the largest that was
   measured. It is drawn as the largest rather than measured again on every
   reload.

## What is proven

- The journey that edits a row ends by shading its key's column on SQLite,
  MySQL, MariaDB and PostgreSQL, and each says the range in its own type for
  a number.

## What is not done

- **Rules.** "Colour the rows where status is overdue" is conditional
  formatting too, and is not built: this is the heat map the requirement
  names, and nothing a person writes a condition for.
- **A query's result** is not a table a source measures, and is not offered a
  heat map.
- The shading is not kept: a tab opened again is unshaded. Nor is it measured
  again when the rows are refreshed or a commit lands.
- The scale is linear from smallest to largest, so one very large value
  leaves everything else pale.
- A shaded column loses its rows' alternate stripe.
