# ADR-0003: A hand-written SQL lexer, not Chroma

**Status:** Accepted · **Date:** 2026-09-10
**Spike:** W2 (TASKS.md T0.45–T0.49) · **Risk:** RISK-2

## Context

The query editor is the largest single build risk in the project. Nothing in
Go approaches CodeMirror, and ADR-0001 committed us to building one.

The plan (T0.45) named Chroma as the lexer, so Chroma was measured first,
against a 5 000-line, 256 KB PostgreSQL workload containing the constructs that
break naive highlighters: string literals containing `/*` and `--`, nested block
comments, and dollar-quoted function bodies.

## Measurement

| | Chroma | Hand-written | Factor |
|---|---|---|---|
| Full 5 000-line buffer | 261 ms | **1.02 ms** | 256x |
| Single line | 190 µs | **0.75 µs** | 254x |
| 60-line viewport | 3.19 ms | **0.011 ms** | 279x |
| Throughput | 0.98 MB/s | **213–251 MB/s** | — |
| Allocations, full buffer | 1 908 554 | **12 981** | 147x fewer |

NFR-P5 allows **16 ms** from keystroke to glyph for everything — lexing, layout
and paint. Chroma spends 261 ms lexing alone, and 1 262 allocations on a single
line.

## The disqualifying problem is not speed

Chroma cannot resume. Colouring line 4 000 correctly requires knowing whether
it begins inside a block comment, a string, or a dollar-quoted body, and
Chroma's API offers no way to tokenise from a saved state. Every incremental
scheme built on it would have to re-lex from the top of the file on each
keystroke — which is exactly the 261 ms measured above.

Even at zero cost, Chroma could not produce correct highlighting incrementally.

## Decision

**Write the lexer.** `internal/ui/editor/lexer.go`, roughly 400 lines.

SQL tokenisation is a small, well-understood problem — identifiers, keywords,
literals, comments, operators — and writing it directly buys three things:

1. **Resumable state.** `State{Kind, Depth, Tag}` at each line boundary, which
   is what makes incremental highlighting possible at all. `Depth` exists
   because PostgreSQL nests `/* /* */ */`; `Tag` because `$body$ ... $body$`
   must not be closed by a different tag.
2. **No allocation on the hot path.** Tokens are offsets into the line, and the
   token buffer is reused between calls.
3. **Dialect control.** Chroma has no T-SQL lexer at all. Ours covers
   PostgreSQL, MySQL/MariaDB, SQLite, SQL Server and CQL, differing in
   identifier quoting (`"` / `` ` `` / `[]`), `#` comments, backslash escapes
   and comment nesting.

Chroma is removed from the editor path entirely.

## Result: GATE G0-2 passes

Keystroke to tokens-ready, on the 5 000-line file, against a 4 ms budget
(a quarter of the frame, leaving the rest for layout and paint):

| Edit | Mean |
|---|---|
| Typing mid-file | **2.0 µs** |
| Typing at the top | **1.7 µs** |
| Newline mid-file | **35 µs** |
| Opening a block comment at line 0 — invalidates all 5 201 lines | **231 µs** |

Even the pathological edit, which recolours the entire file, uses 1.4% of the
16 ms budget.

## Design notes

**Two caches with different lifetimes.** Per-line *state* is kept for the whole
buffer, because correctness downstream depends on it. Per-line *tokens* are
computed only for lines actually drawn and evicted outside a window — there is
no reason to hold tokens for 5 000 lines when 60 are visible (NFR-P6).

**The buffer is a slice of lines.** A rope would win on a single multi-megabyte
line, but SQL is short-lined and line rebuilds measure far below budget.
Choosing the simpler structure keeps the genuinely hard parts legible.

## Two bugs worth recording

Both were caught by tests, and both would have shipped.

**The early-stop was invalid on the initial build.** The cascade stops when a
recomputed line state matches the cached one. On a fresh highlighter the cache
is zero-valued, and a zero `State` reads as `StateNormal` — indistinguishable
from a genuinely computed normal state. So the first build stopped at line 1 and
never computed the rest of the chain. Any file containing a block comment
mis-coloured *from construction*. Fixed with a `statesValid` watermark.

This one hid behind itself: the test originally compared an incrementally
updated `Highlighter` against a freshly constructed one, and both were wrong in
the same way, so they agreed. The test now compares against a direct sequential
lex from line 0 — a genuinely independent oracle. **A test whose oracle shares
the implementation's assumptions proves nothing.**

**The block-comment opener was counted twice**, once when setting `Depth: 1` and
again by the nesting check, so `/* outer /* inner` reached depth 3 instead of 2
and comments never closed correctly.

## Two more, found when the editor model arrived (Phase 1)

The editor's random-edit test uses a fresh lex as its oracle and checks after
every edit. It found two more cache bugs, both in paths W2's tests never took,
because those tests only inserted single lines.

**A multi-line deletion shifted the deleted lines too.** Every cached line from
the edit onward moved by the line delta, including the lines just deleted. So a
deleted line's tokens and start state landed on the lines *above* the deletion.
Now only lines after the removed block move.

**A multi-line paste brought back the zero-state early stop.** Inserting k lines
leaves k−1 new start states zero-filled, and a zero `State` reads as
`StateNormal`. The cascade could therefore stop by agreeing with a state nobody
had computed: the first bug above, by another route. The early stop now trusts
cached states only from the end of the inserted block.

How often the test checked mattered. Checking every 500 edits, it passed,
because the second divergence had healed itself before the next checkpoint.

## Follow-ups

- Autocomplete (FR-5.2) is a separate build and remains the editor's larger
  unknown. Highlighting was the part with a hard latency budget; completion is
  latency-tolerant because it is user-initiated.
- The lexer is dialect-driven but keyword lists are curated rather than
  exhaustive. Colouring every obscure reserved word adds noise, not meaning.
- Rendering — turning tokens into drawn glyphs — is measured separately and is
  Fyne's cost, not the lexer's.
