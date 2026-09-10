# ADR-0016: The grid's interaction model

**Status:** Accepted · **Date:** 2026-09-10
**Tasks:** T1.49, T1.54, T1.55 (and T1.48–T1.56 as they land) · **Packages:** `internal/ui/grid`, `internal/app` (`browse.go`), `internal/ui/shell`

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
Ticking everything removes the column's filter. Otherwise the shorter side
is sent: a few ticked become IN, all but a few become NOT IN. Both select
the same rows when every value is listed. When the list stops at its limit
(1,000) they do not, and the shorter side is what a person means: NOT IN
keeps the values not listed. The filter row shows the choice in its own
notation (`a,"b,c",NULL`, `!NULL`), but the filter sent carries the values
as the source typed them, so a date or a decimal filters exactly the rows it
was counted from. Edit that text and the text is the filter again.
Reopening the list shows what is picked, and closing it stops a count still
running.

## Not decided here

The WHERE editor and the effective SQL
(T1.56), selection and copy (T1.51–T1.52), the cell viewer (T1.53), and
column resize, reorder and hide (T1.48). Each will be added here as it lands.
