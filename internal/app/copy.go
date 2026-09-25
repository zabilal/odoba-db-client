package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/transfer"
)

// Copying a table's rows into another table, on the same connection or on
// another one (FR-10.9).
//
// It is the import, with a table where the file was. A file's rows reach a
// table as a model.RowStream, made the table's values column by column and
// handed to the destination's bulk loader as it asks for them; a table's
// rows are a model.RowStream too. So this adds no machinery: it reads one
// source's rows and writes them through the other's loader, and everything
// about types, batches, keys and what to do with a row that will not go in
// is the import's, already written and already proved (ADR-0046, ADR-0049,
// ADR-0054).

// ErrNoLoader is a destination that cannot be written to in bulk.
var ErrNoLoader = errors.New("app: this connection cannot load rows into a table")

// CopyOptions say how the rows are written. They are the import's, because
// they are the same question.
type CopyOptions = transfer.LoadOptions

// Copied is how far a copy has got.
type Copied = transfer.Loaded

// CanCopyInto reports whether a connection can be written to in bulk, which
// is what a copy needs of its destination.
func CanCopyInto(src source.Source) bool {
	_, ok := src.(source.BulkLoader)
	return ok
}

// CanCopyRowsInto reports whether these rows' connection can be written to
// in bulk. It asks the same question of what a form has in its hand.
func (b *BrowseSource) CanCopyRowsInto() bool { return CanCopyInto(b.src) }

// CopyRows reads every row of from and writes it into to.
//
// The columns are paired by name, telling no difference between cases,
// spaces, underscores and hyphens, and a column the destination has no
// match for is left behind — said here rather than found out halfway, so
// that whatever asks can show what will be copied before anything is.
//
// progress, when not nil, is told now and then how many rows have gone.
// Cancelling ctx stops it, and the batch under way is undone.
func CopyRows(ctx context.Context, from *BrowseSource, to *BrowseSource, opt CopyOptions,
	progress func(Copied)) (_ Copied, err error) {
	defer panics.Recover(&err, "copying rows")
	if !CanCopyInto(to.src) {
		return Copied{}, ErrNoLoader
	}
	pairs, cols := CopyPairs(from, to)
	if len(pairs) == 0 {
		return Copied{}, fmt.Errorf("app: none of %s's columns matches one of %s's by name",
			from.Ref().Name(), to.Ref().Name())
	}
	rows := from.Rows()
	defer rows.Close()
	return transfer.Load(ctx, rows, pairs, cols, to.Ref(), to, opt, progress)
}

// CopyPairs is which of the source's columns fills which of the
// destination's, and the destination's columns by name.
func CopyPairs(from, to *BrowseSource) ([]transfer.Pair, map[string]model.ColumnDef) {
	cols := map[string]model.ColumnDef{}
	for _, c := range to.Columns() {
		cols[c.Name] = c
	}
	return transfer.Suggest(from.Columns(), to.Columns()), cols
}

// CopyLeftBehind names the source's columns the destination has nowhere to
// put, in the order they are in, so that what will not be copied can be
// said before anything is.
func CopyLeftBehind(from, to *BrowseSource) []string {
	pairs, _ := CopyPairs(from, to)
	taken := map[int]bool{}
	for _, p := range pairs {
		taken[p.From] = true
	}
	var out []string
	for i, c := range from.Columns() {
		if !taken[i] {
			out = append(out, c.Name)
		}
	}
	return out
}

// TableLimit is how many tables a connection is listed as having, for
// choosing where rows go. A list longer than this is not a list anybody
// picks from, and walking on would be a round trip per object for nothing.
const TableLimit = 2000

// Tables lists the objects on a connection that rows could be copied into:
// everything with rows of its own, under every database and schema.
//
// It walks the tree, which is a round trip per class folder rather than
// per object, and stops at limit. Whatever asks says so when it has
// stopped short, because a destination missing from a list reads as a
// destination that cannot be written to.
func Tables(ctx context.Context, src source.Source, limit int) (_ []model.Node, all bool, err error) {
	defer panics.Recover(&err, "listing the tables")
	var out []model.Node
	on, err := walkUnder(ctx, src, model.ObjectRef{}, func(n model.Node) (bool, error) {
		if !n.Browsable {
			return true, nil
		}
		if len(out) >= limit {
			return false, nil // there are more, and whatever asked says so
		}
		out = append(out, n)
		return true, nil
	})
	return out, on, err
}
