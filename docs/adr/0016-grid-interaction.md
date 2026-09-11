# ADR-0016: The grid's interaction model

**Status:** Accepted · **Date:** 2026-09-10
**Tasks:** T1.49, T1.51–T1.56 (and T1.48, T1.50 as they land) · **Packages:** `internal/ui/grid`, `internal/app` (`browse.go`), `internal/ui/shell`

## Context

The grid shows a window of a result that may be far larger than memory
(ADR-0002, ADR-0011). Every interaction that reorders or narrows the rows has
to take that into account: a sort or filter over the loaded rows would be a
sort or filter over a fragment.

## Decisions

**The server sorts.** Clicking a column header asks for the object to be
browsed again in that order (`BrowseSource.With`), and the grid's model
switches to the new source. Sorting only what is loaded would sort a
fragment and pass it off as the table (FR-3.3).

**Header clicks follow the familiar pattern.** A column alone cycles through
ascending, descending and unsorted, replacing any other sort. ⇧-click adds
it as the next key, or cycles it within the sort. Headers show ↑ or ↓,
numbered when several columns sort. Fyne's tap event carries no modifier
keys, so a header cell notes Shift on mouse-down.

**Only what can be re-sorted is sortable.** A browse re-sorts on the server
if the source says it can (`capability.Data.ServerSort`). A query's result
cannot be re-sorted without running the query again, so its headers do
nothing. The grid only emits sort keys; the shell turns them into
`BrowseOptions`, so no SQL is built in the UI (ARCH-2).

**A result from a replaced source is dropped, not shown.** The model's
fetcher is swapped behind an atomic pointer, and every swap or invalidation
bumps a generation. A page or count begun in an older generation is
discarded when it lands. Without that, a page requested under the old order,
still in flight when the sort changed, would land in the new grid and show
rows in the wrong places. Reload Rows had the same latent flaw. A test holds
a fetch open across the switch; with the guard removed, it fails.

**The latest request wins.** Two quick clicks send two browses. The tab
numbers its sort requests and applies only the latest, and a sort that
fails puts the header back as it was.

**Filters are typed into the header, and the server applies them.** A
filterable grid has a field under each column's title (FR-3.5). Return
applies every column's filter at once; Escape clears one column's. Filtering
on each keystroke would send a query per letter to a table that may be
large. The grid only holds the text. `internal/app/filterexpr` parses it into
`source.Filter` values, which the driver's dialect renders with every value
bound, so the notation never becomes SQL in the UI (ARCH-2, NFR-S6):

| Typed | Means |
|---|---|
| `text` | contains, ignoring case (text columns); equals (others) |
| `=x` `>x` `>=x` `<x` `<=x` | the comparison |
| `!=x` `<>x` `!x` | is not x |
| `a,b,c` / `!a,b,c` | is one of them / none of them |
| `x..y` | between, inclusive |
| `~re` / `!~re` | matches the regular expression / does not |
| `NULL` / `!NULL` | is NULL / is not NULL |
| `a%b` | LIKE |
| `"…"` | literal text: commas, operators and NULL lose their meaning |

**Values are checked against the column's type.** `>abc` on an integer
column is refused, with the reason, before anything is sent. Decimals travel
as text, so no digit is lost on the way.

**"Not x" keeps the NULL rows.** `!x` and `!=x` become NOT IN, which the
drivers render so that NULL rows stay: NULL is not x either, and unticking x
in a picklist will mean the same. SQL's `<>` would drop them without a word.
`!NULL` is how to drop them.

**A filter that cannot be applied is marked, never half-applied.** If any
column's text does not parse, nothing is sent: the column's title turns red
and the footer names the column and the reason. A filter the server refuses
(SQLite has no regular expressions) leaves the rows as they were and is
marked the same way. The footer keeps the reason beside the row count until
a filter succeeds, so a page loading behind it cannot wipe it. A filtered
grid says so in the footer.

**Sort and filter compose.** Both go through one re-browse, numbered so that
only the latest applies. Each builds on what the latest request asked for,
not on what is on screen, so a sort clicked while a filter is still loading
keeps the filter.

**Recycled header cells are rebound.** Fyne's table reuses header cells as it
scrolls sideways. So the filter text lives in the grid, and a field shows
whichever column it is bound to. A focused field moved to another column
gives up the focus, so typing never lands in a column the person is not
looking at.

**A filter row does not make the rows taller.** Fyne makes every row as high
as the taller of its cell and header templates. So the header reports one
row's height, and the grid sets the header's real height itself. The fields
use a small control's metrics, so the header is two rows high, not three.
The first screenshot of the filter row had every data row at the header's
height; a test now lays the grid out and checks both.

**The picklist's values come from the server, counted.** A column's
distinct values are listed by `source.DistinctLister`. It takes the other
columns' filters, so the list offers only what can still be picked, and it
returns values most frequent first, so a list cut short at its limit keeps
the values most rows have. `BrowseSource.Distinct` leaves the column's own
filter out; with it, the list could only offer what is already picked. The
relational drivers answer with one `GROUP BY` under the same filters a browse
uses, and decode the rows with the browse's own decoder. So every listed
value round-trips: used as an IN filter, it selects exactly the rows it was
counted from. The conformance suite checks that on every engine, for every
column whose type can be grouped, against a full read, and requires one
column whose values repeat unevenly so the order is really tested. Mutating
the count, the filters or the order each makes it fail. Its first run found
a real fault: SQLite's driver reads a DATE's text as a time and writes a time
back in its own layout, so a date picked from the list matched no rows. A
time operand is now compared through `strftime` on both sides. Counting scans
the filtered rows, so it runs only when asked for.

**Picking from the list filters by the values themselves.** Right-clicking
a column's title, or View › Filter by Values… on the selected cell's column,
lists the column's values with their counts: searchable, each with a tick.
It opens as a popover at the click, not as a dialog, so the data stays in
view (UX principle 4); the first version was a modal dialog, which that
principle rules out.
Ticking everything removes the column's filter. Otherwise the shorter side
is sent: a few ticked become IN, all but a few become NOT IN. Both select
the same rows when every value is listed. When the list stops at its limit
(1,000) they do not, and the shorter side is what a person means: NOT IN
keeps the values not listed. The filter row shows the choice in its own
notation (`a,"b,c",NULL`, `!NULL`), but the filter sent carries the values
as the source typed them, so a date or a decimal filters exactly the rows it
was counted from. Edit that text and the text is the filter again.
Reopening the list shows what is picked. Closing it stops a count still
running, however it closes: a click outside hides a popover without telling
anyone, so a running count looks every 250 ms whether its list is still
open.

**A typed WHERE is the person's own SQL, held to being one condition.** The
WHERE bar (View › WHERE Clause, hidden until asked for) takes a condition in
the source's query language and ANDs it with the filters (FR-3.6). Unlike a
filter's values it cannot be bound: it is SQL, as a query tab's is.
`sqlscript.Predicate` checks that it is one condition: no `;`, no string,
comment or quoted name left open, brackets that balance, and no parameters,
since the statement's own are numbered around it. It goes in brackets on
lines of its own, so a trailing `--` comment ends there rather than
swallowing the `LIMIT` that bounds every read (NFR-P11). The dialect
classifies the finished statement and refuses it if it could change
anything, so `pg_terminate_backend(…)` is not a filter. Everything else is
the server's to judge: a WHERE it refuses leaves the rows as they were, and
the bar says why and keeps the text to correct. The WHERE composes with sort
and filters and narrows the picklist. The conformance suite checks on every
engine that it narrows, that a comment cannot lift the LIMIT, and that five
kinds of non-condition are refused.

**The statement is shown.** Under the WHERE field, the bar shows the
statement the grid runs, with its bound values listed after it rather than
spliced in, because that is how they travel (UX principle 6). It can be
selected and copied. Its first screenshot had the statement drawn over the
grid, for two reasons. Refreshing the bar alone left it the room it had
before, so the tab's frame now re-divides the room. And a wrapping label
cannot know its height before it is laid out, so the statement's lines no
longer wrap and scroll sideways instead. A test lays the tab out and checks
that the bar ends where the grid begins.

**Selection is blocks of cells, kept by the grid.** Fyne's table selects one
cell and hears no modifier keys, so the grid keeps its own selection
(FR-3.7): blocks of cells and an anchor. A click selects a cell; ⇧-click
stretches the last block from the anchor; ⌘-click (Ctrl elsewhere) adds or
removes a single cell; the arrow keys move the selection, and stretch it
with ⇧. Select Row, Select Column and Select All Cells are in the Edit menu,
and ⌘A selects everything. A whole column runs to the last row, even on a
grid that has not counted its rows. The grid's table is a thin subclass: it
notes the modifiers at mouse-down, and lets Fyne's own selected cell go after
each click, so that a second click on the same cell is heard. That subclass
must itself be what is on screen: Fyne hands mouse events to the object in
the tree, and the first screenshot of a ⇧-click showed one cell selected,
because the view held the Table inside the subclass. A test now checks what
the view holds.

**Copy puts tab-separated text on the clipboard.** ⌘C on a focused grid, or
Edit › Copy Cells, copies the rows that have a selected cell, in order. Each
row holds the columns any block covers, with cells outside the selection
left empty, so the paste stays rectangular. One cell is copied as its bare
text. Values are written as export writes them, exactly. Copy reads rows
that were never drawn, off the UI goroutine, and does not keep them, so a
large copy cannot evict what is on screen. It stops at 100,000 rows and says
to export instead, because a clipboard holds everything at once. ⌘C and ⌘A
are not on the menu bar, because the editor owns them too; each goes to
whatever has the focus. Copying uses the app's clipboard, not the window's:
the window's is deprecated, and under the test driver it forgets what it is
given.

**Copy As writes whole rows.** Edit › Copy As offers CSV, JSON and
Markdown, each with its header. Where ⌘C copies exactly the selected cells,
these write every cell of the rows that have a selected cell, in the columns
the selection covers. They describe rows, and a blank where the selection
skipped would misstate one. Markdown, now an export format too, right-aligns
number columns, escapes `|` and line breaks so each value keeps to its cell,
and writes NULL as `_NULL_`, apart from the text "NULL".

**Copy as INSERT writes literals, and each reads back as its value.** Copy
As › INSERT, on a table's rows, writes one INSERT a row into the table they
came from, rows whole as Copy As writes them. The statements are for a
person to keep or run elsewhere, so the values cannot travel beside the SQL
as they do in statements this application runs. Each dialect writes them as
literals through `source.RowScripter`, and a value it cannot write
faithfully is an error, not a guess. PostgreSQL gets typed NaN and infinity,
`'\x…'::bytea`, a date, a wall time or a time with its offset as the column
says, and arrays cast to the column's type. MySQL doubles quotes, but text
holding a backslash goes as `_utf8mb4 X'…'`, because what a backslash means
depends on `sql_mode`. SQLite gets 1 and 0 for booleans, and times in the
layout its date functions read. Tests read rows from a table, write them
into a twin through the generated INSERTs, and read them back unchanged, on
PostgreSQL, MySQL, MariaDB and SQLite. Writing them found that PostgreSQL
`time` values lost their fractional seconds when read; they now keep them.
A query's result has no one table to insert into, so it does not offer
INSERT.

**The cell viewer shows a value whole, beside the grid.** View › Cell Viewer,
or Space on a focused grid as Quick Look does, opens a panel in a split to
the right of the grid (FR-3.9). It is a panel, not a dialog, so the rows stay
in view (UX principle 4). It follows the selection and shows the active
cell's whole value, which a grid cell cuts at 200 characters. JSON is
indented without being re-encoded, so every number keeps its digits and
every key its place, and it is coloured by token. Bytes become a hex dump, a
time is shown in local time and in UTC, and text wraps. A value past a limit
(a million characters, 64 KiB of bytes) is shown from its start, and the
viewer says so; Copy Value always copies all of it. A row not in memory is
read for the viewer off the UI goroutine. What the viewer makes of a value
is decided in `internal/ui/cellview`, which draws nothing and is tested
without a window.

**Columns can be hidden, moved and frozen, and keep their numbers.** The
grid shows the model's columns in an order of its own (FR-3.2). View ›
Columns, or a right-click on a title, hides a column, shows all again,
moves one left or right, or freezes the columns up to one so they stay in
view as the rest scroll. A right-click on a title now opens this menu, with
Filter by Values… at its top where the source can list values. Sorts,
filters and the active cell's column speak of the model's columns, since
they are about the data. The selection speaks of shown columns, since a
block is what a person sees, and Copy and the cell viewer translate it.
Hiding or moving a column clears the selection, which no longer means what
it did, but a moved column's active cell goes with it, so it can be moved
again. A hidden column comes back after the column before it in the model.

## Not decided here

Column widths the grid remembers (T1.48), and type-aware rendering (T1.50). Each will be added here as it lands.
