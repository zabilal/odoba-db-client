# ADR-0173: A search engine is read

**Status:** Accepted · **Date:** 2026-10-11
**Tasks:** T5.15 · **Requirements:** §4.3, FR-2.1, FR-2.5, FR-3.1, FR-3.3, FR-3.4, FR-3.8, FR-12.4, REQ-DRV-3, NFR-S3, NFR-P8, NFR-P11
**Packages:** `internal/source/drivers/elasticsearch`, `cmd/ikigai`, `cmd/ikigai-cli`, `.github/workflows`

## Context

T5.15 names four sources. Two of them need an account nobody here has
(Snowflake, BigQuery) and one needs a cluster to stand up (Trino). The fourth is
a search engine, which runs in a container and is the one this record is about.

A search engine is not a database that happens to search. It has no databases
above its indices, no declared columns, no transactions, and it holds a string
in two different ways depending on what somebody intended when the index was
made. Most of the decisions below are about saying that honestly through a grid
built for rows.

## Decisions

1. **One driver for Elasticsearch and OpenSearch, speaking the protocol
   directly.** They are one protocol for everything this asks: the root says what
   the server is, `_cat/indices` lists what it holds, `_mapping` says what a
   document looks like, `_search` reads and `_count` counts. The official
   Elasticsearch client checks a product header and refuses OpenSearch, so
   choosing a library would have been choosing which half of the users to serve;
   and seven endpoints do not need a library that speaks four hundred (NFR-P8).
   It is `net/http` and `encoding/json`, in one file, which is the one file of a
   driver the offline test now allows to speak HTTP — a server somebody
   configured, like every other driver's, reached without a library in between.

2. **Version 1 reads.** It browses, filters, sorts, counts and describes. It
   writes nothing, makes and changes no index, and has no query editor. Each of
   those is a decision with its own questions — a write to a search engine is not
   visible until a refresh, so "the row I just changed" is a claim that needs
   care — and none of them is needed for the thing a database client is first
   asked to do with a search engine, which is to look at what is in it.

3. **An index is a collection, and the mapping is its columns.** The engine keeps
   a record of every field it has seen and what it took each to be, so there is
   nothing to sample: the columns are the mapping's leaves, flattened to the
   dotted names the engine itself uses, with `_id` first and `_source` last. A
   field the mapping does not know is in `_source` and not in a column, which is
   the engine's answer about its own data.

   A sub-field — the `keyword` beside a `text` — is the same value indexed a
   second way and is not a column. It is where the column's whole value is asked
   for (decision 5).

   An array of objects is walked into: `authors.name` is every author's name,
   which is how the engine reads such a field. A key written with a dot in it is
   the same field as a nested one, and both are looked for.

4. **A number keeps every digit.** JSON has one kind of number, and read the easy
   way an identifier of nineteen digits comes back with its last three changed.
   Documents are decoded with numbers as their digits and become what the mapping
   says: an integer, a float, a time where the field is a date, and their own
   digits (`model.Decimal`) where an `unsigned_long` is past what an `int64`
   holds.

5. **A filter means what it says, or is refused.** A keyword is a string as it
   was written; a text field has been analysed into words and the engine holds
   the words. There is no whole value of a text field left to be equal to, to
   order, or to run a pattern across. So the mapping decides what a filter
   becomes:

   | Field | Equals | Contains | Order, range, list, LIKE, regex | Sort |
   |---|---|---|---|---|
   | keyword, number, date, boolean, ip | `term` | `wildcard`, any case | as asked | yes |
   | text with a keyword beside it | `term` on the keyword | `wildcard` on the keyword | on the keyword | on the keyword |
   | text alone | `match_phrase` | `match_phrase` | refused | refused |
   | a place, a shape, a vector, bytes, an empty object | refused | refused | refused | refused |
   | inside a nested object | refused | refused | refused | refused |

   "Has none" and "has one" are `exists`, and are the only thing that can be
   asked of the fourth row. Equal to nothing is a document that does not hold the
   field: the engine does not index a null, so one written as null and one never
   written are the same to it.

   Equals and contains on text alone match the words rather than refusing,
   because that is what a field somebody chose to analyse can be asked, and a
   log index whose `message` cannot be searched is not worth opening. It is a
   different question from the one a keyword answers — "river" does not find
   "rivers" — and it is written down here and in `docs/KNOWN-DEFECTS.md` rather
   than left to be discovered.

   The live suite holds this the only way it can be held: thirty-odd filters,
   each compared with the same question asked in Go of the documents as they
   were written. A filter being accepted proves nothing; selecting the right
   hundredth does.

6. **A sort always ends in `_doc`.** The order the engine holds documents in is
   the cheapest there is, and it is what makes one page follow another when the
   sort somebody chose ties. Sorting by `_id` is refused: the engine will not do
   it by default, since it would load every identity into memory.

7. **No typed condition.** The engine's query string would be the obvious thing
   to take as a grid's WHERE, and the draft of this driver took it. It is refused
   instead: the window offers a typed condition only to a source that has
   claimed a language (`source.Dialect`), so a condition taken without one is a
   feature nobody can reach and nothing can check (REQ-DRV-3).

8. **No estimate in the tree.** `_cat/indices` answers a document count for
   every index in one request, and the draft badged the tree with it. That
   number counts each object in a `nested` field as a document: a hundred
   documents with two notes on a third of them answers 166. An estimate that is
   wrong by what the data looks like is not an estimate, so the tree shows none
   and `Badge` counts — exactly, with `_count`, which is cheap here.

   The live suite found this. Its seed has a nested field for another reason.

9. **Presence is counted, not sampled.** The structure tab shows how many
   documents hold each field. The mapping does not say, and the draft said 100%
   for all of them. The engine can be asked of every document — one `exists`
   filter to a field, no documents read, two hundred fields to a request — so the
   shape is of the whole index and says so. It is the same question a not-null
   filter asks, which is what makes the figure agree with what filtering finds.

10. **Nothing said about encryption is encrypted and verified.** The draft read
    an empty TLS mode as plain HTTP. `source.TLSConfig` says verification is on
    unless a connection was told otherwise, so only `disable` is plain (NFR-S3).
    A certificate nobody vouches for, and a server speaking plain HTTP on a port
    this was told to encrypt, are both failures about encryption rather than
    about the address.

11. **Deep paging is refused in its own terms.** Past `index.max_result_window`
    the engine refuses, in words about a setting. What is said instead is which
    documents were asked for and that narrowing the filters is the way through.
    `search_after` would page further but only forwards, and the grid jumps.

## What is not proven

- **Elasticsearch itself.** The live suite runs against OpenSearch 2. The
  requests are ones both projects answer, but a difference between them would be
  found by somebody using it. `case_insensitive` on a wildcard query needs
  Elasticsearch 7.10 or later.
- **A server that asks who is calling.** The test server has security off. A
  refused password, a missing one, a certificate and a client certificate are
  held by tests over a server made for the purpose.

## Consequences

- T5.15 stays open: Trino is next and needs a cluster in Docker; Snowflake and
  BigQuery need accounts and stay open with that reason, as T4.1 does.
- `gate.sh` and the nightly conformance run require the OpenSearch server, so
  the driver cannot quietly stop being proved.
- Writing, a query editor over the engine's own language, nested queries and
  `search_after` are each unclaimed. None is blocked by anything here.
- Trying this through the command line found something older than it:
  `ikigai export --table` names everything a table, so no source whose objects
  are collections can be exported from there — this one, MongoDB or DynamoDB.
  Recorded in `docs/KNOWN-DEFECTS.md`; not fixed here.
