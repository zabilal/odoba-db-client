# ADR-0186: More than one caret

**Status:** Accepted · **Date:** 2026-10-12
**Tasks:** T5.23 · **Requirements:** FR-5.15
**Packages:** `internal/ui/editor`, `internal/ui/editor/view`

## Context

FR-5.15 asks for multiple cursors and block selection in the editor. The
editor is this project's own (REQUIREMENTS §8.4): a document that holds the text, one
caret and one selection, and a view that draws it.

## Decisions

1. **What happens at several carets is what happens at one, done at each.**
   Typing, deleting, a new line and Tab are the single-caret operation run at
   every caret, from the last in the document to the first, so that no edit
   moves text a caret still to be visited is in; the carets already visited
   are carried along by each edit above them. Nothing was written twice, and
   every rule the editor has for one caret — a new line keeps its line's
   indentation, Backspace in leading spaces goes back a stop — holds at each
   of five.

2. **A block selection is carets on consecutive lines.** There is no second
   kind of selection: a block is, on every line from the first to the last,
   what lies between two visual columns, with a caret on each. A line that
   ends inside the block has what it has of it selected, and one too short to
   reach it has a caret at its end, so typing still lands on every line.

3. **One keystroke is one step to undo.** The changes a keystroke makes at
   several carets are tied together, taken back together, and every caret is
   put back where it was. With several carets each keystroke is its own step;
   with one, a run of typing still undoes a word at a time as it did.

4. **Carets that meet are one.** Two at one place become one, and two
   selections that overlap become one selection. Two selections that only
   touch stay two.

5. **There is a first caret, and the rest of the editor knows only that
   one.** Completion, bracket matching, the current-line highlight, the
   statement Run runs and where the view scrolls to are the first caret's.
   The one most recently placed is the first, because that is where the eye
   is.

6. **Placing the caret leaves one.** A plain click, a ⇧-click, a double-click,
   Select All, indenting lines, Escape. That is what a click means in every
   other editor with this feature, and it is the way out.

7. **Copy takes each caret's selection, a line apiece; Paste puts back.**
   Text of as many lines as there are carets is dealt out, a line to each,
   which is what makes copy and paste of a block round-trip. Any other text
   goes in whole at every caret.

8. **The keys and the mouse.** ⌥-click adds a caret, or takes away the one
   clicked. ⌥-drag selects a block. ⌥⌘↑ and ⌥⌘↓ add a caret on the line above
   the highest or below the lowest, at the same column, through lines too
   short to hold it. Elsewhere than macOS, Alt and Ctrl+Alt. The two chords
   are among those the editor keeps from the menu bar.

## What is not done

- **Completion and snippets at several carets.** Accepting a candidate
  places the caret, and so leaves one.
- **Indenting lines with several carets** (⌘], ⌘[, Tab over a selection of
  lines) acts on the first caret's lines and leaves one caret.
- **Find's "select every match"**, and adding the next match of the word
  under the caret (⌘D elsewhere). Carets are placed by hand.
- With several carets, a run of typing is undone a keystroke at a time rather
  than a word at a time.
- A caret past the visible lines is not scrolled to; the view follows the
  first.
