package cassandra

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/gocql/gocql"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Reading a table's rows (FR-3.1, FR-12.3, T2.51).
//
// A page of a Cassandra table is not an offset into it. The cluster hands back
// a paging state — a token that resumes one query where it stopped — and there
// is no way to ask for the thousandth row but to read the nine hundred and
// ninety-nine before it. CQL has no OFFSET for that reason, and the dialect
// refuses one (ADR-0082).
//
// The grid, meanwhile, asks for pages by offset and may jump: a scrollbar drag
// schedules a page nothing before it has read. So a browse keeps the states it
// has reached for a query, resumes exactly where it has one, walks forward
// from the nearest one below when it has not, and refuses a jump further than
// it is willing to walk — saying so, rather than quietly reading a table's
// worth of rows to answer one page.

const (
	// walkLimit is how many rows a browse will read past to reach a page
	// nothing before it has read. Ten pages of the grid's own size: enough
	// that scrolling a screen or two ahead lands, and little enough that a
	// jump to the end of a large table is refused rather than paid for.
	walkLimit = 10 * 256

	// statesKept bounds what one connection remembers. A state is small, and
	// a person scrolling a table reaches a few hundred pages at most; a
	// script that browsed thousands would grow this without bound.
	statesKept = 512
)

// Browse reads a page of a table's or a materialized view's rows.
func (s *cassandraSource) Browse(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (_ model.RowStream, err error) {
	defer panics.Recover(&err, "reading the rows")
	if !browsableKinds[ref.Kind] {
		return nil, fmt.Errorf("cassandra: %s is not browsable", ref)
	}
	if opt.Seek != nil || opt.Follow {
		return nil, errors.New("cassandra: seek and follow apply only to stream sources")
	}
	cols, err := s.columns(ctx, ref.Path[0], ref.Path[1])
	if err != nil {
		return nil, err
	}
	if err := orderable(opt.Sorts, cols); err != nil {
		return nil, err
	}

	limit := opt.Limit
	if limit <= 0 {
		limit = DefaultPageSize
	}
	// The statement is the same for every page: what differs is where the
	// cluster is told to resume. The builder is told to write no LIMIT, which
	// in CQL would bound the whole query rather than a page of it, so what
	// the options ask for is not read here; and an offset is not something
	// CQL has, so it is not carried into the statement either.
	paged := opt
	paged.Offset = 0
	st, err := dialect{}.browse(ref, paged, false)
	if err != nil {
		return nil, err
	}

	from, skip, err := s.states.resume(st, opt.Offset)
	if err != nil {
		return nil, err
	}
	rows := &tableRows{
		src: s, statement: st, pageSize: limit,
		identity: identityOf(ref, cols), at: opt.Offset - skip, left: limit, skip: skip,
	}
	rows.iter = rows.open(ctx, from)
	rows.cols = columnDefs(rows.iter.Columns())
	if len(rows.cols) == 0 {
		// A statement that answers with no columns read nothing: the table is
		// gone, or the cluster refused what was asked of it.
		if err := rows.iter.Close(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("cassandra: %s answered with no columns", ref)
	}
	return rows, nil
}

// orderable refuses a sort CQL cannot do. Rows are ordered within a partition
// by the columns that cluster them, and by nothing else: sorting by a name is
// a sort of the whole table, which a cluster does not do at all.
func orderable(sorts []source.Sort, cols []model.Column) error {
	if len(sorts) == 0 {
		return nil
	}
	var clustering []string
	for _, c := range cols {
		if c.Attrs["kind"] == clusteringKey {
			clustering = append(clustering, c.Name)
		}
	}
	for _, s := range sorts {
		if !contains(clustering, s.Column) {
			if len(clustering) == 0 {
				return fmt.Errorf("cassandra: this table's rows have no order to sort by, and %q is not one", s.Column)
			}
			return fmt.Errorf("cassandra: rows are ordered within a partition by %s, and not by %q",
				strings.Join(clustering, ", "), s.Column)
		}
	}
	return nil
}

func contains(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// identityOf is how a row of a table is addressed: the partition key, then
// what orders rows within the partition (FR-4.7). A materialized view's rows
// are the table's, written again, and are addressed the same way.
func identityOf(ref model.ObjectRef, cols []model.Column) model.RowIdentity {
	key := primaryKey(cols)
	if len(key) == 0 {
		return model.RowIdentity{Kind: model.IdentityNone}
	}
	return model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: key, Target: ref}
}

// pageStates remembers where a query's pages ended, so that the page after one
// already read resumes rather than re-reads.
//
// It is keyed by the statement, because a paging state belongs to the query it
// came from: change a filter and the states of the old query mean nothing.
type pageStates struct {
	mu sync.Mutex
	at map[string]map[int64][]byte
}

// resume is where to start reading for a page, and how many rows to pass over
// after starting there.
func (p *pageStates) resume(st source.Statement, offset int64) (state []byte, skip int64, err error) {
	if offset == 0 {
		return nil, 0, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	known := p.at[key(st)]
	if s, ok := known[offset]; ok {
		return s, 0, nil
	}
	// The furthest point known that is not past the page asked for.
	best := int64(0)
	for at := range known {
		if at < offset && at > best {
			best = at
		}
	}
	if offset-best > walkLimit {
		return nil, 0, fmt.Errorf(
			"cassandra: rows are read forward from where the last page ended, and this page is %d rows past the furthest read; scroll to it rather than jumping",
			offset-best)
	}
	if best == 0 {
		return nil, offset, nil
	}
	return known[best], offset - best, nil
}

// reached records where a page ended, for the page after it to resume from.
func (p *pageStates) reached(st source.Statement, offset int64, state []byte) {
	if len(state) == 0 || offset <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.at == nil {
		p.at = map[string]map[int64][]byte{}
	}
	k := key(st)
	if p.at[k] == nil {
		p.at[k] = map[int64][]byte{}
	}
	if len(p.at[k]) >= statesKept {
		// What a person scrolled past long ago is worth less than the memory
		// it costs; what is left is read forward again.
		p.at[k] = map[int64][]byte{}
	}
	p.at[k][offset] = state
}

// known is how many pages' endings are remembered, across every query.
func (p *pageStates) known() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, states := range p.at {
		n += len(states)
	}
	return n
}

// key names a query: the same text with the same values is the same query.
func key(st source.Statement) string { return fmt.Sprint(st.SQL, st.Args) }

// tableRows reads one page, passing over the rows before it where the page
// began at a point nothing had reached.
//
// A cluster hands back one page at a time, and resuming from a paging state
// turns its own paging off: a read that wants more than the page it resumed
// into asks for the page after it, and so on. That is also what keeps a short
// page — which a cluster may answer with at a partition's edge — from reading
// as the end of the rows.
type tableRows struct {
	src       *cassandraSource
	iter      *gocql.Iter
	statement source.Statement
	cols      []model.ColumnDef
	identity  model.RowIdentity
	pageSize  int64

	mu     sync.Mutex
	at     int64 // where the rows being read begin
	skip   int64 // rows still to be passed over before the page begins
	left   int64 // rows still wanted
	inPage int64 // rows read from the cluster's current page
	done   bool
}

// open begins reading, from a paging state where there is one.
func (r *tableRows) open(ctx context.Context, from []byte) *gocql.Iter {
	q := r.src.session.Query(r.statement.SQL, r.statement.Args...).
		WithContext(ctx).PageSize(int(r.pageSize))
	if len(from) > 0 {
		q = q.PageState(from)
	}
	r.inPage = 0
	return q.Iter()
}

var (
	_ model.RowStream  = (*tableRows)(nil)
	_ model.Identified = (*tableRows)(nil)
)

func (r *tableRows) Columns() []model.ColumnDef  { return r.cols }
func (r *tableRows) Identity() model.RowIdentity { return r.identity }

func (r *tableRows) Next(ctx context.Context) (_ model.Row, err error) {
	defer panics.Recover(&err, "reading a row")
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for {
		if r.done || r.left <= 0 {
			return nil, io.EOF
		}
		row, ok, err := r.read(ctx)
		if err != nil {
			return nil, err
		}
		if !ok {
			r.done = true
			return nil, io.EOF
		}
		r.at++
		if r.skip > 0 {
			// A row before the page asked for: read, and let go of.
			r.skip--
			continue
		}
		r.left--
		if r.left == 0 && r.inPage == r.pageSize {
			// The page asked for ended where the cluster's own page ended, so
			// the state that resumes after it is this page's ending. Every
			// row read moves at, and a page of the cluster's ends inPage rows
			// after it began, so those two are the same row.
			r.src.states.reached(r.statement, r.at, r.iter.PageState())
		}
		return row, nil
	}
}

// read is one row, from the cluster's current page or from the page after it.
func (r *tableRows) read(ctx context.Context) (model.Row, bool, error) {
	for {
		data, err := r.iter.RowData()
		if err != nil {
			return nil, false, err
		}
		if r.iter.Scan(data.Values...) {
			r.inPage++
			row := make(model.Row, 0, len(data.Values))
			for _, v := range data.Values {
				row = append(row, normalize(deref(v)))
			}
			return row, true, nil
		}
		// This page is read: another follows it where the cluster says so.
		from := r.iter.PageState()
		if err := r.iter.Close(); err != nil {
			return nil, false, err
		}
		if len(from) == 0 {
			return nil, false, nil
		}
		r.iter = r.open(ctx, from)
	}
}

func (r *tableRows) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return nil
	}
	r.done = true
	return r.iter.Close()
}
