package sqlscript

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Rows imported into a table (source.BulkLoader, FR-10.6, ADR-0050): each
// row one bound INSERT, written as a changeset's new row is, in
// transactions of BatchSize rows. A load that empties the table first runs
// in one transaction whole, so that one that fails leaves the table as it
// was.

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
// load was emptying the table, which is then as it was. Only the "abort"
// error policy is taken so far.
func LoadWith(ctx context.Context, d source.Dialect, guard source.Guard, target model.ObjectRef, columns []string,
	rows model.RowStream, opt source.LoadOptions, begin func() (Tx, error)) (int64, error) {
	if err := guard.Allow(source.AccessWrite, opt.Confirmed); err != nil {
		return 0, err
	}
	switch {
	case opt.Truncate && !opt.Confirmed:
		return 0, source.ErrConfirmationRequired
	case opt.OnError != "" && opt.OnError != "abort":
		return 0, fmt.Errorf("sqlscript: the %q error policy is not supported", opt.OnError)
	case len(columns) == 0:
		return 0, errors.New("sqlscript: a load names no columns")
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
	var committed, open, at int64 // rows committed, rows not yet, and rows read
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
		if err := insertRow(d, table, columns, row, tx); err != nil {
			_ = tx.Rollback()
			return committed, &source.LoadError{Row: at, Err: err}
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

// insertRow writes one row as a new row: its values, in the columns' order,
// NULL where it is short.
func insertRow(d source.Dialect, table string, columns []string, row model.Row, tx Tx) error {
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
	n, err := tx.Exec(st)
	if err == nil && n != 1 {
		err = fmt.Errorf("%d rows added, where the row was one", n)
	}
	return err
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
