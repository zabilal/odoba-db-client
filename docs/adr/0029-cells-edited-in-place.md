# ADR-0029: Cells edited in place

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.3 · **Requirements:** FR-4.1, FR-4.3 · **Packages:** `internal/ui/grid`, `internal/ui/shell`

## Context

FR-4.1 asks for a cell to be edited in place, with an editor fitted to its
type: text, number, date, bool, enum, JSON. T2.1 gave a table tab its
pending changes (ADR-0027) and T2.2 marked them in the grid (ADR-0028). No
editor put anything into them yet.

## Decisions

1. **Return, or typing, edits the active cell.** Return opens its editor
   with the value whole; a character typed opens it with that character in
   the value's place, as a spreadsheet does. A space still opens the cell
   viewer. Edit Cell on the Edit menu does the same. A double-click does
   not: Fyne holds every single click on a widget that hears double-clicks
   until the double-click wait has passed, and that would slow selecting.

2. **The editor is drawn inside its cell**, so it scrolls with the cell. The
   table recycles cells as it scrolls, so each cell drawn takes the editor
   when it is at the editor's place and gives it up when it is not. Opening
   an editor scrolls its cell into view: Fyne gives the keyboard only to
   something drawn.

3. **Values are typed as text and read as the column's type** (`Parse`).
   Whole numbers, numbers, decimals (kept as their digits), dates, times,
   dates and times, UUIDs and JSON are each checked. Text is kept exactly as
   typed; anything else is read with the spaces around it trimmed. Empty
   text is NULL, except in a text column, where it is the empty string. A
   column that cannot hold NULL refuses it. The editor starts from
   `EditText`, the value whole and in the form `Parse` reads. An instant is
   in local time, as the grid shows it, and may be typed with an offset. A
   time with no zone is as stored.

4. **Text left as it started is no edit.** A cell opened and closed changes
   nothing: NULL stays NULL, and no value is rewritten only by being read
   back.

5. **Return writes and moves down; Tab writes and moves right, ⇧Tab left;
   Escape leaves the cell as it was.** Leaving the cell writes it, as in a
   spreadsheet. Text that cannot be read, or a value refused, is said under
   the cell ("quantity: not a whole number"). The editor stays open with
   what was typed, and no other cell is edited until it is written or
   given up.

6. **True and false, and an enum's labels, are picked from a menu** under
   the cell, with NULL where the column allows it and the value it has
   ticked. The menu takes the keyboard.

7. **Bytes, arrays, composites and shapes are not edited in place.** Typed
   text is no way to write them.

8. **Set to NULL sets every selected cell** of the rows loaded. A column
   that cannot hold NULL is left, and the footer says so. The footer counts
   the rows with pending changes.

9. **The cell viewer edits a value at length.** Its Edit button, or Edit
   in Cell Viewer, opens the value in an editor of many lines, with JSON
   laid out on lines in a fixed font. A date or an instant also has a
   calendar: the day picked replaces the date and keeps the time of day
   typed. Done reads the text as `Parse` does and writes JSON compact
   again; text left as it started is no edit; what cannot be read is said,
   and the editor stays. Cancel or Escape gives it up. While editing, the
   viewer stays on its cell; it shows a cell's pending value, not only the
   value read. The calendar and the long editor are in the viewer rather
   than the cell because Fyne's calendar is made of buttons, which take the
   focus, and a cell's editor writes itself when it loses the focus.

## Consequences

- Whether a column can hold NULL is known only where the driver says so:
  SQLite and PostgreSQL's browses say every column can, so the server
  refuses a NULL at commit (T2.6).
- An enum's labels come from the column's type. The browses do not read
  them yet, so an enum is typed and checked by the server.
- The editor's Tab needs Fyne to ask `AcceptsTab`, which only a real window
  does; tests send the key to the editor.
