# ADR-0147: A workspace that holds nothing

**Status:** Accepted · **Date:** 2026-09-24
**Tasks:** T4.12 · **Requirements:** FR-15.9, FR-1.6, FR-5.9, FR-15.2, NFR-R3
**Packages:** `internal/store/localdb`, `internal/app`, `internal/ui/shell`, `internal/ui/explorer/view`

## Context

A workspace groups the connections a piece of work is over, the tabs it was
left at and the saved queries in it. Somebody with four customers' databases
and one of their own wants to see one customer's at a time, come back to the
tabs they left, and not read past the other three.

Everything a workspace would group is already kept, and kept somewhere it is
also used from: the connections are in the settings file, with folders of
their own; the saved queries are rows in the local database; the open tabs are
a session, written as they change so that a crash loses at most the autosave
delay. A workspace could hold its own copy of any of them.

## Decisions

1. **A workspace names, and does not copy.** It is `{ID, Name, Connections
   []string, Tabs Session, Used}`: the connections by ID, and the tabs as the
   same `Session` the window already writes. It holds no connection, no query
   and no query text. A saved query is in a workspace because its connection
   is, which is a rule and not a field — there is nothing to keep in step, and
   nothing that can disagree.

   The cost is that a workspace can name a connection that has since been
   deleted. That is read as what it is: the list says "3 connections, none of
   them still saved" rather than showing a row that opens onto nothing.

2. **Forgetting a workspace loses nothing.** It is one key deleted from the
   key-value table. Every connection, saved query and scratch buffer it
   grouped is still exactly where it was, because none of them were ever in
   it. This is the property that makes a workspace safe to make on a whim.

3. **The explorer narrows through one function on its loader.** `Loader.Shows
   func(connID string) bool`, nil for every connection. The tree has one place
   that lists connections, and this is a filter on it rather than a second
   listing. A folder the workspace has emptied is left out, because it would
   open onto nothing; a folder nobody has filled yet stays, because it is a
   shelf somebody just made and something is about to go in it.

4. **Leaving a workspace closes its tabs the way the application closes a tab,
   not the way a user does.** Unsaved query text goes on being kept, and going
   back opens the tabs it names — and only those, not every buffer a crash has
   left lying about. Closing a tab by hand still forgets its text: switching
   workspaces is not closing anything, it is putting it down.

5. **The window remembers which workspace it is in, in the session.** The
   session is the window as it was left, and which workspace it was in is part
   of that. It is read before any tab opens, because the explorer must not
   first show what the workspace is not over.

6. **A connection made inside a narrowed workspace joins it.** Otherwise it
   would be made and then not be there. A workspace over no connection in
   particular is left alone: it holds the new one already, and naming it would
   narrow the workspace to that one connection.

## Consequences

- Workspaces are per window, and a second window opens in none. Two windows
  can be in two workspaces over the same connections, which is the thing a
  second window was for (ADR on T4.11).
- A workspace's tabs are written when it is left and as the window closes,
  not continuously. A crash mid-workspace loses the tabs since the last
  switch, and the live session — which is written continuously — puts the
  window back regardless.
- Saved queries are narrowed by their connection, so a query saved against no
  connection is in every workspace. There is no way to put a query in a
  workspace and nowhere else, and nothing yet asks for one.
- Nothing here is a permission. A workspace narrows what is shown, not what
  can be reached: a query tab can still name any database the connection can,
  and read-only mode is the thing that stops a write (NFR-S4).
