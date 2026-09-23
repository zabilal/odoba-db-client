# ADR-0137: A transaction open, and saying so

**Status:** Accepted · **Date:** 2026-09-23
**Tasks:** T3.26 · **Requirements:** FR-5.14, NFR-S4
**Packages:** `internal/source`, `internal/source/drivers/{postgres,mysql,sqlite}`, `internal/app`, `internal/ui/shell`

## Context

The `Transactor` contract has been in `internal/source/query.go` since the
driver interfaces were written, and nothing implemented it. FR-5.14 asks for
explicit begin, commit and rollback with a persistent indicator whenever a
transaction is open — the indicator being part of the requirement because an
unnoticed open transaction holds locks: other people's statements wait on it,
and the tables it touched cannot be altered until it ends.

## Decisions

1. **A transaction belongs to a session, not to a source.** It lives on one
   server connection, and a source hands out whichever connection is free. A
   `Begin` whose `Commit` landed on another connection would be worse than
   no transactions at all, so the conformance suite refuses a source that
   claims them without sessions.

2. **The contract gained a state where it had a boolean.** `InTransaction()
   bool` became `Transaction() TxState`, with none, open and failed. A
   statement that fails inside a PostgreSQL transaction leaves it unable to
   commit: every statement after it answers "current transaction is
   aborted", and only a rollback ends it. A window that said "open" would be
   offering a commit that cannot happen. Nothing implemented the old
   contract, so there was nothing to migrate — this is what implementing it
   found out.

3. **PostgreSQL's state is asked of the connection, not kept in a flag.**
   The server reports it in every message it sends, and it is the only
   answer that stays true when somebody types COMMIT in the editor, or an
   idle-in-transaction timeout ends the transaction underneath them.

4. **MySQL, MariaDB and SQLite report open or none, and never failed.** A
   failed statement does not poison a transaction on those engines, so
   reporting that it had would be inventing a state they do not have.

5. **Read-only is enforced where each engine enforces it, and claimed once.**
   PostgreSQL asks for an explicit `READ ONLY` transaction, because the
   session default it would otherwise inherit can be turned off from inside
   a function body the lexer cannot see into — which is proved by doing
   exactly that. MySQL, MariaDB and SQLite open the connection itself
   read-only (a session variable, and `query_only` with a read-only file),
   so every transaction on it is read-only already and asking again on the
   transaction would be a second claim about the same thing that nothing
   could tell apart.

6. **A second transaction is refused here rather than at the server.**
   PostgreSQL and SQLite refuse one too, and MySQL's drivers wait for the
   first to end rather than refusing at all — a window that hung would be
   worse than one that explains. Ending a transaction that never began is
   refused the same way.

7. **A transaction is let go whatever the server answered.** A commit that
   failed has ended it too, and holding on to it would leave the window
   saying one was open when none is.

8. **A session closing rolls back what it was holding.** A connection handed
   back to a pool mid-transaction holds its locks until something else
   notices, and a SQLite file stays locked. A transaction nobody committed
   was not meant to commit. Closing must not wait on the transaction it is
   responsible for ending, which the tests bound rather than hanging over.

9. **The window says so in the tab's name as well as its footer.** The
   footer says what can still be done with it; the name carries a mark, so a
   transaction left open in one tab is visible from another. Closing such a
   tab asks first, because closing rolls it back and what it did is undone.

10. **Every run says what is true of the transaction it ran in.** What ran
    is not committed, and a statement that failed may have ended what could
    be.

## Consequences

- PostgreSQL, MySQL, MariaDB and SQLite hold explicit transactions.
  Cassandra, Mongo, Redis and Kafka do not, and the window offers none.
- Where a statement is sent — the connection, or the transaction on it — is
  now one small function per driver, which is what makes it provable without
  a server: a pinned connection and a transaction on it share one driver
  connection, so which of the two a statement went through cannot be told
  apart from the data.
- Nothing nests transactions or names savepoints. Both would be worth
  having; neither is claimed by a task.
- A statement of somebody's own that ends the transaction — a typed COMMIT —
  leaves PostgreSQL holding a transaction object it will never use again.
  The window reports none, offers nothing to end, and the session rolls it
  back when it closes.
