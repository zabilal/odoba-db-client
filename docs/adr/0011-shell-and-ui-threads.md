# ADR-0011: The shell, where UI work runs, and tables without a row count

**Status:** Accepted · **Date:** 2026-09-10
**Tasks:** T1.1–T1.12, T1.24, T1.41 · **Packages:** `internal/ui/shell`, `internal/ui/uithread`,
`internal/ui/explorer/view`, `internal/ui/grid`, `internal/e2e`, `cmd/ikigai`

## Context

Everything below the UI existed (ADR-0008–0010) but nothing opened a window.
Wiring it to Fyne forced four decisions that the code alone does not explain.

## 1. UI work goes through an injected Runner

Fyne wants widget work on its main goroutine, via `fyne.Do`. Calling `fyne.Do`
from a data callback is right in the app and wrong under test: Fyne's test
driver runs the function at once, on the calling goroutine, so a page-load
callback ends up refreshing widgets from a fetch goroutine. The race detector
caught exactly that in the explorer (text shaping from a timer goroutine).

**Decision.** Code that schedules UI work takes a `uithread.Runner`
(`func(func())`). The app passes `uithread.Fyne`; tests pass a
`uithread.Queue` and drain it on the test goroutine, which then stands in for
the UI goroutine. `uithread.Coalesce` turns a burst of triggers into one run
and replaces the grid's own flag (ADR-0002).

Every asynchronous result in the shell arrives through the Runner and checks
its tab's context first, so a result that lands after its tab closed is
dropped rather than drawn into a detached widget.

## 2. Every shortcut is a menu item, and only that

From Fyne 2.8.1's desktop driver:

- On a key press the window tries the main menu's shortcuts *before* the
  focused widget, on every platform. A focused `Entry` swallows every shortcut
  it does not own, so a shortcut registered on the canvas is dead whenever a
  text field has focus. A menu shortcut is not.
- On macOS the native menu owns its key equivalents. Cocoa hands the event to
  the menu and never to GLFW's `keyDown`, so it fires once. Registering the
  same chord on the canvas as well would add a second path; we do not.
- Fyne's menu-shortcut matching calls an item's `Action` even when the item is
  `Disabled`. So every menu action goes through `Registry.Run`, which checks
  `Enabled` again.
- The native macOS menu reads `Disabled` as it opens, with no hook to compute
  it then. The shell recomputes enabled and checked state on each selection,
  tab and connection-status change, and refreshes the menu only if something
  changed.

**Decision.** The menu bar is declared as command IDs. A test fails any
command that has a shortcut but no menu item, and any two commands sharing a
chord. Phase 1's chords: ⌘K palette · ⌘N new connection · ⌘W close tab ·
⌘R refresh sidebar · ⇧⌘R reload rows · ⌘↓ open data (Finder's Open) ·
⌘0 toggle sidebar · ⇧⌘] and ⇧⌘[ next and previous tab. Off macOS, ⌘ is Ctrl.

## 3. A table with no row count grows as it is read

PostgreSQL reports no exact count, because counting is a full scan
(`Data.ExactCount` is false), so a browse's count is -1. The grid sized itself
by resident pages until a count arrived. With nothing resident that is zero
rows, so no cell was drawn, so no row was asked for, so no page was fetched:
**every real PostgreSQL table opened empty.** The grid's synthetic fetcher
always counts, which hid it. The J1 journey test below would have caught it.

**Decision.**

- The model tracks how far fetches have reached. A short page is the end of
  the data (the `Fetcher` contract). It makes an unknown total known, and
  corrects a known one that disagrees.
- With the total unknown, the grid shows the rows loaded so far plus one page
  of placeholders. Drawing those placeholders is what fetches the next page.
- `Invalidate` keeps a counted total until the next count replaces it, so
  Reload Rows does not collapse the grid to one page and lose the scroll
  position. If a filter shrank the data, the first short page corrects the
  total sooner. A total known only from reaching the end is dropped.
- The tab's footer says "1,024+ rows" until the end is found, then the count.

**Consequence.** An uncounted table's scrollbar is not proportional until the
end is reached. The estimate the sidebar already shows (`~1.2M`, from
`reltuples`) could size it provisionally. That is deferred until the grid can
mark an estimate as one, because an estimate must never pass for exact.

## 4. The connection form is data

The form's fields come from the driver's `Descriptor`. Host, port, database
and user map to the saved connection the way `connstr` maps them. Secret
fields go to the keychain through `app.Connections`, and anything else goes
into `Params`. Blank fields take the driver defaults their placeholders show.

A pasted URL fills the form and is then cleared, because it may hold a
password, which belongs in the masked field. Parameters that have no field
survive a save. Editing never reads a saved secret back into the form, and a
blank password keeps the saved one.

## 5. Testing without a display

Shell tests run on Fyne's test driver, with a Queue as the Runner. Their fake
driver registers as `postgres` so pasted URLs resolve to it, because depguard
keeps real drivers out of `internal/ui`. `internal/e2e` holds journey tests
over real drivers. J1 (tag `conformance`) walks the sidebar to a table on
PostgreSQL, runs Open Data, and waits for the footer's row count. For
PostgreSQL that count can only come from rows actually fetched.

## 6. Reviewing without a window server

`internal/e2e`'s `TestScreenshots` drives the real shell over a SQLite
fixture and renders its main states to PNG files, through Fyne's software
painter and the real theme. Run it with
`IKIGAI_SCREENSHOTS=dir go test -run TestScreenshots ./internal/e2e/`.
It is opt-in, so the ordinary run writes nothing.

Its first run found six problems, one of them a crash:

- **The find bar's fields had negative widths.** The bar was laid out while
  hidden, and showing it re-laid out only the bar, still zero wide, not the
  container that holds it. The fields were unusable, and the software
  painter crashed on them. It now re-lays out its parent when shown or
  hidden, and a test checks both fields get room.
- **The match count drew clipped** ("4" for "4 matches"), for the same
  underlying reason: a label that grows does not make its container re-lay
  out. The count now has a fixed width, which also keeps the buttons still.
- **The grid showed the window through it** around and between cells; it now
  has its own content background.
- **Alternate-row stripes drew as grey blocks,** because a cell can tint only
  its own width. Rows are plain until stripes can be drawn at full width
  (T1.50).
- **The status line read "local —"** when connected; it now says
  "Connected".
- **The tree used arrows** for disclosure, and now uses the chevrons the
  design system asks for (ADR-0006).

## 7. An error is a band across the top of the window

An error that is not a form's own answer is said in a band across the top
of the window, over the sidebar and the tabs alike (FR-15.7): its first line, an action where there is one, Details for the
rest, Copy for all of it, and a close button. It replaces Fyne's error
dialog, which was modal, covered the data and could not be copied, against
UX principle 4. A later error replaces an earlier one, and the band
re-divides the window as it opens, as the WHERE bar does (ADR-0016). An
error about a side panel's list, such as a failed delete in Saved Queries,
is said on the panel's own line, beside the list it concerns (§11). A test fails if the modal error dialog
comes back. The band's tint is drawn, not themed, so an appearance change
repaints it: the first version kept the dark tint under light-mode text,
and could not be read.

## 8. Tabs move and pin, by command

Tabs move left and right, and pin, from the Window menu (FR-15.2). Pinned
tabs stay together at the left, in the order they were pinned, marked with
a pin, and a tab moves only among the tabs of its own kind; new tabs open
after the pinned ones. Fyne's tab bar cannot be dragged, so moving was by
command at first; dragging came with a tab bar of our own (§18).

## 9. Unsaved query text is kept as it is typed

A query tab's text that is saved nowhere else is written to the local
database within a second of an edit (NFR-R2). Writes are throttled, not
debounced, so steady typing is still kept every second. The text lives in
the key-value table under `scratch/`, one entry per tab. One goroutine
writes the entries in turn, the latest word on each replacing any not yet
written, so text forgotten after a save is never written back by a slower,
earlier write.

Saving forgets the text, as does closing the tab on purpose or deleting its
connection, whose dialog says the tabs close. Quitting and a disconnect keep
it, and so does a crash, up to the last second. At the next start each kept
buffer reopens as a tab on its connection, still marked unsaved, and edits
to a saved query still save in place. Text whose connection has gone
reopens in a tab that does not connect and says why: it is never thrown
away unseen. An unreadable entry is left where it is and named, and a
failed write is said once in the error band. Only unsaved query text comes
back at the next start; the rest of the session (T1.14) does not yet.

## 10. The session comes back at the next start

The window is saved as it is left: its size, the sidebar's width, and the
tabs in order with their pins and which one is selected (FR-15.2, NFR-R3).
An object tab keeps the filters, sort and WHERE clause its rows are in, by
column name, so that a column added or dropped since cannot carry them onto
another. A query tab names its scratch buffer (§9) and its saved query. The
session goes through the same writer as the scratch buffers, within a
second of a change and again at quit, so a crash loses at most a second of
it.

At the next start the session is read before the window first shows, since
it is small and local, and each tab reopens and connects as if opened by
hand. A reopened object's view is put back once its rows first show, and
the WHERE bar comes back shown: a condition quietly applied would hide rows
without saying so. What no longer fits is said in the error band, not
dropped unseen: a filter or sort on a column that has gone, a filter that no
longer reads on its column's type, or a WHERE clause the source can no
longer take. An object on a deleted connection stays closed; nothing unsaved
goes with it.

A table's column layout comes back too, by name: the order its columns are
shown in, which are hidden, how many are frozen, and any width a person set.
Only a layout someone changed is saved, so a column added since appears
last and one dropped is simply not there; a width left alone follows the
column's type, as it would in a new tab. A resize or move is saved within
a second, as any other change is.

The explorer's open nodes come back too, but only under connections an open
tab is already using. Opening a connection's node connects to it, and
starting the app should connect to nothing a tab does not need, so a
connection left expanded without a tab comes back closed.

Not yet restored: the grid's scroll position. Fyne's table keeps its offset
to itself, so it can be set but not read.

## 11. History and Saved Queries are panels, not dialogs

Query History and Saved Queries open in a panel beside the tabs, not in a
modal dialog over them (UX principle 4), so the data they are about stays in
view. One panel is open at a time, in a split the person can widen, and a
width given to one is kept when the other replaces it. Each command toggles
its panel and the menu ticks the one open; Escape in the panel's field, or
its Close button, closes it. The Close button has words, not a bare ×, so
that it has a name to be read out. Opening an entry leaves the panel open,
as an inspector stays, and the same entry can be chosen again. Confirming a
delete and naming a query to save are still sheets: each asks one question
and waits for the answer, which is what a sheet is for. The commands lost
their trailing ellipsis, which on the Mac promises a question before acting.

## 12. A keyboard-shortcut reference, on ⌘/

Help › Keyboard Shortcuts (⌘/) opens a third side panel (§11) listing every
command that has a shortcut, whether or not it can run just now, by
category and title, with the keys as the platform writes them (FR-15.4).
Each word typed must match the title, category, keywords or keys, so "save"
and "⌘S" both find Save Query. It reads the command registry each time it
opens, so bindings a person changes (T1.8) will show there with no more
work. It is for reading: running a command is the palette's job.

## 13. The explorer's context menu is made of commands

Right-clicking a node in the explorer selects it and shows a menu of the
commands that act on it (FR-2.4): Open Data, Favourite and Refresh for an
object, and Edit, Duplicate, Refresh, Disconnect and Delete for a
connection. Loading and error rows have none. The items are the registered
commands, so their titles, shortcuts, ticks and disabled states are the
menu bar's, worked out as the menu is made. They are not registered as
the menu bar's items are, since `sync` keeps only those up to date, and a
pop-up that took their place would leave the menu bar stale. A test holds
that line.

## 14. The structure tab shows what Describe returns

Explorer › Open Structure (⌥⌘O, and in the explorer's context menu) opens
a read-only tab for a table, view or collection (FR-2.4). It shows what the
driver's Describe reports, through `app.Describe`, which contains a
driver's panic as every other way into a driver does (ADR-0017). For a
table that is its comment and estimated row count, then columns (type,
null, default, identity, auto-increment or generation, comment), primary
key, indexes, foreign keys, unique and check constraints, and triggers; for
a view, its definition and columns. Sections with nothing in them are left
out, and every cell can be selected and copied. Like a data tab, it opens
at once and fills in when the description arrives, and it comes back with
the session as a structure tab. Editing a structure is the table designer's
(T3.x), and writing a CREATE statement from one is T3.7's.

## 15. Script as writes a starting point, in the dialect's words

Explorer › Script As writes a SELECT, INSERT or UPDATE for the selected
object and opens it in a new query tab, unsaved, for the person to edit and
run; nothing is run for them (FR-2.4). The columns and key come from the
driver's Describe, and every quoted name and placeholder from the source's
dialect, so the statement is the engine's own and no statement text is
built in the UI (ARCH-2): `sqlscript` only lays the statement out, and
`app.ScriptAs` fills it in. SELECT names every column. INSERT leaves out
what the server fills in itself: generated, identity and auto-increment
columns. UPDATE sets the columns outside the primary key and matches on the
key; a table with none is matched on every column, and a comment in the
script says so. A view is written only as SELECT, and a source with no
query language cannot be scripted. Writing a CREATE statement is T3.7's.

## 16. Custom shortcuts are kept, and the menu bar is rebuilt

A command's shortcut can be changed (FR-15.4, T1.8). The registry's
`Rebind` refuses a chord another command has, as macOS and other platforms
each see it, which is the check `Register` makes at startup; each command
keeps the shortcut it was registered with, to go back to. A change is kept
in the settings file in a form that reads back the same on every platform,
`Shift+Shortcut+F`, the `+` key included (`Shortcut++`): modifiers are read
off the front and the rest is the key. One set back to its default leaves
the file. The
menu bar is rebuilt on every change, since its items are what make a
shortcut work (§2). At startup the kept shortcuts are applied once every
command is registered and before the menu bar is built. One that no longer
fits, for a command that has gone, in a form that does not read, or on a
chord another command now has, is said once and left in the file untouched,
rather than dropped unseen.

A shortcut is changed in the Keyboard Shortcuts panel (§12): each row has
a Change… button, which opens an editor in the panel rather than over it.
Its modifiers are ticked and its key chosen from the keys Fyne delivers as a
shortcut, rather than pressed. On a Mac the native menu bar takes a chord it
holds before the application sees it, so a chord already in use could never
be pressed there; and a choice made by ticking can be read out. A letter,
digit or punctuation key needs ⌘, ⌃ or ⌥ with it, since with only ⇧ it would
type. A chord another command has is refused with that command's name. The
editor also sets a command back to its default, or to no shortcut; and a
box lists the commands without one, to give them one.

## 17. Long work is a task, beside the window

An export showed its progress in a sheet over the window, which kept the
window from use until the export ended; UX principle 5 sends long work to a
task centre instead (FR-15.6, T1.10). A task (tasks.go) has a title, a
status line, a share done when the total is known, a Cancel, and an end:
done, failed or cancelled. The Tasks panel (§11's side panel, Window ›
Tasks) lists them newest first, with a bar that measures when the total is
known and one that moves without measuring when it is not. A finished task
stays, saying how it ended, until Clear Finished. The status bar says while
any task is running (the one task and how far it has got, or how many are
running) and opens the panel. Exports are the first tasks; long queries and
imports are to join them.

A task stops with the tab it works from, so closing that tab asks first, as
quitting does while any task runs; the question says what stopping loses.
The window's close intercept asks it, and Fyne's Quit comes the same way.
Quitting then waits, up to the three seconds it gives connections, for
stopped tasks to clean up. An export removes its partial file, and one
stopped by quitting could otherwise leave a file that looks complete.

## 18. The tab bar is our own, and a tab is dragged

Fyne's DocTabs cannot drag a tab, so §8 moved tabs by command only. The tab
bar is now internal/ui/tabbar. It holds the same *container.TabItem and
keeps the selection as DocTabs does (the first tab added is selected;
removing the selected tab selects the one taking its place), so the shell
uses it as it used DocTabs. A tab dragged along the row shows an accent
line where it would land; let go, it moves there and is selected, kept
among the tabs of its kind as a move by command keeps it. Only the drag's
distance is used, since Fyne's drivers report where the pointer is
differently. A secondary tap on a tab selects it and opens a menu of the
Window menu's commands (§13). Tabs that do not fit scroll, the selected one
kept in view, and an All Tabs button lists them all.

The selected tab is marked by more than colour: its title is bold and
underlined, on the control background. The line, like the mark where a
dragged tab would land, is in the accent's text colour: the plain accent is
2.99:1 against the control background in dark, short of the 3:1 a mark
needs, and the contrast test now holds both colours to it. Each tab keeps its
width when its title turns bold. A tab's close control is an ×, shown on the
selected tab and the one under the pointer. It is named "Close" and the tab's
title for a screen reader through Fyne 2.8's accessibility interface, and
the tab is named by its title. The words ADR-0006 asks of buttons stay with
buttons: a word on every tab's close control would not fit.

Not done: the row does not scroll while a tab is dragged past its edge, so a
tab going to a place out of view goes by All Tabs or by command.

## 19. Two panes of tabs

The tabs can be split into two panes, side by side (Split Right) or one
above the other (Split Down), each with its own tab bar (FR-15.2, T1.5).
Splitting moves the active tab into the new pane, so it needs a second tab
to leave behind: an object has one tab, and a pane is never empty. Move Tab
to Other Pane moves a tab across, after the tabs of its kind there. A pane
left with no tab closes, and Join Panes puts the second pane's tabs after
the first's, pinned with the pinned. The commands are in the Window menu,
and Move Tab to Other Pane is in a tab's menu too.

Commands act on the pane worked in: the one holding the keyboard's focus,
or else the one whose tab was last chosen, so ⌘C copies from the grid a
person last clicked in, whichever pane holds it. The pane is found by where
the focused widget is, since Fyne cannot say which container holds a
widget. Choosing a tab in one pane lets go of a focus left in the other, so
the keyboard and the work are never in different panes. The menu's enabled
items follow the pane worked in as of the last change, and a command checks
again as it runs.

The session keeps each tab's pane, the tab in front in each, the split's
direction and the first pane's share, and puts them back at the next start.

Not done: a tab is not dragged from one pane to the other.

## Not decided here

Single-instance handling is ADR-0022's.
A quit by signal, which Fyne turns into its driver's Quit without closing
the window, skips the close and so the wait for tasks. When a connection has no saved password, the tab offers
"Edit Connection…". A proper prompt with a "remember" choice is still to be
designed. Nothing here has been looked at in a real window yet; the first
`go run ./cmd/ikigai` is the first visual review.
