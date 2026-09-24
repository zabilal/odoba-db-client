# Ikigai DB — Requirements Specification

**Version:** 0.2 (revised after toolkit and engine decisions)
**Date:** 2026-09-09
**Status:** Requirements gathering — pre-implementation
**Owner:** globaltrustmortgage@gmail.com

**Decisions locked in v0.2:**
- UI toolkit: **pure Go — Fyne** (§8)
- Phase 1–2 engines: PostgreSQL, MySQL/MariaDB, SQLite, MongoDB, Redis, Cassandra, **Kafka** (§4)
- Licence: **deferred**; permissive working assumption (§13, RISK-5)

---

## 1. Vision & Positioning

### 1.1 One-line statement

A single-binary, cross-platform, **pure-Go** data client that matches DBGate's functional breadth
(SQL + NoSQL + streaming: browse, edit, query, model, compare, import/export, visualise) while
replacing its dense, tool-first interface with a calm, keyboard-first, progressively-disclosed one.

### 1.2 Why this exists

DBGate is functionally excellent and unusually broad — one of the few clients treating MongoDB,
Redis and Cassandra as first-class alongside Postgres and SQL Server. Its weaknesses are not
features but *ergonomics*: crowded chrome, heavy modal use, an Electron runtime, and a Node core
that makes native distribution awkward.

Ikigai DB's wedge is **not** "more features". It is:

| Axis | DBGate today | Ikigai DB target |
|---|---|---|
| Runtime | Electron (~250 MB installed) | Native Go binary, no webview, no JS runtime |
| Cold start | 2–4 s | < 800 ms to interactive |
| Interface | Tool-first, dense, modal-heavy | Content-first, progressive disclosure, inline |
| Entry point | Menus + toolbars | Command palette (⌘K) + full keyboard coverage |
| Implementation | TypeScript/Node + Electron | **One language, one toolchain, top to bottom** |
| Streaming data | Not supported | **Kafka as a first-class citizen** |
| Safety | Uniform treatment of all connections | Environment-aware guardrails (prod is visibly different) |

The single-language property is itself a goal, not just a means: one build, one debugger, one
profiler, one dependency graph, no Node toolchain, no bridge serialisation between UI and core.

### 1.3 Non-goals (explicitly out of scope)

- **NG-1** Not an ORM, migration framework, or replacement for Flyway/Atlas/goose. We *read and
  diff* schemas and emit scripts; we do not own a migration lifecycle.
- **NG-2** Not a BI/reporting product. Charts exist to understand a result set, not to build
  stakeholder dashboards.
- **NG-3** Not a database server, proxy, or connection pooler.
- **NG-4** No mobile clients in any planned phase.
- **NG-5** No multi-tenant SaaS/cloud sync in v1.
- **NG-6** No bug-for-bug parity with DBGate's UI. Parity is measured against the *capability*
  inventory in §5, not against its screens.
- **NG-7** Not a Kafka *operations* console (no cluster provisioning, rebalancing, or Connect
  cluster management). We browse, produce, and administer topics — see §5.13.

---

## 2. Personas & Primary Journeys

### 2.1 Personas

| ID | Persona | Description | Dominant need |
|---|---|---|---|
| P1 | **Application developer** | Writes app code, touches the DB daily to inspect state and debug | Fast browse/filter, edit a few rows, run ad-hoc SQL |
| P2 | **Data engineer / analyst** | Lives in SQL, moves data between systems | Powerful editor, large result sets, export, cross-store |
| P3 | **DBA / platform engineer** | Owns schema and production safety | Schema compare, DDL, index editing, audit, read-only prod |
| P4 | **Polyglot developer** | Postgres + Mongo + Redis + Cassandra + Kafka in one product | Uniform UX across paradigms, not five separate tools |
| P5 | **Occasional/non-expert user** | Support, PM, QA — needs a value, not a query | Search, filters without SQL, form view, safe editing |

### 2.2 Critical journeys (must be frictionless)

- **J1 — Zero to first result.** Launch → add connection → expand schema → open table → see rows.
  *Under 60 s first-time, under 10 s returning.*
- **J2 — Find a row and fix it.** Filter by a value, edit a cell, review the generated SQL, commit.
  *No SQL written by the user.*
- **J3 — Write and iterate on a query.** Type SQL with completion, run, inspect, refine, save, find
  it again next week via history.
- **J4 — Understand an unfamiliar schema.** ER diagram / FK navigation / master-detail, without
  reading DDL.
- **J5 — Move data.** Export a filtered result to CSV/Excel/JSON; import a CSV with column mapping
  and a dry run.
- **J6 — Promote a schema change.** Diff dev vs staging, review the script, apply or save.
- **J7 — Work safely in production.** Read-only enforced, visually distinct, writes require
  explicit confirmation.
- **J8 — Debug a stream.** Open a Kafka topic, seek to a timestamp, tail live messages, decode
  Avro/JSON against the schema registry, inspect consumer-group lag.

---

## 3. Scope Summary

### 3.1 In scope for v1.0

Connection management · object explorer · data browser & grid · in-place editing with changeset
preview · SQL editor with completion and history · result grids · schema/DDL editing · schema
compare & sync · ER diagram · import/export · charts · Mongo/Redis/Cassandra first-class ·
**Kafka topic and message browsing** · themes · command palette · SSH tunnelling · credential vault.

### 3.2 In scope, later phases

Visual query designer · AI assistant · local NDJSON data tools · CLI · plugin SDK · additional
engines · API-endpoint browsing.

### 3.3 MoSCoW key

`M` = Must (v1.0 blocker) · `S` = Should (v1.0 if time) · `C` = Could (v1.1+) · `W` = Won't (this cycle)

---

## 4. Data Source Support Matrix

Capability tiering keeps scope honest: not every engine gets every feature.

### 4.1 Support tiers

- **Tier 1 — Full.** Every applicable feature: browse, edit, DDL, compare, ER, import/export.
- **Tier 2 — Core.** Browse, edit, query, export, read-only introspection. No schema sync/ER.
- **Tier 3 — Basic.** Connect, query/command, read results, export. Minimal introspection.

### 4.2 Matrix

| Source | Tier | Go driver | Phase | Notes |
|---|---|---|---|---|
| **PostgreSQL** | 1 | `jackc/pgx/v5` | 1 | Reference implementation; COPY for bulk I/O |
| **MySQL** | 1 | `go-sql-driver/mysql` | 1 | |
| **MariaDB** | 1 | `go-sql-driver/mysql` | 1 | Same wire, divergent catalog & feature probes |
| **SQLite** | 1 | `modernc.org/sqlite` | 1 | `mattn/go-sqlite3` viable — CGO is already required (§8.5) |
| **MongoDB** | 1* | `mongodb/mongo-go-driver` | 2 | *Full within the document paradigm |
| **Redis / Valkey** | 1* | `redis/go-redis/v9` | 2 | All types: string, hash, list, set, zset, JSON, stream |
| **Cassandra / ScyllaDB** | 2 | `gocql/gocql` | 2 | CQL editor, partition-key-aware paging |
| **Apache Kafka** | 1* | `twmb/franz-go` + `kadm` + `sr` | 2 | *Full within the streaming paradigm — see §5.13 |
| SQL Server | 1 | `microsoft/go-mssqldb` | 3 | |
| ClickHouse | 2 | `ClickHouse/clickhouse-go/v2` | 3 | Native protocol |
| Oracle | 2 | `sijms/go-ora/v2` | 3 | Thin mode; `godror` only if thick features demanded |
| CockroachDB | 2 | `jackc/pgx/v5` | 3 | pg wire, distinct catalog |
| DuckDB | 2 | `marcboeker/go-duckdb` | 3 | CGO — no longer a constraint (§8.5) |
| Amazon Redshift | 2 | `jackc/pgx/v5` | 4 | pg wire, distinct catalog |
| Firebird | 3 | `nakagami/firebirdsql` | 4 | |
| libSQL / Turso | 2 | `tursodatabase/go-libsql` | 4 | |
| Azure Cosmos DB | 3 | `azure-sdk-for-go/.../azcosmos` | 4 | |
| Google Firestore | 3 | `cloud.google.com/go/firestore` | 4 | |
| DynamoDB | 3 | `aws-sdk-go-v2` | 4 | |

**Kafka-adjacent (Phase 5):** Redpanda (Kafka-compatible, same driver) · Confluent Schema Registry
(Phase 2, required for J8) · AWS Glue Schema Registry.

### 4.3 Beyond DBGate (differentiators, Phase 5+)

Snowflake (`gosnowflake`) · BigQuery · Elasticsearch/OpenSearch · Trino · NATS JetStream ·
Pulsar · InfluxDB · Neo4j.

### 4.4 Cross-cutting requirements

- **REQ-DB-1 (M)** Adding a source must require implementing one interface (§10.2) and **zero edits
  to UI code**.
- **REQ-DB-2 (M)** Every source declares a machine-readable capability descriptor; the UI hides or
  disables affordances the source does not support rather than failing at runtime.
- **REQ-DB-3 (M)** The canonical model must accommodate **four paradigms** — relational, document,
  key-value, and **stream/log** — without special-casing in the UI. Kafka is the forcing function:
  it has no rows, no schema owned by the server, and offsets instead of primary keys.
- **REQ-DB-4 (M)** Paradigm-specific behaviour is expressed through capability descriptors and
  per-paradigm view components, never through `if engine == "kafka"` branches in shared code.

---

## 5. Functional Requirements

### 5.1 Connection Management

| ID | Pri | Requirement |
|---|---|---|
| FR-1.1 | M | Create, edit, duplicate, delete, reorder saved connections |
| FR-1.2 | M | Per-source connection forms with only relevant fields; sensible defaults (port, SSL) |
| FR-1.3 | M | Paste-a-connection-string parsing that auto-fills the form (`postgres://`, `mongodb+srv://`, JDBC, `.pgpass`, ODBC, Kafka bootstrap lists) |
| FR-1.4 | M | "Test connection" with a precise, actionable error (host unreachable vs auth vs TLS vs DB missing) |
| FR-1.5 | M | Credentials in the OS keychain, never plaintext in config |
| FR-1.6 | M | Connection folders/groups with colour and icon |
| FR-1.7 | M | **Environment tagging** (`local`/`dev`/`staging`/`production`) driving colour, badge, guardrails |
| FR-1.8 | M | **Read-only mode** enforced in the Go layer, not just the UI |
| FR-1.9 | M | SSH tunnel: password, private key, key+passphrase, `ssh-agent`, jump host |
| FR-1.10 | M | TLS/SSL: CA, client cert/key, `verify-full`/`verify-ca`/`require`/`disable` |
| FR-1.11 | M | **SASL** for Kafka and Cassandra: PLAIN, SCRAM-SHA-256/512, OAUTHBEARER, GSSAPI/Kerberos, AWS MSK IAM |
| FR-1.12 | S | Import connections from DBeaver, DBGate, TablePlus, DataGrip, `.pgpass`, `~/.my.cnf` |
| FR-1.13 | S | Export/import a connection set as JSON, secrets excluded by default |
| FR-1.14 | S | Cloud auth: AWS IAM/RDS token, GCP ADC, Azure AD/Entra |
| FR-1.15 | S | Auto-reconnect with backoff; explicit "disconnected" state, never a silent failure |
| FR-1.16 | C | Health indicator (latency, server version, active sessions, broker count) |
| FR-1.17 | C | Per-connection session defaults: search_path, timezone, statement_timeout, role, consistency level |

### 5.2 Object Explorer

| ID | Pri | Requirement |
|---|---|---|
| FR-2.1 | M | Lazy, virtualised tree: connection → database → schema → object class → object |
| FR-2.2 | M | Object classes per paradigm: tables, views, materialised views, columns, indexes, constraints, procedures, functions, triggers, sequences, types (relational); collections (document); keyspaces (Cassandra); key patterns (Redis); **topics, partitions, consumer groups, schemas** (Kafka) |
| FR-2.3 | M | Fuzzy filter across the whole tree, matching on path not just leaf name |
| FR-2.4 | M | Context actions: open data, open structure, script as (SELECT/INSERT/UPDATE/DDL), rename, drop, truncate, refresh |
| FR-2.5 | M | Row-count/size/message-count badges, fetched lazily and cancellable (never block expansion) |
| FR-2.6 | M | Pinned/favourite objects surfaced above the tree |
| FR-2.7 | S | Full-text search across object *definitions* (find a column, a string in a proc body) |
| FR-2.8 | S | Multi-select for batch operations (drop N, export N, script N) |
| FR-2.9 | C | Recently-opened objects per connection |

### 5.3 Data Grid — *the single most important component*

| ID | Pri | Requirement |
|---|---|---|
| FR-3.1 | M | Virtualised grid: smooth scroll over result sets of any size, windowed server-side fetch |
| FR-3.2 | M | Column resize, reorder, hide/show, **freeze/pin left** |
| FR-3.3 | M | Sort by one or many columns, applied server-side |
| FR-3.4 | M | Per-column filters, Excel-style: distinct-value picklist, multi-select, operators (`=`, `≠`, `>`, `<`, `LIKE`, `IN`, `IS NULL`, `BETWEEN`, regex where supported) |
| FR-3.5 | M | Free-text filter row accepting a compact DSL per column (`>100`, `!=x`, `a,b,c`, `~regex`, `NULL`) |
| FR-3.6 | M | Global WHERE-clause editor bound to the grid, showing the effective generated SQL |
| FR-3.7 | M | Cell selection: single, range, row, column, ⌘/Ctrl-click multi; copy as TSV/CSV/JSON/INSERT/Markdown |
| FR-3.8 | M | Type-aware rendering: NULL distinct from empty string, booleans, dates in local tz with UTC on hover, JSON pretty-printed, binary as hex/size, geometry, arrays, enums |
| FR-3.9 | M | Expandable cell viewer for long text / JSON / XML / blob with syntax highlighting |
| FR-3.10 | M | **Form view** — one record laid out vertically; essential for wide tables and documents |
| FR-3.11 | M | Foreign-key navigation: jump to the referenced row, and to referencing rows |
| FR-3.12 | M | FK lookup display — show the human-readable label of a FK target inline |
| FR-3.13 | S | Master-detail: expand a row into child rows in a nested grid |
| FR-3.14 | S | Column statistics: distinct count, null %, min/max/avg, histogram |
| FR-3.15 | S | Aggregate footer per column (sum/avg/count/min/max) over selection or full set |
| FR-3.16 | C | Save named views (filter + sort + visible columns) per table |
| FR-3.17 | C | Conditional formatting / heatmap on numeric columns |

> **Build note.** No pure-Go toolkit ships a grid meeting FR-3.1–FR-3.8. This is a first-class
> engineering workstream, not a widget selection. See §8.4 and RISK-1.

### 5.4 Data Editing & Changeset

| ID | Pri | Requirement |
|---|---|---|
| FR-4.1 | M | In-place cell editing with the correct editor per type (text, number, date picker, bool toggle, enum select, JSON editor) |
| FR-4.2 | M | Insert row, delete row(s), duplicate row |
| FR-4.3 | M | **Pending changeset model**: edits accumulate locally, visually marked (added/modified/deleted); nothing hits the store until committed |
| FR-4.4 | M | **Preview the exact statement** the changeset will execute, before commit, always |
| FR-4.5 | M | Commit as one transaction where the store supports it; on failure roll back fully and report the offending row |
| FR-4.6 | M | Revert individual cell, row, or the entire changeset |
| FR-4.7 | M | Refuse to edit a result set with no reliable row identity, with a clear explanation and the option to nominate a key |
| FR-4.8 | M | **Editable query results** — edit `SELECT` output when it maps to a single updatable table |
| FR-4.9 | M | Production guardrail: writes on a `production` connection require typed confirmation; `DELETE`/`UPDATE` with no WHERE always require it |
| FR-4.10 | S | Paste a block of TSV/CSV from the clipboard directly into the grid |
| FR-4.11 | S | Bulk set-column-value across a selection |
| FR-4.12 | C | Optimistic-concurrency check (warn if the row changed underneath you) |

### 5.5 Query Editor

| ID | Pri | Requirement |
|---|---|---|
| FR-5.1 | M | Per-dialect syntax highlighting (SQL, CQL, MongoDB shell, Redis commands) |
| FR-5.2 | M | Context-aware autocomplete: keywords, schemas, tables, columns (alias-resolved), functions, snippets |
| FR-5.3 | M | Run all / run selection / run statement at cursor (⌘↵) |
| FR-5.4 | M | Multi-statement scripts producing multiple result tabs |
| FR-5.5 | M | Cancel a running query genuinely, at the driver level |
| FR-5.6 | M | Execution timing, rows affected, server messages/notices pane |
| FR-5.7 | M | Query parameters (`:name` / `?`) with a prompt panel and remembered values |
| FR-5.8 | M | **Query history** — persistent, searchable, per-connection, with timestamp, duration, row count, success/failure |
| FR-5.9 | M | Saved queries/scripts with folders; tabs restored across restarts |
| FR-5.10 | M | Errors mapped back to a position in the editor |
| FR-5.11 | M | Line numbers, current-line highlight, bracket matching, auto-indent, find/replace with regex |
| FR-5.12 | S | Format/beautify per dialect |
| FR-5.13 | S | `EXPLAIN` / `EXPLAIN ANALYZE` rendered as a readable plan tree with cost heat |
| FR-5.14 | S | Transaction control: explicit begin/commit/rollback with a persistent indicator when open |
| FR-5.15 | S | Multiple cursors, block selection |
| FR-5.16 | C | Inline result preview per statement (notebook-style) |
| FR-5.17 | C | Cross-connection query in one script (`@dev`/`@prod` directives) |

> **Build note.** Fyne's `widget.Entry` is a plain text field. FR-5.1, FR-5.2, FR-5.11 and FR-5.15
> require a custom editor widget. This is the largest single build risk in the project. See §8.4
> and RISK-2.

### 5.6 Schema / DDL Editing

| ID | Pri | Requirement |
|---|---|---|
| FR-6.1 | M | Table designer: columns (name, type, length/precision, nullable, default, identity, comment) |
| FR-6.2 | M | Primary key, unique constraints, foreign keys (ON DELETE/UPDATE), check constraints |
| FR-6.3 | M | Index editor: columns, order, uniqueness, method (btree/hash/gin/gist), partial predicate, include columns |
| FR-6.4 | M | Every structural change previews its DDL before execution |
| FR-6.5 | M | Create/alter/drop for views, procedures, functions, triggers, sequences with a source editor |
| FR-6.6 | S | Rename with dependency awareness (warn what breaks) |
| FR-6.7 | S | Generate full DDL for any object or whole schema |
| FR-6.8 | C | Partition and tablespace management where supported |

### 5.7 Schema Compare & Synchronise

| ID | Pri | Requirement |
|---|---|---|
| FR-7.1 | M | Compare two live databases, or a live database against a saved model |
| FR-7.2 | M | Side-by-side diff tree: added / removed / changed / identical, with per-object detail |
| FR-7.3 | M | Generate a sync script; user selects which differences to include |
| FR-7.4 | M | Apply the script, or save it to a file |
| FR-7.5 | M | Ignore rules (schemas, name patterns, whitespace, collation, comments) |
| FR-7.6 | S | **Save the model to disk** as a VCS-friendly file tree (one file per object) |
| FR-7.7 | S | Deploy a saved model to a target from the CLI, for CI/CD |
| FR-7.8 | C | Data compare & sync for reference/lookup tables |

### 5.8 ER Diagram

| ID | Pri | Requirement |
|---|---|---|
| FR-8.1 | M | Auto-generate from a schema or a chosen subset of tables |
| FR-8.2 | M | Pan, zoom, drag nodes; auto-layout with persisted manual override |
| FR-8.3 | M | Show columns, keys, and relationship cardinality |
| FR-8.4 | S | Export to PNG / SVG |
| FR-8.5 | S | Filter to N-degree neighbours of a selected table |
| FR-8.6 | C | Edit schema from the diagram |

### 5.9 Visual Query Designer

| ID | Pri | Requirement |
|---|---|---|
| FR-9.1 | C | Drag tables onto a canvas; joins inferred from foreign keys, editable |
| FR-9.2 | C | Pick output columns, conditions, grouping, aggregates, ordering without SQL |
| FR-9.3 | C | Live bidirectional view of the generated SQL |

> Downgraded from `S` to `C` in v0.2: it shares the canvas infrastructure with the ER diagram
> (FR-8) but adds a large amount of bespoke interaction. Deferred to protect Phase 1–3.

### 5.10 Import & Export

| ID | Pri | Requirement |
|---|---|---|
| FR-10.1 | M | Export formats: CSV, TSV, JSON, NDJSON, Excel (xlsx), SQL INSERT, Markdown, HTML, XML |
| FR-10.2 | M | Export scope: current selection, filtered result, whole table, multiple tables in one batch |
| FR-10.3 | M | Streaming export — memory must not scale with row count |
| FR-10.4 | M | Import CSV/TSV/JSON/NDJSON/Excel with delimiter/encoding/header detection |
| FR-10.5 | M | Import column mapping UI, type coercion, and a **dry-run preview** listing error rows |
| FR-10.6 | M | Import modes: insert / upsert / replace / append-to-new-table, with batch size and error policy |
| FR-10.7 | M | Progress with rows/sec, ETA, and a working cancel |
| FR-10.8 | S | **Kafka import/export**: export topic messages to NDJSON/CSV; replay a file into a topic |
| FR-10.9 | S | Table-to-table copy, including **across different sources** |
| FR-10.10 | S | DBF, YAML, Parquet |
| FR-10.11 | C | Saved, re-runnable import/export job definitions |

### 5.11 Visualisation

| ID | Pri | Requirement |
|---|---|---|
| FR-11.1 | S | Charts from a result set: line, bar, stacked bar, area, pie, scatter, histogram |
| FR-11.2 | S | Column-role assignment (x, y, series) with sane auto-detection |
| FR-11.3 | S | Export chart as PNG/SVG |
| FR-11.4 | S | Hover tooltips and click-to-filter on chart marks |
| FR-11.5 | C | Map view for geo columns (PostGIS geometry, lat/lon pairs, GeoJSON) |

> **Build note.** Pure-Go charting libraries (`gonum/plot`, `wcharczuk/go-chart`) render static
> images. FR-11.4 requires a hit-test overlay we build ourselves, or native drawing via Fyne canvas
> primitives. FR-11.5 has no viable pure-Go tile-map story and is `C` accordingly.

### 5.12 NoSQL-Specific

| ID | Pri | Requirement |
|---|---|---|
| FR-12.1 | M | **Mongo**: browse collections in table *and* JSON view; edit documents in a schema-aware JSON editor; aggregation-pipeline editor; index management; `mongosh`-compatible command console |
| FR-12.2 | M | **Redis**: key browser with pattern SCAN and type filter; dedicated editors for string, hash, list, set, sorted set, JSON, stream; TTL display and edit; raw command console; memory/keyspace info |
| FR-12.3 | M | **Cassandra**: keyspace/table browse, CQL editor with completion, partition-key-aware paging, consistency-level selector, replication/strategy display |
| FR-12.4 | S | Document-store schema inference — sample N documents, present an inferred shape, drive grid columns from it |
| FR-12.5 | C | Mongo change streams / Redis pub-sub live tail |

### 5.13 Kafka & Streaming — *new in v0.2*

Kafka is not a database; it is a partitioned, append-only log. It gets its own paradigm rather
than being forced into the relational model (see REQ-DB-3).

| ID | Pri | Requirement |
|---|---|---|
| FR-13.1 | M | **Cluster overview**: brokers (id, host, rack, controller), cluster id, API versions, aggregate topic/partition counts |
| FR-13.2 | M | **Topic browser**: list topics with partition count, replication factor, total messages, size on disk; filter internal topics |
| FR-13.3 | M | **Topic detail**: per-partition leader, replicas, ISR, earliest/latest offset, lag |
| FR-13.4 | M | **Message browser**: consume from a topic into the standard data grid — columns for partition, offset, timestamp, key, value, headers |
| FR-13.5 | M | **Seek modes**: from beginning, from end (last N), from a specific offset, **from a timestamp**; per-partition or all |
| FR-13.6 | M | **Live tail** with pause/resume and a bounded ring buffer (never unbounded memory) |
| FR-13.7 | M | **Deserialisation**: raw bytes/hex, UTF-8 string, JSON, and **Avro / Protobuf / JSON Schema via Confluent Schema Registry**; per-topic key and value decoders, remembered |
| FR-13.8 | M | Message detail view: pretty-printed decoded value, headers table, raw hex, copy in any form |
| FR-13.9 | M | **Filter messages** client-side by key, header, or a value predicate over decoded JSON fields |
| FR-13.10 | M | **Consumer groups**: list groups, state, members, per-partition current offset / end offset / **lag** |
| FR-13.11 | S | **Produce a message**: key, value, headers, target partition, with schema validation when a registry is configured |
| FR-13.12 | S | **Topic admin**: create, delete, add partitions, view and alter topic configs (with defaults distinguished from overrides) |
| FR-13.13 | S | **Reset consumer-group offsets** (to earliest / latest / timestamp / specific), gated behind the production guardrail (FR-4.9) |
| FR-13.14 | S | **Schema Registry browser**: subjects, versions, schema text, compatibility mode, diff between versions |
| FR-13.15 | C | ACL browsing and management |
| FR-13.16 | C | Export a consumed window to NDJSON/CSV (satisfies FR-10.8) |
| FR-13.17 | C | Throughput sparkline per topic (messages/sec, bytes/sec) |
| FR-13.18 | W | Kafka Connect and ksqlDB management (see NG-7) |

**Kafka-specific constraints**

- **FR-13.19 (M)** Browsing must never join a consumer group or commit offsets. All reads use
  explicit partition assignment so that inspecting a topic cannot perturb production consumers.
- **FR-13.20 (M)** Every consume operation is bounded (max messages, max bytes, or a time window)
  and cancellable. Tailing a high-volume topic must not exhaust memory or saturate the network.
- **FR-13.21 (M)** Producing and offset resets are **write operations** and inherit read-only mode
  (FR-1.8) and production guardrails (FR-4.9) without exception.

### 5.14 AI Assistant

| ID | Pri | Requirement |
|---|---|---|
| FR-14.1 | C | Natural language → SQL/CQL, grounded in the live schema |
| FR-14.2 | C | Explain a query; explain a query plan |
| FR-14.3 | C | Chat over schema, with an explicit toggle for whether *data* may be sent |
| FR-14.4 | C | **Off by default.** Opt-in per connection. Never send production data without explicit per-session consent |
| FR-14.5 | C | Pluggable providers including a local/self-hosted endpoint; provider and model always visible |
| FR-14.6 | C | Generated statements are never auto-executed; they land in the editor for review |

### 5.15 Application Shell & UX Infrastructure

| ID | Pri | Requirement |
|---|---|---|
| FR-15.1 | M | **Command palette (⌘K)** exposing every action, with fuzzy search and shortcut discovery |
| FR-15.2 | M | Tabbed workspace: split panes, drag to reorder, pin, full session restore on relaunch |
| FR-15.3 | M | Light and dark themes following the OS, plus manual override; a small set of accent choices |
| FR-15.4 | M | Complete keyboard coverage; a searchable shortcut reference; customisable bindings |
| FR-15.5 | M | Native menu bar, native file dialogs, native notifications |
| FR-15.6 | M | Non-blocking background tasks with a task centre (exports, imports, long queries, tails) |
| FR-15.7 | M | Errors surfaced as actionable, dismissible, copyable messages — never a raw stack trace, never a silent failure |
| FR-15.8 | S | Multiple windows |
| FR-15.9 | S | Workspaces/projects grouping connections + tabs + saved queries |
| FR-15.10 | S | In-app update check with release notes and user-controlled install |
| FR-15.11 | C | Localisation framework (English first; no hardcoded UI strings) |

### 5.16 Automation & Integration

| ID | Pri | Requirement |
|---|---|---|
| FR-16.1 | C | CLI: run a saved query, export, import, deploy a model, diff two databases — for CI/CD |
| FR-16.2 | C | Plugin SDK for third-party sources and file formats (Go plugin interface or subprocess) |
| FR-16.3 | W | Headless web / Docker mode — see §8.6 |
| FR-16.4 | W | Shared team storage / cloud sync |

---

## 6. Non-Functional Requirements

### 6.1 Performance budgets (acceptance criteria, not aspirations)

| ID | Metric | Budget |
|---|---|---|
| NFR-P1 | Cold start to interactive window | < 800 ms |
| NFR-P2 | Connect + render object tree, 1 000-object schema | < 2 s |
| NFR-P3 | First 200 rows visible after the server returns | < 300 ms |
| NFR-P4 | Grid scroll, any result size | sustained 60 fps; **hard floor 30 fps** |
| NFR-P5 | Keystroke-to-glyph in the query editor | < 16 ms |
| NFR-P6 | Idle RSS: 5 connections, 3 open result sets | < 300 MB |
| NFR-P7 | Export 1 M rows to CSV | memory flat; ≥ 100 k rows/s on local Postgres |
| NFR-P8 | Binary size per platform | < 60 MB |
| NFR-P9 | Query / consume cancellation takes effect | < 200 ms |
| NFR-P10 | Kafka live tail, 10 k msg/s topic | UI responsive, memory bounded by the ring buffer |

- **NFR-P11 (M)** No operation may materialise an unbounded result set in memory. All reads stream
  with bounded buffers and backpressure.
- **NFR-P12 (M)** The UI goroutine never blocks on I/O. Every data-source call is cancellable via
  `context.Context` and every long operation reports progress.
- **NFR-P13 (M)** Grid rendering cost must scale with *visible* cells, not total rows. This is
  verified by benchmark, not by inspection.

> NFR-P4 and NFR-P8 were adjusted in v0.2. The 60 fps target is retained but a 30 fps hard floor is
> introduced because grid rendering is now our own code rather than a browser compositor. Binary
> budget rose to 60 MB because Fyne statically links font and graphics assets.

### 6.2 Reliability

- **NFR-R1 (M)** A driver panic must not take down the application; each connection is isolated and
  recoverable.
- **NFR-R2 (M)** A crash must not lose unsaved editor content — autosave scratch buffers continuously.
- **NFR-R3 (M)** Session state (tabs, filters, scroll, unsaved SQL) restores exactly on relaunch.
- **NFR-R4 (S)** Structured local logs with rotation, secret redaction, and one-click
  "export diagnostics".

### 6.3 Security

- **NFR-S1 (M)** Credentials in the OS keychain (macOS Keychain / Windows DPAPI / libsecret).
  Never plaintext, never in the settings file, never in logs.
- **NFR-S2 (M)** Connection strings redacted in every log line and error message.
- **NFR-S3 (M)** TLS verification on by default; disabling requires an explicit, per-connection,
  acknowledged choice.
- **NFR-S4 (M)** Read-only mode enforced in the data layer — statements are classified and
  write-class operations refused before reaching the driver. Covers Kafka produce and offset reset.
- **NFR-S5 (M)** No telemetry, no phone-home, no analytics without explicit opt-in. Default off.
- **NFR-S6 (M)** All statements built from user grid input use parameter binding; identifiers are
  quoted through a per-dialect quoter. No string concatenation of user values.
- **NFR-S7 (S)** Optional app-level lock (password/biometric) for the credential vault.
- **NFR-S8 (S)** Signed and notarised release artifacts.
- **NFR-S9 (S)** Dependency SBOM and CI vulnerability scanning.

### 6.4 Platform & distribution

- **NFR-D1 (M)** macOS 13+ (arm64 + amd64), Windows 10+ (amd64), Linux (amd64 + arm64).
- **NFR-D2 (M)** A single self-contained binary at **runtime** — no runtime prerequisites beyond
  the platform's standard graphics stack.
- **NFR-D3 (M)** **Build requires CGO and a platform C toolchain** (§8.5). CI must therefore use
  native runners per platform, or `fyne-cross` containers. Cross-compiling from one host is not
  a supported workflow.
- **NFR-D4 (M)** Signed `.dmg`/notarised on macOS, signed `.exe`/`.msi` on Windows,
  `.deb`/`.rpm`/AppImage on Linux, produced by `fyne package`.
- **NFR-D5 (S)** Homebrew cask, winget, Scoop, AUR.
- **NFR-D6 (M)** Offline-capable: no network access required for any core function.

### 6.5 Accessibility & quality

- **NFR-A1 (S)** Keyboard-operable end to end; visible focus rings; no mouse-only actions.
- **NFR-A2 (S)** WCAG 2.1 AA contrast in both themes; respects OS text-scaling.
- **NFR-A3 (C)** Screen-reader support. *Downgraded in v0.2:* Fyne's accessibility story is
  immature, so this cannot be promised for v1.0. Treated as a known gap, tracked openly and
  written down for users in [docs/ACCESSIBILITY.md](docs/ACCESSIBILITY.md), which says what
  works, what does not, and why.
- **NFR-Q1 (M)** Driver conformance suite run against real servers in Docker via testcontainers —
  including a Kafka + Schema Registry stack.
- **NFR-Q2 (M)** ≥ 70 % coverage on the core (`internal/source`, `internal/sqlgen`, `internal/model`).
- **NFR-Q3 (M)** Benchmarks asserting every §6.1 budget, run in CI, failing the build on regression.
- **NFR-Q4 (S)** End-to-end tests for J1–J8.

---

## 7. UX Principles — how "simple, clean, modern" is made testable

Binding design constraints, not sentiment.

1. **Content over chrome.** At least 80 % of the window is data or the editor. One toolbar row
   maximum. No nested toolbars.
2. **Progressive disclosure.** The default view shows the 20 % of controls used 80 % of the time.
   Everything else lives behind ⌘K, a context menu, or a disclosure.
3. **The command palette is the real menu.** Every action is reachable from ⌘K and shows its
   shortcut there. This is how features stay discoverable without living in the chrome.
4. **Panels, not modals.** Modals are reserved for destructive confirmations. Everything else is an
   inline panel, drawer, or popover that keeps the data visible.
5. **Never a blocking spinner.** Long work goes to the task centre; the UI stays usable.
6. **Show the statement.** Every generated statement — edits, DDL, sync scripts, Kafka produces —
   is visible and copyable before it runs. Trust comes from transparency.
7. **Type-honest rendering.** NULL, empty string, `0`, and `false` are always visually distinct.
8. **Dangerous is visibly dangerous.** Production connections carry a persistent colour treatment
   across every tab derived from them.
9. **One accent colour.** Colour carries meaning (environment, diff state, error), never decoration.
10. **Zero configuration to first query.** No settings need touching before J1 completes.
11. **Native feel — specifically macOS.** The application follows Apple's Human Interface
    Guidelines ([ADR-0006](docs/adr/0006-macos-design-system.md)): Apple's system colours used
    verbatim, macOS's label and background hierarchies, 13pt body text, 6pt control radii, depth
    through surface tone rather than Material elevation shadows. Real menu bar, real dialogs,
    platform-correct shortcuts (⌘ vs Ctrl) and window behaviour.

**A pure-Go-specific constraint, added in v0.2:**

12. **A custom theme is mandatory, not optional.** Fyne's stock Material-derived theme will not
    produce the intended look. A bespoke `fyne.Theme` — typography scale, spacing, palette, icon
    set, corner radii — is a **Phase 0 deliverable**, not a polish task deferred to the end.
    Retrofitting visual identity onto a built application is where this kind of project usually
    fails aesthetically.

13. **Where HIG and accessibility conflict, accessibility wins — and the deviation is recorded.**
    Several of Apple's neutrals fall below WCAG AA (`secondaryLabelColor` is 3.55:1 on white;
    `systemBlue` is 4.02:1 as body text). NFR-A2 commits us to AA, so those values are tuned while
    keeping HIG's hue and hierarchy, and each departure is marked `HIG-DEVIATION` in the tokens.

14. **Colour is never the only channel.** Environment tagging marks `dev` green and `production`
    red — the exact axis of the most common colour vision deficiency. Every such treatment carries
    a text label as a second channel. A safety signal that fails for one in twelve men is not a
    safety signal.

---

## 8. UI Toolkit Decision

### 8.1 Decision: **Fyne** (pure Go, no web technologies, no embedded browser)

Selected in v0.2 by owner direction: the application is to be pure Go end to end.

### 8.2 Why Fyne rather than Gio

The application's centre of gravity is a table, a tree, splits and tabs. Fyne ships production
versions of all four; Gio ships none of them.

| Need | Fyne | Gio |
|---|---|---|
| Virtualised table with frozen rows/columns | `widget.Table` + `StickyRowCount`/`StickyColumnCount` | build from scratch |
| Lazy virtualised tree | `widget.Tree` | build from scratch |
| Split panes | `container.Split` | build from scratch |
| Document tabs | `container.DocTabs` | build from scratch |
| Native menu bar, file/confirm dialogs | `fyne.MainMenu`, `dialog` | build from scratch |
| Full custom theming | `fyne.Theme` interface | manual, per-widget |
| Packaging & signing | `fyne package` / `fyne-cross` | roll your own |

Gio's advantages — immediate-mode rendering and excellent text throughput — are real, and would
matter if the grid were the *only* hard problem. They do not offset having to build the tree, tabs,
splits, menus and dialogs **in addition to** the four components below. Fyne is strictly less work
for this application's shape.

### 8.3 What Fyne gives us directly

Virtualised table, lazy tree, virtualised list, split containers, doc tabs, forms and inputs, native
menus and dialogs, notifications, clipboard, preferences, a complete theming interface, canvas
primitives (line, rect, circle, text, image, raster, gradient), data binding, and a packaging
toolchain for all three platforms.

### 8.4 What we must build ourselves — four workstreams

These are the honest cost of the pure-Go decision. Each gets a **Phase 0 spike** that must pass
before the phase depending on it begins.

| # | Component | Foundation | Effort | Spike gate |
|---|---|---|---|---|
| **W1** | **Data grid layer** — range selection, in-place editors, filter row, type-aware renderers, changeset highlighting, column freeze/reorder | `widget.Table` | High | 10 M-row Postgres table sustains NFR-P4's 30 fps floor. **Fallback:** a custom `canvas.Raster`-drawn grid if `widget.Table` cannot hit budget |
| **W2** | **Query editor** — syntax highlighting, autocomplete popup, line numbers, bracket matching, find/replace, multi-cursor | Custom widget; `alecthomas/chroma` for lexing (pure Go, ships SQL/CQL lexers); `widget.RichText` or direct canvas text | **Highest** | 5 000-line SQL file meets NFR-P5's 16 ms keystroke budget with highlighting live |
| **W3** | **Node canvas** — pan/zoom, draggable nodes, edge routing, auto-layout; serves both ER (FR-8) and designer (FR-9) | `fyne.Container` + custom `Layout` + `canvas.Line`; `gonum/graph` or `dominikbraun/graph` for layout | Medium-High | 200-table schema lays out and pans smoothly |
| **W4** | **Charts** — the seven chart types plus hover and click-to-filter | `gonum/plot` or `wcharczuk/go-chart` rendering to `canvas.Image`, plus a hit-test overlay; or native canvas drawing | Medium | Tooltip hit-testing is accurate at 100 k points |

**W2 is the project's largest single risk.** Nothing in the Go ecosystem approaches CodeMirror or
Monaco. Mitigation: ship a *staged* editor. Phase 1 delivers highlighting + line numbers +
find/replace only; autocomplete lands in Phase 2; multi-cursor is `S` and may not ship in v1.0.
FR-5.2 (autocomplete) is the requirement most likely to slip, and slipping it does not block J3.

### 8.5 Consequences accepted

- **CGO is required to build.** Fyne's desktop driver binds OpenGL/GLFW through cgo. The
  *application* is pure Go; the *build* is not.
  - **Upside:** the pure-Go build constraint from v0.1 is gone. `marcboeker/go-duckdb`,
    `mattn/go-sqlite3` and `tursodatabase/go-libsql` no longer need build tags (REQ-DB-3 in v0.1 is
    withdrawn).
  - **Downside:** single-host cross-compilation is not viable. CI needs native runners per platform
    or `fyne-cross` Docker images (NFR-D3).
- **Canvas-based text rendering.** Fyne draws its own text rather than using platform text stacks.
  Dense, text-heavy views are the workload most exposed to this — hence NFR-P13 and the W1 spike.
- **Accessibility is a known gap** (NFR-A3, downgraded to `C`), documented in
  [docs/ACCESSIBILITY.md](docs/ACCESSIBILITY.md): keyboard, focus, contrast and text size are
  done and tested; screen readers are not, because in Fyne v2.8.1 only Button, Hyperlink and
  Label describe themselves and the bridge is behind a build tag.
- **The stock theme is not shippable** for this product's intended feel — see UX principle 12.
- **Rejected earlier alternatives**, recorded for the decision log: **Wails/webview** (rejected by
  owner direction: not pure Go, requires a Node toolchain); **Qt bindings** (thin, inconsistently
  maintained Go bindings; LGPL/commercial friction; packaging pain); **Electron/astilectron**
  (reproduces the runtime weight we differentiate against); **terminal UI** (cannot deliver the
  grid, diagram or chart requirements).

### 8.6 Effect on headless web mode

FR-16.3 is downgraded to `W` (won't, this cycle). With a web frontend it would have been nearly
free; with Fyne it is not. Fyne can compile to WebAssembly, which keeps a *path* open, but a
WASM-hosted grid is unlikely to meet NFR-P4, and browser-side WASM cannot open raw TCP to a
database — it would need a server-side proxy that does not otherwise exist in this architecture.
Revisit after v1.0 if there is demand.

---

## 9. Data & Persistence

| ID | Pri | Requirement |
|---|---|---|
| FR-17.1 | M | Config directory follows OS convention (`~/Library/Application Support/…`, `%APPDATA%`, `$XDG_CONFIG_HOME`) |
| FR-17.2 | M | Settings and connection metadata in human-readable, VCS-friendly files (JSON/TOML); secrets only in the keychain |
| FR-17.3 | M | Query history, saved queries and session state in a local SQLite store |
| FR-17.4 | M | Forward-compatible schema versioning with automatic migration on upgrade |
| FR-17.5 | S | Backup/restore of all app data as a single archive |
| FR-17.6 | S | Portable mode — all state alongside the binary |

---

## 10. Architectural Requirements

### 10.1 Layering (dependencies point downward only)

```
  cmd/ikigai              entrypoint (and, later, the CLI)
  internal/ui             Fyne widgets, layouts, theme
    ├─ theme/             custom fyne.Theme — Phase 0 deliverable
    ├─ grid/              W1 — data grid layer
    ├─ editor/            W2 — query editor widget
    ├─ canvas/            W3 — node canvas (ER + designer)
    └─ chart/             W4 — charting
  internal/app            use cases: session, tabs, tasks, changesets, commands
  internal/source         data-source abstraction, pooling, streaming
    ├─ drivers/…          one package per source (incl. kafka/)
    ├─ introspect/        schema reading → canonical model
    ├─ sqlgen/            per-dialect SQL/CQL/DDL generation, quoting
    └─ capability/        per-source feature descriptors
  internal/model          canonical model (relational · document · kv · stream)
  internal/diff           schema comparison & sync-script synthesis
  internal/transfer       import/export pipelines (streaming)
  internal/store          settings, history, keychain, session state
```

- **ARCH-1 (M)** `internal/source` and everything below it must be usable as a library with **no
  Fyne import anywhere**, and independently testable headlessly. This is the single most important
  architectural rule: it keeps the valuable core portable if the toolkit decision is ever revisited.
- **ARCH-2 (M)** `internal/ui` never constructs SQL. All statements originate in `internal/sqlgen`.
- **ARCH-3 (M)** The canonical model is source-agnostic across all four paradigms; source specifics
  live in drivers and capability descriptors.
- **ARCH-4 (M)** Every driver call takes a `context.Context` and honours cancellation.
- **ARCH-5 (M)** Result sets cross the boundary as a cursor/stream abstraction, never as a slice.
- **ARCH-6 (M)** All UI mutation happens on the Fyne main goroutine; driver work happens on worker
  goroutines and marshals results across a single, explicit channel boundary. No shared mutable
  state between the two.
- **ARCH-7 (S)** `internal/ui` components take data through interfaces defined in `internal/app`,
  so they can be exercised in tests without a live data source.

### 10.2 The driver contract (shape, finalised in design)

A new source is added by implementing:

- `Connector` — dial, authenticate, tunnel, health, close
- `Introspector` — enumerate objects into the canonical model
- `Queryer` — execute, stream rows, cancel, report messages and affected counts
- `Dialect` — quoting, type mapping, paging, DDL synthesis
- `Capabilities` — declarative feature descriptor consumed by the UI

> **Revised 2026-09-10 by [ADR-0005](docs/adr/0005-browser-as-required-data-path.md).** Working
> T0.29 through concretely showed the sketch above was wrong: it assumed the grid is fed by a query
> language, which is true for relational sources and false for Kafka, Redis and document stores.
> `Browser` is now required and `Queryer`/`Dialect` are optional and paired. `StreamConsumer` was
> removed entirely — seeking and live tailing are `BrowseOptions.Seek` and `.Follow`, which also
> serve Mongo change streams and Redis pub-sub.

**Required of every source:**

- `Driver` — static metadata, and `Open` to dial and authenticate
- `Introspector` — enumerate objects lazily into canonical `Node`s
- `Browser` — open an object as a `RowStream`; the paradigm-neutral data path
- `Capabilities` — declarative feature descriptor consumed by the UI

**Optional, discovered by type assertion:** `Queryer`, `Sessioner`, `Dialect`, `Writer`, `Explainer`,
`Transactor`, `Killer`, `Snapshotter`, `Searcher`, `Countable`, `DistinctLister`, `BulkLoader`,
`Scriptable`, `Completer`, `StreamAdmin`, `StreamProducer`, `SchemaRegistry`.

- **REQ-DRV-1 (M)** A conformance suite runs the same battery against every driver; unimplemented
  optional interfaces are reported as skipped, not failed. A driver that *claims* a capability
  without implementing the interface behind it is a failure, not a skip.
- **REQ-DRV-2 (M)** Kafka must be a first-class source implementing only `Driver`, `Introspector`,
  `Browser` and `Capabilities` — no `Queryer`, no `Dialect`. Enforced by compilation in
  `internal/source/contract_kafka_test.go`; if the required contract ever grows something a log
  cannot provide, that file stops compiling.
- **REQ-DRV-3 (M)** `Browse` must **refuse** options it cannot honour, never ignore them. Silently
  dropping a sort would show unsorted rows under a sort indicator, which is worse than an error.

---

## 11. Delivery Plan

Revised in v0.2. Phase 1–2 now carries seven sources plus four bespoke UI components, so the
skeleton phase is deliberately narrow and the spikes come first.

| Phase | Theme | Contents | Exit criterion |
|---|---|---|---|
| **0** | **Foundations & spikes** | Repo, CI (native runners per platform), custom Fyne theme, source contract, canonical model, conformance harness, **W1–W4 spikes** | All four spike gates in §8.4 pass, or fallbacks are chosen. **No further UI work starts until this holds.** |
| **1** | **Walking skeleton** | PostgreSQL, MySQL/MariaDB, SQLite; connections + keychain; object tree; data grid (read); query editor *stage 1* (highlighting, line numbers, find/replace); query history; CSV/JSON export; themes; ⌘K | **J1 and J3 complete end to end** |
| **2** | **Editing, NoSQL & streaming** | Changeset editing + statement preview; form view; FK navigation; import with dry run; autocomplete (editor stage 2); MongoDB, Redis, Cassandra, **Kafka + Schema Registry**; SSH tunnel; SASL; task centre | **J2, J5, J7, J8 complete** |
| **3** | **Modelling** | DDL/table designer; schema compare & sync; ER diagram; charts; `EXPLAIN`; SQL Server, ClickHouse, Oracle, CockroachDB, DuckDB | **J4 and J6 complete** |
| **4** | **Hardening & release** | Remaining engines; saved models on disk; accessibility pass; packaging, signing, notarisation, auto-update; performance budgets green in CI | **v1.0 GA** |
| **5** | **Differentiators** | Visual query designer; AI assistant; CLI; plugin SDK; cross-source copy; map view; Redpanda/NATS/Pulsar; localisation | v1.1+ |

**v1.0 = through Phase 4, with every `M` requirement met and every §6.1 budget verified in CI.**

---

## 12. Risks

| ID | Risk | Impact | Mitigation |
|---|---|---|---|
| **RISK-1** | **Fyne's `widget.Table` cannot meet the grid requirements** (FR-3.1–3.8, NFR-P4). The grid is the product's centre; if it is mediocre, the product is mediocre | Existential | Phase 0 spike W1 against a 10 M-row table *before* anything is built on it. Documented fallback to a custom raster-drawn grid. 30 fps hard floor accepted |
| **RISK-2** | **No production-grade pure-Go code editor exists** (FR-5.1, 5.2, 5.11) | High — J3 degraded | Staged delivery (§8.4 W2): highlighting in Phase 1, autocomplete in Phase 2, multi-cursor optional. `chroma` for lexing. FR-5.2 slipping does not block J3 |
| **RISK-3** | **Scope.** Seven sources in Phase 1–2 *and* four bespoke UI components is more than the v0.1 plan, with a toolkit that does less for us | Project stalls | Narrow Phase 1 to three relational sources; tiering (§4.1); ruthless MoSCoW; FR-9 already downgraded to `C`; phase exits are *journeys*, not feature counts |
| **RISK-4** | **Kafka does not fit the model.** It has no rows, no server-owned schema, and offsets instead of keys; forcing it into a relational abstraction corrupts the core | High — rework | REQ-DB-3/DB-4 and REQ-DRV-2 make Kafka a *first-class paradigm test* of the contract. Design the contract with Kafka in hand, not retrofitted |
| **RISK-5** | **DBGate is GPL-3.0**; Ikigai DB's licence is deferred (OQ-1) | Legal | Clean-room: this specification is drawn entirely from public documentation and observable product behaviour. **Do not read, copy, or port DBGate source.** Settle the licence before any public release |
| **RISK-6** | **Visual quality.** Fyne's stock look is Material-derived and will not read as "clean and modern" | Core differentiator lost | Custom `fyne.Theme` is a Phase 0 deliverable (UX principle 12), not end-stage polish |
| **RISK-7** | Dialect divergence explodes `sqlgen` across four paradigms | Maintenance | Canonical model + capability descriptors; conformance suite from day one |
| **RISK-8** | **Data-loss incident** from an edit, sync, or offset-reset bug | Trust destroyed | Mandatory statement preview; transactional commits; production guardrails; dry runs; FR-13.19 (never join a consumer group while browsing); heavy testing of `internal/diff` |
| **RISK-9** | Credential mishandling | Security incident | Keychain only; redaction everywhere; security review before GA |
| **RISK-10** | CGO cross-compilation friction (NFR-D3) | Release delay | Native CI runners per platform from Phase 0, so packaging is proven early rather than discovered late |

---

## 13. Open Questions

| ID | Question | Status |
|---|---|---|
| OQ-1 | Licence for Ikigai DB? | **Deferred by owner.** Working assumption: permissive (Apache-2.0/MIT). Must be settled before public release (RISK-5) |
| OQ-2 | Open source, commercial, or open-core? | Deferred with OQ-1. Assume open source, no paid tier in v1 |
| OQ-3 | Solo build or a team? | Assume solo/small — reinforces the tiering and phasing |
| OQ-4 | Target platform priority? | Assume macOS first (dev machine is darwin/arm64), then Linux, then Windows |
| OQ-5 | Is the AI assistant a differentiator or a distraction? | Assume Phase 5, off by default |
| OQ-6 | Which Kafka distributions matter — OSS, Confluent Cloud, MSK, Redpanda? | Assume OSS + Confluent Schema Registry for Phase 2; MSK IAM auth is `S` (FR-1.11) |
| OQ-7 | Is Cassandra usage self-hosted or Astra/managed? | Assume self-hosted; Astra needs a secure-connect-bundle path, additive if required |
| OQ-8 | Should the grid fallback (custom raster) be prototyped in Phase 0 alongside W1, or only on failure? | **Recommend alongside.** Discovering the fallback is also inadequate in Phase 3 would be severe |

---

## 14. Definition of Done (v1.0)

1. Every `M` requirement is implemented and covered by a test.
2. Every §6.1 performance budget is asserted by an automated benchmark in CI, failing the build on
   regression.
3. Journeys J1–J8 pass end to end on macOS, Windows and Linux.
4. The driver conformance suite passes for every Tier-1 and Tier-2 source against a real server,
   including a Kafka + Schema Registry stack.
5. Signed, notarised installers exist for all three platforms, built on native CI runners.
6. A security review covering NFR-S1 … NFR-S6 is complete.
7. No `M`-severity known data-loss or credential-handling defect is open.
8. Known gaps — screen-reader support (NFR-A3), headless web mode (FR-16.3), visual query designer
   (FR-9) — are documented publicly rather than left implicit.
