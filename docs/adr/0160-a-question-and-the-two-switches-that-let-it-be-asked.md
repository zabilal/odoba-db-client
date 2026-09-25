# ADR-0160: A question, and the two switches that let it be asked

**Status:** Accepted · **Date:** 2026-09-25
**Tasks:** T5.5, T5.6, T5.7, T5.8 · **Requirements:** FR-14.1, FR-14.2, FR-14.3, FR-14.4, FR-14.5, FR-14.6, NFR-S1, NFR-S3
**Packages:** `internal/assistant`, `internal/store`, `internal/ui/shell`

## Context

ADR-0159 left `internal/assistant` able to answer a question and nothing in the
application able to ask one. What was missing was all of the saying-so: where a
provider is configured, where a connection agrees to be asked about, and how the
answer gets in front of somebody without running.

FR-14.4 is in capitals in the requirement — **off by default**, opt-in per
connection, per-session consent for production data — so the shape of the
settings matters as much as the code that reads them.

## Decisions

1. **Off by default is the absence of a section, not a flag set to false.** The
   settings hold `assistant` only once somebody has configured one, and a
   connection holds `assistant` only once somebody has ticked a box. A file that
   has never seen the feature says nothing about it, and `ProviderFor(nil)` is
   no provider. A test marshals a fresh settings file and fails on the word.

2. **The window does not decide anything.** `assistant.ProviderFor`,
   `ConsentFor` and `Ready` read the settings and the connection; the shell asks
   them. A `Consent` built in a dialog would be a `Consent` a dialog could get
   wrong, and the reason is NFR-S4's: a disabled menu item is a courtesy, not a
   control. Asking is refused again inside `askAssistant`, and a test drives
   that path directly rather than through the menu.

3. **Two switches on a connection, the second following the first.** "The
   assistant may be asked about this connection" and "…and its rows, not only
   its schema". Unticking the first unticks and disables the second, because a
   tick nobody can act on reads as one that means something. A connection that
   has agreed nothing writes no section at all.

4. **`Consent.Data` carries the connection's switch as it stands.** The
   redundant `Enabled &&` came out: `Allow` and `Describe` both answer for the
   opt-in before they look at the data, so nothing can read `Data` without
   having passed `Connection` first. It survived having either half removed,
   which is what a doubled guard does.

5. **Every place the assistant appears says what it may see.** The question box
   names the provider, the model and `Consent.Describe()` in a line, and offers
   the rows switch only where rows are allowed. Somebody about to send their
   schema to a company should not have to open Settings to find out that they
   are (FR-14.5).

6. **A key is refused before it is saved, not at the first question.** Turning
   the assistant on validates the provider it would use — model chosen, address
   readable, plain `http://` only to this machine — so a setting that could not
   work is never written. A setting saved and then rejected is a setting
   somebody thinks they made.

7. **The key is not shown back, and not lost either.** The dialog's key field
   starts empty every time; a blank field leaves the keychain alone. Somebody
   changing which model to ask has not asked for their key to be forgotten.

8. **The keys live under a name of their own, not a connection's ID.** A secret
   filed under a connection's ID is deleted with that connection.

9. **A question about a connection is about the tab in front, or else the
   explorer's selection.** The selection is read as the connection a row belongs
   to, rather than as a selected object, so a question can be asked with nothing
   but the connection picked out — which is the state somebody is in before they
   have opened anything. `SelectedNode` deliberately reports nothing for a
   connection row, and using it here meant the command was offered and then did
   nothing.

10. **An answer with a statement in it opens a script, unrun.** Two comment
    lines above it: what answered, and that nothing here has run. The editor is
    where a statement is read and Run is where it runs, and there is no path
    from an answer to an execution — the test asserts the absence of one
    (FR-14.6). An answer with no statement in it is shown as the words it is.

11. **Rows are read only where they were asked for.** `grounding` gathers a
    sample only for a question that ticked the box; gathering somebody's data
    and then being refused would be reading it for nothing. Ten rows of the
    first table, to show a model what a value looks like — not to answer the
    question.

12. **Explaining explains what Run would run:** the selection where there is
    one, the whole script otherwise, which is the same reading `Run` makes. It
    sends no rows even where rows are allowed, a question about a statement
    being answerable from the statement.

13. **A transient message goes after `sync`, not before.** `sync` writes the
    status line from the selection, so a message set before it is replaced by
    it. `saveAssistant` had that the wrong way round and said nothing at all.

14. **A window with no settings file offers none of this.** `Deps.Settings` is
    documented as nil-able and the end-to-end harness is such a window. `sync`
    asks every command whether it is enabled, so a single reach for a file that
    is not there panics the whole window — which is what happened, and is why
    the guard is in `assistantSettings` rather than at each of its callers. The
    reach only became reachable when decision 9 fixed the selection, which is
    the useful thing about fixing a guard that was hiding behind another.

## Consequences

The assistant is reachable: Assistant… configures it, Ask the Assistant…
generates a statement into an editor, and Explain This Statement explains one.
All three are in the Tools menu and the command palette, and all three are
absent-by-disabled until a provider is configured and a connection has opted in.

What a stub model in this process proves is the whole path from the question box
to the editor, including what is in the prompt: the tests assert on the decoded
prompt rather than the request body, because a body checked for `id > 1` would
pass on the `id > 1` that encoding produces and would be reading the
transport rather than the prompt. What it does not prove is any real provider's
behaviour, which is ADR-0159's position and unchanged.

Per-session consent for production data is enforced (`Consent.Confirmed`) and
not yet *asked* anywhere: no dialog sets it, so a production connection can be
asked about its schema and not about its rows. That is the safe direction, and
the dialog is small; it is noted here rather than left to be discovered.

## Alternatives

**One switch per connection.** Rejected: schema and rows are different promises,
and somebody who will send column names will not always send values.

**A consent dialog at the moment of asking, with no settings at all.** Rejected
by FR-14.4: opt-in per connection is a property of the connection, and a dialog
every time is a dialog people click through.

**Running the statement and showing the rows.** Rejected by FR-14.6, and it is
the whole point: a generated statement is a draft, and a draft that has already
run is not one.

**Keeping the key in the settings file, encrypted.** Rejected by NFR-S1. There
is a keychain, and every other credential in this application is in it.
