# Ikigai DB

A database client that opens fast, stays out of the way, and tells you the truth
about what it is doing. One binary, no runtime to install, no browser inside it.

It reads and writes PostgreSQL, MySQL, MariaDB, SQLite, libSQL and Turso,
SQL Server, Oracle, Firebird, CockroachDB, ClickHouse, DuckDB, MongoDB,
DynamoDB, Redis and Valkey, Cassandra and ScyllaDB, and Apache Kafka — through
one window, with the same grid, the same editor and the same keyboard.

**Licence: not yet decided.** Until it is, this is source you can read and build,
and not something to depend on. See `LICENSE`.

---

## What it does

**Browse and edit.** A grid that pages, filters and sorts on the server, edits in
place with a reviewable preview before anything is written, and says when a row
cannot be told apart from another rather than guessing. Every write goes through
a plan you can read.

**Query.** A SQL editor per dialect with completion from the live schema,
formatting that changes nothing but the layout, `EXPLAIN` with the plan drawn as
a tree, transactions where the engine has them, and a history of everything you
have run.

**Understand.** An object tree across databases and schemas, an ER diagram,
schema comparison against a saved model, full-text search across definitions —
column names, procedure bodies, check expressions — and saved views of a table
arranged the way you left it.

**Move data.** Import from CSV, TSV, JSON, NDJSON and Excel with the column
mapping shown before it runs; export the same ways plus SQL `INSERT`s and
Markdown; copy a table's rows into another table, on the same connection or
another one, across engines.

**Streams and key-value stores.** Kafka topics, partitions, consumer groups and
their lag; records decoded as text, JSON or through a Schema Registry schema.
Redis keys of every type. MongoDB documents with their own editor. Browsing a
Kafka topic never joins a consumer group and never commits an offset.

**Build a query without writing one.** A canvas of tables and joins, every part
of a `SELECT` beside it, and the SQL it makes shown as it changes.

**Ask, if you want to.** A language model can turn a question into a statement
grounded in the live schema, or say what a statement does. It is off until you
turn it on, off again per connection until you opt that connection in, and it
sends no rows unless you say so. The model can be one on your own machine.
Nothing it writes ever runs: a statement lands in the editor, unrun.

**Keep your work.** Tabs and unsaved query text come back after a crash. Named
workspaces group the connections, tabs and saved queries of one piece of work.
Everything can be backed up into one archive and put back.

## What it does not do

Read `docs/KNOWN-DEFECTS.md` for the whole list, and
`docs/ACCESSIBILITY.md` for the one that matters most:

- **A screen reader will announce almost nothing.** Keyboard operation, visible
  focus, AA contrast and text scaling are all done and tested; screen-reader
  support is not, because of where the toolkit is. That page says exactly why.
- There is no web mode, and there will not be one this cycle.
- Releases are not yet signed or notarised, so there are no installers yet —
  only a build from source.

## In a pipeline

There is a second binary for the things a build does with a database, and it
draws nothing at all, so it runs where there is no display:

    ikigai-cli query   --url $URL --sql "SELECT count(*) FROM items"
    ikigai-cli export  --url $URL --table public.items --out items.csv
    ikigai-cli import  --url $URL --into public.items --from items.csv --dry-run
    ikigai-cli save    --url $URL --model ./schema
    ikigai-cli diff    --url $URL --model ./schema --exit-code
    ikigai-cli deploy  --url $URL --model ./schema --apply

A password comes from the environment — `IKIGAI_PASSWORD`, or `IKIGAI_URL` for a
whole connection string — because a command line is visible to everything else
on the machine. `--connection NAME` uses a connection saved in the application
instead, keychain and all.

Nothing changes a schema unasked: `deploy` prints the statements it would run and
stops, and dropping anything needs `--allow-drops` as well as `--apply`. The exit
status is 0 for done, 1 for failed, 2 for arguments it could not act on, and 3
for "there is a difference", which is what a build gate reads.

## Building it

Go 1.26 or later, and a C toolchain, which the graphics layer needs:

    go build ./cmd/ikigai        # the application
    go build ./cmd/ikigai-cli    # the command line; no C toolchain, no display
    go test ./...                # everything that does not need a server

DuckDB is behind a build tag, because it brings a hundred megabytes of prebuilt
engine with it:

    go build -tags duckdb ./cmd/ikigai

The driver conformance suites run against real servers in Docker and are behind
the `conformance` tag; `.github/workflows/conformance.yml` is how they are run.

## Where things are kept

Settings and the local database go where each platform puts them — `~/Library/
Application Support/Ikigai DB` on macOS, `%APPDATA%`/`%LOCALAPPDATA%` on Windows,
the XDG directories on Linux. Passwords go in the OS keychain and never into a
file.

Put a file called `ikigai-portable` beside the binary and everything moves next
to it instead, passwords included — sealed with a passphrase you set, because a
password in a file beside the application is worth keeping only sealed.

## How it is built

- `ARCHITECTURE.md` — the layers and the rules between them.
- `REQUIREMENTS.md` — every requirement, with its priority and its reason.
- `TASKS.md` — what is done, what is not, and what each thing cost.
- `docs/adr/` — every architectural decision, with what was rejected and why.
- `docs/SECURITY-REVIEW.md` — NFR-S1 to NFR-S6, what enforces each and what is
  left open.
- `CLEANROOM.md` — why no DBGate source has been read, and what was used instead.
