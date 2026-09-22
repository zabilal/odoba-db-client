# Ikigai DB — Master Task List

**Companion to:** [REQUIREMENTS.md](REQUIREMENTS.md) v0.2
**Created:** 2026-09-09
**Covers:** Phase 0 → Phase 5 (whole project)

---

## How to use this file

- Task IDs are stable. Never renumber; append with a letter suffix (`T1.12a`) if inserting.
- `→` traces a task to the requirement, risk, or NFR it satisfies.
- **GATE** rows are hard stops. Do not start dependent work until the gate passes.
- Update **Current Position** below on every session. That block is the resume point.

**Status legend:** `[ ]` not started · `[~]` in progress · `[x]` done · `[!]` blocked · `[-]` dropped/deferred

---

## Current Position

```
PHASE:     1 — Walking skeleton (J1 + J3)
STATUS:    Phase 1's exit criterion is met: J1 and J3 run end to end on PostgreSQL,
           MySQL, MariaDB and SQLite (internal/e2e). Partial Phase 1 tasks remain.
NEXT TASK: T2.84 — the conformance suite green against kafka and
           schema-registry testcontainers. The suite is already green: the
           tagged runs go against containers the owner starts and gate.sh
           requires — ikigai-kafka on 59092, ikigai-kafka-sasl, and
           ikigai-kafka-sr with ikigai-schema-registry on 58081. What is
           missing is that the suite brings its own. Decide first whether
           this project takes a testcontainers dependency at all: it puts a
           Docker client in the test path, and the alternative is a script
           that starts what is needed plus the skip the tests already do
           when it is absent. That choice is the task; the wiring after it
           is small either way, and it is the owner's call as much as mine.
           T2.56 is half done and says so: OAUTHBEARER lands, GSSAPI waits
           for a Kerberos library of this project's own plus a KDC and a
           keytab, franz-go shipping no Kerberos mechanism at all. T2.57
           landed without live proof: MSK IAM answers only to real AWS.
           Two Kafka containers: ikigai-kafka (59092, plaintext) and
           ikigai-kafka-sasl (9092 plaintext, 9094 SASL: PLAIN, SCRAM-256
           and -512, OAUTHBEARER; 9095 SSL, requiring a client certificate),
           both required by gate.sh. The certificates and the broker's JAAS
           file live in ~/.claude/projects/-Users-one-Work-ikigai-db/
           kafka-certs, which the container mounts and the tests read.
           Handing franz-go a logger is still unclaimed work.
           Flaky under load: TestCopyAsWritesWholeRowsWithTheirHeader failed
           one gate run at the 5s budget of the pump it waits on for an async
           status line, then passed three times straight after. The wait is
           what is fragile, not the copying. Two more on 2026-09-19, in
           consecutive tagged runs, each passing in the other:
           TestCopyReachesRowsNeverDrawnAndStopsAtItsLimit at its 5s pump
           budget, and TestRowsKeepArrivingAfterTheScriptEnds with
           "TempDir RemoveAll cleanup: directory not empty" — which is not a
           budget at all but work still running as the test returns, most
           likely a localdb write landing after t.Cleanup has begun removing
           the directory its file sits in. Both pass alone, five times out of
           five; the shell package takes 29-40s tagged, and that is when they
           bite. Nothing here is the copying or the script: it is what
           outlives a test under load. A fourth on 2026-09-22, untagged and
           under no tags at all: TestCopyAsInsertAsksTheDialect at the same
           5s pump budget, with the shell package taking 42s, passing five
           times out of five alone. Three of the four are that one wait.
           Open beside them: gocql writes its own account of a failed
           connection to stderr, beside the ConnectError this driver makes
           of it; quieting it means handing gocql a logger, which belongs
           with the session options.
           T1.58 waited on the MongoDB and Redis drivers, which are done:
           whether their dialects are drawn in colour is worth a look before
           it is closed. T2.21's new table waits on the table designer (3.A),
           and T1.14's scroll position on Fyne.
[~] tasks: T1.14 and T2.21 partly done; their lines say what is open.
OWNER:     first look at the real window: `go run ./cmd/ikigai`, which is also
           the first sight of the native save sheet (T1.3). Also the G0-1
           interactive run, and a push to a remote so CI runs. Test servers:
           ikigai-pg (55432), ikigai-mysql (53306), ikigai-mariadb (53307),
           ikigai-mongo (57017), ikigai-redis (56379), ikigai-redis-json
           (56380, redis-stack, for the JSON module), and
           ikigai-redis-cluster (three shards on 7001-7003, published as
           themselves so each is reachable where it announces itself), and
           ikigai-cassandra (59042, pulled from public.ecr.aws because
           Docker Hub refuses an unauthenticated pull here). For the schemas
           (T2.69): ikigai-schema-registry (58081) with ikigai-kafka-sr
           (59093) beside it on the user-defined network ikigai-sr, which is
           a pair of their own because both other brokers advertise localhost
           on the default bridge, where a registry container would be told to
           reach the broker at itself.
LAST DONE: 2026-09-22 — one test holding every operation that changes a
           cluster to its guard, with the list of them held to the
           interfaces by reflection so a new one cannot arrive unguarded
           (T2.83); before it every read bounded by how much it may pull as
           well
           as by how many records it wants, the count decision made
           checkable rather than claimed, and a tail's real bounds written
           down (T2.82); before it evidence that reading a topic joins no
           consumer
           group and moves nobody's offsets, held both by a live cluster and
           by a check on the driver's own source, because the promise is
           about what it never does (T2.81); before it a consumer group
           moved to where it should read from
           next: the beginning, the end, a time or a named offset, sharing
           the code that decides where a read begins so that both mean the
           same thing, and refusing a group that anything is still reading
           through (T2.80); before it a topic's settings read and shown,
           with what
           somebody chose told apart from what the cluster leaves alone, and
           a helper that had been drawing that line wrongly since nothing
           filled the type it belongs to (T2.79); before it topics made,
           unmade and reshaped, each asked about
           before it happens and each refused before it dials where the
           connection forbids it, with stream administration split into the
           three claims it always had (T2.78); before it a record written to
           a topic: the first thing this
           driver does that changes anything, refused before it dials where
           the connection is read-only and asked about where it is
           production, checked against a schema where one is named, and sent
           to the partition it was addressed to or refused if there is no
           such partition (T2.77, ADR-0108); before it how far a consumer
           group has got, read only when
           somebody asks for it, with what could not be measured saying so
           rather than reading as zero and not counted in the total; and the
           half of stream administration that changes nothing split into an
           interface of its own, so that reading a group promises nothing
           about resetting one (T2.76, ADR-0107); before it the structure of
           an object with no rows made
           reachable at all: a node now says whether it can be described,
           separately from whether it can be read, and the four kinds whose
           description nothing could ask for — Kafka's cluster, its subjects
           and its consumer groups, and Cassandra's keyspaces — open at last
           (ADR-0106); before it a consumer group described by who is in it, what
           each member was given to read, and what the group is doing, its
           lag left to T2.76 because reading it costs two requests nobody
           asked for (T2.75); before it a registry's subjects in the tree, a
           subject
           described by its versions, and two of them compared line by line,
           the schema laid out first because a registry keeps it as the one
           line it was registered as (T2.74, ADR-0105); before it a
           remembered reading made to outlast the first
           record, so a schema's decoder is put back once the registry
           answers (T2.73); before it JSON Schema read by stripping the five
           bytes that
           keep a document from reading as one, with validation left out
           deliberately (T2.72, ADR-0104); before it Protobuf read by a
           schema compiled in process, the
           message chosen by the index a record carries and a path that
           leads nowhere refused (T2.71, ADR-0103); before it a record read
           by its schema: Avro decoded through a
           real registry, its reading offered first among the forms, and
           nested bytes shown as text or hex rather than base64 (T2.70,
           ADR-0102); before it the registry that says what records mean,
           named per
           connection, claimed only where named, and reading the five bytes
           that say which schema wrote a record (T2.69, ADR-0101); before it
           what a record's bytes can be read as, decided by
           decoders rather than inline, and how a topic is read remembered
           for whoever opens it next (T2.68, ADR-0100); before it a topic's
           records filtered where a broker will not
           filter them: over what has been read, saying so rather than
           claiming the log was searched (T2.67, ADR-0099); before it one
           record read whole: the forms its bytes admit and
           no others, headers as a table that keeps what was sent, and copy
           of whatever form is on screen (T2.66, ADR-0098); before it a log
           followed: a read that waits rather than ending,
           a window that keeps the last records and counts what it dropped,
           and pausing that holds off rather than loses (T2.65, ADR-0097);
           before it where a read begins, five positions of which one is a
           way of following rather than reading (T2.64, ADR-0096); before it
           a log read as it stands, into the grid (T2.63, ADR-0095).
           2026-09-18 — a Cassandra table paged: where its pages ended
           remembered, a page nothing reached walked to within a bound and a
           jump past that refused (T2.51, ADR-0084); before it CQL run: a console's own session, a script guarded
           whole before any of it runs, and every value narrowed to one the
           model holds (T2.50, ADR-0083); before it the CQL dialect: how a name is written, where a
           statement ends, what it does, and what a browse would send
           (T2.49, ADR-0082); before it the Cassandra tree: a cluster's keyspaces, what
           each holds, and how it is replicated (T2.48, ADR-0081); before it
           the Cassandra connection, a form of fields over
           gocql that says what is wrong and claims nothing it has not
           written (T2.47, ADR-0080). 2026-09-14 — the conformance suite's key-value paradigm, run
           against a single Redis server, one with the JSON module and a
           cluster, and the four faults it found fixed (T2.46, ADR-0079).
           2026-09-13 — what a server says about itself, in the structure
           tab (T2.45, ADR-0078); the Redis console, its commands being its
           language (T2.44, ADR-0077); a key's own time to live and name, edited in
           the keyspace (T2.43, ADR-0076); a stream's entries and a JSON
           document, the last two kinds a key holds (T2.42, ADR-0075); the editors for what a key
           holds, which are the grid five times (T2.41, ADR-0074); the Redis key browser, a database's
           keys in the grid (T2.40, ADR-0073). 2026-09-12 — the Redis connection, three
           topologies in one form
           (T2.39, ADR-0072); the command console and MongoDB's own query
           language (T2.37, ADR-0070); index management (T2.36, ADR-0069),
           the first structural change the app makes; the aggregation
           pipeline (T2.35, ADR-0068);
           documents written and edited as JSON (T2.34, ADR-0066, ADR-0067); the conformance suite run against
           MongoDB, writes among the checks and in a paradigm of its own, and the
           three faults it found fixed (T2.38, ADR-0071); a collection's
           documents in the grid and as JSON (T2.33, ADR-0065), its shape read from a sample of them
           (T2.32, ADR-0064), the MongoDB tree (T2.31, ADR-0063)
           and its connection (T2.30, ADR-0062). Before them, 2.D: snippets (T2.28, ADR-0061), the schema
           cache behind completion (T2.29,
           ADR-0060), the completion popup (T2.27, ADR-0059), the
           tables a statement reads, so that a column can be offered by its
           alias (T2.26, ADR-0058), and the completion engine (T2.25,
           ADR-0057). Before them: exports as SQL INSERT, HTML and XML (T2.24,
           ADR-0056); an export can be an Excel workbook (T2.23,
           ADR-0055); the import's rows a transaction and what a
           row that would not go in does, the rows left out listed (T2.22,
           ADR-0053, ADR-0054); an import updates the rows whose primary
           key is there already and adds the rest (ADR-0052); an import adds to a table's rows or replaces them,
           in one transaction, asked first (ADR-0051), through the bulk
           loader every SQL driver now has (ADR-0050); an import's rows written into its table, a
           batch a transaction, as a task (T2.21's inserting, ADR-0049); a
           dry run over a whole file to import, listing each value
           that would not go in (T2.20, ADR-0048); a
           file to import read, its columns mapped and its first rows
           previewed (T2.19, ADR-0046, ADR-0047), a file's
           format, encoding, delimiter and header found (T2.18, ADR-0045), the files an import
           takes read as rows (T2.17, ADR-0044), master
           and detail (T2.16, ADR-0043), a foreign key's value shown
           with its row's label (T2.15, ADR-0042), the rows that refer to a row shown
           (T2.14, ADR-0041), a foreign key followed to the row it refers
           to (T2.13, ADR-0040), a row read down in a form view
           (T2.12, ADR-0039), one value set across a selection, whole columns too
           (T2.11, ADR-0038), a block of TSV or CSV pasted into
           the grid (T2.10, ADR-0037), a query's result edited in its grid,
           committed to its table and read again (T2.9, ADR-0036, ADR-0035),
           row identity (T2.8, ADR-0034), changes reverted, and asked about before
           they are lost (T2.7, ADR-0033), changes reviewed and committed (T2.5,
           T2.6, ADR-0031, ADR-0032), rows inserted, duplicated and
           deleted (T2.4, ADR-0030), cells edited in place and in the cell
           viewer (T2.3, ADR-0029), pending changes marked in the grid (T2.2,
           ADR-0028), the pending changeset (T2.1, ADR-0027),
           query parameters with a prompt panel (T1.65, ADR-0026), a
           lost connection said on its row and
           tabs, with Reconnect Now (T1.31, ADR-0011 §21), connection
           folders (T1.27, ADR-0025), the environment on every tab (T1.28,
           ADR-0011 §20), object classes from the model (T1.42,
           ADR-0024), native file dialogs and notifications (T1.3,
           ADR-0023), split panes (T1.5, ADR-0011 §19), tabs
           dragged on a tab bar of our own (T1.4), the task centre (T1.10),
           shortcuts changed in the Keyboard Shortcuts panel (T1.8), one
           copy to a data directory (T1.1), gates for NFR-P1 to P6 (T1.73),
           coverage past 70% (T1.76), explorer badges (T1.45), exporting the
           selection (T1.71),
           T1.44's context actions, favourites (T1.46), buttons with words
           (T1.78), ⇧⌘F filter (T1.43), ⌘/ shortcuts (T1.9), side panels
           (T1.77), the session (T1.14), autosave (T1.15), panics (T1.16).
DOCKER:    Docker Desktop stops answering now and then (twice on 2026-09-11):
           ports accept TCP, servers never reply. Each time, the next gate
           that could reach them ran the tagged suites over everything since;
           T1.43's did, covering a8019b0 and d12cb5d. If it stays down,
           restarting it is the owner's call (it also restarts gtmb-backend-*).
```

**Phase 0 findings so far**

- **ADR-0005** — T0.29 changed the driver contract. `Browser` is required, `Queryer`/
  `Dialect` optional, `StreamConsumer` removed. Proven by compilation in
  `internal/source/contract_kafka_test.go`. REQ-DRV-2/3 updated.
- **ADR-0006** — design system is macOS HIG (owner decision, replacing a generic
  neutral palette). Apple's system colours are pinned by test. Where HIG falls below
  WCAG AA — `secondaryLabelColor` 3.55:1, `systemBlue` 4.02:1 as text — AA wins and
  the deviation is marked `HIG-DEVIATION` in tokens.go.
- **T0.8 closed**: `fyne package` produces a working 16MB arm64 `.app` on macOS, well
  inside the 60MB budget (NFR-P8). RISK-10 partially retired — Linux/Windows still
  unproven, and CI has never run (no remote).
- Coverage: `model` 87.8%, `redact` 78.1%, `ui/theme` 82.7%.
- Two bugs caught by tests rather than review: the `redact` bearer-token ordering
  leak, and three palette roles that were never contrast-checked.
- **Timing gates cannot run under `-race`.** `go test -race ./...` — what CI's build
  job runs — failed G0-1 at 11.7ms against an 8ms budget; the same test measures
  1.4ms without the detector, which slows execution 5-20x. The other gates passed
  only on this machine's headroom. Gates now skip themselves under `-race` via
  `internal/testutil/race`, G0-4 keeps its oracle-agreement check there, and CI's
  performance job runs the gates without `-race`. Before this, that job ran only
  `-bench`, so the gates executed nowhere in CI at all.

**Phase 1 findings so far**

- **ADR-0011** — the shell. UI work goes through an injected Runner (Fyne's test
  driver runs `fyne.Do` inline). Shortcuts live only on menu items, because a focused
  Entry swallows canvas shortcuts. Found by the J1 test: uncounted tables opened empty.
- **pgx's default cancel destroys the whole connection.** It stops the query by
  tearing down the backend, taking the user's temp tables, SETs and open
  transaction with it. The driver sends a CancelRequest that keeps the session
  instead: 102ms to return, and state survives the cancel. This was first
  recorded the other way round ("never reaches the server"), from a test that
  sampled once, at +0ms, before pgconn's async teardown had run. ADR-0008.
- **Read-only needs three layers.** A function that calls `set_config` flips the
  session default to off without the lexer seeing it. An explicit READ ONLY
  transaction around every statement still stops the next hidden write. Proven
  against the live server.
- **The contract needed sessions.** Session state lives on one connection, so a
  pooled editor loses temp tables and SETs between runs. `Sessioner` added;
  `QueryMulti` gained `confirmed`; `StatementError` and `ConnectError` added.
- Follow-up: `Session` has no database target yet. The editor's database
  selector will need one.
- **History would have been a plaintext password log.** `CREATE USER ... PASSWORD
  'x'` is exactly what a query history records. History entries are now
  redacted through the lexer before they are written; saved queries stay
  verbatim, because the user saved them deliberately. ADR-0009.
- **A feared weakness turned out not to exist.** go-keyring on macOS was thought
  to put passwords on `security`'s command line, visible in `ps`. Its source
  shows v0.2.8 writes them over stdin. Checking in the source cut both ways
  here: it disproved a worry this time, where on pgx it disproved a claim.
- **Two stores, no transaction.** Settings and secrets can't be updated
  atomically together, so every operation has an ordering rule and a rollback.
  The rollbacks are tested against real validation failures. ADR-0010.
- **Pasting a URL could leak its password into an error.** `url.Parse` quotes
  the whole input, and its inner error echoes the bad fragment. Parse errors
  are now fixed messages. Tests check every fragment of the password.


**Phase 0 findings so far**

- **ADR-0005** — T0.29 changed the driver contract. `Browser` is now required and
  `Queryer`/`Dialect` are optional; `StreamConsumer` was removed entirely. Proven by
  compilation in `internal/source/contract_kafka_test.go`. REQ-DRV-2/3 updated.
- Coverage: `model` 87.8%, `redact` 78.1% — both clear NFR-Q2. `capability` and
  `conformance` are 0% by design (data declarations, and a harness that only runs
  against real servers).
- `redact` bearer-token ordering bug was caught by its own test, not by review.

**Locked decisions — do not re-open without explicit instruction:**
1. UI toolkit: **Fyne**, pure Go. Owner overrode a Wails/webview recommendation. Pure Go end to end is a goal in itself.
2. Phase 1–2 sources: PostgreSQL, MySQL/MariaDB, SQLite, MongoDB, Redis, Cassandra, **Kafka**.
3. Licence: **deferred**. Permissive working assumption. Clean-room vs DBGate's GPL-3.0 is mandatory.

---

# PHASE 0 — Foundations & Spikes

> Exit: all four spike gates pass (or documented fallbacks chosen). **No Phase 1 UI work starts until then.**

## 0.A Repository & tooling

- [x] **T0.1** `go mod init` — module path (placeholder until OQ-1 settles)
- [x] **T0.2** `git init`, `.gitignore` (binaries, `.DS_Store`, `dist/`, `fyne-cross/`), initial commit
- [x] **T0.3** Create directory skeleton per REQUIREMENTS §10.1 with package doc comments → ARCH-1
- [x] **T0.4** `Makefile`: `build` `test` `lint` `bench` `package` `conformance` targets
- [x] **T0.5** `golangci-lint` config; enable `depguard`
- [x] **T0.6** **depguard rule: no `fyne.io/**` import below `internal/app`** → ARCH-1 *(the single most important structural guard)*
- [x] **T0.7** CI: native runners for macOS (arm64+amd64), Linux (amd64+arm64), Windows (amd64) → NFR-D3, RISK-10
- [x] **T0.8** CI: prove `fyne package` works on all three platforms **now**, not at Phase 4 → RISK-10
- [x] **T0.9** CI: testcontainers matrix — postgres, mysql, mariadb, mongo, redis, cassandra, kafka+schema-registry → NFR-Q1
- [x] **T0.10** Benchmark harness + CI budget assertions that fail the build on regression → NFR-Q3 — the four `TestGate*` gates run in CI's performance job without `-race`; `internal/testutil/race` makes them skip under the detector
- [x] **T0.11** `docs/adr/` decision-record folder; ADR-0001 records the Fyne decision and its rationale
- [x] **T0.12** `LICENSE` placeholder + `CLEANROOM.md` stating the no-DBGate-source rule → RISK-5
- [x] **T0.13** `ARCHITECTURE.md` summarising §10 layering for future contributors

## 0.B Core contracts — no UI, no Fyne

- [x] **T0.14** Canonical model: **relational** (database, schema, table, column, index, constraint, FK, view, routine, sequence, type)
- [x] **T0.15** Canonical model: **document** (collection, inferred shape, index)
- [x] **T0.16** Canonical model: **key-value** (keyspace, key, type, TTL)
- [x] **T0.17** Canonical model: **stream/log** (topic, partition, offset, consumer group, registry subject) → REQ-DB-3
- [x] **T0.18** Unify the four into one source-agnostic model surface → ARCH-3, REQ-DB-3
- [x] **T0.19** `Capabilities` descriptor type + registry → REQ-DB-2
- [x] **T0.20** `Connector` interface (dial, auth, tunnel, health, close)
- [x] **T0.21** `Introspector` interface
- [x] **T0.22** `Queryer` interface + **cursor/stream abstraction** (never a slice) → ARCH-5, NFR-P11
- [x] **T0.23** `Dialect` interface + per-dialect identifier quoter → ARCH-2, NFR-S6
- [x] **T0.24** `StreamConsumer` / `StreamProducer` interfaces → REQ-DRV-2
- [x] **T0.25** Optional interfaces: `Diffable`, `BulkLoader`, `PlanExplainer`, `Scriptable`
- [x] **T0.26** `context.Context` + cancellation contract on every call → ARCH-4, NFR-P12
- [x] **T0.27** Statement classifier (read vs write) for read-only enforcement → NFR-S4
- [x] **T0.28** Driver conformance suite skeleton; optional interfaces report *skipped*, not failed → REQ-DRV-1
- [x] **T0.29** **Paper-validate the contract against Kafka before any driver is written.** Kafka must work without `Queryer` or `Dialect`. If it cannot, redesign the contract → REQ-DRV-2, RISK-4
- [x] **T0.30** Secret-redaction helper used by all logging and error paths → NFR-S2

## 0.C Theme — Phase 0 deliverable, not end-stage polish

- [x] **T0.31** Design tokens: palette (light + dark), typography scale, spacing scale, corner radii → UX-12
- [x] **T0.32** Implement custom `fyne.Theme` → RISK-6
- [x] **T0.33** Icon set (object classes, actions, source types)
- [x] **T0.34** Environment colour treatment (local/dev/staging/production) → FR-1.7, UX-8
- [x] **T0.35** Semantic colours: diff states, NULL, error, changeset add/modify/delete → UX-7, UX-9
- [x] **T0.36** Theme gallery harness app for visual review of every token
- [x] **T0.37** WCAG 2.1 AA contrast check on both themes → NFR-A2

## 0.D SPIKE W1 — Data grid → RISK-1 *(existential)*

- [x] **T0.38** Seed script: 10M-row Postgres table, mixed types
- [x] **T0.39** Prototype `widget.Table` with `StickyRowCount`/`StickyColumnCount`
- [x] **T0.40** Windowed server-side fetch behind the prototype
- [x] **T0.41** Instrument and measure sustained fps under scroll → NFR-P4
- [x] **T0.42** Verify render cost scales with *visible* cells, not total rows → NFR-P13
- [-] **T0.43** Prototype the `canvas.Raster` fallback grid **in parallel, not on failure** → OQ-8
- [x] **T0.44** Compare both, decide, write ADR-0002
- [~] **GATE G0-1** — provisionally passed on CPU-path evidence (ADR-0002). Closes when `go run -tags spike ./cmd/gridspike -bench` is run on an attended machine and p95 lands inside the 30fps floor. **Owner action.**

## 0.E SPIKE W2 — Query editor → RISK-2 *(largest single build risk)*

- [x] **T0.45** `alecthomas/chroma` lexing → styled output prototype (SQL + CQL lexers)
- [x] **T0.46** Caret, selection, and scroll model
- [x] **T0.47** Incremental re-highlight on edit (do not re-lex the whole buffer per keystroke)
- [x] **T0.48** 5 000-line SQL file: measure keystroke-to-glyph with highlighting live → NFR-P5
- [x] **T0.49** Decide `widget.RichText` vs direct canvas text drawing; ADR-0003
- [x] **GATE G0-2** — PASSED. 2.0µs typing mid-file, 231µs worst case (block comment at line 0, invalidating all 5201 lines) against a 16ms budget. ADR-0003.

## 0.F SPIKE W3 — Node canvas (ER + designer)

- [x] **T0.50** Pan/zoom container with a custom `fyne.Layout`
- [x] **T0.51** Draggable nodes with persisted positions
- [x] **T0.52** Edge routing (orthogonal preferred)
- [x] **T0.53** Auto-layout via `gonum/graph` or `dominikbraun/graph`
- [x] **T0.54** 200-table schema: layout time and pan smoothness
- [x] **GATE G0-3** — PASSED. 200 tables lay out in 15-28ms; panning costs 24us/frame at overview zoom with 183 nodes visible. Visible count saturates at ~52 from 50 to 800 tables. ADR-0007.

## 0.G SPIKE W4 — Charts

- [x] **T0.55** Render `gonum/plot` or `wcharczuk/go-chart` into `canvas.Image`
- [x] **T0.56** Hit-test overlay for hover tooltips
- [x] **T0.57** Accuracy test at 100k points → FR-11.4
- [x] **T0.58** Decide image-render vs native canvas drawing; ADR-0004
- [x] **GATE G0-4** — PASSED. 100k-point index builds in 14.3ms; mean query 428ns; 0 of 20,000 queries disagree with a brute-force oracle. ADR-0004.

### ✅ Phase 0 exit criteria
- [~] G0-2, G0-3, G0-4 PASSED. G0-1 provisionally passed on CPU evidence; closes with one interactive run (owner action)
- [~] Theme gallery builds and compiles; not yet viewed on a display — the build session had no window server
- [!] Conformance suite and CI written but never run — the repository has no remote
- [~] `fyne package` proven on macOS arm64 (16MB .app); Linux and Windows unproven until CI runs

---

# PHASE 1 — Walking Skeleton → J1, J3

## 1.A Application shell

- [x] **T1.1** App entry, window lifecycle, single-instance handling — *window, lifecycle, startup wiring done; one copy to a data directory: a second copy asks the first to come forward and gives way, a crashed copy's socket is taken over, and a copy with its own data runs beside it (ADR-0022)*
- [x] **T1.2** Native menu bar → FR-15.5 — *built from the command registry; native on macOS (ADR-0011)*
- [x] **T1.3** Native file dialogs + notifications → FR-15.5 — *internal/ui/filedlg asks the platform: a sheet on the window on macOS, comdlg32's dialog on Windows, the file chooser portal on Linux, and Fyne's own where there is none. Export saves through it, and a connection's file field has a Choose… that opens through it. Long work that ends while the app is in the background sends a notification: a task, or a query that ran 10 s or more. It names the work, never its statement or error (ADR-0023). Open: the Windows and Linux dialogs have been vetted but have never run*
- [x] **T1.4** Doc-tab workspace with reorder and pin → FR-15.2 — *tabs close, cycle, reuse an open object's tab, move and pin from the Window menu (ADR-0011 §8); on a tab bar of our own (internal/ui/tabbar) a tab is dragged to a new place, kept among its kind, and a secondary tap opens its menu of commands. Tabs that do not fit scroll and are listed by All Tabs; the close control is named for a screen reader (ADR-0011 §18). Open: the row does not scroll while a tab is dragged past its edge*
- [x] **T1.5** Split panes (horizontal + vertical) → FR-15.2 — *Split Right and Split Down move the active tab into a second pane with its own tab bar; Move Tab to Other Pane and Join Panes, from the Window menu (and a tab's menu); a pane left empty closes. Commands act in the pane the focus moved into, or whose tab was last chosen; the session keeps each tab's pane and the split (ADR-0011 §19). Open: dragging a tab from one pane to the other*
- [x] **T1.6** **Command palette (⌘K)** with fuzzy search + shortcut display → FR-15.1, UX-3
- [x] **T1.7** Central command registry — every action registers once, surfaces in menu + palette + shortcut → FR-15.1, FR-15.4
- [x] **T1.8** Keyboard binding map, platform-correct (⌘ vs Ctrl) → FR-15.4, UX-11 — *platform-correct chords on menu items; a command's shortcut is changed in the Keyboard Shortcuts panel, its modifiers ticked and key chosen, kept in the settings file, refused on a chord another command has with that command named, and applied at startup and on the rebuilt menu bar (ADR-0011 §16)*
- [x] **T1.9** Searchable shortcut reference sheet → FR-15.4 — *Help › Keyboard Shortcuts (⌘/) is a side panel listing every command with a shortcut, whether or not it can run just now, filtered by name, keywords or keys (ADR-0011 §12)*
- [x] **T1.10** Task centre: background tasks, progress, cancel → FR-15.6, UX-5 — *exports run as tasks in a Tasks panel (Window › Tasks), not a sheet over the window: each says how far it has got and can be cancelled, and says how it ended until cleared; the status bar says while any runs and opens the panel. Closing a tab or quitting while one runs asks first, and quitting waits for a stopped export to remove its partial file (ADR-0011 §17). Long queries and imports join it as they come*
- [x] **T1.11** Error surface: actionable, dismissible, copyable; never a raw stack trace → FR-15.7 — *tab errors explain and offer the fix; everything else is said in a band across the top of the window, dismissible and copyable, with an action where there is one; the modal error dialog is gone and a test keeps it gone (ADR-0011 §7)*
- [x] **T1.12** Theme switching (OS-follow + manual override + accent choice) → FR-15.3 — *follow system / light / dark, persisted; eight macOS accents from View › Accent Colour, derived so each passes AA in both appearances, persisted (ADR-0006 addendum)*
- [x] **T1.13** UI-goroutine discipline: worker→UI marshalling boundary → ARCH-6 — *uithread.Runner: UI work goes through an injected runner, tests drain a queue (ADR-0011)*
- [~] **T1.14** Session restore: tabs, layout, scroll, unsaved buffers → FR-15.2, NFR-R3 — *tabs come back in order with their pins and selection, as do the window size and sidebar width, and each table's filters, sort, WHERE clause and column layout (order, hidden, frozen, set widths) by column name (ADR-0011 §10); unsaved query text reopens through T1.15. The explorer's open nodes come back under connections a tab uses. Open: scroll position (Fyne's table does not report its offset)*
- [x] **T1.15** Autosave scratch buffers → NFR-R2 — *a query tab's unsaved text is kept within a second of an edit and reopens at the next start on its connection, still unsaved; saving or closing on purpose forgets it, quitting and disconnecting keep it, and text whose connection has gone reopens without connecting (ADR-0011 §9)*
- [x] **T1.16** Per-connection panic isolation and recovery → NFR-R1 — *a driver's panic becomes an error at every way into a driver, its stack in the log; the shell offers Disconnect to open the connection afresh (ADR-0017)*

## 1.B Persistence & store

- [x] **T1.17** OS-convention config directories → FR-17.1
- [x] **T1.18** Settings file (JSON/TOML), human-readable → FR-17.2
- [x] **T1.19** **OS keychain integration** (Keychain / DPAPI / libsecret) → FR-1.5, NFR-S1
- [x] **T1.20** Local SQLite store: history, saved queries, session state → FR-17.3
- [x] **T1.21** Store schema versioning + migration on upgrade → FR-17.4
- [x] **T1.22** Structured logging with rotation and redaction → NFR-R4, NFR-S2

## 1.C Connection management

- [x] **T1.23** Connection CRUD, duplicate, reorder → FR-1.1
- [x] **T1.24** Per-source connection forms with defaults → FR-1.2 — *descriptor-driven form, URL paste, test, keychain hints*
- [x] **T1.25** Connection-string parser (`postgres://`, JDBC, `.pgpass`, `~/.my.cnf`) → FR-1.3
- [x] **T1.26** Test connection with precise error classification → FR-1.4 — *every driver classifies connect failures (auth, no database, refused, unreachable, TLS) with a hint the form shows*
- [x] **T1.27** Connection folders/groups with colour and icon → FR-1.6 — *the explorer lists folders first, each holding its connections, listed at once so the filter finds a connection in a closed folder; a folder's colour is an accent, drawn as a dot beside its name as Finder marks a tag. New, Edit and Delete Folder from the Connection menu or a folder's own menu (deleting keeps the connections); a connection's form puts it in a folder, and a new one starts in the folder selected; open folders reopen (ADR-0025). Open: an icon of the user's choosing; dragging a connection into a folder*
- [x] **T1.28** Environment tagging + persistent visual treatment across derived tabs → FR-1.7, UX-8 — *every tab on a connection with an environment carries its word on the tab bar in its colours (PROD on red), heard by a screen reader with the title; a production tab (table, query or structure) also has a band across its content saying what working there means, or that it is read-only. Both are read as they are drawn, so an appearance change or a saved connection redraws them (ADR-0011 §20)*
- [x] **T1.29** **Read-only mode enforced in the Go layer** → FR-1.8, NFR-S4 — *twice on every engine: guard classification, and the server or file opened read-only (ADR-0014, ADR-0015)*
- [x] **T1.30** TLS/SSL config incl. verify modes; verification on by default → FR-1.10, NFR-S3 — *internal/source/tlsconf, shared by the network drivers; verify-full by default*
- [x] **T1.31** Auto-reconnect with backoff + explicit disconnected state → FR-1.15 — *the monitor retries with backoff (ADR-0010); a lost connection's row says Disconnected, each of its tabs has a band saying when and why it was lost and how many tries so far, and Reconnect Now (the band, the Connection menu, a connection's menu) tries at once. All of it goes when the connection comes back (ADR-0011 §21). Open: a query tab's session, lost with the connection, is not yet told it was reset (ADR-0010 follow-up)*

## 1.D Drivers — relational core

- [x] **T1.32** PostgreSQL driver (`jackc/pgx/v5`) — reference implementation
- [x] **T1.33** PostgreSQL introspection → canonical model
- [x] **T1.34** PostgreSQL dialect + quoter + paging
- [x] **T1.35** MySQL driver (`go-sql-driver/mysql`) — *go-sql-driver/mysql; cancel by KILL QUERY keeps the session (ADR-0015)*
- [x] **T1.36** MySQL introspection + dialect — *information_schema tree, Describe with keys, indexes and foreign keys; DELIMITER-aware splitting*
- [x] **T1.37** MariaDB feature-probe divergence from MySQL → REQ-DB-2 — *flavour probed from VERSION(); read-only variable, JSON and STATISTICS divergences handled and tested*
- [x] **T1.38** SQLite driver (`modernc.org/sqlite`) + introspection + dialect — *modernc; no file is ever created by opening; read-only refused by guard and engine; trigger-aware splitting; genuine cancel (ADR-0014)*
- [x] **T1.39** Capability descriptors for all four → REQ-DB-2 — *PostgreSQL, SQLite, MySQL, MariaDB*
- [x] **T1.40** Conformance suite green for all four → REQ-DRV-1 — *green on all four; SQLite every run, the servers under -tags conformance*

## 1.E Object explorer

- [x] **T1.41** Lazy virtualised tree on `widget.Tree` → FR-2.1
- [x] **T1.42** Object classes per source, driven by capability descriptors → FR-2.2, REQ-DB-4 — *the classes are one list in internal/model (kind, label, order), and a class folder's key is the kind it holds. Drivers only count and list; conformance walks the tree and fails a folder that is not the model's class, holds an undeclared kind, is misnamed, or holds another kind. PostgreSQL adds Indexes, Triggers and Types, MySQL/MariaDB Indexes, Triggers and Routines, SQLite Indexes and Triggers; an index or trigger is named with its table. Constraints stay in a table's structure (ADR-0024)*
- [x] **T1.43** Fuzzy filter matching on full path → FR-2.3 — *⇧⌘F filters loaded objects by a fuzzy match on their full path, listed in the tree's place; a table opens, anything else is shown in the tree; unopened connections are named, not searched (ADR-0018)*
- [x] **T1.44** Context actions: open data, open structure, script as, refresh → FR-2.4 — *right-clicking a node selects it and shows its commands (ADR-0011 §13); Open Structure (⌥⌘O) shows a read-only structure tab (§14); Script As writes SELECT, INSERT or UPDATE from Describe and the dialect into a new query tab (§15). CREATE scripts are T3.7's; rename, drop and truncate belong with the table designer (3.A)*
- [x] **T1.45** Lazy cancellable row-count/size badges → FR-2.5 — *read the first time a row is drawn, four at a time with a timeout, remembered (none included), and cancelled when their branch closes (ADR-0020)*
- [x] **T1.46** Pinned/favourite objects → FR-2.6 — *⌘D makes the selected table, view or collection a favourite, listed above the tree only when there are any; kept in the settings file and deleted with their connection (ADR-0019)*

## 1.F Data grid — read path (productionise W1)

- [x] **T1.47** Windowed server-side fetch with bounded buffer → FR-3.1, NFR-P11 — *paged LRU model with MaxResidentPages; tables without a count grow as they are read (ADR-0011)*
- [x] **T1.48** Column resize, reorder, hide/show, freeze left → FR-3.2 — *hide/show, move and freeze from View › Columns or a title's menu; a handle on each title sets the width, which the grid keeps for the column wherever it moves (ADR-0016)*
- [x] **T1.49** Server-side multi-column sort → FR-3.3 — *header click cycles, ⇧-click adds a key; the server re-sorts; stale pages from the old order are dropped (ADR-0016)*
- [x] **T1.50** Type-aware cell renderers; NULL vs empty visually distinct → FR-3.8, UX-7 — *rows striped to the edge by a filler column; cut values end in an ellipsis; instants in local time with UTC on hover, dates and zone-less times as stored; geometry as extended WKT, round-tripped on MySQL and MariaDB and read from PostGIS's bytes (the test server has no PostGIS); NULL, booleans, JSON, bytes, arrays and enums typed (ADR-0016)*
- [x] **T1.51** Cell/range/row/column selection → FR-3.7 — *click, ⇧-click, ⌘-click, arrows and ⇧-arrows; Select Row/Column/All Cells; the grid keeps blocks of cells (ADR-0016)*
- [x] **T1.52** Copy as TSV/CSV/JSON/INSERT/Markdown → FR-3.7 — *⌘C and Edit › Copy Cells (TSV); Edit › Copy As CSV, JSON, Markdown and INSERT; exact values, up to 100,000 rows; INSERT literals round-trip on every engine (ADR-0016)*
- [x] **T1.53** Expandable cell viewer (long text, JSON, XML, blob) → FR-3.9 — *View › Cell Viewer or Space: a panel beside the grid, following the selection; JSON indented exactly and coloured, bytes as a hex dump, times local and UTC; Copy Value copies all of it (ADR-0016)*
- [x] **T1.54** Per-column filters with distinct-value picklist and operators → FR-3.4 — *right-click a title, or View › Filter by Values…; counted values from the server, searchable; IN or NOT IN by the shorter side; the listed values are sent as typed, the notation shown (ADR-0016)*
- [x] **T1.55** Compact filter-row DSL (`>100`, `!=x`, `a,b,c`, `~regex`, `NULL`) → FR-3.5 — *a field under each header; parsed in internal/app/filterexpr and checked against the column's type; applied on the server; failures marked and explained (ADR-0016)*
- [x] **T1.56** Global WHERE editor showing the generated SQL → FR-3.6, UX-6 — *View › WHERE Clause; checked as one condition, refused if it could write; the statement and its values shown and copyable (ADR-0016)*

## 1.G Query editor — stage 1 (productionise W2)

- [x] **T1.57** Editor widget: caret, selection, scroll, undo/redo — *virtualised drawing, caret, selection, scroll-follow, undo/redo, clipboard, platform chords; headless-tested, not yet seen in a real window*
- [~] **T1.58** Per-dialect syntax highlighting → FR-5.1 — *SQL dialects drawn in palette colours; MongoDB shell and Redis syntaxes come with those drivers*
- [x] **T1.59** Line numbers, current-line highlight, bracket matching, auto-indent → FR-5.11 — *line numbers, current-line highlight, bracket pairs outside strings and comments, auto-indent*
- [x] **T1.60** Find/replace with regex → FR-5.11 — *find bar in query tabs: literal or regex, case, whole words, "3 of 12", matches outlined, replace one or all in one undo; ⌘F ⌥⌘F ⌘G ⇧⌘G*
- [x] **T1.61** Run all / run selection / run statement at cursor (⌘↵) → FR-5.3 — *⌘↵ runs the selection or the statement at the caret; ⇧⌘↵ runs the script (ADR-0012)*
- [x] **T1.62** Multi-statement scripts → multiple result tabs → FR-5.4 — *a result tab per result set, plus Messages*
- [x] **T1.63** **Driver-level query cancellation** → FR-5.5, NFR-P9 — *Stop (⌘.) cancels the script and its streaming results; the session survives (ADR-0008)*
- [x] **T1.64** Timing, rows affected, server messages pane → FR-5.6 — *per-statement timing and rows affected, a run summary, server messages*
- [x] **T1.65** Query parameters with prompt panel and remembered values → FR-5.7 — *:name on every engine, found through the lexer; a script carries its values (QueryMulti's ScriptOptions), bound never interpolated: PostgreSQL $n, MySQL ?, SQLite natively. Before a script with parameters runs, a side panel asks for each with the value last given on the connection, or NULL; values are sent as typed, and history keeps the :name, never the value (ADR-0026). Open: positional ? and $1 are not asked for*
- [x] **T1.66** Persistent searchable query history → FR-5.8 — *every statement recorded, redacted, with its outcome and row count; ⇧⌘H searches as you type and reopens it*
- [x] **T1.67** Saved queries with folders → FR-5.9 — *⌘S names and saves, then saves in place; ⇧⌘S copies; ⇧⌘O filters, reopens or brings forward, deletes; edited tabs show • and ask before closing. Tab restore across restarts is still open*
- [x] **T1.68** Map server errors back to editor position → FR-5.10 — *the rejected token is underlined and the caret moved to it; the message gives line and column; skipped if the text changed during the run*

## 1.H Export

- [x] **T1.69** Streaming export engine (memory flat) → FR-10.3, NFR-P11 — *export.Copy streams a RowStream; 2M rows stay under 16 MB of heap (ADR-0013)*
- [x] **T1.70** CSV, TSV, JSON, NDJSON writers → FR-10.1 — *CSV, TSV, JSON, NDJSON; exact decimals, NaN as text, bytes hex/base64, times by column type*
- [x] **T1.71** Export scope: selection / filtered result / whole table → FR-10.2 — *whole table or filtered browse, and query results, stream page by page; with a selection, the form offers it by its size, All rows staying the default, and it streams a page at a time with only its columns (ADR-0013 addendum)*
- [x] **T1.72** Progress, rows/sec, ETA, working cancel → FR-10.7 — *progress sheet with rows, rows/s, a bar and time left when the total is known; Cancel (or closing the tab) stops and removes the partial file*

## 1.I Quality

- [x] **T1.73** Benchmarks asserting NFR-P1…P6 in CI → NFR-Q3 — *a gate for each, timing the application's own CPU work against a fraction of its budget: P1 91 ms of 200, P2 4.3 ms of 500, P3 0.46 ms of 100, P4 and P5 the Phase 0 gates, P6 the 2–5 MB of heap five connections add, of 100; not under -race. True frame rate, time to a window on screen and resident memory need an attended session (ADR-0021)*
- [x] **T1.74** E2E test: **J1** (zero to first result) — *internal/e2e: J1 on PostgreSQL, MySQL, MariaDB and SQLite*
- [x] **T1.75** E2E test: **J3** (write and iterate on a query) — *internal/e2e: J3 on PostgreSQL, MySQL, MariaDB and SQLite*
- [x] **T1.76** Coverage ≥70% on `internal/source`, `internal/sqlgen`, `internal/model` → NFR-Q2 — *`internal/source` 100% (from 57.5%), `internal/source/capability` 100% (from none), `internal/source/sqlscript` 89.3%, `internal/model` 87.4%. There is no `internal/sqlgen`: statement text lives in each driver's dialect and in `sqlscript`. The new tests check behaviour, among it that a connection error hides a URL's password (NFR-S2)*
- [x] **T1.77** Panels, not modals (UX principle 4): History and Saved Queries open as modal dialogs that hide the data; make them panels or popovers — *both open in a side panel beside the tabs, one at a time, toggled by their commands with a tick in the menu; Escape or Close shuts it, and opening an entry leaves it open (ADR-0011 §11)*
- [x] **T1.78** Every button has words, not an icon alone (UX principle 13: a button with no text has no name to be read out) — *the sidebar's New, the error band's and the cell viewer's Close, and Saved Queries' Delete; a test walks the window for any button with no words (ADR-0006 addendum)*

### ✅ Phase 1 exit: J1 and J3 complete end to end on all three platforms

---

# PHASE 2 — Editing, NoSQL & Streaming → J2, J5, J7, J8

## 2.A Changeset editing

- [x] **T2.1** Pending-changeset model with add/modify/delete states → FR-4.3 — *app.Pending: a table's edits, kept by each row's identity values so they follow it through a sort; only real changes count; a deleted row loses its edits; revert by cell, row or all; new rows kept apart; the contract's Changeset out, updates carrying only the columns changed and the key the row had. Refuses rows it cannot tell apart (ADR-0027)*
- [x] **T2.2** Visual marking of changed cells/rows → FR-4.3, UX-9 — *grid.Changes, asked about a row, not its place: a changed cell shows its new value bold on its tint and says what it was; a deleted row struck through to the edge; a new row on its tint; a gutter (Fyne's header column) marks each changed row •, − or + and says it in words. On the selection a change's text takes the selection's colour, the deleted red failing AA on it. A table tab holds app.Pending when its rows have a key (ADR-0028)*
- [x] **T2.3** In-place editors per type (text, number, date, bool, enum, JSON) → FR-4.1 — *in the cell: Return or typing opens an editor drawn in the cell; what is typed is read as the column's type (grid.Parse), and text left as it started is no edit; Return and Tab write and move, Escape gives up, leaving writes; true/false and an enum's labels are picked from a menu; Set to NULL; the footer counts the pending changes. The cell viewer's Edit: a long editor, JSON on lines, and a calendar for dates (ADR-0029)*
- [x] **T2.4** Insert / delete / duplicate row → FR-4.2 — *new rows shown first, held in grid.Model before the rows read, so every index counts them; a column not given is DEFAULT (model.Default), left out of the INSERT; Insert Row selects the new row's first cell; Duplicate Rows copies all but the key; Delete Rows marks rows read and takes out new ones (ADR-0030)*
- [x] **T2.5** **Statement preview before commit, always** → FR-4.4, UX-6 — *the drivers' side: sqlscript.PlanWrites renders a changeset as bound UPDATE, DELETE and INSERT statements, a line describing each, refusing what it cannot write whole, on PostgreSQL, MySQL, MariaDB and SQLite (ADR-0031). Review Changes shows every statement, its SQL and its values, before anything runs; production asks again (ADR-0032)*
- [x] **T2.6** Transactional commit; full rollback + offending row on failure → FR-4.5 — *the drivers' side: a plan applied in one transaction, a statement refused or matching no row rolling it all back and named; the guard asked first (ADR-0031). Commit from the review; written, the changes go and the rows are read again; failed, the change is named and its row selected (ADR-0032)*
- [x] **T2.7** Revert cell / row / entire changeset → FR-4.6 — *Revert Cells and Revert Rows on the selection (a new row's column back to DEFAULT, a new row taken out), Discard All Changes… asking first; closing a tab or quitting with changes asks (ADR-0033)*
- [x] **T2.8** Row-identity detection; refuse edit without a key, offer to nominate one → FR-4.7 — *SQLite knows a table by its primary key, or else its rowid, which the browse then selects; a statement changing more than one row fails as one changing none does (ADR-0034); a table with no key says why, and Choose a Key… edits it by the columns picked*
- [x] **T2.9** Editable query results when mapping to one updatable table → FR-4.8 — *a result's columns say their table and their name there, and a result reading one table with its key among the columns is known by that key, on PostgreSQL and SQLite; MySQL's driver keeps the table to itself (ADR-0035). Such a result, of a statement that only reads, is edited in its grid, committed to its table on a connection of its own, and read again on the tab's session; running again asks first (ADR-0036)*
- [x] **T2.10** Paste TSV/CSV block into the grid → FR-4.10 — *read by its shape, written from the active cell as pending changes; one value fills a selection; begun on a new row it adds new rows; ⌘V, or Edit ▸ Paste Cells (ADR-0037)*
- [x] **T2.11** Bulk set-column-value across selection → FR-4.11 — *Set Value… writes one value into every selected cell, as each column reads it; it, Set to NULL and one value pasted share one fill, which reads the rows a selection reaches, those not loaded too, up to Copy's limit (ADR-0038)*

## 2.B Form view & navigation

- [x] **T2.12** Form view — one record laid out vertically → FR-3.10 — *View ▸ Form View shows the active row in the grid's place, a field per column shown; Previous and Next Row move the grid; fields are typed into where the grid edits, changes said in words (ADR-0039)*
- [x] **T2.13** FK navigation: jump to referenced row → FR-3.11 — *a table's tab reads its foreign keys when it opens; View ▸ Go to Referenced Row opens the table a cell's key refers to, or its tab, filtered to that row in the filter row, other filters cleared (ADR-0040)*
- [x] **T2.14** Reverse FK navigation: "what points at this?" → FR-3.11 — *an optional `source.Referrer` lists the keys that refer to a table, on PostgreSQL, MySQL/MariaDB and SQLite; View ▸ Show Referring Rows opens a referring table filtered to the row, asking which where several refer (ADR-0041)*
- [x] **T2.15** FK lookup labels rendered inline → FR-3.12 — *a key of one column shows each value as `3 · Alice`, the referred table's first text column outside its key, read a page at a time and kept; the whole label is the cell's hint (ADR-0042)*
- [x] **T2.16** Master-detail nested grid → FR-3.13 — *View ▸ Detail Rows shows, under the grid, the rows of a referring table whose key holds the active row's values, following the row; a picker chooses the table, Open in Tab opens them to edit (ADR-0043)*

## 2.C Import

- [x] **T2.17** Import readers: CSV/TSV/JSON/NDJSON/Excel → FR-10.4 — *`internal/transfer.Open` streams a file's rows: CSV and TSV as export writes them, JSON and NDJSON with their types, Excel with the standard library alone (ADR-0044)*
- [x] **T2.18** Delimiter/encoding/header auto-detection → FR-10.4 — *`transfer.Detect` finds a file's format, encoding (UTF-8, UTF-16 with or without its mark, Windows-1252), delimiter and header from its first 64 KiB, for a person to correct (ADR-0045)*
- [x] **T2.19** Column mapping UI with type coercion → FR-10.5 — *the mapping: `transfer.Suggest` pairs columns by name, `transfer.Coerce` makes values the table's types as typing a cell does, every failure said; a value's text is read by `internal/value`, moved out of the grid (ADR-0046). File ▸ Import… opens a panel in a tab: the options as found, each table column's file column, the first 20 rows as the table would take them (ADR-0047)*
- [x] **T2.20** **Dry-run preview listing error rows** → FR-10.5 — *Dry Run in the import panel reads every row as the import would write it, as a task in the task centre, and lists each value that would not go in by row, column and why; text past its column's length, and a column no file column fills, are said too (`transfer.Check`, `Unfilled`; ADR-0048)*
- [~] **T2.21** Modes: insert / upsert / replace / append-to-new-table → FR-10.6 — *inserting done: Import in the panel writes the rows through each source's Writer, 500 rows a transaction, as a task that says how long is left after a dry run; new rows need no key (`transfer.Load`, ADR-0049). Every SQL driver loads rows in bulk, emptying the table first in one transaction (`source.BulkLoader`, `sqlscript.LoadWith`, ADR-0050). The import goes through it, adding rows or replacing the table's in one transaction, asked first (ADR-0051). Rows with a primary key already there are updated, the rest added: ON CONFLICT, or ON DUPLICATE KEY on MySQL and MariaDB (`LoadOptions.Keys`, `sqlscript.Upserter`, ADR-0052). Importing into a new table waits on the table designer's DDL and its preview (T3.1, T3.4, T3.7), as FR-6.4 asks that every structural change be previewed*
- [x] **T2.22** Batch size + error policy → FR-10.6 — *every SQL driver leaves a refused row out when told, each row in a savepoint: skip, or collect up to a most (`LoadOptions.OnError`, `Skipped`; ADR-0053). The panel asks the rows a transaction and what a row that would not go in does, checked where typed; the rows left out are listed in the Problems tab, by their place in the file (ADR-0054)*
- [x] **T2.23** Excel (xlsx) export writer → FR-10.1 — *an export can be an Excel workbook, streamed with the standard library: strings inline, a number a number where Excel keeps its digits and text where not, dates as Excel's days with ISO formats, NULL an empty cell, a bold frozen header, the sheet named for what is exported; what a sheet cannot hold is refused, not cut; the import reads Excel's _xHHHH_ escapes (`export.XLSX`, ADR-0055)*
- [x] **T2.24** SQL INSERT, Markdown, HTML, XML writers → FR-10.1 — *Markdown was written already; SQL INSERT is written by the table's own source a row at a time, offered for a table's rows and its selection; HTML a page of one table, NULL reading apart unstyled, light and dark; XML a <row> of named <field>s, base64 where XML cannot hold a value (ADR-0056)*

## 2.D Query editor — stage 2

- [x] **T2.25** Completion engine: keywords, schemas, tables, functions → FR-5.2 — *`internal/source/sqlcomplete` offers what can be typed at a cursor: the token before it and the clause it is in decide whether a name would be a table's or a column's; a dotted chain is read as a schema, a table or both; nothing is offered inside a comment, a string or a parameter; the catalog answers from memory, so no keystroke waits on the server; a name is quoted where it must be, through the source's own quoter, and the result says what it replaces (ADR-0057)*
- [x] **T2.26** **Alias-resolved column completion** → FR-5.2 — *`sqlcomplete.Scope` lists the tables a statement reads, under the names it calls them by, reading the whole statement and only the one the cursor is in; a subquery's tables are in scope inside its brackets alone, and the statement's own within them; a qualifier is an alias before it is a path, matched whatever its case; unqualified, every table in scope offers its columns, saying which table where two are read, and writing a name two of them have qualified (ADR-0058)*
- [x] **T2.27** Completion popup widget with keyboard navigation → FR-5.2 — *the popup is drawn inside the editor, not as an overlay, so the focus never leaves the text; it opens as a word is typed and after a dot, closes on anything that ends a word, and ⌃Space (Query ▸ Complete) asks outright; ↑↓ move, Page keys by a windowful, Return or Tab accepts the candidate over the whole word as one undoable edit, Escape closes; ten rows at a time, each saying the candidate in its kind's colour, its detail and its kind in words, spoken for anyone who cannot see it (ADR-0059)*
- [x] **T2.28** Snippets → FR-5.2 — *a dozen shared snippets are offered where a keyword can go, under the keyword the same letters may have begun, written in the case being typed; the engine writes the places to fill in into the candidate's insert (`${1:name}`, `$0`), which only a snippet's is read for, and the editor steps through them on Tab, selecting each so typing replaces it, moving the rest along with what is typed, ending at the last place or wherever the caret goes (ADR-0061)*
- [x] **T2.29** Schema cache feeding completion, invalidated on DDL → FR-5.2 — *`app.SchemaCache` is the completion catalog, one per connection, hung off `Live`: it answers with what it holds and loads the rest behind, one request however many keystrokes, telling the popup when something lands; it reads the explorer's own lazy tree, and the tree's shape tells a source with schemas from one whose databases are its schemas from one with a single database; DDL and an explorer refresh empty it, and a load in flight when that happens is dropped (ADR-0060)*

## 2.E MongoDB

- [x] **T2.30** Driver + connection (incl. `mongodb+srv://`) → FR-1.3 — *`internal/source/drivers/mongo` over the official driver: a form of fields rather than a URI, credentials set apart from the URI the driver takes so no error quotes a password, a seed list carrying no port and refusing encryption outright where it is refused, failures said in terms of what to fix, and a server that will not list its databases still showing the one the connection names; a pasted `+srv` scheme sets the seed-list setting, and `authSource` is no longer taken for a secret (ADR-0062). Listing collections is T2.31*
- [x] **T2.31** Introspection: databases, collections, indexes — *the document tree in the relational shape: a database holds a class of collections, a collection a class of indexes; a view is a collection marked as one, the server's own `system.` collections are hidden, collections come in name order and `_id_` leads the indexes; a refusal a view earns is not a failure; a badge is the count the server already holds, said to be an estimate; the structure tab reads a collection's indexes, its document estimate and the shape inference will find (ADR-0063)*
- [x] **T2.32** Document shape inference over a sample → FR-12.4 — *`source.ShapeInferrer`, paired with `Structure.InferredShape` and checked by the conformance suite: MongoDB samples with `$sample`, reads into embedded documents and arrays four levels down, and answers with its evidence — how many documents were read, every type each field was seen with and in what fraction of them; the structure tab offers the sampling and never runs it unasked, saying how many it read (ADR-0064)*
- [x] **T2.33** Table view + JSON view toggle → FR-12.1 — *a collection browses into the grid: the columns are the fields a fifty-document sample holds, a document keeps what they do not show, values come back as themselves (a map, a slice, an instant, a decimal's digits), and the server does the filtering, ordering, paging and counting; a document is told from another by its `_id`. View ▸ JSON View shows the documents in the grid's place, fifty at a time, opening where the grid was looking (ADR-0065)*
- [x] **T2.34** Schema-aware JSON document editor → FR-12.1 — *`source.Writer` over a collection: a plan carrying the mongosh call it renders (`Statement.Op`), a document keyed by its `_id` and nothing else, `model.Removed` for a field a change takes away (`$unset`; a relational store refuses it), nothing undone on a standalone server and the plan saying so (ADR-0066). The JSON view edits one document: what is saved is what differs, a whole number stays whole, the `_id` cannot be edited, and the shape says which fields are empty here and where a type is not the rest's — said once, never refused; saving goes through the same writer, guard and commit as the grid's own changes (ADR-0067)*
- [x] **T2.35** Aggregation-pipeline editor → FR-12.1 — *`source.Aggregator`, paired with `Data.Pipeline` and checked by the conformance suite: the stages are the person's own text, a pipeline says its own matching and order so filters and sorts beside it are refused, only paging is added and only where it reads, and a pipeline that writes (`$out`, `$merge`) asks the guard first; the columns are the fields it produced and nothing it produces is written back. `app.PipelineSource` feeds the grid as a browse does, and the editor sits above the grid with Show Documents to go back (ADR-0068)*
- [x] **T2.36** Index management → FR-12.1 — *`source.IndexManager`, paired with `Schema.Indexes` and checked by the conformance suite: a change is planned as the call it would make and applied only once it has been shown (FR-6.4), guarded as DDL so nothing runs read-only and production asks again; every index at once and the `_id` index are refused. The structure tab offers Add Index… and Drop Index… under the indexes it lists, fields typed as `name, score:-1, body:text`, and reads the structure again once a change has run (ADR-0069)*
- [x] **T2.37** `mongosh`-compatible command console → FR-12.1 — *the console reads the commands a person types at a prompt — `db.people.find({…})`, `show collections`, `use shop` — in extended JSON with JavaScript's quotes, one call a line, and says by name what it does not read; mongosh is the source's query language, with a lexer dialect of its own (double-quoted strings, `//` comments); every command is classified and put to the guard before any of a script runs, a session holds the database `use` changes, and a condition typed in the grid is now a filter document, ANDed with its filters and refused where it is more than one (ADR-0070)*
- [x] **T2.38** Conformance green — *the suite runs against the MongoDB driver and is green for everything it claims: the lifecycle, the capabilities, the tree, browsing, cancellation, paging and now writing. The write checks had been written against a table — a numbered key, server defaults, a rollback — none of which a collection has, so the suite gained a paradigm of its own: `checkWriter` dispatches on `Capabilities().Paradigm`, and the document checks assume only the contract (a change writes the fields it names, a field can be taken away, a change to a document that is gone fails, new documents need no key, a plan's atomicity is what the source claims), with the guard checked in both paradigms in the same words. A writable object's identity is asked of it rather than assumed (ADR-0071). It found three faults, all fixed: a database whose collections are all the server's own opened onto nothing, a cursor's batch went on being read after its context was cancelled, and `{} {}` was taken as one condition*

## 2.F Redis

- [x] **T2.39** Driver + connection (standalone, sentinel, cluster) → FR-1.3, FR-12.2 — *`internal/source/drivers/redis` over go-redis: one form for three topologies, where the mode is a setting and a setting the mode has not got is refused rather than dropped — a cluster has one keyspace, a sentinel set is known by its master's name, a single server has one address. A database is a number here, and the tree lists what the server was configured with, or the ones holding keys where the server refuses CONFIG. The credentials are settings and never part of an address, the sentinels' own password is read under its own name, and `rediss://` means encryption because the scheme says so — `Descriptor.TLSSchemes`, which connstr reads without knowing what Redis is. A failure is said by the server's own code: WRONGPASS, NOAUTH, NOPERM, a cluster that is not one, a master no sentinel knows. Nothing is claimed that is not written; the key browser is T2.40 (ADR-0072)*
- [x] **T2.40** Key browser with pattern SCAN + type filter → FR-12.2 — *a database's keys are its rows, not a tree: the node opens into the grid, and a key shows its name, its kind and what is left of it — not its value, which is a command for each and a different one by kind (T2.41). The pattern and the type are the server's to match (`MATCH`, `SCAN TYPE`), where the grid's own `%` and `_` become the server's `*` and `?` and a name holding either is looked for as it is; an order the keyspace has not got, a typed condition, a filter on the time left and a second pattern are refused. A page is walked rather than sought, a piece of the keyspace at a time; a key that went between the walk and the question is no row; the database is chosen on the connection that walks it and put back afterwards; a cluster is walked shard by shard, each asked about its own keys. `DBSIZE` is the count and the tree's badge, exact (ADR-0073)*
- [x] **T2.41** Editors: string, hash, list, set, sorted set → FR-12.2 — *every kind of value is rows of its own shape, so the five editors are the grid five times: a hash is its fields, a list its elements in order, a set its members, a sorted set its members and scores, a string the one value it is. The column a row is known by is the one the server addresses it with, and the two that are addresses rather than contents — a key's name, an element's position — are read-only. A row of a keyspace is an object of its own: `source.RowObject` with `Data.RowObjects`, and View ▸ Open What the Row Holds, so the UI still knows nothing about Redis. A plan asks what the key is before it writes a word; each change looks before it writes, so a part that has gone fails as a row changed since it was read and one that is there already is refused rather than written over; a part renamed moves in one transaction carrying what it holds. What Redis cannot do is refused in its own words, a value is text where it is text and bytes where it is not, and setting a string keeps its expiry (ADR-0074)*
- [x] **T2.42** Editors: JSON, stream → FR-12.2 — *a stream is its entries in the order they were written, the id beside the fields it was added with — one value rather than columns, because they are the entry's own — paged by id with `XRANGE` from the last one read; an entry is written once, added or deleted and never changed, and the server gives it the id that orders it. A JSON document is one value as a string is: one row, the key beside the document, typed as JSON so the cell viewer reads and writes the structure, set whole with `JSON.SET $` and refused before it is sent if it is not JSON. Neither is narrowed by the server, and a kind nothing here reads is said to be that by name (ADR-0075)*
- [x] **T2.43** TTL display and edit → FR-12.2 — *the keyspace's rows are edited where they are shown: a change to one is a change to the key itself rather than to what it holds, so the writer takes a database as a target as well as a key. A time to live is read however it is written — a bare number is seconds, anything else a length of time as Go writes one, and nothing at all (an empty cell, a value taken away, the -1 the server itself answers) is a key that never expires, which is `PERSIST` and no failure to do twice. An expiry of nothing does not delete the key, a key is renamed with `RENAMENX` so another is never written over, and a key is deleted from the keyspace and never added there, because a key comes into being when something is written to it. A cluster sends each command to the shard that holds the key it names (ADR-0076)*
- [x] **T2.44** Raw command console → FR-12.2 — *Redis's own commands are its query language: a command is a line, read as redis-cli reads one (words apart, `"…"` with its escapes, `'…'` where only the quote is, a quote left open an error), and a `#` line is a comment. A console holds a connection of its own, so `SELECT` moves it and nothing else and `MULTI` and `WATCH` are its own state; a cluster's console is the cluster, each command going to the shard its key names. What a command does is read from its name — the reading ones listed, the server-changing ones listed, everything else a write — and every command of a script is put to the guard before the first runs; `SUBSCRIBE` and `MONITOR` are refused outright, being commands that stop a connection answering. A reply is drawn as what it is: a word, a number, a list, a list of lists, the pairs of a map, with what is nested shown as the structure it is and a reply of nothing an answer. Redis is a lexer dialect of its own, a condition typed in the grid is the pattern names are matched by, and the grid is shown the command a browse sends (ADR-0077)*
- [x] **T2.45** Memory / keyspace info panel → FR-12.2 — *a keyspace and a key are model types of their own beside a table and a collection, neither having columns: a database says how many keys it holds and how many are set to expire, with the server's own figures under INFO's own headings and by INFO's own names, in a settled order, a figure the server did not report left out and a heading with nothing under it not shown; a server that will not say is not a failure. A cluster's shards are a group of their own, each with its keys and its memory. A key is described by asking after it — `TYPE`, `TTL`, `MEMORY USAGE`, `OBJECT ENCODING` and the length its kind is counted in. Open Structure now shows whatever a person is looking at: the explorer's choice, or the object the tab in front is on, which is the only way to reach a key at all (ADR-0078)*
- [x] **T2.46** Conformance green — *the suite runs against the Redis driver in all three shapes a connection takes — a single server, one with the JSON module, a cluster — and is green for everything it claims. Its writes gained a third paradigm: a keyspace is rows twice over, its keys and what each key holds (`source.RowObject`), so the key-value checks open every key onto its value and write at both levels. The suite cannot make a key, which comes into being when something is written to it, so the target fills the keyspace, and a key is not added as a row. A kind takes only some changes, so each change is refused when planned or does just what it says, and a refusal is logged rather than failed; what is written is what the grid hands a writer for text typed into a cell (`value.Parse`), rows are compared as sets in no order, a time to live as what is left of it, and a count claimed exact is checked at both levels. The checks are checked themselves, against a keyspace in memory with a fault put in it (ADR-0079). It found three faults, all fixed: a string's or a document's value was written through a change for another key, a list position past 32 bits wrote over another element, and JSON as the grid reads it reached Redis as a slice of numbers. Putting the cluster through it found a fourth: a rename between hash slots, now refused before it is sent*

## 2.G Cassandra

- [x] **T2.47** Driver (`gocql/gocql`) + connection — *`internal/source/drivers/cassandra`: a wide-column store in the relational paradigm, because keyspaces hold tables of declared columns and CQL is a SQL-family language; what Cassandra does differently shows in the capabilities it claims rather than in a paradigm of its own. A form of fields rather than a URI — any node and its port, an optional keyspace, a role and its password, further nodes, and the data centre whose nodes are asked first, token-aware within it. The credentials are settings gocql takes on their own and never part of an address, encryption is the settings' to say with nothing downgraded silently, and `SslOptions.EnableHostVerification` is made to say what the mode says. `CreateSession` takes no context, so a connection given up is raced against one and a session that arrives late is closed rather than leaked. A failure is said in terms of what to fix, and where a keyspace was named the cluster is dialled again without it and asked whether it has that keyspace — gocql folds every node's refusal into "no connections were made", which would otherwise read as a network fault. Nothing is claimed that is not written: no query language (T2.49), no rows (T2.51), an empty tree and a browse that refuses until T2.48, which is also when the driver joins the application's own list (ADR-0080)*
- [x] **T2.48** Keyspace/table introspection, replication strategy display → FR-12.3 — *the tree is the shape every other relational driver presents: a keyspace is this paradigm's database, its children are the classes the model already knows (Tables, Materialized Views, Indexes, Types), and a table's children are its columns, so the explorer and the tabs need know nothing about Cassandra. The cluster's own keyspaces are hidden as PostgreSQL hides its system schemas, and one may still be opened on by name. What a keyspace holds arrives in name order already — `system_schema` clusters by the object's own name — so only the keyspaces are sorted, they being partitioned by name and arriving in token order; that sort and the hiding are one pure function, proven without a live cluster's order having to disagree with the alphabet. A keyspace's structure is how it is replicated — strategy, factors, durable writes — as `*model.Schema.Attrs`, which the model already names as where a Cassandra strategy goes, with the strategy read as the end of its Java class name and a new case in the structure tab. A table's is what addresses its rows: the partition key, then what orders rows within a partition, then the static columns a partition shares, then the rest, with a primary key Cassandra does not name and key columns that can never be empty. CQL's types are read as the model's own with the cluster's words kept as `Native`, frozen unwrapped before classifying, and a type nobody knows a keyspace's own structure. Nothing is badged, counting a table's rows being a read of every partition (FR-2.5), and no node is browsable until paging is written (T2.51). With a tree worth opening, the driver joins `cmd/ikigai`'s list (ADR-0081)*
- [x] **T2.49** CQL dialect + quoter — *`source.Dialect` for CQL, which reads like SQL and is not: names are double-quoted, which is also what keeps their case, since CQL folds an unquoted name; a batch is one statement, so the shared splitter gained `SplitWith` and a closer of its own — every SQL body here ends at END, and `BEGIN BATCH` ends at `APPLY BATCH` — with `Split` delegating unchanged for its three callers and CASE counted only under the END closer. What a statement does is read from its first word, the safe direction being that a verb nobody listed writes: SELECT, USE, DESCRIBE and LIST read, a batch is the writes in it, TRUNCATE is structural, CREATE/ALTER/DROP are too unless they are about a role or a user, and GRANT and REVOKE administer. A browse refuses what CQL cannot mean — an offset (a page is where the last ended, T2.51), where the nulls go, a pattern, a regular expression, a range, an inequality, a negation, text inside a value, a comparison with nothing — and never adds ALLOW FILTERING, which reads every partition on every node and is a person's decision to take knowingly. The source carries its dialect as SQLite's does; `Capabilities.Query` still claims nothing, which waits on a Queryer (ADR-0082)*
- [x] **T2.50** CQL editor with highlighting and completion → FR-5.1, FR-5.2 — *the editor was already built: it colours by the lexer dialect the source names and completes from the connection's schema cache, which fills itself from the driver's own tree. So this is the driver's half — `source.Queryer` and a `Session` — and the claim that goes with it: `Query.Supported`, `cql`, `MultiStatement`, which the conformance suite holds to having something behind it. USE opens a session of the console's own, gocql pinning a keyspace at the session rather than taking it per statement: a console that moves leaves every other tab where it was, and never moves the connection's own session, which the tree is read through. Every statement of a script is classified and put to the guard before the first runs, and a script stops where it failed. Cassandra counts nothing it changed, so `Affected` is -1 rather than a fiction, and the server's warnings come back as messages. A value is narrowed to the set the model holds: a UUID and a duration as Cassandra writes them, a decimal and a varint keeping their digits exactly, a collection as the JSON the cell viewer reads down into, and a CQL time as the reading of the clock it is. A row's destinations are fresh every row, or a row would change as the next was read (ADR-0083)*
- [x] **T2.51** **Partition-key-aware paging** → FR-12.3 — *the grid asks for rows by offset and may jump; Cassandra has no offset, only a paging state that resumes one query forward. So a connection remembers where each of a query's pages ended: the page after one already read resumes exactly, a page nothing has reached is walked to from the nearest state below it — ten of the grid's pages at most — and a jump past that is refused in words that say to scroll to it, rather than reading a table's worth of rows to draw one screen (NFR-P11). The states are keyed by the query, because a state belongs to the query it came from, and bounded. A paged read leaves off the LIMIT, which in CQL bounds the whole query rather than a page of it, while the statement the grid shows keeps it. Rows are ordered only by what clusters them, which the driver knows from the key it reads for the tree, and a row is addressed by its primary key. Nothing is counted: `ExactCount` and `ApproximateCount` are both unclaimed, because counting a table is a read of every partition on every node (ADR-0084)*
- [x] **T2.52** Consistency-level selector → FR-12.3 — *how many replicas must answer is Cassandra's question for the person asking, not the server's to settle, so it is a field on the connection form: a select of the levels a read can use, applied at the cluster rather than per statement, because gocql gives a query the session's level and CQL has no way to say one — the `CONSISTENCY` people know is a cqlsh command, not a statement — and because a level is how somebody means to work with data for as long as they are looking at it. ANY is not among them: it is a write's level alone, a write that reached any node at all even as a hint nobody has replayed, and a read at ANY is refused by the cluster, so offering it would be offering a setting that stops reading working. A name nobody offers is refused before a node is dialled, saying what there is to choose from, rather than quietly read at the default — somebody who typed EACH_QUORM meant something — and what nobody chose is a majority, which is what gocql itself would have used. The one setting reaches everything the connection sends, the tree's own reads of `system_schema` included, a cheap level for schema having been considered and rejected as a lie about the one setting whose purpose is to say how much agreement is enough: a keyspace of one replica asked for three is refused by Cassandra in its own words, which is also how a person finds out how it is replicated. Serial consistency is the other question, asked only of a conditional write, and waits for the row writer that would ask it (ADR-0085)*
- [x] **T2.53** Conformance green — *the shared driver suite (REQ-DRV-1) against a real cluster, in each shape a connection takes: opened on a keyspace, opened on none — which is how a person looks around before choosing one, and where every name the driver writes must carry the keyspace it is in — and over a materialized view, which a cluster keeps as a table of its own and this driver reads through the same contract as any other. The suite is told what a condition looks like here, CQL's being unlike SQL's: there is none true of every row without naming what the rows are partitioned by, because a cluster reads a partition and not a table. Nothing is offered as writable, this driver claiming no Writer, so the checks that change a row, guard one, or edit a query's result skip rather than pretend — and what is claimed is what it is held to: a relational paradigm, CQL with a lexer that knows it, the cluster's own filtering and ordering, and no count, no distinct values, no bulk load. Every mutation of the driver the suite would notice, it does: a claim with nothing behind it, a condition taken and never asked, a view with nothing under it, and a name written without its keyspace. The view was the one it did not notice, and that was the suite's own gap: it promised that a node claiming children opens onto some, and held it at one root node only, so anything deeper could lie — the walk now holds it at every depth, and the first thing it caught was PostgreSQL's, where an empty schema claimed children and listed none, the expander that turns and never opens; an empty schema now opens onto its tables, none of them, as an empty keyspace already did. 2.G is done — a wide-column store reads end to end*

## 2.H Kafka → J8 *(largest Phase 2 block)*

### Connect & auth
- [x] **T2.54** `twmb/franz-go` driver + bootstrap-server connection — *the first source here that holds no rows: a topic is an append-only log cut into partitions, nothing declares what a record looks like, a record is addressed by the offset it was written at rather than by a key of its own, and reading is consuming rather than querying — the model's stream paradigm, and everything this application comes to show of Kafka follows from it. franz-go is pure Go with no cgo, speaks the protocol directly rather than wrapping librdkafka, and its `kadm` and `kversion` packages answer what the cluster overview will ask. The form asks for one broker and allows more, a seed being only a way in — it is asked who the brokers are, and every later request goes to the broker that holds what is being asked about — and a further address written without a port takes the first's, a cluster usually being configured alike throughout. Opening dials, because the library will not: `kgo.NewClient` only reads its options, so a driver that handed that back as a live connection would report success for a cluster that is not there and fail later where nobody can connect it to a cause; `Open` pings instead, a broker-only metadata request tried against each seed until one answers, and a failure is classified into what to fix. Nothing is claimed that is not written — the paradigm and nothing else, no object kind until the tree (T2.59 onward), no stream operation until the task that writes it, and a browse that says plainly it reads no records yet rather than consuming without assigning partitions or minding whose offsets it commits (FR-13.19); `Query` stays zeroed for good, Kafka having no query language, which is the case that capability was written to leave empty. The version is a guess and says so: a broker states which versions of each API it speaks, not what release it is, so franz-go's reading of it is passed on as worded, "at least v4.0" included. The driver registers itself but stays out of `cmd/ikigai` until it has a tree, as Cassandra did (ADR-0086)*
- [x] **T2.55** SASL: PLAIN, SCRAM-SHA-256/512 → FR-1.11 — *Kafka carries no credentials in an address: a connection is made first and who you are is negotiated over it, the client naming a mechanism and the broker agreeing or not. So the form gained a mechanism to choose, defaulting to None because most brokers somebody runs for themselves ask nothing, a user, and a password kept where secrets are kept (FR-1.5). What is offered is what the driver speaks — OAUTHBEARER and GSSAPI are real and deliberately absent until T2.56, a setting nothing implements being a promise broken at the broker, the same rule that keeps ANY out of the consistency list. Everything a person can get wrong is answered before a broker has to: credentials typed with nothing chosen are refused rather than silently unsent, because somebody who typed a password is entitled to think it was used; a mechanism with no user is refused before dialling, the broker's own answer being a round trip later and less clear; a mechanism nobody speaks here is refused naming the choices rather than quietly downgraded to connecting as nobody; and a keychain that will not open is a fault in the settings rather than a refusal by the broker, the two needing different fixes. What only the broker can judge — a password it will not take — comes back as an authentication failure. PLAIN sends the password as text, which the field says and does not forbid: a broker on one's own machine is a fair thing to connect to, and refusing would only push people to worse ways round it, TLS staying available with verification on by default (NFR-S3). A second container, ikigai-kafka-sasl, asks for it: PLAIN from a JAAS file the image will not start a SASL listener without, SCRAM from credentials stored in the cluster (ADR-0087)*
- [~] **T2.56** SASL: OAUTHBEARER, GSSAPI/Kerberos → FR-1.11 — *OAUTHBEARER done; GSSAPI waits for a Kerberos library and a KDC. A token is unlike the mechanisms beside it: somebody else issued it, it already says who the bearer is and for how long, and there is no password and often no user name. So it gets a field of its own, kept in the keychain like any credential (FR-1.5), and where a user name is given it is passed as the identity to act as, which is what that field means here. A token and a password together are refused, one of them being ignored with nothing on screen to say which; so is a token typed under PLAIN or SCRAM, and a token with nothing chosen at all — a credential that would be sent nowhere is a lie about what is happening. The driver takes a token as given and neither fetches nor refreshes one: franz-go's mechanism can be built round a function returning a token, which is exactly where refreshing would go, and that function needs an issuer, a client id, a secret and a flow, none of which is asked for yet, so nothing pretends to do it — a token that expires ends the connection and the fix is a new token. Proven against a broker that really validates one, the tests minting their own unsecured JWS rather than carrying a written token that would expire; that validator is a test facility and nothing like a production issuer. It found a fault in the driver: a broker refusing a token answers inside the OAUTHBEARER exchange, which franz-go reports as unexpected data in an oauth response, and this driver read that as a cluster nobody could reach — sending a person to look at their network instead of their token. GSSAPI stays out of the offered list because franz-go ships no Kerberos mechanism at all: speaking it means a Kerberos library of this project's own plus a KDC and a keytab to prove it against (ADR-0088)*
- [x] **T2.57** AWS MSK IAM auth → FR-1.11 — *MSK IAM sends no secret at all: the client signs a request with AWS credentials and the broker asks IAM whether that signature belongs to somebody allowed in. The fields already there carry it — the access key id in User, the secret access key in Password, the session token in Token, each field's help saying what it means here — because three more always-visible fields, useful to one mechanism and empty for every other connection, would cost more clarity than reused labels do. No region is asked for: franz-go reads it from the broker's own address and falls back to AWS_REGION, so a field would decide a second time what is already decided correctly; but that failure is deferred to the handshake and worded as not determining a region, so it is classified as a setting to fix, naming AWS_REGION, rather than reaching a person as a cluster nobody could reach when the broker is answering perfectly well. Nothing reads the environment for the keys themselves: every credential here is one somebody typed and kept in the keychain (FR-1.5), and an ambient identity nobody typed — a profile, an instance role — is FR-1.14's business, separately and deliberately. Both keys are wanted before anything is dialled; a session token is optional, temporary credentials having one and permanent ones not. It lands with unit tests and no live proof, and says so: the mechanism answers only to real AWS and no local broker speaks it, so what tests hold is the mechanism built, the refusals before dialling, a session token accepted where the password mechanisms refuse one, and a region nobody named — while how the keys reach the signature is franz-go's own and unproven here (ADR-0089)*
- [x] **T2.58** mTLS → FR-1.10 — *no driver code at all, and that is the finding: the shared `tlsconf` has loaded a CA and a client certificate, and meant four different things by its four modes, since before this application spoke to Kafka, and the driver hands whatever it builds straight to franz-go. So mutual TLS was already written, and what had never happened was proving it against a server that actually demands a client certificate; until then, "it is written" and "it works" are different claims, and where a task turns out to be done the honest work is proving it rather than writing something so the task has a diff. A certificate is transport rather than a credential, so a connection carrying one with no SASL mechanism chosen is legal and must stay legal: the rule refusing credentials given with nothing chosen (ADR-0087) is about passwords and tokens, which a mechanism would carry, and a test now says so lest the two rules are tidied into one. The tests hold the shared helper rather than this driver's wiring, which every other driver depends on too: a certificate alone is enough to be let in, a listener requiring one refuses a connection without it, verify-full refuses a certificate issued for another name, require encrypts without verifying, and disable does not quietly speak TLS. The certificates live outside the repository in a fixed directory, a private key not belonging in a repository and the broker mounting the same files the tests read. The rig cost two lessons: the image will not start an SSL listener without keystores in its own shape, failing in its setup script before Kafka runs; and a truststore must carry a trust anchor rather than merely a certificate, an openssl-exported PKCS12 leaving PKIX with no anchors at all, which under TLS 1.3 surfaces as the broker closing the connection at the first request and the client library blaming missing TLS, pointing exactly away from the fault (ADR-0090)*

### Browse
- [x] **T2.59** Cluster overview: brokers, controller, cluster id, API versions → FR-13.1 — *every source so far had something to choose between at the top of its tree; a Kafka connection has none, being a connection to one cluster. So the root is the cluster itself, one node, with everything else to hang beneath it as it is written — a root left empty would say a connected source has nothing in it. Brokers are not nodes: the model has no object kind for one, deliberately, having been designed against Kafka before any driver existed (T0.29), and `model.Cluster` already carries them as a field — so they are part of what a cluster is, arriving through Describe into the structure tab beside how a Cassandra keyspace shows its replication. The node claims no children until it has some, topics and consumer groups being T2.62: that is the rule the conformance suite was taught to hold at every depth in T2.53, and it binds the driver that added the rule as much as the one it caught. A seed is not a broker — the addresses a connection started from are not nodes of the cluster, franz-go marking a seed by numbering it very negatively and giving it no rack — so seeds are dropped rather than shown as brokers nobody can find. The version is an attribute rather than a field, a broker never stating its release, only which API versions it speaks. The structure tab gained a cluster: brokers by id with their addresses and racks, the controller marked on its own row rather than explained in a legend elsewhere, one broker reading as one broker, and a cluster that named none saying so instead of showing an empty table (ADR-0091)*
- [x] **T2.60** Topic list with partitions, RF, message count, size; internal-topic filter → FR-13.2 — *the four numbers the requirement asks for do not cost the same: partition count and replication factor come free with the metadata that names the topics at all, a message count needs the earliest and latest offset of every partition, and a size needs a log-directory description from every broker holding a replica, sharded across the cluster. On a cluster of thousands of topics, drawing a list would mean thousands of partitions' offsets and a fan-out to every broker each time somebody opens a node. So the list carries what listing costs — topics in the tree, badged with how many logs each is cut into — and the expensive numbers belong to a topic somebody asked about, which is the rule that keeps a Cassandra table unbadged (NFR-P11). A count of records says "up to": it is the sum of high minus low watermark, an upper bound on what is retained rather than what was ever written, because a log is aged out and compacted behind its readers, and a number labelled "messages" would be believed. A size is replicated bytes and says so, a topic kept three times being reported three times over. Kafka's own topics are hidden as PostgreSQL's system schemas and Cassandra's system keyspaces are, and the filter is a pure function over the metadata so a test can prove it with an internal topic this rig has never had — the live cluster has none, and a mutation nothing could catch would have called it covered. Where the offsets cannot be read the watermarks stay unknown rather than reading as a log with nothing in it (ADR-0092)*
- [x] **T2.61** Topic detail: leader, replicas, ISR, earliest/latest offset, lag → FR-13.3 — *no driver code at all: everything shown was already read when a topic is described (T2.60), so a view saying considerably more costs exactly what it did before. The table is what a partition is — its number, the broker it is led from, the brokers holding copies, the copies in sync, and where its log begins and ends. A partition with no leader says "none" rather than -1: it cannot be read or written until one is elected, and -1 is a fact about the protocol that somebody would have to know how to read. Offsets nobody could read say "unknown", and a log whose ends meet says it is empty — the rule the driver already holds, said again where it is read, because not knowing and holding nothing are different things. Partitions whose in-sync set is smaller than their replica set are counted in words above the table: the copies exist, but some would not be there if the leader failed now, and that is what somebody opens this view to find. Lag is not here and is not missing either. A partition has no lag of its own, only a lag whoever reads it is behind by; franz-go's lag is keyed by group and begins by describing groups, and the model puts `Lag` on `GroupOffset` saying in as many words that group offsets are read on demand because a cluster may have thousands of groups. Showing it here would mean enumerating every group on a view that opens with a click — the cost ADR-0092 refused for the topic list, arriving by another door — so it lands with T2.76, where a group's lag is the subject rather than a figure borrowed from one (ADR-0093)*
- [x] **T2.62** Object-explorer integration: topics, partitions, groups, schemas → FR-2.2 — *the tree now goes cluster → topics → partitions, and cluster → consumer groups, which is what somebody connecting to Kafka came to look at. A partition is a leaf: what is under it is records, and a log of a million offsets is not a thing to expand — that is the grid's business (T2.63). Listing a topic's partitions costs the metadata and the offsets and nothing else; what a topic occupies on disk is a request to every broker holding a replica, and that is asked when somebody describes a topic rather than when they expand one, expanding a node and asking about it being different acts. A group node needs nothing described, the listing already carrying each group's state and protocol — describing every group to draw a list is the shape ADR-0092 refused for topics and would be worse here, a cluster having thousands of groups. Opening a cluster now costs two requests, the metadata that names its topics and a listing of its groups, both bounded by the size of the cluster rather than by what it holds, which is the line ADR-0092 actually drew. A partition says what is wrong with it and nothing when nothing is: it carries its leader and where its log runs, and how many copies are in sync only when that is fewer than the copies that exist, a remark on every healthy partition being noise that hides the one unhealthy one. Subjects are not here: they need a registry to ask (T2.69), and `KindSubject` stays unclaimed until then. A group exists only while something is reading, so the tests join one and stay joined while they look — which creates `__consumer_offsets`, and with it the first live proof of the internal-topic filter, which T2.60 could only hold with a unit test because no test cluster had an internal topic (ADR-0094)*

### Messages
- [x] **T2.63** Message browser rendering into the **standard data grid** (partition, offset, timestamp, key, value, headers) → FR-13.4 — *the grid reads rows and a log has records, and the difference runs through every decision: no order to ask for, a partition being ordered by itself and nothing ordering across partitions; nothing to filter on, a broker handing over bytes and asking no questions about them; and no language to write a condition in. What a log has instead is a position and a bound. A browse reads the log as it stands — where each partition ends is asked before any record is read, so a read that has caught up ends because it is known to have caught up rather than because nothing arrived within some timeout, and records written after it began are not in it, which is the only reading that can end at all. Partitions are assigned explicitly and no group is ever joined and nothing is committed, so looking at a topic cannot move anybody else's place in it (FR-13.19): not an option a caller sets, there being no way to ask this driver for the other thing. Every read is bounded, an unbounded read of a log being an unbounded read of a disk. What a log cannot be asked it refuses in its own words — a condition, a filter, an order, a row offset, a position, a log to follow — rather than ignoring it, with seeking and following saying they are written next. A key and a value are bytes, what they mean being a decoder's business (T2.68). Headers are a list rather than a map, Kafka letting a name repeat and a map quietly keeping one. A partition that fails is reported rather than read as a log that ended. This is also the first stream source the shared suite has met, and it passes what every driver passes while the checks for what it does not claim skip — the WHERE check of its own accord, this driver implementing no dialect (ADR-0095)*
- [x] **T2.64** Seek modes: beginning, end/last-N, specific offset, **timestamp** → FR-13.5 — *four of the five modes are a question about where to start, and one is not: reading from the end of a log means reading what has not been written yet, which is following it (T2.65), so it is refused in those words rather than silently returning nothing — nothing looks like an empty topic. The last N is N in each log: every partition has its own end, so asking a topic of eight partitions for the last hundred asks for the last hundred of each and may return eight hundred, which is what the mode can mean without ordering records across partitions, and what the UI must say rather than "the last N messages". A position before a log begins is its beginning, logs being aged out from the front and an offset valid yesterday being gone today — that is the log moving on, not an error; a position past the end leaves that log out rather than waiting for records nobody has written; and a time with nothing after it leaves its log out too, reading from the beginning instead being an answer to a question nobody asked. The arithmetic is a function over what the brokers said — where the logs begin, where they end, where a time falls in them — so every one of those cases is proved without a cluster, which is the lesson of every rule in this driver that a mutation walked past: a healthy broker will not hold still in these states long enough to test them. `Stream.SeekTimestamp` is claimed, a time costing one more request and only when somebody asks by time (ADR-0096)*
- [x] **T2.65** **Live tail with pause/resume and a bounded ring buffer** → FR-13.6, NFR-P10 — *every other read here is paged — the grid asks for a window, the source answers it, and a short page means the data ran out — and a tail is the opposite of all of it: it answers when somebody writes, for as long as they keep writing, and never runs out. So a following read never says the log ended, because it has not; returning end-of-data when nothing has been written yet would tell the grid a busy topic was empty. It follows that a tail cannot be bounded by a count of records either, there being no number that is the right number to stop a live log at — what keeps memory flat is the window the reader holds (NFR-P10) rather than a limit the driver was handed. A tail begins where the log is now, following being about what happens next; replaying a week of records to reach the present is a different request, and one that can still be made by naming a position — the last N, an offset, a time — which reads what is there and then carries on. Reading from the end, refused everywhere else because it returns nothing at all, is exactly what following means, so inside a tail it is allowed and means from now on; that refusal now ends "unless the log is being followed", and T2.64's live test checks the word rather than the sentence. The window drops the oldest and counts what it dropped, a person watching a topic move faster than they can read being owed that rather than a quiet gap. Pausing is the other half: a paused tail asks the source for nothing, so records wait where they are and resuming carries on from them — backpressure rather than loss, which is the only reading of the word that does not lose somebody's data while they are looking at it — and it takes effect at the next record rather than inside a read, a record already in hand being the one thing pausing must not discard. Two tests came out of asking what a mutation would prove rather than what the code does: a closed tail reports no error, but the guard doing that work was the one checking it had been closed, so the branch holding that being given up on is not a fault was never reached — cancelling a tail's context is the other way one ends, and something now exercises it; and nothing had ever closed a paused tail, where a missing wake-up would leave the follower asleep forever holding a stream, a deadlock no test would have noticed. Writing the second of those taught its own lesson: it deadlocked on an unbuffered send, because a follower started by NewTail may reach its first read or its first look at being paused in either order, and this one was asleep before it ever read — so a test may not assume where the follower is, it has to establish it, which a send that is not taken does. The window lives in the application layer beside the paged reads rather than in the driver, how much to keep being a question about the person watching rather than about the log. Browsing joins no consumer group and commits no offset while following either, a tail assigning its partitions explicitly, so watching a topic cannot move a production consumer's place in it (FR-13.19). And the mutation run found one more, by hanging rather than failing: a following read holds its lock inside the poll it waits in, so closing one — which is exactly what a reader who is done with a tail does — could never acquire that lock, and the close that would have ended the poll sat behind the reader waiting for a record that might never come. Closing now sets a flag nothing guards and closes the client, and a read closed under a poll ends rather than fails. The window had been shielded from it by cancelling before closing, which is precisely the ordering a driver should not need its callers to know (ADR-0097)*
- [x] **T2.66** Message detail view: decoded value, headers table, raw hex, copy → FR-13.8 — *a record reaches the grid as a row and that is enough to scan a topic and nowhere near enough to read one record, because the two fields worth reading are bytes and bytes are not one thing: the same bytes are text to whoever wrote them, JSON to the service that parses them, and a dump to whoever has to know exactly what went over the wire. So a value is offered in every form it admits and no others — bytes that do not parse are not offered as JSON, bytes that are not valid UTF-8 are not offered as text — because a form that cannot be read is worse than a form that is not there: text made of replacement characters is a lie about what was written, and somebody seeing it cannot tell whether the record or the reader is at fault. Hex is always there, bytes being always bytes, and it is what everything falls back to; the most decoded form comes first, a person opening a record wanting to see what it says and descending towards the bytes only when the meaning is in doubt. The form chosen is kept by name while it still applies, so that reading down a topic in hex does not jump back to text at every record, and where the next record's bytes do not admit it the choice falls back rather than pointing past the forms there are — a mutation proved that one by panicking with an index out of range, which is exactly the bug the reset exists to prevent. Headers are a table in the order they were written, repeats and all: Kafka lets a name repeat, the driver keeps every one, and a table built from a map would throw away the difference between a header sent once and a header sent twice. Their values are bytes and are shown as what they are, text where they are text and hex where they are not — before this they went through JSON and came out base64, so a trace-id read YWJjMTIz, which is not something anybody can act on. Nothing written and nothing there stay different: a record with no key says so rather than showing empty bytes, a producer sending no key and one sending an empty key meaning different things. Copy copies the form on screen, whole — somebody reading a record as hex who presses Copy means the hex. And the view is offered where the object is a topic and nowhere else, every other source's rows being rows that the form view already reads down. Which forms a value admits and what a headers table holds are decided in cellview where they are tested without a window, the shell drawing what it is given as it already does for the cell viewer; decoding beyond what the bytes say of themselves — Avro, Protobuf, a schema from a registry (FR-13.7, T2.68) — lands behind this same chooser as further forms (ADR-0098)*
- [x] **T2.67** Client-side filtering by key, header, or decoded-JSON predicate → FR-13.9 — *every filter until now went down to the server with the browse: the filter row's notation became source.Filters, a dialect rendered them, and the answer was over the whole table. A broker will not do that — it hands over bytes and asks no questions about them, which is why the driver refuses a filter in those words — so filtering records happens here instead, and the weakness of that is the whole design problem. A topic's browse is a window over a log, a few hundred records out of millions, so a filter over it has seen what it has read and nothing else; the footer therefore says filtering what has been read rather than filtered, because somebody who types a key and sees nothing must be able to tell the difference between no record has this key and no record I have read has this key. What it must not do is mean something different here: the same text typed on a table and on a topic has to mean the same thing, so these are the drivers' rules reproduced — contains ignores case, a pattern's % stands for any run and _ for one, NOT IN keeps NULL rows unless NULL was listed (which is why a filter excluding one key does not hide every record that has none), an empty list selects nothing and excluding nothing keeps everything. Bytes are filtered as the text they carry, a person typing order-1 meaning the word rather than its bytes, and numbers compare as numbers however they were carried. A header is addressed by its name or by name=value, each header reading as a pair, so typing the name finds it and a name sent twice keeps both of its values findable; a list matches where any one of its elements does. A window is filled rather than shortened, the grid taking a short window to mean the data ran out — a filter that dropped rows from one would end the grid wherever the first few matches did — so it reads further until the window is full or the source has nothing more. How many rows match stays unknown until everything has been read, a count that was only what had been found so far drawing a scrollbar that lies, and a filter stops at a bound rather than reading a log forever and says that too (NFR-P10). It is offered where the server will not filter and the source is a stream; every other paradigm that cannot filter is a separate decision about what such a grid may claim, left open deliberately. Searching a whole topic is a different act and is not this: that would mean reading the log from a position with a bound and a progress report, closer to the tail than to the filter row, and it should be asked for deliberately rather than smuggled in behind a gesture that means something smaller everywhere else (ADR-0099)*

### Deserialisation
- [x] **T2.68** Decoders: raw bytes/hex, UTF-8, JSON → FR-13.7 — *the record view already offered a value in the forms its bytes admit, but it decided that inline, by asking whether they parsed and whether they were valid UTF-8; Avro, Protobuf and JSON Schema cannot be decided that way, needing a registry to ask, a subject to ask about and a schema that may change between one record and the next. So the question was not how to add three more forms but what a form is, and the answer is that a form is a decoder seen from the side the person is on: the forms a value admits are the decoders that can read it, and the names somebody chooses between are the decoders' own names. One list and one answer to what these bytes can be read as, rather than a local mechanism and a registry mechanism kept agreeing with each other. A decoder says what the bytes are and the view says how that reads — JSON returns the document's own text so no number loses a digit, text returns a string, and raw returns the bytes unchanged, which is how hex stays a hex dump without the decoder knowing anything about dumps — so what T2.69 adds is entries in a list rather than a second renderer. A decoder that cannot read the bytes says so rather than guessing, and only the decoders that can read a value are offered for it; a fault is shown beside the bytes and never in place of them, an undecodable record still being worth seeing. Empty bytes are not a JSON document: they are the empty string, which somebody may well have written, and they are bytes — nothing written at all is a third thing again, which the record view says before any decoder is asked. How a topic is read is remembered by connection, topic and field, a key and a value being different bytes that get different answers and the same topic name on another connection being another topic; the choice is kept as the name it goes by rather than an index or a type, a name being what survives a build that offers a different set. Only a choice somebody made is remembered: opening a topic is not choosing how to read it and putting back a remembered form is not choosing it again — a probe showed that merely opening a record wrote the default back as though chosen, which would have recorded a preference nobody expressed and outlasted the default that produced it, so that a topic becoming decodable later would still open the old way. And a remembered way of reading that no longer fits falls back rather than showing nothing, records in one topic not having to agree. Nothing decodes in the grid yet: a topic's key and value cells still show a hex preview whatever form is chosen, which is a separate decision about what a cell of a log's row should say (ADR-0100)*
- [x] **T2.69** Confluent Schema Registry client (`franz-go/pkg/sr`) → FR-13.7 — *a broker hands over bytes and knows nothing about what they mean; a registry knows what they mean and nothing about the broker; and the only thing tying them together is five bytes at the front of a record — a zero, then the four-byte id of the schema that wrote it. That shape decides the rest. A registry is a second server, reached over HTTP, named per connection, authenticated separately and failing separately, and it is optional: most clusters are read without one, so naming none is not a fault and must not read as one — every registry call then refuses in those words, keeping "no subjects" and "no registry" different answers. The capability is claimed only where one is named, claiming it otherwise promising subjects a connection can never list, which makes this a claim about the connection rather than about the driver. A registry that was named and cannot be reached fails at connect, somebody who typed an address wanting to hear about it then rather than an hour later; one that was not named fails nothing. It refuses in the registry's own terms — unauthorised, a certificate that would not verify, a host that does not resolve, a refused connection — four problems with four fixes, and a registry fails in ways a broker never does. Listing subjects does not read their versions, a registry holding thousands and reading every version of each to draw a list being the shape ADR-0092 refused for topics; versions are read when a subject is opened. Credentials live in the keychain as every password here does, and an address carrying userinfo is redacted wherever it is written; over HTTPS it verifies by the same rules the broker's own connection follows. A decoder says which schema wrote a record and says what it cannot do: it names the schema, refuses a record written by a different one rather than reading it with the wrong schema, and refuses bytes with no header at all — Avro, Protobuf and JSON Schema are their own languages, written next, and a guess in the meantime would be worse than an honest refusal, which the decoder contract already expects to show beside the bytes rather than instead of them. The test registry needed a broker of its own: both existing brokers advertise localhost and sit on the default bridge network, which has no DNS, so a registry container bootstrapping from either would be handed the address localhost and try to reach the broker at itself — rather than recreate a working broker the gate depends on, the registry has its own pair on a user-defined network, one listener advertised for the containers beside it and another for tests on this machine (ADR-0101)*
- [x] **T2.70** Avro decoding → FR-13.7 — *the registry client could say which schema wrote a record and nothing more; this reads the record by it. Avro carries no field names or types in the payload — the schema is what makes the bytes mean anything at all — so reading means holding both, and a record read by the wrong schema would be nonsense rather than an error, which is why the id is checked before any decoding runs. A registry's decoder joins the list of forms a value is offered in rather than standing beside it (ADR-0100), and joins it first: it knows more about these bytes than anything worked out from the bytes alone, so it is the reading somebody most likely wants, with JSON, text and hex behind it so that a record whose schema cannot be read is still readable as what it is. The schema is parsed when the decoder is asked for rather than per record, a registry being free to hold a schema this build cannot read and saying so once beating failing on every record; resolving the decoder is a round trip, so it happens once when a record view opens rather than once per row drawn. A topic with no schema is the ordinary case and so is a connection with no registry: neither is a fault, and neither changes what the view could already do. Only the language that has been written is read — Avro decodes, Protobuf and JSON Schema still say they are not written yet, with a test of their own so the next task cannot quietly skip past them — and bytes that disagree with their schema say so plainly, the record still being shown as its bytes beside that so somebody looking at both can see which of the two is wrong. One thing decoding exposed: a decoded record may carry bytes in a field, and JSON has no way to write bytes, so through json.Marshal alone they come back base64 — the same unreadable answer a header gave before ADR-0100. Valid UTF-8 turned out not to be enough either, 0x01 being a perfectly good rune that shows as nothing, so bytes are shown as text where they are printable and as hex where they are not, in nested values and in a header's value alike; that closed a hole ten mutations had walked past, because the header fixture only ever used bytes that were invalid UTF-8 (ADR-0102)*
- [x] **T2.71** Protobuf decoding → FR-13.7 — *Avro arrives from a registry as a schema this build can parse and use at once; Protobuf does not. A registry holds the .proto source somebody wrote, and nothing can be read by it until that text has been compiled into descriptors — work protoc does on a developer's machine and which has to happen here, in process, with no files on disk. So the schema is compiled when the decoder is asked for rather than per record, compiling being work and a topic's records all sharing a schema; a schema this build cannot compile says so then, rather than failing on every record afterwards. The compiler is given the standard imports protoc has, a registry's schema being free to import google/protobuf/timestamp.proto and expect it to be there. Protobuf also says something about a record that Avro does not: a file may declare several messages, so Confluent's framing carries an index after the five-byte header naming which one this record is, and that index is followed exactly. A path that leads nowhere is refused, because on the wire a record's fields are numbered rather than named — reading it as the wrong message decodes into nonsense rather than failing, which makes a wrong index worse than a missing one; an empty index means the first message, which is what the shortcut a producer writes means. The index counts what the descriptor holds rather than what somebody typed: a map field generates a synthetic entry message, nested like any other and taking its place in the ordering, which a test assuming the source order of hand-written messages got wrong and would have got wrong for any schema with a map in it. Only populated fields are shown — Protobuf gives an unset field its type's zero value, and a record that says nothing about a field is not the same as one that says zero, so showing every field would put words in the producer's mouth. Values stay the values they are: bytes stay bytes so that what is shown is text where it reads as text and hex where it does not, and 64-bit integers stay integers, going through JSON having given base64 for the first and strings for the second — which is why the message is walked rather than marshalled. An enum reads as the name it was written as, a number saying nothing to somebody reading a record. JSON Schema is the one language left unwritten, and its refusal is held by a live test rather than by luck (ADR-0103)*
- [x] **T2.72** JSON Schema decoding → FR-13.7 — *Avro needed a schema parsed and Protobuf needed one compiled; JSON Schema needs neither, a record written under one being a JSON document with Confluent's five-byte header in front of it, and the schema saying what shape that document should have rather than how to read it. So the question was not how to decode it but what a schema adds to a record that can already be read without one, and the answer is the five bytes: they are valid UTF-8, so a framed record already reads as text today — the document showing with five junk characters glued to its front and never as JSON at all — and stripping them is what turns it back into a document, returned as its own text so that every number keeps its digits. What follows the header has to be a document: bytes that do not parse say so rather than being handed over as though they were JSON, and so does a record with nothing after its header. Validation is left out, and that is a decision rather than an omission — a decoder reports through its error, and a decoder whose Decode returns an error is dropped from the forms a value is offered in, so a validating decoder that found a document the wrong shape would take the value off the screen with it, contradicting what a decode failure is supposed to do: be shown beside the bytes rather than instead of them (ADR-0102). Saying valid JSON, wrong shape needs somewhere to say it that is neither the value nor an error, and there is no such place today; adding one changes what a decoder is and should be asked for rather than arrive as a side effect of reading a third language. All three languages a Confluent registry holds are now read, by three different routes — parsed, compiled and unwrapped — and all three arrive as entries in the same list of forms (ADR-0100), nothing about how a record is read, where the choice is remembered, or what a value is shown as having changed to accommodate any of them. The test that held the boundary while JSON Schema was unwritten is now the test that it reads: leaving it asserting a refusal that no longer happens would have been a lie the suite told quietly (ADR-0104)*
- [x] **T2.73** Per-topic key/value decoder selection, remembered → FR-13.7 — *most of this was built in T2.68 and proved there: the choice is kept by connection, topic and field, only a choice somebody made is remembered rather than the default a view opened with, and one that no longer fits falls back. Reading the task against that code found one clause of the requirement unmet, and it was the clause that mattered most for the languages a registry describes. A schema's decoder is resolved after the bytes arrive — a round trip to the registry, made once when the view opens — so at the moment the first record is drawn, the form somebody chose by name is not among the forms on offer. The old rule consulted what was remembered only while nothing had been chosen in this view, and inferred that from the list of forms being empty; after the first draw it stopped asking, so a remembered Avro reading was written and then silently never put back. A probe showed it plainly: the Avro form offered, and JSON selected beside it. The rule now says what it means — nobody has picked in this view — with a field for it rather than an inference from an empty list, so a remembered reading outlasts the first record and is restored when the registry answers. What somebody picks here is still theirs, and a remembered local reading still comes back as it did. The lesson is the one about limitations: T2.68's remembering was complete for the decoders that need nothing, and incomplete for the ones that need a server, and nothing said so until something asked for a schema's reading twice*
- [x] **T2.74** Schema Registry browser: subjects, versions, compatibility, version diff → FR-13.14 — *the client could read subjects, versions and schema text since T2.69, and there was nowhere to see any of it. Subjects hang under the cluster wherever a registry was named and nowhere else: a class opening onto nothing reads as a tree that failed, and a kind declared in the capabilities but never in the tree is the broken promise the shared suite looks for, so the claim is made from the same fact the tree is built from. Nothing is counted to draw it — counting subjects would put a third round trip, to a second server that can be down while the cluster is perfectly well, in front of every cluster somebody opens, and would stop them browsing topics when it failed; so the class is unbadged and what the registry has to say is said when somebody opens it, which is the cost ADR-0092 refused when it left a topic unbadged by size. A subject is a leaf: its versions are revisions of one thing rather than things of their own, so describing it is what shows them — every version with its schema id and language, the text of the newest, and the rule the next one will be checked against, asked for in the way that lets the registry resolve what a subject inherits, because what somebody wants to know is the rule that applies and not that this subject is silent about it; a registry too old for that parameter ignores it and says nothing, reaching the same place by a longer road, and a rule that cannot be read leaves the field empty rather than failing the describe, the versions being what a subject is and the rule a remark about it. The comparison had to be built rather than found, nothing in this application comparing two texts: a longest common subsequence over lines, written here rather than taken as a dependency — a hundred lines with no ambiguity in them, wanted in exactly one view — with the lines both texts share at their ends matched directly and kept out of the table, which is exact rather than a shortcut and keeps the ordinary change, a field added to a schema otherwise untouched, down to almost nothing; and a cap past which the answer becomes coarse, so that a text nobody anticipated degrades into a blunt answer rather than into a pause. The decision the feature rests on is smaller than any of that: a registry keeps a schema as the single string it was registered with, and for Avro and JSON Schema that is one line however large the schema is, so compared as they arrive two versions can only ever be reported as one line changing — true, and useless — and laying them out first is the entire difference between a comparison that says something and one that does not. It compares text and not meaning, two schemas saying the same thing in a different order reading as a change, because that is what was registered and what the next version will be checked against; a comparison that understood all three languages well enough to say nothing that matters changed would be three comparisons, each able to be wrong in its own way. What changed is marked by a sign as well as by colour, and the comparison names the two versions in itself rather than only in the pickers above it, a control not being a caption. Three tests were too weak and writing the mutations found them: the cap first tested with two texts sharing no lines at all, where the coarse answer and the careful one are identical and the guard cannot be observed at all; nothing holding the comparison to opening on the two most recent versions, because with two versions the last two and the first and the last are the same pair and it takes three to tell them apart; and an assertion on a picker's selected text, which Fyne draws through a rich text rather than a label and which the test walk could not see — answered not by weakening the test but by having the comparison say which two versions it is between. The driver's own tree work is held by tagged tests against a live registry, the mutation runner building without tags and reaching none of it (ADR-0105)*

### Groups, produce, admin
- [x] **T2.75** Consumer groups: list, state, members → FR-13.10 — *the tree has listed groups with their state since T2.59 and opening one gave nothing; this describes one. Almost all of it was decided already: the model held ConsumerGroup, GroupMember and GroupOffset before the task started, so what was missing was a case in Describe and a structure arm to draw it, which is the shape T2.74 left behind and following it was the whole of the work. A group stays a leaf — its members are not nodes, the live test from T2.59 saying so in as many words — and describing it says what the group is doing, who is in it, what client each member is and where it runs, and what each was given to read. An assignment is gathered by topic, so a member holding twenty partitions of one topic reads as that rather than as twenty entries; a member given nothing says so, which is what somebody is looking for when a group has members and is not getting through its logs. A group need not be consuming a log at all — Kafka Connect uses the same machinery for its own purposes — so an assignment that is not a consumer's reads as none rather than being unpacked as though it were partitions of a topic, which would invent an assignment nobody made. Lag is left out on purpose: it costs a request for the group's committed offsets and another for the ends of the logs, and the model says as much where it holds them — offsets are populated on demand, never while a group is being listed — so a live test holds describing a group to spending neither until T2.76 asks for them. One mutation was removed rather than excused: the guard it first aimed at, a check that a group has members before drawing a table of them, survived because section already declines to draw a table that is nothing but its own headings, so the check in front of it decided nothing and came out; the mutation now holds section's own check, which is what the arm leans on instead and what the rest of the file has relied on for as long as it has had tables*
- [x] **T2.76** Per-partition current offset / end offset / **lag** → FR-13.10 — *the model held GroupOffset and TotalLag before this started and T2.75 left Offsets empty on purpose, so what was missing was the reading and somewhere to see it. The capability was in the way first: Stream.ConsumerGroups promised the whole of StreamAdmin — resetting offsets, creating and deleting topics, altering configuration, adding partitions — so a driver that could show how far a group had got could not say so without also promising six destructive operations it had not written. The flags were already separate and the interface had not caught up; the half that changes nothing is now an interface of its own, StreamAdmin embeds it so anything implementing the whole still satisfies both, and the suite checks each claim against the interface that backs it (ADR-0107). Reading is one call: kadm's Lag describes the group, fetches its commits, lists the ends of the logs and works out the difference, and what it returns maps onto the model's GroupOffset field for field — including every -1, because a partition nothing has committed to, an end nobody could read, and a lag that could not be worked out from either are all -1 in both, and they are passed through rather than smoothed over: "has not committed" and "committed at the beginning" are different facts about a group and the difference is what somebody is looking for. Nothing is spent unless it is asked for — listing groups works out no lag, describing one reads no offsets, and the view offers a button rather than making the request, which is the shape a collection's sampling already had. What is shown never reads as -1: a position nobody could give says none, a lag that could not be measured says unknown, and a partition appearing to be ahead of its own log reads as up to date, the commit and the end of the log being read a moment apart so that is the measurement rather than the group, and the model says to clamp rather than trust the sign. The total counts only what could be measured and says how many it left out, a total quietly omitting the partitions nobody could read being the most misleading number on the page. The structure view's optional actions became a struct along the way: a third of them would have meant a fourth positional argument and a third nil at thirty call sites, the same churn as the struct and nothing better left behind. One collision worth recording: the new helper was called offsetsOf, which a tagged test file already used for something else, so the untagged build could not see it and only the tagged one failed — the pre-run check reported it as a package that does not build, which is the distinction it grew after T2.73, where exactly this looked like three wrong test names*
- [x] **T2.77** Produce a message: key, value, headers, partition, schema validation → FR-13.11 — *the first thing this driver does that changes anything, held to what every write here is held to: the guard is consulted before the client is touched, so a read-only connection refuses without dialling and a production one asks with nothing yet sent — which makes "nothing has been sent yet" in the dialog literally true rather than merely reassuring. Consent belongs to the record it was given for and is not left behind in the request for the next one. Validation checks rather than encodes: a subject names a schema the record must be readable by, and the bytes go to the same decoder a reader would use, because encoding a typed value into framed Avro, Protobuf or JSON Schema is ADR-0102 and ADR-0103 in reverse — three languages, each able to be wrong in its own way — and is not what the field asks for; the cost is stated rather than hidden, writing a schema'd record meaning bytes that are already framed, and an encoder being a task of its own. Naming no subject writes what it was given, most topics having no schema and a connection that names a registry still having to write to them. A partition is honoured or refused and never quietly moved: kgo chooses a partitioner once per client rather than once per record, so one partitioner does both — a record addressed to a partition goes there, one addressed to none is hashed by key as the default does, and none is -1 rather than 0 because 0 is a partition — and a partition the topic has not got is refused before producing by counting them, the partitioner answering with an index and having nowhere to put an error, a record in the wrong log being worse than one never written. The explorer offers it on a topic's menu and nowhere else, a greyed item on every table being an advertisement for something a table cannot do. An empty key box stays no key rather than becoming an empty one, those being different records: the first is spread across the partitions and the second always hashes to the same one. New here: the guard's own refusals are held by untagged tests, because the check happens before the client is touched, so a source with no connection at all still answers them and three mutations of the guard die to a source that never dials (ADR-0108)*
- [x] **T2.78** Topic admin: create, delete, add partitions → FR-13.12 — *the capability was in the way again and the same cut fixed it: StreamAdmin still bundled making and unmaking topics with resetting a group's offsets, so claiming the first meant implementing the second, which is T2.80 and one of the most destructive things here. There are three interfaces now — reading groups, administering topics, resetting offsets — StreamAdmin is all of them together, and the suite checks each claim against the one that backs it, which is ADR-0107's decision applied to the flags it did not reach the first time. Each operation passes the guard before a broker is asked anything, so a read-only connection refuses without dialling and a production one refuses without consent given for that act; untagged tests hold that, and they check which refusal came back rather than merely that something failed — the first version asked only whether there was an error and four mutations survived it, because a source with no connection fails at everything and a crash reads like a refusal. Kafka answers these as batch requests with a result per topic, so a failure arrives in either of two places and both are read: a call looking only at the request would report success for a topic that was never made, and the broker's own message is kept beside the error because "policy violation" alone leaves somebody with no idea which policy. Configuration is changed incrementally rather than by state, the whole-state form clearing every setting nobody mentioned and turning one change into an undeclared reset of everything else. In the interface each change is asked about before it happens and not only on production: deleting a topic says every record in it goes, and adding partitions says it cannot be undone and that which partition a key lands in changes for every record written afterwards. A topic's menu offers what can be done to a topic and a table's offers none of it. And the live test waits for the cluster to know what it was told rather than assuming it — creating a topic is accepted by the controller before every broker has heard of it, and reading it back at once found nothing, where a fixed pause would only have been lucky*
- [x] **T2.79** Topic config view/alter, defaults distinguished from overrides → FR-13.12 — *altering was written and claimed already (T2.78), incremental so that changing one setting leaves the others where they were; what was missing was reading. A topic now carries its settings, read best-effort as its size is — a broker that will not say leaves a topic described without them rather than one that cannot be described at all. Each setting keeps the broker's own word for where it came from, because what that means in English is the view's business and a word this build has never met must still arrive intact: one does, and it is shown as the broker said it rather than called unknown. The model's IsOverride was wrong and nothing had noticed, because nothing filled the type it belongs to — it counted DYNAMIC_DEFAULT_BROKER_CONFIG as somebody's choice, which is a default however dynamically it was set, and the helper's own doc says inherited; what is not an override is now named rather than inferred. A value the server withholds says so rather than showing blank, an empty cell reading as a setting with no value rather than one withheld, and a setting that really is empty says that instead, which is a different fact about it. The view counts what somebody chose before the table of forty, that being the question worth answering first; a topic whose settings could not be read says nothing about them rather than counting none, and one mutation exists only because the first test checked that the table was absent — which section already guarantees for a table of nothing but its own headings, so it proved nothing until the test also held that nothing was counted. Altering from the interface sets what is typed and nothing else, and says so: the form is not prefilled with what is there now, because the change is incremental and a form showing everything would imply all of it gets sent*
- [x] **T2.80** Consumer-group offset reset (earliest/latest/timestamp/specific) → FR-13.13 — *the most destructive thing here, and it deletes nothing: a group moved back processes again everything it has already done, and one moved forward never sees what it skipped. It is AccessAdmin, refused before the client is touched, so a read-only connection refuses without dialling and a production one refuses until it has been asked; untagged tests hold all of that. Where a seek lands was pulled out of the browsing code and shared rather than written again — the model asks that resetting to a timestamp mean exactly what reading from that timestamp meant, and one piece of code is the only way to promise it — but what was deliberately not shared is browsing's own question of whether there is anything there to read, which drops a partition whose position is at or past the end; moving a group to the end of its log is the commonest move there is, and borrowing that rule wholesale would have silently skipped every partition. Two of the mutations land on the extracted part and die to the browse tests that were already there, which is what says the extraction kept its meaning. A position past the end is clamped to the end, a group committed beyond its own log reading nothing until the log caught up. A group is moved where it has been rather than everywhere it could go: committing an offset for a partition it never read would add to what it is doing rather than change it. A group with anything reading through it is refused in words about what somebody was trying to do — Kafka refuses the commit too, but in words about group membership, which tells nobody to go and stop their consumers. The interface says what will happen before it happens and reads the position back in the confirmation, because a time somebody typed and a time this understood are not always the same time; a time written without a zone is this machine's own rather than silently UTC, since moving a group half a day because nobody said which noon was meant is exactly the incident all of this guards against*

### Safety — non-negotiable
- [x] **T2.81** **Browsing never joins a consumer group or commits offsets** — explicit partition assignment only → FR-13.19 — *the driver has read this way since T2.63, so this is a task about evidence rather than about code, and the evidence has to be of two kinds because the requirement is about what the driver never does. A live test reads a topic twice over and shows that the cluster gained no group from it, and that a group already sitting where it had got to is exactly where it was afterwards — which is the whole of what the requirement protects: somebody inspecting a topic must not move anybody's consumers. And an untagged test reads the driver's own source, because a test exercising only today's paths says nothing about tomorrow's: no file outside the tests may name a consumer group to the client, and browse.go must still assign partitions explicitly, so the check fails both if somebody adds the wrong thing and if somebody takes the right thing away. Both mutations are the forbidden defect written into the driver — a read made a member of a group, and explicit assignment swapped for a subscription — and both die to that check. What this does not do is prove the property for code nobody has written yet elsewhere; it holds the driver, which is where reading happens*
- [x] **T2.82** Every consume bounded (max messages / bytes / time) and cancellable → FR-13.20 — *most of the count half was already true: a browse takes a limit and stops at each log's end (T2.63), and a tail ends when it is cancelled (T2.65). What was not true was the second sentence of the requirement — a tail must not saturate the network or exhaust memory — because the driver was relying on franz-go's own fetch defaults, which are tens of megabytes and are defaults for a service meaning to keep up with a topic rather than for an application somebody is looking at one through. A read now says how much it may pull: a bound across a fetch, a smaller one from any single partition, and how long a fetch may wait. A record larger than the partition bound still arrives, a broker always returning at least one batch, so this slows a pathological topic rather than breaking it. The count decision came out into a function of its own so that bounded is something a test can check rather than a claim somebody made: a caller naming no limit is given this driver's own rather than all of a log, an unbounded read being how looking at a topic becomes an incident. What a test of the values alone would never notice is whether they reach the reader, so a check reads the driver's own source for that too — the same technique T2.81 needed, for the same reason. Left undone on purpose: a tail still honours no count, because the stream ignores a remaining count while following and changing that means touching the read loop where a Close-versus-Next deadlock was fixed in T2.65; a tail is held instead by the fetch bound, by the window of whoever is reading it, and by cancellation — written down here rather than left to be discovered*
- [x] **T2.83** Produce and offset-reset inherit read-only mode + production guardrails → FR-13.21 — *already true when the task was reached: T2.77 put the guard in front of producing, T2.78 in front of making and unmaking topics, and T2.80 in front of moving a group's offsets — each before the client is touched, so a refusal costs no request and a read-only connection never dials at all. What this task added is the one thing none of those could give on its own: a single test holding every operation that changes anything, with the list of them held to the interfaces themselves by reflection, so that a seventh way to change a cluster cannot quietly arrive without a guard in front of it. Three of the mutations are the guard taken off one family apiece, each already caught by that family's own test when it was written; what they prove here is that the systematic one catches them too, which is what makes it worth having. The fourth is the table falling behind the interfaces, which is the only thing this test can see and the others cannot. The per-family tests stay: when one of those fails it says which family, and when this one fails it says somebody added an operation and forgot*
- [ ] **T2.84** Conformance green against kafka + schema-registry testcontainers

## 2.I Auth & tunnelling

- [ ] **T2.85** SSH tunnel: password, private key, key+passphrase → FR-1.9
- [ ] **T2.86** SSH tunnel: `ssh-agent`, jump host → FR-1.9
- [ ] **T2.87** Cloud auth: AWS IAM/RDS token, GCP ADC, Azure AD/Entra → FR-1.14
- [~] **T2.88** Import connections from DBeaver, DBGate, TablePlus, DataGrip → FR-1.12
- [ ] **T2.89** Export/import connection set as JSON, secrets excluded → FR-1.13

## 2.J Production guardrails → J7

- [ ] **T2.90** Typed confirmation for writes on `production` connections → FR-4.9
- [ ] **T2.91** Always confirm `DELETE`/`UPDATE` with no WHERE → FR-4.9
- [ ] **T2.92** Verify read-only blocks every write path incl. Kafka produce → NFR-S4

## 2.K Quality

- [ ] **T2.93** E2E: **J2** (find a row and fix it)
- [ ] **T2.94** E2E: **J5** (move data)
- [ ] **T2.95** E2E: **J7** (work safely in production)
- [ ] **T2.96** E2E: **J8** (debug a stream)
- [ ] **T2.97** NFR-P10 benchmark: 10k msg/s tail stays responsive, memory bounded

### ✅ Phase 2 exit: J2, J5, J7, J8 complete

---

# PHASE 3 — Modelling → J4, J6

## 3.A DDL / table designer

- [ ] **T3.1** Column editor (name, type, length/precision, nullable, default, identity, comment) → FR-6.1
- [ ] **T3.2** PK, unique, FK (ON DELETE/UPDATE), check constraints → FR-6.2
- [ ] **T3.3** Index editor (columns, order, uniqueness, method, partial, include) → FR-6.3
- [ ] **T3.4** **DDL preview before execution** → FR-6.4, UX-6
- [ ] **T3.5** Source editors for views, procedures, functions, triggers, sequences → FR-6.5
- [ ] **T3.6** Rename with dependency awareness → FR-6.6
- [ ] **T3.7** Generate full DDL for object or schema → FR-6.7

## 3.B Schema compare & sync → J6

- [ ] **T3.8** Diff engine over the canonical model → `internal/diff`
- [ ] **T3.9** Compare two live databases → FR-7.1
- [ ] **T3.10** Compare live database against a saved model → FR-7.1
- [ ] **T3.11** Side-by-side diff tree (added/removed/changed/identical) → FR-7.2
- [ ] **T3.12** Sync-script generation with per-difference selection → FR-7.3
- [ ] **T3.13** Apply script or save to file → FR-7.4
- [ ] **T3.14** Ignore rules (schemas, patterns, whitespace, collation, comments) → FR-7.5
- [ ] **T3.15** **Heavy test coverage of `internal/diff`** → RISK-8

## 3.C ER diagram → J4 (productionise W3)

- [ ] **T3.16** Auto-generate from schema or table subset → FR-8.1
- [ ] **T3.17** Pan, zoom, drag with persisted layout → FR-8.2
- [ ] **T3.18** Render columns, keys, cardinality → FR-8.3
- [ ] **T3.19** Export PNG / SVG → FR-8.4
- [ ] **T3.20** Filter to N-degree neighbours → FR-8.5

## 3.D Charts (productionise W4)

- [ ] **T3.21** Chart types: line, bar, stacked bar, area, pie, scatter, histogram → FR-11.1
- [ ] **T3.22** Column-role assignment with auto-detection → FR-11.2
- [ ] **T3.23** Export PNG / SVG → FR-11.3
- [ ] **T3.24** Hover tooltips and click-to-filter → FR-11.4

## 3.E Query analysis

- [ ] **T3.25** `EXPLAIN` / `EXPLAIN ANALYZE` plan tree with cost heat → FR-5.13
- [ ] **T3.26** Transaction control with persistent open-transaction indicator → FR-5.14
- [ ] **T3.27** SQL formatter per dialect → FR-5.12
- [ ] **T3.28** Column statistics panel → FR-3.14
- [ ] **T3.29** Aggregate footer per column → FR-3.15

## 3.F Drivers — Tier 1/2 expansion

- [ ] **T3.30** SQL Server (`microsoft/go-mssqldb`) + introspection + dialect
- [ ] **T3.31** ClickHouse (`clickhouse-go/v2`)
- [ ] **T3.32** Oracle (`sijms/go-ora/v2`, thin mode)
- [ ] **T3.33** CockroachDB (pgx, distinct catalog)
- [ ] **T3.34** DuckDB (`marcboeker/go-duckdb`)
- [ ] **T3.35** Conformance green for all five

## 3.G Quality

- [ ] **T3.36** E2E: **J4** (understand an unfamiliar schema)
- [ ] **T3.37** E2E: **J6** (promote a schema change)

### ✅ Phase 3 exit: J4 and J6 complete

---

# PHASE 4 — Hardening & Release (v1.0 GA)

## 4.A Remaining sources

- [ ] **T4.1** Amazon Redshift (pgx, distinct catalog)
- [ ] **T4.2** Firebird (`nakagami/firebirdsql`)
- [ ] **T4.3** libSQL / Turso (`tursodatabase/go-libsql`)
- [ ] **T4.4** Azure Cosmos DB (`azcosmos`)
- [ ] **T4.5** Google Firestore
- [ ] **T4.6** DynamoDB (`aws-sdk-go-v2`)
- [ ] **T4.7** Conformance green for all six

## 4.B Models on disk

- [ ] **T4.8** Save model as a VCS-friendly file tree, one file per object → FR-7.6
- [ ] **T4.9** Load model from disk for comparison → FR-7.1

## 4.C Remaining features

- [ ] **T4.10** Saved named views per table → FR-3.16
- [ ] **T4.11** Multiple windows → FR-15.8
- [ ] **T4.12** Workspaces/projects → FR-15.9
- [ ] **T4.13** Full-text search across object definitions → FR-2.7
- [ ] **T4.14** Multi-select batch operations in explorer → FR-2.8
- [ ] **T4.15** Table-to-table copy across sources → FR-10.9
- [ ] **T4.16** Kafka export to NDJSON/CSV → FR-10.8, FR-13.16
- [ ] **T4.17** Optional app-level vault lock → NFR-S7
- [ ] **T4.18** Backup/restore app data as one archive → FR-17.5
- [~] **T4.19** Portable mode → FR-17.6

## 4.D Accessibility

- [ ] **T4.20** Full keyboard operability audit; no mouse-only actions → NFR-A1
- [ ] **T4.21** Visible focus rings throughout → NFR-A1
- [ ] **T4.22** WCAG AA contrast re-verified; OS text-scaling respected → NFR-A2
- [ ] **T4.23** **Document the screen-reader gap publicly** → NFR-A3, DoD-8

## 4.E Packaging & release

- [ ] **T4.24** macOS: signed `.dmg` + notarisation → NFR-D4
- [ ] **T4.25** Windows: signed `.exe`/`.msi` → NFR-D4
- [ ] **T4.26** Linux: `.deb`, `.rpm`, AppImage → NFR-D4
- [ ] **T4.27** In-app update check with release notes, user-controlled install → FR-15.10
- [ ] **T4.28** Homebrew cask, winget, Scoop, AUR → NFR-D5
- [ ] **T4.29** Dependency SBOM + CI vulnerability scanning → NFR-S9

## 4.F Final verification

- [ ] **T4.30** All §6.1 budgets green in CI → DoD-2
- [ ] **T4.31** Conformance passes for every Tier-1/Tier-2 source against real servers → DoD-4
- [ ] **T4.32** J1–J8 pass on macOS, Windows, Linux → DoD-3
- [ ] **T4.33** **Security review covering NFR-S1…S6** → DoD-6
- [ ] **T4.34** Verify offline operation → NFR-D6
- [ ] **T4.35** Zero open `M`-severity data-loss or credential defects → DoD-7
- [ ] **T4.36** **Resolve OQ-1: licence decided and applied** → RISK-5
- [ ] **T4.37** User documentation + known-gaps page → DoD-8

### ✅ Phase 4 exit: v1.0 GA

---

# PHASE 5 — Differentiators (v1.1+)

- [ ] **T5.1** Visual query designer: canvas, FK-inferred joins → FR-9.1
- [ ] **T5.2** Designer: columns, conditions, grouping, aggregates, ordering → FR-9.2
- [ ] **T5.3** Designer: live bidirectional SQL view → FR-9.3
- [ ] **T5.4** AI: provider abstraction incl. local endpoint → FR-14.5
- [ ] **T5.5** AI: NL → SQL/CQL grounded in live schema → FR-14.1
- [ ] **T5.6** AI: explain query / explain plan → FR-14.2
- [ ] **T5.7** AI: consent model — off by default, per-connection opt-in, data toggle → FR-14.3, FR-14.4
- [ ] **T5.8** AI: never auto-execute; statements land in the editor → FR-14.6
- [ ] **T5.9** CLI: run query, export, import, deploy model, diff → FR-16.1, FR-7.7
- [ ] **T5.10** Plugin SDK for third-party sources and formats → FR-16.2
- [ ] **T5.11** Map view for geo columns → FR-11.5
- [ ] **T5.12** Mongo change streams / Redis pub-sub live tail → FR-12.5
- [ ] **T5.13** Kafka ACL browsing/management → FR-13.15
- [ ] **T5.14** Kafka throughput sparklines → FR-13.17
- [ ] **T5.15** Sources: Snowflake, BigQuery, Elasticsearch/OpenSearch, Trino → §4.3
- [ ] **T5.16** Sources: Redpanda, NATS JetStream, Pulsar → §4.3
- [ ] **T5.17** Parquet, DBF, YAML formats → FR-10.10
- [ ] **T5.18** Saved import/export job definitions → FR-10.11
- [ ] **T5.19** Localisation framework → FR-15.11
- [ ] **T5.20** Optimistic-concurrency check on edit → FR-4.12
- [ ] **T5.21** Conditional formatting / heatmap → FR-3.17
- [ ] **T5.22** Cross-connection query directives → FR-5.17
- [ ] **T5.23** Multi-cursor / block selection in editor → FR-5.15
- [ ] **T5.24** Re-evaluate headless web mode (WASM) → FR-16.3, §8.6

---

# Standing tasks (every phase)

- [ ] **S1** Keep the conformance suite green — no phase ends with a red driver
- [ ] **S2** Keep §6.1 budget benchmarks green; investigate every regression at the commit that caused it
- [ ] **S3** Write an ADR for every architectural decision
- [ ] **S4** Review §12 risks at each phase boundary; update mitigations
- [ ] **S5** Never read, copy, or port DBGate source → RISK-5
- [ ] **S6** No new `if source == "x"` branches in shared code — use capability descriptors → REQ-DB-4
- [ ] **S7** Update **Current Position** at the top of this file every session

---

# Open questions blocking tasks

| OQ | Question | Blocks | Workaround |
|---|---|---|---|
| OQ-1 | Licence / hosting | T0.1 (module path), T4.36 | Placeholder module path; permissive assumption |
| OQ-4 | Platform priority | T0.7 CI runner order | Assume macOS → Linux → Windows |
| OQ-6 | Kafka distributions (OSS / Confluent Cloud / MSK / Redpanda) | T2.57 (MSK IAM) | OSS + Confluent SR for Phase 2 |
| OQ-7 | Cassandra self-hosted vs Astra | T2.47 | Assume self-hosted; Astra bundle is additive |
| OQ-8 | Prototype raster fallback alongside W1? | T0.43 | **Recommended: yes, in parallel** |
