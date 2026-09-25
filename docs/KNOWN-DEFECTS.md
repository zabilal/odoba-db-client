# Known defects and gaps

DoD-7 asks that no `M`-severity data-loss or credential-handling defect be open.
A claim like that is only worth something beside the list it was made against, so
this is the list: everything known, what kind of thing it is, and why it is or is
not the kind DoD-7 is about.

**Last reviewed:** 2026-09-25, at T5.9.

Severity here is the requirements' own: `M` is must-fix for v1.0.

---

## Data loss and credentials: nothing open

Nothing in the list below can lose somebody's data or mishandle a credential.
That is the claim DoD-7 asks for, and these are the things it was checked
against.

Two defects of that kind were found and fixed during Phase 4, and are named
because a register with no history in it is a register nobody has used:

- **A data race on the vault lock** (T4.17). Putting a lock on happens off the
  goroutine the window runs on, and the window read the lock while it did.
  Fixed: both the lock a vault holds and the key a lock holds are behind a
  mutex. Found by the commit gate, not by review.
- **Rows decoded in place** (T4.16). The stream that decodes a topic's records
  for an export wrote into its caller's rows, so a record could be read once in
  the window or once into a file but not both. Fixed: it copies. No data was at
  risk — the rows had already been read — but a second reader saw the wrong
  thing.

## Open: things a person may notice

| What | Kind | Why not `M` data-loss or credential |
|---|---|---|
| A screen reader announces almost nothing | accessibility | Documented in `docs/ACCESSIBILITY.md`. A toolkit limitation, not a defect of ours, and nothing is lost. |
| The binary is 86.3 MB against a 60 MB budget, and the command line 69.8 MB | performance | Measured on every build; the decision is the owner's (T4.30). Three drivers are most of it — Oracle 14.8 MB, Kafka 10.4 MB, DynamoDB 5.1 MB — and the command line is the same drivers without the toolkit, so both move together whichever way it is decided. Nothing is lost. |
| Cassandra reads an unset `int` back as `0` rather than as nothing | display | gocql gives no way to tell one from the other. Nothing is written wrongly: the grid shows `0`, and an edit writes what somebody typed. |
| A ClickHouse table with an `AggregateFunction` column cannot be read at all | driver limit | clickhouse-go's limit. The table refuses to open, which is a visible failure rather than a quiet wrong answer. |
| SQLite draws no ER diagram | missing feature | Its tree has nothing above its class folders, so there is no node to draw one from. Unclaimed by any task. The same shape made every snapshot of a SQLite database empty until T5.9 found it; that is fixed, and this is only the diagram. |
| Oracle has no bulk loading, `EXPLAIN` plan, editable results or explicit transactions | missing features | Each is absent and says so where it would be offered; nothing pretends to work. |
| ClickHouse has no bulk loading, `EXPLAIN` plans, editable query results or transactions | missing features | The last two are experimental in the engine itself. |
| Where in a statement an Oracle error was is not reported | diagnostics | go-ora keeps the offset to itself. The error is still shown. |
| Replaying a file into a Kafka topic is not possible | missing feature | The other half of FR-10.8; the import writes through a bulk loader and the Kafka driver has none. Unclaimed. |
| Comparing two live databases has no way in from the window | missing feature | The comparison itself works against a saved model, and saving one has a way in. |
| DynamoDB has no query editor, no filter picklist, no bulk loading and no atomic changeset | driver limits | Each is absent for a reason written in ADR-0157: PartiQL is a dialect this driver does not offer, a picklist would scan the whole table, the load contract's rules are rules DynamoDB has not got, and `TransactWriteItems` takes only a hundred items. Each says so where it would be offered. |
| A DynamoDB table's items cannot be sorted in the grid | engine limit | A scan returns items in the order the service finds them and there is no order to ask for. A sort is refused rather than ignored, so unsorted items are never presented as sorted. |
| Firebird shows no `EXPLAIN` plan, no row-count badge and no editable query results | driver limits | Each is absent for a reason written in ADR-0156: the library keeps the plan to itself, Firebird stores no row estimate, and a result says nothing about where its columns came from. Each says so where it would be offered. |
| A Firebird connection cannot verify the server's identity | protocol limit | Firebird does not speak TLS; its own wire encryption carries no certificate. The connection is encrypted by default and refuses to fall back to plain text, and asking for verification is refused rather than silently downgraded. A deployment needing a verified identity needs a tunnel in front of it. |
| The result of a query is not editable over libSQL or Turso | driver limit | Editing a query's rows needs to know which table each column came from; the libSQL wire protocol does not carry it, so the capability is not claimed and the grid is not editable there. Browsing a table and editing it works as everywhere else. |
| MySQL, SQLite and Cassandra render no DDL | missing feature | So the designer previews nothing and the source editors are unavailable on them. Each says so rather than failing. |
| Sending a production connection's rows to the assistant cannot be consented to | missing feature | The per-session confirmation FR-14.3 asks for is enforced (`assistant.Consent.Confirmed`) and no dialog sets it, so a production connection can be asked about its schema and not about its rows. The refusal says why. This is the safe direction: nothing leaves that should not, and what is missing is a way to allow more. ADR-0160. |
| Three Avro advisories with no fix | dependency | Denial of service by unbounded allocation while decoding. Accepted by name in `security/accepted-vulnerabilities.md`. Nothing is written and no credential is involved. |

## Open: things only a developer notices

| What | Why it is here |
|---|---|
| `GATE G0-1` passed on CPU-path evidence | Closes with one interactive run on a display, which needs a window server this environment has not had. |
| The theme gallery has never been looked at on a display | Same reason. It builds and compiles. |
| The conformance suite and CI have never run on a remote | The repository has no remote. Everything here runs locally, every commit. |
| `fyne package` is proven on macOS arm64 only | Linux and Windows are unproven until CI runs on them. |
| A writer check for a store that writes without transactions | Why ClickHouse skips the conformance suite's; unclaimed. |
| The diagram's footer line about relationships leading outside it cannot fire | It waits for T3.20's neighbourhood filter to give a way to ask. |

---

## How to use this file

Add to it when something is found and cannot be fixed the same day, with the two
things that make an entry useful: what somebody would notice, and why it is the
severity it is. Take an entry out when it is fixed, and say so in the commit that
fixes it. An entry nobody can justify in a sentence is an entry that should be a
fix.
