# ADR-0025: Connection folders

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T1.27 · **Requirements:** FR-1.6, FR-2.3, UX principle 9 · **Packages:** `internal/app`, `internal/ui/explorer`, `internal/ui/explorer/view`, `internal/ui/shell`, `internal/ui/theme`

## Context

FR-1.6 asks for connection folders, with a colour and an icon. The
settings file and `internal/app` had folders already: a folder could be
made, renamed or deleted, and a connection moved into one, and the store
refuses a connection in a folder that does not exist. But nothing showed
them: the explorer listed every connection at its top level. UX principle 9
says colour carries meaning, never decoration.

## Decisions

1. **The explorer lists the folders first**, in the order they were made,
   each holding its connections; then the connections in no folder. A
   folder with nothing in it has no expander.

2. **A folder's connections are listed as soon as the folder is**
   (`explorer.Item.Eager`). They come from the settings file, so listing
   them costs nothing. The filter (FR-2.3, ADR-0018) searches only what is
   loaded, so without this a connection in a closed folder could not be
   found by its name. The filter's "not searched" count now counts every
   connection not opened, wherever it sits (`explorer.Item.Connects`), not
   the tree's top level.

3. **A folder's colour is a dot beside its name**, as Finder marks a tag.
   It is one of the eight accents, kept by name in the settings file. The
   dot is drawn in the shade the palette gives that accent as text, and a
   test holds that shade to 3:1 on the sidebar and on the content, in both
   appearances. The name says which folder it is, and the dot only groups
   what the eye finds. Principle 9 allows it because the meaning is the
   user's own.

4. **Commands.** New Folder…, Edit Folder… (name and colour) and Delete
   Folder… are in the Connection menu and in a folder's context menu,
   which also offers New Connection…. Delete Folder… asks first, and keeps
   the folder's connections, which move out of it to the top of the list.
   A connection is put in a folder, or taken out, from its form. The form
   offers Folder only when there are folders, and a new connection starts
   in the folder selected.

5. **A folder left open reopens at the next start.** Only connections a tab
   uses reopen (ADR-0011 §10), because opening a connection connects to
   it. Opening a folder connects to nothing, so that rule does not apply.

## Consequences

- Not done: an icon of the user's choosing, which FR-1.6 names; a folder
  has the folder icon. Nor can a connection be dragged into a folder, or a
  folder be put inside another or moved in the list.
- A connection's own `color` in the settings file is still unused.
