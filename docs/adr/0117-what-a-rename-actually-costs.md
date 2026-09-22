# ADR-0117: What a rename actually costs

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T3.6 · **Requirements:** FR-6.6, FR-6.4
**Packages:** `internal/source`, `internal/source/drivers/postgres`, `internal/app`, `internal/ui/shell`

## Context

FR-6.6 asks for rename with dependency awareness: warn what breaks. The
obvious reading is "list everything that refers to the object", and that
reading is wrong on PostgreSQL, which is the only engine that renders DDL
here.

A view holds its base table by OID in a stored parse tree. Rename the table
and the view still reads; `pg_get_viewdef` prints the *new* name afterwards.
The same is true of a foreign key, an index, a trigger and a column default
naming a sequence. All of them were verified against a live server before
any of this was written, along with the thing that does break.

So a list of references would be a list of things that are fine. A warning
made of those is a warning somebody learns to dismiss, and then the one that
matters goes past with the rest.

## Decisions

1. **The question is what a rename does to each dependent, not what refers
   to the object.** `model.Dependent` carries `Breaks`, and that field is the
   whole feature. The dialog shows what breaks first, on its own, and
   accounts for the rest in one line.

2. **What breaks is text the engine never resolved.** A PL/pgSQL body, or a
   SQL function whose body is a string: both look their names up when they
   run. A SQL function written with PostgreSQL 14's `BEGIN ATOMIC` body is
   parsed at creation, so it is carried — and it falls out of the search for
   free, because its `prosrc` is empty. Asking `prosqlbody IS NULL` would say
   the same thing and would not run at all before PostgreSQL 14.

3. **Everything that breaks is a guess, and says so.** The only way to find
   a name inside a body PostgreSQL never parsed is to look for the word in
   the text. That is a search, not a lookup, and the note beside each one
   says the body names it in text. Saying nothing was the alternative, and
   saying nothing about the only thing that breaks would make the warning
   worthless.

   The name is escaped before it goes into the pattern, so a table called
   `a.b` is looked for as itself rather than matching `axb`.

4. **Everything that does not break is a fact, read from the catalogue.**
   `pg_rewrite` through `pg_depend` for the views, `pg_constraint` for the
   keys. The two halves come from different places because they are different
   kinds of claim.

5. **A connection that cannot be asked says so, and does not say nothing.**
   `source.DependencyReader` is optional, and `app.ErrNoDependencies` is not
   an error condition — it is the answer "there is no way to ask this
   engine". An empty list would read as "nothing depends on this", which is a
   promise nobody made. The dialog says nothing has been checked, and does
   not phrase it as a failure, because it is not one.

6. **The warning is read before the name is typed.** What depends on an
   object does not depend on what it is about to be called, so there is no
   reason to make somebody commit to a name and only then find out the cost.
   It is read as the dialog opens, and the dialog starts by saying it is
   reading rather than by showing an empty space.

7. **A rename is previewed like every other structural change.** It renders
   statements, shows them, and runs the list that was shown (ADR-0115), so it
   is refused on a read-only connection and typed for on a production one
   without a line of code here knowing about either.

8. **A rename keeps the object where it is.** `RENAME TO` takes a bare name,
   and a qualified one is a request to move the object — a different act with
   different consequences. It is refused rather than obeyed as half of
   itself.

9. **A routine is `ALTER ROUTINE`.** The ref says a routine is there, not
   whether it is a function or a procedure, and ROUTINE renames either. Its
   name in the ref is a signature — `total(a integer, b text)` — so the name
   is quoted and the arguments are not; quoting the lot would ask for a
   routine actually called `total(a integer, b text)`.

10. **A schema is not renameable here.** Renaming one is not a rename of an
    object but a move of everything in it, and FR-6.6's question has a
    different answer for each thing inside.

## Consequences

- Only PostgreSQL offers a rename, for the same one missing implementation
  that leaves the designer previewing nothing on MySQL, SQLite and Cassandra.
  On those the command is unavailable rather than silently ineffective.
- After a rename the tree reads the object's class folder again — the
  schema's Triggers, not the table the trigger sits on, because that is where
  the tree lists it.
- A tab already open on the old name is not followed. It will fail the next
  time it reads, which is honest but not helpful, and following it is work no
  task claims yet.
- The body search is over every routine in the database, not only the schema
  the object is in, because a body in another schema can name it just as
  easily.
