# ADR-0059: The completion popup

**Status:** Accepted · **Date:** 2026-09-12
**Tasks:** T2.27 · **Requirements:** FR-5.2 · **Packages:** `internal/ui/editor/view`, `internal/ui/shell`

## Context

ADR-0057 and ADR-0058 say what can be typed next. This is how it is shown
and taken. The editor is our own widget (RISK-2), so the popup is ours too.

## Decisions

1. **The popup is drawn inside the editor, not as a canvas overlay.** Fyne
   gives every overlay its own focus manager and `Canvas.Focused` reads the
   topmost one, so showing an overlay would stop the editor beneath it
   receiving the very keystrokes completion exists to help with. Drawn as a
   child of the editor, the focus never moves and the caret keeps blinking
   where the typing is going.

2. **It opens as a word is typed.** A letter, an underscore or a dot asks
   the completer and shows what comes back; a digit refines a word already
   being completed; anything else — a space, a bracket, an operator — ends
   the word and closes the popup. Nothing to offer closes it too: an empty
   popup says nothing and hides the text. Deleting refines what is open but
   never opens anything, because deleting is not asking. ⌃Space (Query ▸
   Complete) asks outright, which is how a column is offered where nothing
   has been typed yet.

3. **The keys the popup takes are the ones it needs**: ↑ and ↓ to move,
   wrapping at either end; Page Up and Page Down by a windowful; Return or
   Tab to accept; Escape to close. Everything else goes to the editor, so
   typing goes on refining the word. Moving the caret, clicking, a shortcut
   (⌘↵ runs the query) and losing the focus all close it.

4. **Accepting writes the candidate's `Insert` over the whole word being
   typed**, as one undoable edit — the word whole, not only the part before
   the caret, so completing in the middle of a name does not leave its tail
   behind. What is written is what the engine said to write: quoted where
   the name needs it, qualified where a column would be ambiguous.

5. **Ten rows at a time**, with the drawn window following the selection. A
   popup taller than that covers the text it is meant to help write.

6. **Each row says three things**: the candidate, in the colour its kind has
   in the text; its detail — a column's type, a table's schema; and its kind
   in words. The kind is written, not only coloured: colour alone would lose
   the distinction for anyone who cannot tell two of them apart (WCAG 1.4.1).
   The chosen row is filled, and the popup speaks the chosen candidate, its
   kind, its detail and its place in the list.

7. **It sits under the start of the word**, so the list lines up with what is
   being typed, above it where there is no room below, and never off the
   right edge.

8. **The editor knows no SQL.** It takes a `Completer` function — text and a
   cursor in runes, candidates back — and the shell wires one per query tab
   to a `sqlcomplete.Engine` built with the source's dialect and its
   `QuoteIdentifier` (ARCH-2), once the tab has connected.

## Consequences

- Completion runs on the UI goroutine, on the keystroke path, and never
  reaches the server: the engine answers from the dialect and its catalog
  (ADR-0057). NFR-P5's 16 ms covers lexing the statement twice and ranking
  what comes back.
- Until the schema cache fills the catalog (T2.29), what is offered is the
  dialect's own words: its keywords, types and functions.
- A popup taller than the editor's pane draws past it, under the results
  pane. It is clamped to the pane wherever it fits, so this is only an
  editor a few lines tall.
