# ADR-0023: The platform's own file dialogs, and notifications from the background

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T1.3 · **Requirements:** FR-15.5, FR-15.6 · **Packages:** `internal/ui/filedlg`, `internal/ui/shell`

## Context

FR-15.5 asks for native file dialogs and native notifications. Fyne 2.8.1's
notifications are native already: UserNotifications from a bundled Mac app
(osascript when unbundled, as under `go run`), a toast on Windows, and the
desktop's notification service on Linux. Its file dialog is not. On macOS,
on Windows and on Linux outside a Flatpak it draws a dialog of its own inside
the window, with its own list of places and none of the system's: no
favourites, tags, recent folders, iCloud or network locations, and none of
the keyboard habits of the system's own. Only inside a Flatpak does it ask
the desktop.

## Decisions

1. **`internal/ui/filedlg` asks the platform.** On macOS, an NSSavePanel or
   NSOpenPanel, as a sheet on the window, as a Mac app's are (cgo and
   Objective-C); its answer comes back on the main thread through a
   `cgo.Handle`, and `fyne.Do` queues it for Fyne's loop. On Windows,
   `GetSaveFileNameW` and `GetOpenFileNameW` from comdlg32, through `syscall`
   with no cgo. With no hook or template these show the Explorer dialog of
   the Windows they run on. The dialog runs its own message loop, so it runs
   on a locked thread of its own with COM set up, owned by the window, which
   keeps drawing meanwhile. On Linux and the BSDs, the file chooser portal
   over D-Bus (godbus, already in the module graph through Fyne), which
   GNOME, KDE and the others answer with their own dialogs. Where there is
   none to be had, Fyne's dialog stands in with the same options: a window
   with no native handle (as a test's), a desktop with no portal, or another
   platform.

2. **A dialog answers with a path, not a writer.** It returns the path, or ""
   for a cancel, and the caller makes the file. Fyne's writer had made the
   file as the dialog closed. Now an export whose tab closed while its
   dialog was open makes no file at all. A failed export still removes the
   file it made.

3. **The portal is called directly.** rymdport/portal, which Fyne uses inside
   a Flatpak, unescapes the URI it gets back as a query, which turns a `+`
   into a space. `c++.sql` would come back as `c  .sql`, and an export would
   write to another file than the one chosen. Here the URI is parsed as a URI.

4. **The platform decides where a dialog starts.** Each remembers the last
   folder, and a save dialog is given the file name, its extension and its
   kind ("CSV Files"), and asks before replacing a file. A file field's
   Choose… starts in the folder of the file it names already, when that is a
   full path.

5. **Where they are used.** Export's Choose File… opens a save dialog whose
   button says Export, and whose message says what is exported and as
   what. Every file field on a connection form, which is SQLite's database
   for now, has a Choose… beside it that opens an open dialog.
   Import (T2.17) and the ER diagram's and charts' export will ask the same
   way.

6. **Notifications come only from the background.** The shell follows Fyne's
   lifecycle, which says when the window loses the focus to another
   application and when it gets it back. While the app is behind, two things
   that end send a notification. One is a task that finishes or fails. A
   cancelled task is not told, since whoever stopped it knows. The other is
   a query run of ten seconds or more that ends without being stopped. In
   front, the window says it already: the Tasks panel, the status bar, the
   tab's footer.

7. **A notification says only that the work ended, and how.** A task's
   notification carries its title and the line it ended with ("Exported
   250 rows to items.csv"), or "Failed. The Tasks panel says why." A query's
   carries its tab's title and the run's summary ("Ran 3 statements in
   12.4 s"). It never carries a statement or an error message.
   Notifications can be read over a shoulder and stay in the system's
   history, and an error can quote data.

## Consequences

- Fyne's test driver cannot drive a native dialog. The shell takes a
  `filedlg.Chooser`, which tests answer themselves. filedlg's own tests cover
  the stand-in dialog with its options, the filters, the Windows filter
  string and the portal's URIs, and check that a test window, having no
  NSWindow, gets the stand-in.
- The sheet, the Windows dialog and the portal have not yet been seen
  working. macOS's will be at the owner's first run, and Windows' and
  Linux's when CI and a person reach them. The Windows and Linux code is
  vetted for windows, linux and freebsd but has never run.
- On Wayland the portal is given no parent window, since Fyne exports no
  handle, so its dialog is not attached to ours.
- On Windows the button's label cannot be set without a hook, and a hook
  brings back the old dialog, so it says Save or Open.
- Unbundled on macOS (`go run`), notifications come through osascript and
  are shown as Script Editor's. The packaged app uses UserNotifications and
  asks permission the first time.
- The app has no setting of its own to turn notifications off. Each
  platform's own settings do that, per application.

## Not decided here

A click on a notification brings the app forward, as the system does, but
not to the tab or the task it was about.
