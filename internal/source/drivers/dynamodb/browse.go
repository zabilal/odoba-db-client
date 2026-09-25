package dynamodb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Reading a table's items (FR-12.1).
//
// A table has no declared columns beyond its key, so the grid's are the
// attributes a sample of its items holds, as MongoDB's are (ADR-0064): the
// shape decides what is shown, and an item holding something the sample missed
// still comes back whole — the value is there, under a column the grid does
// not show, and the JSON view shows it.
//
// Three things about a Scan run through everything below. It has no order: the
// service returns items as it finds them, and there is no ORDER BY to ask for,
// so a sort is refused rather than ignored (REQ-DRV-3). It has no offset: a
// later page is reached by reading the pages before it and dropping them,
// which is what paging means in a store that has no skip, and is what the
// Redis driver does with SCAN for the same reason. And it pages: a call
// returns up to a megabyte and says where to carry on from, so one page of the
// grid may be several calls.

// browseSample is how many items are read to decide the columns. Fewer than
// inference offers a person, because this runs on the way to a first page and
// NFR-P3 allows it two seconds.
const browseSample = 50

// defaultPage is the browse window when none is asked for (NFR-P11).
const defaultPage = 200

// Browse opens a stream over a table's items.
func (s *dynamoSource) Browse(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (model.RowStream, error) {
	table, err := tableOf(ref)
	if err != nil {
		return nil, err
	}
	if len(opt.Sorts) > 0 {
		return nil, errors.New("dynamodb: a table's items come back in the order the service finds " +
			"them, and there is no order to ask for")
	}
	if opt.Seek != nil || opt.Follow {
		return nil, errors.New("dynamodb: seek and follow apply only to stream sources")
	}
	scan, err := s.scanFor(table, opt)
	if err != nil {
		return nil, err
	}
	cols, err := s.browseColumns(ctx, ref, opt)
	if err != nil {
		return nil, err
	}
	id, err := s.identity(ctx, ref)
	if err != nil {
		return nil, err
	}
	limit := opt.Limit
	if limit <= 0 {
		limit = defaultPage
	}
	return &items{src: s, scan: scan, cols: cols, id: id, skip: opt.Offset, left: limit}, nil
}

// scanFor builds the request a browse reads through: the filter, and the
// projection where one was asked for.
func (s *dynamoSource) scanFor(table string, opt source.BrowseOptions) (*dynamodb.ScanInput, error) {
	if opt.Where != "" {
		// A condition a person types is in the source's own language, and this
		// source has none: DynamoDB's filter expressions are not a language
		// this driver offers to write, so taking text as one would be taking
		// text nobody could have written correctly (REQ-DRV-3).
		return nil, errors.New("dynamodb: there is no language here to write a condition in; " +
			"use the column filters")
	}
	in := &dynamodb.ScanInput{TableName: aws.String(table)}
	e := &expression{}
	filter, err := e.filters(opt.Filters)
	if err != nil {
		return nil, err
	}
	if filter != "" {
		in.FilterExpression = aws.String(filter)
	}
	if len(opt.Columns) > 0 {
		// Every attribute name goes through a placeholder, because a name may
		// be a reserved word — "name" and "size" both are — and because a
		// name written into an expression is a name interpolated into a
		// statement (NFR-S6).
		names := make([]string, len(opt.Columns))
		for i, c := range opt.Columns {
			names[i] = e.name(c)
		}
		in.ProjectionExpression = aws.String(join(names, ", "))
	}
	e.applyTo(in)
	return in, nil
}

// browseColumns are the grid's columns: what the caller asked to see, or the
// attributes a sample of the table's items holds.
func (s *dynamoSource) browseColumns(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) ([]model.ColumnDef, error) {
	if len(opt.Columns) > 0 {
		out := make([]model.ColumnDef, 0, len(opt.Columns))
		for _, name := range opt.Columns {
			out = append(out, model.ColumnDef{Name: name, Origin: ref, OriginColumn: name,
				Type: model.DataType{Class: model.TypeUnknown, Nullable: true}})
		}
		return out, nil
	}
	shape, err := s.InferShape(ctx, ref, browseSample)
	if err != nil {
		return nil, err
	}
	out := make([]model.ColumnDef, 0, len(shape.Fields))
	for _, f := range shape.Fields {
		out = append(out, columnOf(f, ref))
	}
	if len(out) == 0 {
		// An empty table still has the attributes its key is made of: they are
		// the only ones every item is certain to have, and they are declared
		// rather than sampled.
		id, err := s.identity(ctx, ref)
		if err != nil {
			return nil, err
		}
		for _, k := range id.Columns {
			out = append(out, model.ColumnDef{Name: k, Origin: ref, OriginColumn: k,
				Type: model.DataType{Class: model.TypeUnknown, Nullable: false}})
		}
	}
	return out, nil
}

// columnOf is the column an attribute becomes. One seen with a single type is
// that type; one seen with several is unknown, because no renderer is right
// for all of them and the grid draws what the value is.
func columnOf(f model.InferredField, ref model.ObjectRef) model.ColumnDef {
	col := model.ColumnDef{Name: f.Name, Origin: ref, OriginColumn: f.Name}
	col.Type.Nullable = f.Presence < 1
	switch {
	case len(f.Types) == 1:
		col.Type.Class, col.Type.Native = f.Types[0].Type.Class, f.Types[0].Type.Native
	case len(f.Types) > 1:
		names := make([]string, len(f.Types))
		for i, t := range f.Types {
			names[i] = t.Type.Native
		}
		col.Type.Class, col.Type.Native = model.TypeUnknown, join(names, " or ")
	}
	col.Type.Length = -1
	return col
}

// Count is how many items a browse would return (source.Countable).
//
// A scan that asks for no items, which the service charges for as a read of
// everything it looked at and which is the only exact count there is. The
// estimate a table keeps is the badge's (FR-2.5).
func (s *dynamoSource) Count(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (int64, error) {
	table, err := tableOf(ref)
	if err != nil {
		return 0, err
	}
	scan, err := s.scanFor(table, source.BrowseOptions{Filters: opt.Filters, Where: opt.Where})
	if err != nil {
		return 0, err
	}
	scan.Select = ddbtypes.SelectCount
	var total int64
	for {
		out, err := s.client.Scan(ctx, scan)
		if err != nil {
			return 0, statementError(err, ctx)
		}
		total += int64(out.Count)
		if len(out.LastEvaluatedKey) == 0 {
			return total, nil
		}
		scan.ExclusiveStartKey = out.LastEvaluatedKey
	}
}

// identity is how a table's items are told apart: its primary key, which is
// one attribute or two and is declared rather than sampled (FR-4.7).
func (s *dynamoSource) identity(ctx context.Context, ref model.ObjectRef) (model.RowIdentity, error) {
	table, err := tableOf(ref)
	if err != nil {
		return model.RowIdentity{}, err
	}
	s.mu.Lock()
	id, ok := s.keys[table]
	s.mu.Unlock()
	if ok {
		return id, nil
	}
	out, err := s.client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(table)})
	if err != nil {
		return model.RowIdentity{}, statementError(err, ctx)
	}
	id = model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: keyColumns(out.Table.KeySchema), Target: ref}
	if len(id.Columns) == 0 {
		// Every DynamoDB table has a partition key; a table that reported
		// none would be one this driver does not understand, and saying the
		// items cannot be told apart is better than addressing them wrongly.
		id = model.RowIdentity{Kind: model.IdentityNone, Target: ref}
	}
	s.mu.Lock()
	s.keys[table] = id
	s.mu.Unlock()
	return id, nil
}

// keyColumns is a key schema's attributes, the partition key first: that is
// the order the service states and the order a key's values are given in.
func keyColumns(schema []ddbtypes.KeySchemaElement) []string {
	hash, sortKey := "", ""
	for _, k := range schema {
		switch k.KeyType {
		case ddbtypes.KeyTypeHash:
			hash = aws.ToString(k.AttributeName)
		case ddbtypes.KeyTypeRange:
			sortKey = aws.ToString(k.AttributeName)
		}
	}
	var out []string
	if hash != "" {
		out = append(out, hash)
	}
	if sortKey != "" {
		out = append(out, sortKey)
	}
	return out
}

// items streams a table's items, a page of the service's at a time.
type items struct {
	src  *dynamoSource
	scan *dynamodb.ScanInput
	cols []model.ColumnDef
	id   model.RowIdentity

	skip int64 // items still to be read and thrown away
	left int64 // items still wanted

	page   []map[string]ddbtypes.AttributeValue
	at     int
	done   bool
	closed bool
}

var (
	_ model.RowStream  = (*items)(nil)
	_ model.Identified = (*items)(nil)
)

func (r *items) Columns() []model.ColumnDef  { return r.cols }
func (r *items) Identity() model.RowIdentity { return r.id }
func (r *items) Close() error                { r.closed = true; return nil }

func (r *items) Next(ctx context.Context) (model.Row, error) {
	if r.closed || r.left <= 0 {
		return nil, io.EOF
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if r.at >= len(r.page) {
			if r.done {
				return nil, io.EOF
			}
			if err := r.fetch(ctx); err != nil {
				return nil, err
			}
			continue
		}
		item := r.page[r.at]
		r.at++
		if r.skip > 0 {
			// A page the grid has already been past. The service has no skip,
			// so this is what one costs: the items are read and dropped.
			r.skip--
			continue
		}
		r.left--
		return r.rowOf(item), nil
	}
}

// fetch reads the next page from the service.
func (r *items) fetch(ctx context.Context) error {
	// Asked for no more than are still wanted, plus what is still to be
	// skipped: the service's own page is a megabyte, and a limit smaller than
	// that is one fewer item read and paid for.
	want := r.skip + r.left
	in := *r.scan
	if want > 0 && want < 1000 {
		in.Limit = aws.Int32(int32(want))
	}
	out, err := r.src.client.Scan(ctx, &in)
	if err != nil {
		return statementError(err, ctx)
	}
	r.page, r.at = out.Items, 0
	if len(out.LastEvaluatedKey) == 0 {
		r.done = true
		return nil
	}
	r.scan.ExclusiveStartKey = out.LastEvaluatedKey
	return nil
}

// rowOf is the row an item becomes: its values in the grid's column order,
// and nothing for an attribute it has not got.
func (r *items) rowOf(item map[string]ddbtypes.AttributeValue) model.Row {
	row := make(model.Row, len(r.cols))
	for i, c := range r.cols {
		if av, ok := item[c.Name]; ok {
			row[i] = valueOf(av)
		}
	}
	return row
}

// InferShape samples a table's items and reports the attributes they hold,
// with every type seen and how often (FR-12.4, source.ShapeInferrer).
func (s *dynamoSource) InferShape(ctx context.Context, ref model.ObjectRef, n int) (*model.DocumentShape, error) {
	table, err := tableOf(ref)
	if err != nil {
		return nil, err
	}
	if n <= 0 {
		return nil, errors.New("dynamodb: sampling needs a number of items to sample")
	}
	in := &dynamodb.ScanInput{TableName: aws.String(table), Limit: aws.Int32(int32(min(n, 1000)))}
	shape := &model.DocumentShape{}
	seen := map[string]*model.InferredField{}
	counts := map[string]map[string]int64{}
	for shape.Sampled < int64(n) {
		out, err := s.client.Scan(ctx, in)
		if err != nil {
			return nil, statementError(err, ctx)
		}
		for _, item := range out.Items {
			if shape.Sampled >= int64(n) {
				break
			}
			shape.Sampled++
			for name, av := range item {
				f, ok := seen[name]
				if !ok {
					f = &model.InferredField{Name: name}
					seen[name] = f
					counts[name] = map[string]int64{}
				}
				f.Presence++ // a count for now; a fraction below
				dt := classOf(av)
				counts[name][dt.Native]++
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		in.ExclusiveStartKey = out.LastEvaluatedKey
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	id, err := s.identity(ctx, ref)
	if err != nil {
		return nil, err
	}
	for _, name := range orderedNames(id.Columns, names) {
		f := seen[name]
		if f == nil {
			// A key attribute no sampled item held, which happens when the
			// table is empty: it is still a column, and every item that ever
			// arrives will have it.
			shape.Fields = append(shape.Fields, model.InferredField{Name: name})
			continue
		}
		field := model.InferredField{Name: name, Types: typesOf(counts[name])}
		if shape.Sampled > 0 {
			field.Presence = f.Presence / float64(shape.Sampled)
		}
		shape.Fields = append(shape.Fields, field)
	}
	return shape, nil
}

// orderedNames is the order a sampled table's attributes are shown in: the
// key's first, because they are what an item is addressed by and what somebody
// reading a row from left to right expects, then the rest by name, a table
// having no column order of its own to follow.
//
// A key attribute no sampled item held is still first: an empty table has no
// items to sample and its key is declared all the same.
func orderedNames(keys, seen []string) []string {
	sorted := append([]string(nil), seen...)
	sort.Strings(sorted)
	out := append([]string(nil), keys...)
	for _, name := range sorted {
		if !contains(keys, name) {
			out = append(out, name)
		}
	}
	return out
}

// typesOf is the types an attribute was seen with, most frequent first.
func typesOf(counts map[string]int64) []model.ObservedType {
	out := make([]model.ObservedType, 0, len(counts))
	for native, n := range counts {
		out = append(out, model.ObservedType{Type: typeFor(native), Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Type.Native < out[j].Type.Native
	})
	return out
}

// typeFor is the model's type for one of DynamoDB's nine.
func typeFor(native string) model.DataType {
	dt := model.DataType{Native: native, Length: -1, Nullable: true}
	switch native {
	case "S":
		dt.Class = model.TypeString
	case "N":
		dt.Class = model.TypeDecimal
	case "INT":
		dt.Class, dt.Native = model.TypeInteger, "N"
	case "B":
		dt.Class = model.TypeBytes
	case "BOOL":
		dt.Class = model.TypeBool
	case "M", "L", "SS", "NS", "BS":
		dt.Class = model.TypeJSON
	default:
		dt.Class = model.TypeUnknown
	}
	return dt
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func join(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

var _ = fmt.Sprint
