# ADR-0024: Object classes are the model's

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T1.42 · **Requirements:** FR-2.1, FR-2.2, REQ-DB-1, REQ-DB-4 · **Packages:** `internal/model`, `internal/source/conformance`, the drivers, `internal/ui/explorer/view`

## Context

FR-2.1 puts object classes between a schema and its objects: connection,
database, schema, class, object. FR-2.2 names the classes: tables, views,
materialised views, indexes, constraints, routines, triggers, sequences and
types for the relational sources, and one or more for each other paradigm.
Each driver built its own folders, with keys and labels of its own:
PostgreSQL listed Tables, Views, Materialized Views, Functions and
Sequences, while MySQL and SQLite listed only Tables and Views. The capability
package said "the explorer builds its node classes from this set", but
only the conformance suite read the set, and only for the tree's top level.

## Decisions

1. **The classes are one list, in `internal/model`.** `model.Classes` gives
   each class the kind it holds, its label, and its place in the list.
   The data comes first, then what serves it, then code, then types: Tables,
   Views, Materialized Views, Indexes, Triggers, Routines, Sequences,
   Types. Then come Collections, Keys, Topics, Consumer Groups and Subjects
   for the other paradigms. A class folder's key is the kind it holds
   (`model.ClassRef`). A driver counts its objects of each kind and gets
   the class nodes from `model.ClassNodes`, which leaves out the empty ones
   and gives each an exact count. No driver names a class, and a new source
   adds none of its own: it declares the kinds it has (REQ-DB-1).

2. **The conformance suite holds the drivers to it.** It walks four levels
   down, four nodes to a level. Every folder must be a class the model
   knows, holding a kind the source declares, under the model's name for
   that class, and must hold only objects of that kind. The suite's checks
   have tests of their own, against trees made to break them, since every
   driver passes them.

3. **"Routines" holds functions and procedures.** It is the SQL standard's
   word (`information_schema.ROUTINES`), where PostgreSQL's folder had said
   Functions. Each routine says which it is.

4. **An index or a trigger is named with its table**, as "orders_total on
   orders". Its class lists every table's together, and two tables' may
   share a name, as every MySQL table with a key has a PRIMARY. Where a name
   is only unique within its table, the table is in the object's path
   (PostgreSQL's triggers, MySQL's indexes). A MySQL routine's path holds
   its type, since a procedure and a function may share a name.

5. **What each engine lists.** PostgreSQL adds Indexes (a partitioned
   table's too) and Triggers, leaving out those the system makes for
   foreign keys. It also adds Types: enums, domains, ranges and composites
   of their own, but not the row types of tables or the array and
   multirange types made alongside others. Its Routines likewise leave out
   the functions the system makes for a type, as a range type's
   constructors; the first test run listed them. MySQL and MariaDB add Indexes,
   Triggers and Routines. SQLite adds Indexes, leaving out the
   `sqlite_autoindex_…` ones it makes for keys, and Triggers.

6. **Constraints are not a class.** They belong to a table, and Open
   Structure shows them with its columns, keys, indexes and foreign keys
   (ADR-0011 §14). Columns stay a table's children in the tree.

7. **An object is drawn by its kind; a class keeps the folder icon.**
   Triggers, sequences and types have icons now, so every kind a relational
   class holds has one of its own. A test keeps it so. The schema
   registry's subjects will get theirs with Kafka (T2.74). A class is a
   container, not an object, so it keeps the folder.

## Consequences

- The class keys changed: "tables" became "table", and "functions" became
  "routine". An explorer node left open in a saved session under an old key
  does not come back open, the once. Nothing else stores the keys.
- An index, trigger, type or sequence is listed but opens nothing. Its
  source and structure come with the object editors (T3.5).
- MySQL's database list still decides whether a database holds anything
  from its tables alone. A database with only routines in it shows no
  expander. This is not changed here.
