# Accessibility in Ikigai DB

This page says what works, what does not, and why — including the part we
cannot fix ourselves. It is here because a known gap that is not written down
is a promise somebody will discover the hard way.

Last checked against Fyne v2.8.1.

## What works

**Everything can be done from the keyboard.** Every action in the application
is a command; every command is in the command palette (⇧⌘P) and on the menu
bar, and most of the ones you will use often have a shortcut. That includes the
things that are usually mouse-only:

- **⇧F10** opens the menu for whatever the window is on — the object selected
  in the sidebar, the column the grid's cursor is in, or the tab in front.
  These are the menus a right-click gives.
- Columns are hidden, moved and frozen by command; tabs are moved and closed by
  command; panes are split and joined by command.
- The grid is a spreadsheet: arrow keys move, ⇧ stretches the selection, Return
  edits, Space opens the cell viewer.
- **⇧⌘F** jumps to the sidebar's filter; typing there finds any object by name.

**The focus is visible.** Every control that can take the keyboard shows that
it has it, including the sidebar's tree, which draws a ring in the theme's
focus colour. This is held by a test that captures the window, focuses each
control in turn, and fails if the screen does not change.

**Contrast meets WCAG 2.1 AA** in both light and dark themes — 4.5:1 for text,
3:1 for large text and control boundaries. This is held by tests that enumerate
every foreground and background pair the window draws; a colour added without
being checked, or exempted with a stated reason, fails the build.

**Text can be made larger.** View → Text Size offers Default, Large and Larger.
Only the type scales: spacing and icons stay as they are, so a larger size
gives you larger text rather than fewer rows.

**Every icon-only control has a name in words**, held by a test. Nothing in
this window is a bare glyph whose meaning you have to guess.

## What does not work: screen readers

**Ikigai DB cannot be used with a screen reader today.** VoiceOver, Narrator
and Orca will announce the window and very little inside it.

This is not a decision we made about your needs; it is where the toolkit is.
The application is written with Fyne, which draws its own widgets onto a canvas
rather than using the platform's native controls — the same choice that makes
one binary behave the same on three platforms. A canvas has nothing for an
accessibility API to read unless the toolkit tells it, and in Fyne v2.8.1:

- The accessibility bridge to macOS and Windows exists, but only in a build
  made with `-tags accessibility`. Without that tag it is compiled out entirely
  and nothing is exposed. There is no bridge for Linux at all.
- Exactly three of Fyne's own widgets describe themselves to it: Button,
  Hyperlink and Label. The table, the tree, the text entry, the list, the
  select and the tabs — which is to say the data grid, the object explorer, the
  query editor and most of the rest of this application — expose nothing.

So even a build with the tag would announce some buttons and some labels, and
say nothing about the rows you are reading, the object you have selected, or
the text you are typing. We would rather say that plainly than ship a build
that looks supported and is not.

**What we have done anyway.** The controls this application draws itself — the
tab bar, its close buttons, the completion list — carry accessible names and
roles, so they are ready for the day the toolkit can pass them on. The rest of
the accessibility work above (keyboard, focus, contrast, text size) is real
today and is what makes the application usable without a mouse.

**If you want to try it**, `go build -tags accessibility` produces a build with
the bridge compiled in on macOS and Windows. It compiles; we have not been able
to verify what it announces, and we would genuinely like to hear from anybody
who tries it.

## What would change this

Screen-reader support is tracked as a known gap (NFR-A3 in the requirements,
deliberately not promised for v1.0). It becomes possible when Fyne's own
widgets describe themselves to the platform — the table and the tree above all.
That is upstream work, and we will follow it.

If you need a database client with a screen reader today, we would rather point
you at one that works than have you find out here.
