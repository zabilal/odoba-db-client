# ADR-0159: Two wires and four switches

**Status:** Accepted · **Date:** 2026-09-25
**Tasks:** T5.4 · **Requirements:** FR-14.1, FR-14.3, FR-14.4, FR-14.5, FR-14.6, NFR-S1, NFR-S3, NFR-D6
**Packages:** `internal/assistant`, `cmd/ikigai`

## Context

FR-14 asks for an assistant: natural language to SQL grounded in the live
schema, explanations of statements and plans, pluggable providers including a
local one, and — in capitals in the requirement — **off by default**, opt-in per
connection, and never production data without per-session consent.

Every part of that is a promise about what leaves the machine, in an
application whose offline rule (NFR-D6) is held by a test that names the three
files allowed to reach the network.

## Decisions

1. **Two wire formats, and a local endpoint that is not a special case.** The
   OpenAI-compatible chat-completions call is what OpenAI speaks and what
   Ollama, llama.cpp, LM Studio and vLLM all speak in order to work with
   OpenAI's own clients; Anthropic's messages call is its own. So a provider is
   a wire, an address and a model, and "a model of your own" is the
   OpenAI-compatible wire pointed at a URL somebody gives. Running a model on
   one's own machine needed no code at all, which is the test of whether the
   abstraction is the right one.

2. **Consent is four switches, and the zero value allows nothing.** Enabled,
   Connection, Data, and — for a production connection — Confirmed for this
   session. "Off by default" is then a property of the type rather than of a
   setting somebody has to remember to write. They are asked in that order, so
   somebody who has not turned the assistant on is told *that* rather than told
   about a data toggle they have never seen.

3. **Consent is enforced here, not in the window,** for the reason read-only
   mode is (NFR-S4): a disabled button is a courtesy and not a control. `Ask`
   checks it before it looks at the provider, so a refusal names the switch that
   is off rather than the endpoint that is wrong.

4. **The request says whether it carries data, not the caller.** `SendsData` is
   `len(Grounding.Rows) > 0`. A caller that mis-stated it would send data under a
   consent that did not cover it, and a caller is exactly the thing that will be
   wrong one day.

5. **`http://` only to this machine.** A prompt carries a schema and possibly
   rows. `localhost`, the loopback addresses and `*.localhost` are allowed in
   the clear because that traffic does not leave the machine; `10.x` and
   `192.168.x` are not, because a model on another machine on the network is
   still a prompt crossing a network (NFR-S3).

6. **The key is never in the provider struct.** It is named there and fetched
   from the keychain when a request is made, as a driver's password is, so that
   nothing holding a provider's settings is holding a credential (FR-1.5,
   NFR-S1). An endpoint that wants none — which a local model usually is — is
   asked without one.

7. **A model must be chosen; nothing is defaulted.** A model nobody chose is a
   bill nobody expected and an answer nobody can account for. The built-in
   providers therefore arrive *invalid*, needing a model, which is what makes
   "provider and model always visible" (FR-14.5) true from the first request
   rather than from the first time somebody opens Settings.

8. **The answer carries what produced it.** Provider, model and how long it
   took, so the window can always say — and `Summary` puts the three in a line.

9. **The grounding is bounded, and says what it left out.** Forty tables and
   sixty columns each, not because of any provider's limit — providers differ
   and change — but because a model attends worse to a hundred tables than to
   the dozen that matter. What was cut is counted and stated in the prompt, so
   an answer about part of a schema can be read as one. A sample of rows is
   twenty rows and four hundred cells, and bytes are described as bytes rather
   than shown as text.

10. **The model is told not to write a change, and not trusted about it.** Every
    instruction says so, and the answer is read back through `Statement`, which
    accepts only statements that begin as a read. A model that wrote a `DELETE`
    anyway produces an answer shown as the prose it is, and nothing here runs
    anything, ever (FR-14.6). The instruction makes the common case clean; it is
    not what makes the uncommon case safe.

11. **`Statement` unwraps a fence and a preamble, and refuses to guess.** A
    model told to answer with a statement alone usually does, sometimes fences
    it, and sometimes says a sentence first. An answer with no statement in it —
    which is what a model says when the question cannot be answered from the
    schema — comes back as no statement, and is shown as the sentence it is.

12. **The offline rule gained a fourth entry, deliberately.**
    `TestOnlyTheseFilesSpeakHTTP` failed the moment this package imported
    `net/http`, which is what it is for. `internal/assistant/provider.go` is now
    in the list with its reason, and a mutation removes that entry so the rule
    cannot be softened without a test noticing.

## Consequences

The assistant can be asked something, and nothing in the application asks it
yet: the settings that hold a provider, the per-connection opt-in, and the way
in from the window are T5.5 to T5.8. Until those exist the consent switches are
a type nobody fills in, which is why T5.7 is not closed by this.

What is proved is what a stub server in this process can prove: both wires, both
key headers, the bounds, the refusals, and that nothing is sent before consent
is checked. What is not proved is any real provider's behaviour — no account, no
network — and that is the same position T2.87 took about the cloud token
endpoints, to be read the same way.

## Alternatives

**One provider, hard-coded.** Rejected by FR-14.5, and rightly: an organisation
that allows this at all has a gateway of its own, which is why a provider may
name the header its key goes in.

**A provider interface with an implementation per service.** Rejected: two wire
formats cover every service worth having, and an interface would invite a third
implementation for something that speaks one of the two.

**Sending the whole schema.** Rejected: see decision 9. A prompt with two
thousand tables in it produces worse answers than one with forty, and costs more.

**Trusting the instruction not to write a change.** Rejected as the only
defence. It is one of three, the others being that only reads are recognised as
statements and that nothing is ever executed.
