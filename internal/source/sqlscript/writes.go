package sqlscript

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// A changeset written as SQL (FR-4.4, FR-4.5, ADR-0031): the statements a
// driver's Writer plans, and the one transaction it applies them in. Every
// name comes from the dialect and every value is bound (ARCH-2, NFR-S6).

// ErrNoRow is a statement that changed no row: the row it was written for
// has changed its key, or gone, since it was read.
var ErrNoRow = errors.New("no row matched: it was changed or deleted since it was read")

// PlanWrites renders a changeset as statements, in its own order. A row
// changed is matched by the key it had and given only the columns changed; a
// row deleted is matched by its key; a new row names only the columns given,
// or with none given is written as defaultRow says ("DEFAULT VALUES", or
// MySQL's "() VALUES ()"). A changeset that cannot be written whole is
// refused rather than planned in part.
func PlanWrites(d source.Dialect, guard source.Guard, cs source.Changeset, defaultRow string) (*source.WritePlan, error) {
	if !cs.Identity.Editable() {
		return nil, errors.New("these rows have no key to tell them apart, so they cannot be written")
	}
	table := d.QualifyRef(cs.Target)
	plan := &source.WritePlan{Target: cs.Target, Atomic: true, Guarded: guard.RequiresConfirmation(source.AccessWrite)}
	for i, c := range cs.Changes {
		st, desc, err := write(d, table, cs.Identity.Columns, c, defaultRow)
		if err != nil {
			return nil, fmt.Errorf("change %d: %w", i+1, err)
		}
		st.Confirmed = cs.Confirmed
		plan.Statements = append(plan.Statements, st)
		plan.Descriptions = append(plan.Descriptions, desc)
	}
	return plan, nil
}

// write renders one change, and says in a line what it does.
func write(d source.Dialect, table string, keys []string, c source.RowChange, defaultRow string) (source.Statement, string, error) {
	var st source.Statement
	next := func(v any) string {
		st.Args = append(st.Args, bindable(v))
		return d.Placeholder(len(st.Args))
	}
	names := make([]string, 0, len(c.Values))
	for name := range c.Values {
		names = append(names, name)
	}
	slices.Sort(names)
	where := func() (string, string, error) {
		if len(c.Key) != len(keys) {
			return "", "", fmt.Errorf("the key has %d values for %d columns", len(c.Key), len(keys))
		}
		conds, said := make([]string, len(keys)), make([]string, len(keys))
		for i, k := range keys {
			if c.Key[i] == nil {
				return "", "", fmt.Errorf("the row's %s is NULL, which matches no row", k)
			}
			conds[i] = d.QuoteIdentifier(k) + " = " + next(c.Key[i])
			said[i] = fmt.Sprintf("%s = %v", k, c.Key[i])
		}
		return strings.Join(conds, " AND "), strings.Join(said, ", "), nil
	}
	switch c.Kind {
	case source.ChangeUpdate:
		if len(names) == 0 {
			return st, "", errors.New("an update that changes nothing")
		}
		sets := make([]string, len(names))
		for i, name := range names {
			sets[i] = d.QuoteIdentifier(name) + " = " + next(c.Values[name])
		}
		cond, said, err := where()
		if err != nil {
			return st, "", err
		}
		st.SQL = "UPDATE " + table + " SET " + strings.Join(sets, ", ") + " WHERE " + cond
		return st, "Update " + strings.Join(names, ", ") + " where " + said, nil
	case source.ChangeDelete:
		cond, said, err := where()
		if err != nil {
			return st, "", err
		}
		st.SQL = "DELETE FROM " + table + " WHERE " + cond
		return st, "Delete the row where " + said, nil
	case source.ChangeInsert:
		if len(names) == 0 {
			st.SQL = "INSERT INTO " + table + " " + defaultRow
			return st, "Insert a row of defaults", nil
		}
		cols, marks := make([]string, len(names)), make([]string, len(names))
		for i, name := range names {
			cols[i], marks[i] = d.QuoteIdentifier(name), next(c.Values[name])
		}
		st.SQL = "INSERT INTO " + table + " (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(marks, ", ") + ")"
		return st, "Insert a row: " + strings.Join(names, ", "), nil
	}
	return st, "", fmt.Errorf("a change of unknown kind %d", c.Kind)
}

// bindable is a value as a driver binds it. Decimals and JSON go as their
// text, which every engine reads as the column's type, as it does a
// parameter's text (ADR-0026); anything else goes as it is.
func bindable(v any) any {
	switch x := v.(type) {
	case model.Decimal:
		return string(x)
	case model.JSON:
		return string(x)
	}
	return v
}

// AllowWrites asks the guard whether a plan may run, with the consent each
// of its statements carries (FR-4.9).
func AllowWrites(guard source.Guard, plan *source.WritePlan) error {
	for _, st := range plan.Statements {
		if err := guard.Allow(source.AccessWrite, st.Confirmed); err != nil {
			return err
		}
	}
	return nil
}

// ApplySQL runs a plan on a database/sql connection, in one transaction:
// all of it, or on the first failure none of it (FR-4.5). See ApplyWith.
func ApplySQL(ctx context.Context, db *sql.DB, guard source.Guard, plan *source.WritePlan) (*source.WriteOutcome, error) {
	if err := AllowWrites(guard, plan); err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return ApplyWith(plan, func(st source.Statement) (int64, error) {
		res, err := tx.ExecContext(ctx, st.SQL, st.Args...)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected()
	}, tx.Commit, tx.Rollback), nil
}

// ApplyWith runs a plan's statements through exec, in a transaction already
// begun, then commits. A statement that fails, or that changes no row
// (ErrNoRow), stops it: the transaction is rolled back and the outcome says
// which statement failed and why. Every statement plans one row, so one
// changing none means that row was changed or deleted since it was read.
func ApplyWith(plan *source.WritePlan, exec func(source.Statement) (int64, error), commit, rollback func() error) *source.WriteOutcome {
	out := &source.WriteOutcome{FailedAt: -1}
	for i, st := range plan.Statements {
		n, err := exec(st)
		if err == nil && n == 0 {
			err = ErrNoRow
		}
		if err != nil {
			out.Applied, out.FailedAt, out.Affected, out.Err = i, i, 0, err
			out.RolledBack = rollback() == nil
			return out
		}
		out.Affected += n
	}
	out.Applied = len(plan.Statements)
	if err := commit(); err != nil {
		out.Err = fmt.Errorf("the changes ran, but committing them failed, so the server may not have kept them: %w", err)
	}
	return out
}
