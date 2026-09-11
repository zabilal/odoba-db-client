package sqlscript

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Rows imported into a table (source.BulkLoader, FR-10.6, ADR-0050): each
// row one bound INSERT, written as a changeset's new row is, in
// transactions of BatchSize rows. A load that empties the table first runs
// in one transaction whole, so that one that fails leaves the table as it
// was.

// Upserter is a dialect that can write a row over the row already there
// with its key (FR-10.6, ADR-0052).
type Upserter interface {
	// UpsertClause follows an INSERT of cols, so that a row whose keys are
	// taken updates the row there from the row given.
	UpsertClause(keys, cols []string) string
}

// OnConflict is PostgreSQL's and SQLite's upsert clause: a row whose keys
// are taken updates the other columns of the row there from the row given,
// or, with no other columns, leaves it as it is.
func OnConflict(d source.Dialect, keys, cols []string) string {
	quoted := make([]string, len(keys))
	for i, k := range keys {
		quoted[i] = d.QuoteIdentifier(k)
	}
	var sets []string
	for _, c := range cols {
		if !slices.Contains(keys, c) {
			sets = append(sets, d.QuoteIdentifier(c)+" = EXCLUDED."+d.QuoteIdentifier(c))
		}
	}
	head := " ON CONFLICT (" + strings.Join(quoted, ", ") + ") DO "
	if len(sets) == 0 {
		return head + "NOTHING"
	}
	return head + "UPDATE SET " + strings.Join(sets, ", ")
}

// DefaultLoadBatch is how many rows a load commits at a time, unless told.
const DefaultLoadBatch = 500

// Tx is a transaction a load writes in.
type Tx struct {
	Exec     func(source.Statement) (int64, error)
	Commit   func() error
	Rollback func() error
}

// LoadWith loads rows into target as new rows, each transaction begun with
// begin, and says how many it committed. The guard is asked first; emptying
// the table asks for consent on any connection, as LoadOptions.Truncate
// says. A row the server refuses stops it with a *source.LoadError: that
// row's transaction is rolled back and those before it stay, unless the
// load was emptying the table, which is then as it was. With Keys, a row
// whose key is taken updates the row there, where the dialect can say how
// (Upserter). With the "skip" or "collect" policy, each row is written in a
// savepoint, so that a row refused is left out and the load goes on, and
// Skipped is told of it (ADR-0053).
func LoadWith(ctx context.Context, d source.Dialect, guard source.Guard, target model.ObjectRef, columns []string,
	rows model.RowStream, opt source.LoadOptions, begin func() (Tx, error)) (int64, error) {
	if err := guard.Allow(source.AccessWrite, opt.Confirmed); err != nil {
		return 0, err
	}
	switch {
	case opt.Truncate && !opt.Confirmed:
		return 0, source.ErrConfirmationRequired
	case opt.OnError != "" && opt.OnError != "abort" && opt.OnError != "skip" && opt.OnError != "collect":
		return 0, fmt.Errorf("sqlscript: the %q error policy is not supported", opt.OnError)
	case opt.OnError == "collect" && opt.MaxErrors <= 0:
		return 0, errors.New("sqlscript: collecting refused rows needs the most to collect")
	case len(columns) == 0:
		return 0, errors.New("sqlscript: a load names no columns")
	case len(opt.Keys) > 0 && opt.Truncate:
		return 0, errors.New("sqlscript: a load cannot both empty a table and update its rows by key")
	}
	upsert, err := upsertClause(d, columns, opt.Keys)
	if err != nil {
		return 0, err
	}
	size := int64(opt.BatchSize)
	if size <= 0 {
		size = DefaultLoadBatch
	}
	table := d.QualifyRef(target)
	tx, err := begin()
	if err != nil {
		return 0, err
	}
	if opt.Truncate {
		if _, err := tx.Exec(source.Statement{SQL: "DELETE FROM " + table}); err != nil {
			_ = tx.Rollback()
			return 0, err
		}
	}
	skipping := opt.OnError == "skip" || opt.OnError == "collect"
	var committed, open, at, left int64 // rows committed, rows not yet, rows read, and rows left out
	for {
		row, err := rows.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			_ = tx.Rollback()
			return committed, err
		}
		at++
		refused, err := error(nil), error(nil)
		if skipping {
			refused, err = insertAside(d, table, columns, row, upsert, tx)
		} else {
			refused = insertRow(d, table, columns, row, upsert, tx)
		}
		if err != nil {
			_ = tx.Rollback()
			return committed, err
		}
		if refused != nil && skipping && (opt.OnError == "skip" || left < int64(opt.MaxErrors)) {
			left++
			if opt.Skipped != nil {
				opt.Skipped(&source.LoadError{Row: at, Err: refused})
			}
			continue
		}
		if refused != nil {
			_ = tx.Rollback()
			return committed, &source.LoadError{Row: at, Err: refused}
		}
		if open++; open < size || opt.Truncate {
			continue
		}
		if err := tx.Commit(); err != nil {
			return committed, commitFailed(err)
		}
		committed, open = committed+open, 0
		if tx, err = begin(); err != nil {
			return committed, err
		}
	}
	if err := tx.Commit(); err != nil {
		return committed, commitFailed(err)
	}
	return committed + open, nil
}

// upsertClause is what follows each row's INSERT so that a row whose key is
// taken updates the row there: nothing, when the load names no key.
func upsertClause(d source.Dialect, columns, keys []string) (string, error) {
	if len(keys) == 0 {
		return "", nil
	}
	u, ok := d.(Upserter)
	if !ok {
		return "", errors.New("sqlscript: this source cannot update rows by key")
	}
	for _, k := range keys {
		if !slices.Contains(columns, k) {
			return "", fmt.Errorf("sqlscript: the key column %s is not loaded", k)
		}
	}
	return u.UpsertClause(keys, columns), nil
}

// insertRow writes one row as a new row: its values, in the columns' order,
// NULL where it is short, followed by upsert. A row that adds other than one
// row is refused, unless it may update one instead, which engines count
// their own ways.
func insertRow(d source.Dialect, table string, columns []string, row model.Row, upsert string, tx Tx) error {
	vals := make(map[string]any, len(columns))
	for i, c := range columns {
		var v any
		if i < len(row) {
			v = row[i]
		}
		vals[c] = v
	}
	st, _, err := write(d, table, nil, source.RowChange{Kind: source.ChangeInsert, Values: vals}, "")
	if err != nil {
		return err
	}
	st.SQL += upsert
	n, err := tx.Exec(st)
	if err == nil && upsert == "" && n != 1 {
		err = fmt.Errorf("%d rows added, where the row was one", n)
	}
	return err
}

// insertAside writes one row in a savepoint, so that a row refused can be
// left out and the transaction go on: PostgreSQL aborts a transaction at a
// failed statement, and every engine here rolls back to a savepoint. The
// savepoint is released either way, so that none pile up. It says why the
// row was refused, and, as err, a savepoint that failed.
func insertAside(d source.Dialect, table string, columns []string, row model.Row, upsert string, tx Tx) (refused, err error) {
	if _, err := tx.Exec(source.Statement{SQL: "SAVEPOINT ikigai_row"}); err != nil {
		return nil, err
	}
	if refused = insertRow(d, table, columns, row, upsert, tx); refused != nil {
		if _, err := tx.Exec(source.Statement{SQL: "ROLLBACK TO SAVEPOINT ikigai_row"}); err != nil {
			return nil, err
		}
	}
	_, err = tx.Exec(source.Statement{SQL: "RELEASE SAVEPOINT ikigai_row"})
	return refused, err
}

// LoadSQL loads rows on a database/sql connection. See LoadWith.
func LoadSQL(ctx context.Context, db *sql.DB, d source.Dialect, guard source.Guard, target model.ObjectRef,
	columns []string, rows model.RowStream, opt source.LoadOptions) (int64, error) {
	return LoadWith(ctx, d, guard, target, columns, rows, opt, func() (Tx, error) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return Tx{}, err
		}
		return Tx{
			Exec: func(st source.Statement) (int64, error) {
				res, err := tx.ExecContext(ctx, st.SQL, st.Args...)
				if err != nil {
					return 0, err
				}
				return res.RowsAffected()
			},
			Commit:   tx.Commit,
			Rollback: tx.Rollback,
		}, nil
	})
}
