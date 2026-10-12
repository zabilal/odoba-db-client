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
| A Pulsar topic cannot be read from "an offset" or "the last few" | engine limit | A Pulsar message is addressed by the ledger it was written to and its entry there, not by a number, and a topic moves from ledger to ledger. Both ways of saying where to begin need a number to count with, and both are refused with that reason. Reading from the beginning, from a time, and following all work (ADR-0177). |
| A Pulsar message's offset is text | by design | It is the message's address, `ledger:entry`. The record view names such a record "Record" rather than by partition and offset. |
| A Pulsar read is not fixed to where the topic ended when it began | engine limit | A reader has no end set in advance: a bounded read stops at its bound or when the reader says nothing more is there, so messages written while it reads are read too, up to the bound. Kafka and NATS reads stop where the log ended when they began. |
| Pulsar subscriptions are listed in a topic's structure and are not objects of their own | missing feature | Who is subscribed and how far behind each is, is said where a topic is described. Opening a subscription, moving its place, and administering topics are unclaimed; so is a rate, because the counters a broker keeps start again when a topic is unloaded. |
| A Pulsar connection needs the admin service as well as the brokers | by design | What a cluster holds is asked of the admin service over HTTP, on a port of its own (8080 by default); messages go over the broker protocol. A connection is tested against the admin service, and a wrong broker port is found when something is read. No version is shown: the client library has no way to ask for it. |
| Pulsar has been run against only standalone, without authentication or TLS | unproven | What the settings become is tested — a token, a name and password, certificates — and a cluster that checks any of them, or has more than one broker, has not been connected to. Non-persistent topics have not been tried. |
| A NATS stream is shown with a Kafka topic's columns | by design | A stream is one log, so every message is in partition 0, and what is shown as a message's key is the subject it was published on. That is what lets the same window read both; it is said in ADR-0176 rather than hidden. |
| A NATS stream cannot be made, removed or reconfigured, and a consumer's place cannot be moved | missing features | Reading, following, consumers and their progress, and writing one message are done. Each of the others is a claim of its own and unclaimed. |
| NATS key-value and object stores are shown as the streams they are kept in | missing feature | A bucket is a stream named `KV_…` or `OBJ_…` underneath, and is listed as that. Reading one as keys and values is unclaimed. |
| NATS has been run against only without authentication or TLS | unproven | What the settings become is tested: a user and password, a token, a certificate authority, a client certificate. A server that checks any of them has not been connected to. |
| A Redpanda cluster does not say whether its permissions are enforced | engine limit | A Kafka broker with no authorizer refuses to list permissions, and this says what that means. Redpanda keeps permissions whether or not it enforces them and answers a list either way; nothing it says over Kafka's protocol tells the two apart, so an empty list there may mean nobody is allowed or everybody is. The cluster's description says so (ADR-0175). |
| A Redpanda cluster shows no version | engine limit | A Kafka version is a guess from which API versions a broker speaks; for Redpanda that guess is a Kafka of 2017. None is shown rather than that one. |
| Redpanda has been run against only without authentication or TLS | unproven | The Kafka driver's SASL, TLS and schema-registry suites run against Kafka only. Redpanda's own schema registry has not been tried. |
| Replaying a file into a Kafka topic is not possible | missing feature | The other half of FR-10.8; the import writes through a bulk loader and the Kafka driver has none. Unclaimed. |
| Comparing two live databases has no way in from the window | missing feature | The comparison itself works against a saved model, and saving one has a way in. |
| DynamoDB has no query editor, no filter picklist, no bulk loading and no atomic changeset | driver limits | Each is absent for a reason written in ADR-0157: PartiQL is a dialect this driver does not offer, a picklist would scan the whole table, the load contract's rules are rules DynamoDB has not got, and `TransactWriteItems` takes only a hundred items. Each says so where it would be offered. |
| A Trino table cannot be edited in the grid | engine limit | Trino is a query engine over other people's stores and vouches for no key that tells two rows apart, so the grid reads and does not edit. INSERT, UPDATE and DELETE typed in the editor run, behind the same guard as anywhere (ADR-0174). |
| A Trino table's row count is an estimate, or absent | engine limit | An exact count is a read of the whole table. The badge is the connector's own statistic where it keeps one, and says it is an estimate. |
| Paging a Trino table sorts it | engine limit | Trino promises no order it was not asked for, so every page is ordered by every column that has an order. An OFFSET reads everything before it either way; deep pages of a large table are slow, and a filter is the way through. |
| A Trino session's times are in UTC | by design | A timestamp with no zone is compared with a zoned one by reading it in the session's zone, so a session in somebody's own zone would make a filter on a value the grid showed select a different hour. `current_timestamp` in the editor answers in UTC for the same reason. |
| A Trino statement cannot be stopped from another connection, and has no plan view | missing features | Stopping it from its own tab works and is proved against the server. Typing EXPLAIN works; the plan tab does not read its output. |
| Trino has been run against only without authentication or TLS | unproven | What the settings become is tested; a coordinator that checks a password or a certificate has not been connected to. A certificate cannot be checked against a name other than the host's, and that is refused rather than ignored. |
| A DBF table with a memo column is not imported | file format limit | A memo column holds not its text but where the text is, in a second file beside the table. An import is given one file, so the column would be read as empty, and empty looks like data. The table is refused, naming the column (ADR-0179). |
| A DBF file's text is read as UTF-8 or Windows-1252 | file format limit | A DBF file says its code page in one byte that most writers leave at nought, so text is read as text files are: UTF-8 where it is valid UTF-8, Windows-1252 otherwise, or as the import panel is told. A table written under a DOS code page — 437, 850 — reads its accented letters wrongly. |
| Exporting to DBF changes column names, and refuses long text | file format limit | A DBF column's name is at most ten capital letters, digits and underscores; names are made to fit and made different where fitting made them the same. Text is at most 254 bytes: longer text is refused rather than cut, naming the column. A number of more than twenty characters is written as text with every digit, and a timestamp as text, dBase III having no time of day. |
| Exporting to DBF uses disk while it runs | by design | The header of a DBF file says how many records there are and how wide each column is, which a stream of rows does not know until its last. The rows are kept in a temporary file as they arrive and written out at the end; the file is removed however the export ends. |
| Exporting to Parquet refuses a value that is not its column's type | file format limit | A Parquet column is one type, said before its first value. A row holding something else in it — text in a column its source calls an integer, as SQLite allows; a timestamp finer than microseconds in a column that does not say it keeps more; a decimal with more digits or places than its column — stops the export, naming the row and the column. It is not written as something it is not (ADR-0180). CSV and JSON write such a table as it is. |
| Exporting to Parquet writes some columns as text | file format limit | A decimal whose source does not say how many digits it holds (PostgreSQL's bare `numeric`) is written as its digits in text, because Parquet's decimal must say and a float would lose some. A time of day, a UUID, an interval and anything else that is not a number, a date, a timestamp, bytes or JSON is text too. |
| A Parquet column with parts is imported as JSON, and a decimal inside it as a float | file format limit | A list, a map or a group is read as JSON, which is what a row has for a value with parts. That path goes through the library's reading of a row, in which an exact decimal becomes a float; a decimal that is a column of its own is read exactly. |
| An export or import cannot be saved as a job from the window | not built | A job is a command's flags in a file (ADR-0181), saved with `--save-job` on `ikigai export` and `ikigai import` and run with `ikigai run`. The window's export is of what a tab shows — a filter built in its own panel, a sort, a selection — and its import pairs columns by hand and can be told an encoding and a delimiter; none of those is a flag yet, so a job saved from the window would not do what the window did. |
| A YAML alias cannot point at a value in another record | file format limit | An import reads a YAML list a record at a time, so that memory does not grow with the file, and a record is parsed alone. An alias within a record works; one that points at an anchor in an earlier record is refused, naming the anchor (ADR-0178). |
| A YAML list written in flow style is read into memory whole | file format limit | In brackets, like JSON, a list has no lines at which a record begins, so it cannot be cut into records before it is parsed. Block style — a dash a record, which is how every tool writes one — and a stream of documents are both read a record at a time. |
| The command line exports and imports tables only | missing feature | `ikigai export --table` and `import` name what they are given as a table, and a source whose objects are collections — MongoDB, DynamoDB, a search index — answers that it has no such table. Found while trying the search driver through the command line (T5.15); it predates it and is unclaimed. `--connection` and `--url` reach those sources; nothing yet names a collection. |
| Elasticsearch and OpenSearch are read and not written, and have no query editor | driver limits | Version 1 browses, filters, sorts and counts. Nothing is written, no index is made or changed, and the engine's own query language is not offered: a typed condition is refused rather than taken by a back door (ADR-0173). |
| A filter or a sort on analysed text with no keyword beside it is refused, except "contains" and "equals" | engine limit | The engine holds such a field as its words, so there is no whole value to order, list or run a pattern across. Contains and equals match the words, which is what they can mean there; the rest say why they cannot. |
| A field inside a nested object cannot be filtered or sorted by | missing feature | The engine keeps a nested object's documents apart and matches them with a query of its own kind. Its values are shown, and how many documents hold it is counted. |
| A search index cannot be paged past ten thousand documents | engine limit | `index.max_result_window`. The refusal says so in terms of what was asked; narrowing the filters is the way through. |
| Elasticsearch itself has not been run against | unproven | The live suite runs against OpenSearch 2. The requests are the seven both projects share, but a difference between them would be found by somebody using it. |
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
