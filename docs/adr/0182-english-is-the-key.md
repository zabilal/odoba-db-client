# ADR-0182: English is the key

**Status:** Accepted · **Date:** 2026-10-12
**Tasks:** T5.19 · **Requirements:** FR-15.11
**Packages:** `internal/l10n`, `internal/ui/…`, `internal/store`, `cmd/ikigai`

## Context

FR-15.11 asks for a localisation framework: English first, and no hardcoded UI
strings. The window was forty-one thousand lines that said what they said in
string literals, with tests that read those words back.

## Decisions

1. **The English is the key.** `l10n.T("Save as Job…")` is that sentence in
   the language in use, and the English where the language has none. Nothing
   is given a name before it can be said, the code still reads as what the
   window says, and a catalogue that is half finished is a window half
   translated, never one with holes or identifiers in it. The cost is that
   changing an English sentence orphans its translations; the list in decision
   6 makes that visible.

2. **A sentence with values is one sentence.** `l10n.F("%d rows into %s", n,
   table)` translates the whole and then fills it, because where a value goes
   is the language's to say. Text that was joined to values with `+` was
   rewritten as one format for the same reason.

3. **A translation may move its values and may not change them.** A
   translation says which value it means where it needs another order —
   `"%[2]s, davon %[1]d"`. One that asks for a number where the English gives
   text, or for a value the English does not give, is not used: its sentence
   is said in English and the slip is logged when the application starts. A
   translator's mistake should cost a sentence its translation and not print
   `%!d(string=…)` in somebody's window.

4. **Its own package, with nothing in it.** `internal/l10n` imports nothing of
   this project's and nothing that draws, so any layer that says something to
   a person can use it (ARCH-1). Fyne has a catalogue of its own; using it
   would have put the toolkit under every layer that words an error.

5. **Catalogues are compiled in, one JSON file a language.** The nearest is
   used — `de-AT` speaks `de` — and never another dialect's: `pt-PT` does not
   get Brazilian Portuguese for want of its own. The language is the
   `language` setting, or the system's from `LC_ALL`, `LC_MESSAGES` and `LANG`,
   or English.

6. **The code is read, and that is the rule.** A test parses the packages that
   draw and fails on any text given to a widget as it was written: a literal,
   a `fmt.Sprintf` of one, or one joined to values, as an argument to the
   calls that label things or as a `Title`, `Category`, `Message` or `Label`
   written out. The same reading lists every sentence that goes through the
   catalogue into `messages.json`, which is what a translator starts from, and
   fails when the file and the code disagree. With `-l10n.fix` it does the
   wrapping, which is how the nine hundred and more places that now go through
   the catalogue were changed without one being typed.

7. **A pseudo-language proves it.** `en-XA` is made from English as it is
   asked for — every vowel accented, the whole in brackets. Text that is plain
   under it never went through the catalogue. A test starts the window in it
   and requires every command, in the palette and the menus, and every menu to
   be bracketed; in English, that nothing is.

8. **What is said before a language is set is marked and said later.** A table
   made when its package is — the menu bar, the accent colours, the text
   sizes, a connection's environment badge — is built before any language is
   chosen. Its words are marked with `l10n.N`, which is what lists them, and
   translated where they are shown.

## What is not done

- **There is no translation.** No catalogue is compiled in. This is the
  framework the requirement names; a language is a file.
- **Not everything a person reads goes through it.** The rule holds for text
  given directly to a widget. It does not reach a label held in a constant or
  a variable (78 places pass one), a single lower-case word, which the rule
  takes for a name the program uses, the options of a pop-up or a radio group,
  sentences worded in helper functions and handed on as text, column headers,
  errors worded below the window, the command line, or the toolkit's own
  dialogs, which Fyne translates by its own catalogue and the system's
  language.
- **Plurals.** "1 row" and "3 rows" are still chosen in English, by the code.
- **A menu the system treats by name.** Fyne finds the Help menu on macOS by
  its English title; translated, it would be an ordinary menu.
- **Choosing a language in the window.** It is a key in the settings file.
- **Right-to-left layout**, dates and numbers.

## Consequences

- No dependency.
- In English nothing changed: every test that read a sentence back still reads
  it.
