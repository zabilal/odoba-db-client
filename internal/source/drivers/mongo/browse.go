package mongo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Browsing a collection (FR-12.1, T2.33).
//
// A collection has no columns, so the grid's are the fields a sample of its
// documents holds (ADR-0064): the shape decides what is shown, and a document
// holding something the sample missed still comes back whole — the value is
// there, under a field the grid does not show, and the JSON view shows it.

// browseSample is how many documents are read to decide the columns. Fewer
// than inference offers a person, because this runs on the way to a first
// page and NFR-P3 allows it two seconds.
const browseSample = 50

// Browse opens a stream over a collection's documents.
func (s *mongoSource) Browse(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (_ model.RowStream, err error) {
	defer panics.Recover(&err, "opening a collection's documents")
	if ref.Kind != model.KindCollection || len(ref.Path) < 2 {
		return nil, fmt.Errorf("mongodb: %s holds no documents", ref)
	}
	if strings.TrimSpace(opt.Where) != "" {
		// REQ-DRV-3: a source with no query language refuses one. MongoDB's
		// own is the console's (T2.37), and until it is there a filter
		// document would be a language nothing else in the app knows.
		return nil, errors.New("mongodb: this connection takes filters, not a query")
	}
	filter, err := filterOf(opt.Filters)
	if err != nil {
		return nil, err
	}
	cols, err := s.browseColumns(ctx, ref, opt)
	if err != nil {
		return nil, err
	}

	find := options.Find()
	if sort := sortOf(opt.Sorts); len(sort) > 0 {
		find.SetSort(sort)
	}
	if opt.Offset > 0 {
		find.SetSkip(opt.Offset)
	}
	if opt.Limit > 0 {
		find.SetLimit(opt.Limit)
	}
	if len(opt.Columns) > 0 {
		find.SetProjection(projectionOf(opt.Columns))
	}
	cur, err := s.collection(ref).Find(ctx, filter, find)
	if err != nil {
		return nil, err
	}
	return &documents{cur: cur, cols: cols, ref: ref}, nil
}

// Count is how many documents a browse would return (source.Countable).
func (s *mongoSource) Count(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (_ int64, err error) {
	defer panics.Recover(&err, "counting a collection's documents")
	if ref.Kind != model.KindCollection || len(ref.Path) < 2 {
		return 0, fmt.Errorf("mongodb: %s holds no documents", ref)
	}
	filter, err := filterOf(opt.Filters)
	if err != nil {
		return 0, err
	}
	return s.collection(ref).CountDocuments(ctx, filter)
}

func (s *mongoSource) collection(ref model.ObjectRef) *mongodriver.Collection {
	return s.client.Database(ref.Path[0]).Collection(ref.Path[1])
}

// browseColumns are the grid's columns: what the caller asked to see, or the
// fields a sample of the collection holds.
func (s *mongoSource) browseColumns(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) ([]model.ColumnDef, error) {
	if len(opt.Columns) > 0 {
		out := make([]model.ColumnDef, 0, len(opt.Columns))
		for _, name := range opt.Columns {
			out = append(out, model.ColumnDef{Name: name, Type: model.DataType{Class: model.TypeUnknown, Nullable: true}})
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
		// An empty collection still has the field every document gets.
		out = append(out, model.ColumnDef{Name: "_id", Type: model.DataType{Class: model.TypeString},
			Origin: ref, OriginColumn: "_id"})
	}
	return out, nil
}

// columnOf is the column a field becomes. A field seen with one type is that
// type; one seen with several is unknown, because no renderer is right for
// all of them and the grid draws what the value is.
func columnOf(f model.InferredField, ref model.ObjectRef) model.ColumnDef {
	col := model.ColumnDef{Name: f.Name, Origin: ref, OriginColumn: f.Name}
	col.Type.Nullable = f.Presence < 1
	switch {
	case len(f.Types) == 1:
		col.Type = f.Types[0].Type
		col.Type.Nullable = f.Presence < 1
	case len(f.Types) > 1:
		col.Type.Class = model.TypeUnknown
		var names []string
		for _, t := range f.Types {
			names = append(names, t.Type.Native)
		}
		col.Type.Native = strings.Join(names, " or ")
	}
	return col
}

// documents is a stream over a cursor.
type documents struct {
	cur  *mongodriver.Cursor
	cols []model.ColumnDef
	ref  model.ObjectRef
}

var (
	_ model.RowStream  = (*documents)(nil)
	_ model.Identified = (*documents)(nil)
)

func (d *documents) Columns() []model.ColumnDef { return d.cols }

// Identity is the _id every document has: MongoDB gives one to a document
// written without it, and it is unique within the collection (FR-4.7).
func (d *documents) Identity() model.RowIdentity {
	return model.RowIdentity{Kind: model.IdentityDocumentID, Columns: []string{"_id"}, Target: d.ref}
}

func (d *documents) Next(ctx context.Context) (_ model.Row, err error) {
	defer panics.Recover(&err, "reading a document")
	if !d.cur.Next(ctx) {
		if err := d.cur.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	return rowOf(d.cur.Current, d.cols), nil
}

// Close releases the cursor. The contract asks that it be safe to call more
// than once, and the driver's own Close is: a cursor already closed closes
// again without complaint.
func (d *documents) Close() error { return d.cur.Close(context.Background()) }

// rowOf lays a document out under the columns the grid shows. A field the
// document does not have is nil, as a NULL is; one the columns do not show
// is not lost, only not drawn.
func rowOf(doc bson.Raw, cols []model.ColumnDef) model.Row {
	row := make(model.Row, len(cols))
	// A document the driver could not read through leaves what it did read:
	// a row of nils is still a row, and the grid draws it.
	elems, _ := doc.Elements()
	at := make(map[string]int, len(cols))
	for i, c := range cols {
		at[c.Name] = i
	}
	for _, e := range elems {
		if i, ok := at[e.Key()]; ok {
			row[i] = goValue(e.Value())
		}
	}
	return row
}

// goValue is a BSON value as the grid and the editors know values. An
// embedded document is a map and an array a slice, which the grid draws as
// compact JSON and the cell viewer opens in full.
func goValue(v bson.RawValue) any {
	switch v.Type {
	case bson.TypeString:
		return v.StringValue()
	case bson.TypeObjectID:
		oid, _ := v.ObjectIDOK()
		return oid.Hex()
	case bson.TypeBoolean:
		return v.Boolean()
	case bson.TypeInt32:
		return int64(v.Int32())
	case bson.TypeInt64:
		return v.Int64()
	case bson.TypeDouble:
		return v.Double()
	case bson.TypeDecimal128:
		if dec, ok := v.Decimal128OK(); ok {
			// Carried as text so no digit is lost on the way to the grid.
			return model.Decimal(dec.String())
		}
		return nil
	case bson.TypeDateTime:
		return bson.DateTime(v.DateTime()).Time().UTC()
	case bson.TypeTimestamp:
		t, i := v.Timestamp()
		return time.Unix(int64(t), 0).UTC().Format(time.RFC3339) + fmt.Sprintf(" (%d)", i)
	case bson.TypeBinary:
		sub, data := v.Binary()
		if sub == bson.TypeBinaryUUID || sub == bson.TypeBinaryUUIDOld {
			if u, err := uuidText(data); err == nil {
				return u
			}
		}
		return data
	case bson.TypeNull, bson.TypeUndefined:
		return nil
	case bson.TypeEmbeddedDocument:
		return documentValue(v.Document())
	case bson.TypeArray:
		return arrayValue(v.Array())
	case bson.TypeRegex:
		pattern, opts := v.Regex()
		if opts != "" {
			return "/" + pattern + "/" + opts
		}
		return "/" + pattern + "/"
	case bson.TypeJavaScript:
		return v.JavaScript()
	case bson.TypeSymbol:
		return v.Symbol()
	case bson.TypeMinKey:
		return "MinKey"
	case bson.TypeMaxKey:
		return "MaxKey"
	}
	return v.String()
}

func documentValue(doc bson.Raw) map[string]any {
	elems, err := doc.Elements()
	if err != nil {
		return nil
	}
	out := make(map[string]any, len(elems))
	for _, e := range elems {
		out[e.Key()] = goValue(e.Value())
	}
	return out
}

func arrayValue(arr bson.RawArray) []any {
	vals, err := arr.Values()
	if err != nil {
		return nil
	}
	out := make([]any, 0, len(vals))
	for _, v := range vals {
		out = append(out, goValue(v))
	}
	return out
}

// uuidText renders the sixteen bytes of a UUID the way everything else does.
func uuidText(b []byte) (string, error) {
	if len(b) != 16 {
		return "", errors.New("not a uuid")
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// filterOf turns the grid's filters into one query document. They are ANDed,
// as the contract says, and a value is a value: nothing is written into the
// query as text (NFR-S6).
func filterOf(filters []source.Filter) (bson.D, error) {
	if len(filters) == 0 {
		return bson.D{}, nil
	}
	terms := make([]bson.D, 0, len(filters))
	for _, f := range filters {
		term, err := filterTerm(f)
		if err != nil {
			return nil, err
		}
		if f.Negate {
			term = bson.D{{Key: f.Column, Value: bson.D{{Key: "$not", Value: valueOnly(term, f.Column)}}}}
		}
		terms = append(terms, term)
	}
	if len(terms) == 1 {
		return terms[0], nil
	}
	and := make(bson.A, 0, len(terms))
	for _, t := range terms {
		and = append(and, t)
	}
	return bson.D{{Key: "$and", Value: and}}, nil
}

// valueOnly is a term's condition without its field, which is what $not takes.
func valueOnly(term bson.D, column string) any {
	for _, e := range term {
		if e.Key == column {
			return e.Value
		}
	}
	return term
}

func filterTerm(f source.Filter) (bson.D, error) {
	field := func(v any) bson.D { return bson.D{{Key: f.Column, Value: v}} }
	op := func(name string, v any) bson.D { return field(bson.D{{Key: name, Value: v}}) }
	first := func() (any, error) {
		if len(f.Values) < 1 {
			return nil, fmt.Errorf("mongodb: %s needs a value", f.Op)
		}
		return f.Values[0], nil
	}
	switch f.Op {
	case source.OpEqual, source.OpNotEqual, source.OpLess, source.OpLessEqual,
		source.OpGreater, source.OpGreaterEqual:
		v, err := first()
		if err != nil {
			return nil, err
		}
		return op(comparisons[f.Op], v), nil
	case source.OpIsNull:
		// A field that is not there and one holding null both read as null,
		// which is what the grid means by empty in a document store.
		return op("$eq", nil), nil
	case source.OpIsNotNull:
		return op("$ne", nil), nil
	case source.OpIn, source.OpNotIn:
		vals := make(bson.A, 0, len(f.Values))
		vals = append(vals, f.Values...)
		return op(map[source.FilterOp]string{source.OpIn: "$in", source.OpNotIn: "$nin"}[f.Op], vals), nil
	case source.OpBetween:
		if len(f.Values) < 2 {
			return nil, errors.New("mongodb: between needs two values")
		}
		return field(bson.D{{Key: "$gte", Value: f.Values[0]}, {Key: "$lte", Value: f.Values[1]}}), nil
	case source.OpLike, source.OpNotLike:
		v, err := first()
		if err != nil {
			return nil, err
		}
		re := likeRegex(fmt.Sprint(v))
		if f.Op == source.OpNotLike {
			return op("$not", bson.Regex{Pattern: re}), nil
		}
		return field(bson.Regex{Pattern: re}), nil
	case source.OpContains:
		v, err := first()
		if err != nil {
			return nil, err
		}
		return field(bson.Regex{Pattern: regexp.QuoteMeta(fmt.Sprint(v)), Options: "i"}), nil
	case source.OpRegex:
		v, err := first()
		if err != nil {
			return nil, err
		}
		return field(bson.Regex{Pattern: fmt.Sprint(v)}), nil
	}
	return nil, fmt.Errorf("mongodb: %s is not a filter this source knows", f.Op)
}

var comparisons = map[source.FilterOp]string{
	source.OpEqual:        "$eq",
	source.OpNotEqual:     "$ne",
	source.OpLess:         "$lt",
	source.OpLessEqual:    "$lte",
	source.OpGreater:      "$gt",
	source.OpGreaterEqual: "$gte",
}

// likeRegex turns SQL's LIKE pattern into a regular expression: % is any
// run, _ is one character, and everything else is itself.
func likeRegex(pattern string) string {
	var b strings.Builder
	b.WriteByte('^')
	for _, r := range pattern {
		switch r {
		case '%':
			b.WriteString(".*")
		case '_':
			b.WriteByte('.')
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteByte('$')
	return b.String()
}

// sortOf turns the grid's ordering into MongoDB's.
func sortOf(sorts []source.Sort) bson.D {
	out := make(bson.D, 0, len(sorts))
	for _, s := range sorts {
		dir := 1
		if s.Descending {
			dir = -1
		}
		out = append(out, bson.E{Key: s.Column, Value: dir})
	}
	return out
}

// projectionOf asks for the fields the caller wants, and _id whether it asked
// or not: without it a document cannot be told from another (FR-4.7).
func projectionOf(cols []string) bson.D {
	out := make(bson.D, 0, len(cols)+1)
	seen := false
	for _, c := range cols {
		out = append(out, bson.E{Key: c, Value: 1})
		seen = seen || c == "_id"
	}
	if !seen {
		out = append(out, bson.E{Key: "_id", Value: 1})
	}
	return out
}
