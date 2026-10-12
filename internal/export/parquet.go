package export

import (
	"bufio"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/parquet-go/parquet-go"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Parquet: columns, each with its type, compressed (FR-10.10).
//
// Every other format here is text, or cells, and a value that is not what its
// column said it would be is written as what it is. A Parquet column is one
// type, said before the first value and binding on every one after it. So
// each column is given the type its source says it has — a whole number, an
// exact decimal of so many places, a date, a moment — and a row holding
// something else in that column is refused, naming the column and the row,
// rather than written as something it is not or left out.
//
// Where nothing exact can be said, text is: a decimal whose precision its
// source does not give is written as its digits, every one, and not as a
// float that would lose some.
//
// The columns stay in the order they came in. The library's own group of
// columns is a map and sorts them by name; this keeps a list.
//
// Rows are gathered into groups, each written and let go of before the next
// begins, so memory holds one group and not the file (FR-10.3).
type parquetWriter struct {
	w      *parquet.GenericWriter[any]
	cols   []model.ColumnDef
	names  []string
	kinds  []parquetKind
	scale  []int32 // a decimal column's places
	digits []int32 // and how many digits it holds
	size   []int   // and how many bytes each value is written in
	batch  []parquet.Row
	held   int   // bytes of values in the group being gathered, roughly
	rows   int64 // rows in that group
	n      int64 // rows in all
}

type parquetKind int

const (
	pqText parquetKind = iota
	pqJSON
	pqBool
	pqInt
	pqFloat
	pqDecimal
	pqBytes
	pqDate
	pqMicros
	pqNanos
)

// A group of rows is written when it holds this many, or this many bytes.
const (
	parquetGroupRows  = 100_000
	parquetGroupBytes = 32 << 20
	parquetBatch      = 256
)

// orderedGroup is a group of columns in the order they were given.
type orderedGroup struct {
	parquet.Group
	fields []parquet.Field
}

func (g *orderedGroup) Fields() []parquet.Field { return g.fields }

type orderedField struct {
	parquet.Node
	name string
}

func (f *orderedField) Name() string { return f.name }

// Value is how the library finds a field in a Go value. Rows here are written
// as values already, and nothing is looked up.
func (f *orderedField) Value(reflect.Value) reflect.Value { return reflect.Value{} }

func newParquet(w *bufio.Writer, cols []model.ColumnDef) (*parquetWriter, error) {
	if len(cols) == 0 {
		return nil, fmt.Errorf("export: a Parquet file needs at least one column")
	}
	p := &parquetWriter{cols: cols, names: uniqueNames(parquetNamed(cols)), kinds: make([]parquetKind, len(cols)),
		scale: make([]int32, len(cols)), digits: make([]int32, len(cols)), size: make([]int, len(cols))}
	group := &orderedGroup{Group: parquet.Group{}}
	for i, c := range cols {
		var node parquet.Node
		t := c.Type
		switch t.Class {
		case model.TypeBool:
			p.kinds[i], node = pqBool, parquet.Leaf(parquet.BooleanType)
		case model.TypeInteger:
			p.kinds[i], node = pqInt, parquet.Leaf(parquet.Int64Type)
		case model.TypeFloat:
			p.kinds[i], node = pqFloat, parquet.Leaf(parquet.DoubleType)
		case model.TypeBytes:
			p.kinds[i], node = pqBytes, parquet.Leaf(parquet.ByteArrayType)
		case model.TypeDate:
			p.kinds[i], node = pqDate, parquet.Date()
		case model.TypeTimestamp:
			// Microseconds reach every year a database keeps; nanoseconds
			// reach from 1677 to 2262, and are used only where asked for.
			p.kinds[i], node = pqMicros, parquet.TimestampAdjusted(parquet.Microsecond, t.TimeZone)
			if t.Precision > 6 {
				p.kinds[i], node = pqNanos, parquet.TimestampAdjusted(parquet.Nanosecond, t.TimeZone)
			}
		case model.TypeJSON, model.TypeArray:
			p.kinds[i], node = pqJSON, parquet.JSON()
		case model.TypeDecimal:
			if t.Precision >= 1 && t.Precision <= 38 && t.Scale >= 0 && t.Scale <= t.Precision {
				p.kinds[i], p.scale[i], p.digits[i], p.size[i] = pqDecimal, t.Scale, t.Precision, decimalBytes(t.Precision)
				node = parquet.Decimal(int(t.Scale), int(t.Precision), parquet.FixedLenByteArrayType(p.size[i]))
				break
			}
			fallthrough
		default:
			p.kinds[i], node = pqText, parquet.String()
		}
		group.fields = append(group.fields, &orderedField{Node: parquet.Optional(node), name: p.names[i]})
	}
	p.w = parquet.NewGenericWriter[any](w, parquet.NewSchema("rows", group), parquet.Compression(&parquet.Snappy),
		parquet.CreatedBy("ikigai-db", "", ""))
	return p, nil
}

// parquetNamed gives a column with no name one, as its number: a Parquet
// column is found by its name.
func parquetNamed(cols []model.ColumnDef) []model.ColumnDef {
	out := make([]model.ColumnDef, len(cols))
	for i, c := range cols {
		if strings.TrimSpace(c.Name) == "" {
			c.Name = fmt.Sprintf("column %d", i+1)
		}
		out[i] = c
	}
	return out
}

// decimalBytes is the fewest bytes that hold every signed number of so many
// digits.
func decimalBytes(precision int32) int {
	limit := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(precision)), nil)
	for n := 1; ; n++ {
		if new(big.Int).Lsh(big.NewInt(1), uint(8*n-1)).Cmp(limit) >= 0 {
			return n
		}
	}
}

func (p *parquetWriter) row(r model.Row) error {
	row := make(parquet.Row, len(p.cols))
	for i := range p.cols {
		var v any
		if i < len(r) {
			v = r[i]
		}
		if v == nil {
			row[i] = parquet.NullValue().Level(0, 0, i)
			continue
		}
		val, err := p.value(i, v)
		if err != nil {
			return fmt.Errorf("export: row %d, column %q: %w", p.n+1, p.cols[i].Name, err)
		}
		row[i] = val.Level(0, 1, i)
		p.held += 8
		if k := val.Kind(); k == parquet.ByteArray || k == parquet.FixedLenByteArray {
			p.held += len(val.ByteArray())
		}
	}
	p.batch = append(p.batch, row)
	p.n++
	p.rows++
	if len(p.batch) < parquetBatch && p.held < parquetGroupBytes {
		return nil
	}
	if err := p.flush(); err != nil {
		return err
	}
	if p.rows >= parquetGroupRows || p.held >= parquetGroupBytes {
		p.rows, p.held = 0, 0
		return p.w.Flush()
	}
	return nil
}

func (p *parquetWriter) flush() error {
	_, err := p.w.WriteRows(p.batch)
	clear(p.batch)
	p.batch = p.batch[:0]
	return err
}

func (p *parquetWriter) end() error {
	if err := p.flush(); err != nil {
		return err
	}
	return p.w.Close()
}

// value is one value as its column's type, or the reason it cannot be.
func (p *parquetWriter) value(i int, v any) (parquet.Value, error) {
	c := p.cols[i]
	switch p.kinds[i] {
	case pqBool:
		if x, ok := v.(bool); ok {
			return parquet.BooleanValue(x), nil
		}
	case pqInt:
		if x, ok := v.(int64); ok {
			return parquet.Int64Value(x), nil
		}
	case pqFloat:
		if x, ok := v.(float64); ok {
			return parquet.DoubleValue(x), nil
		}
	case pqBytes:
		if x, ok := v.([]byte); ok {
			return parquet.ByteArrayValue(x), nil
		}
	case pqDate:
		if x, ok := v.(time.Time); ok {
			// The day it says, wherever it says it.
			y, m, d := x.Date()
			return parquet.Int32Value(int32(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400)), nil
		}
	case pqMicros, pqNanos:
		if x, ok := v.(time.Time); ok {
			if !c.Type.TimeZone {
				// A wall clock: what it reads, and no offset to take off it.
				x = time.Date(x.Year(), x.Month(), x.Day(), x.Hour(), x.Minute(), x.Second(), x.Nanosecond(), time.UTC)
			}
			return parquetMoment(x, p.kinds[i] == pqNanos)
		}
	case pqDecimal:
		var digits string
		switch x := v.(type) {
		case model.Decimal:
			digits = string(x)
		case int64:
			digits = fmt.Sprint(x)
		default:
			return parquet.Value{}, mismatch(v, c)
		}
		n, err := unscaled(digits, p.scale[i], p.digits[i])
		if err != nil {
			return parquet.Value{}, err
		}
		return parquet.FixedLenByteArrayValue(fixed(n, p.size[i])), nil
	default:
		// Text, and JSON, which is text: anything can be written as its own.
		return parquet.ByteArrayValue([]byte(text(v, c, ""))), nil
	}
	return parquet.Value{}, mismatch(v, c)
}

func mismatch(v any, c model.ColumnDef) error {
	return fmt.Errorf("the column is %s and this value is %s; a Parquet column holds one type", c.Type.Class, kindOf(v))
}

func kindOf(v any) string {
	switch v.(type) {
	case string:
		return "text"
	case bool:
		return "a boolean"
	case int64:
		return "a whole number"
	case float64:
		return "a float"
	case model.Decimal:
		return "an exact number"
	case []byte:
		return "bytes"
	case time.Time:
		return "a time"
	case model.JSON, []any, map[string]any:
		return "JSON"
	}
	return fmt.Sprintf("%T", v)
}

// parquetMoment is a moment as a count since 1970, or the reason the count
// cannot hold it.
func parquetMoment(t time.Time, nanos bool) (parquet.Value, error) {
	sec, sub := t.Unix(), int64(t.Nanosecond())
	if nanos {
		if sec > math.MaxInt64/int64(time.Second)-1 || sec < math.MinInt64/int64(time.Second)+1 {
			return parquet.Value{}, fmt.Errorf("%s is outside what nanoseconds can count, 1677 to 2262", t.Format(time.RFC3339Nano))
		}
		return parquet.Int64Value(sec*int64(time.Second) + sub), nil
	}
	if sub%1000 != 0 {
		return parquet.Value{}, fmt.Errorf("%s is finer than the microseconds its column is written in", t.Format(time.RFC3339Nano))
	}
	if sec > math.MaxInt64/1_000_000-1 || sec < math.MinInt64/1_000_000+1 {
		return parquet.Value{}, fmt.Errorf("the year %d is outside what microseconds can count", t.Year())
	}
	return parquet.Int64Value(sec*1_000_000 + sub/1000), nil
}

// decimalText is a number as a database writes an exact one: digits, a point
// among them or not, and an exponent short enough to be worked out.
var decimalText = regexp.MustCompile(`^[+-]?(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]{1,4})?$`)

// unscaled is a decimal's digits as the whole number a column of so many
// places keeps, or the reason the column cannot keep it.
func unscaled(s string, scale, precision int32) (*big.Int, error) {
	if !decimalText.MatchString(s) {
		return nil, fmt.Errorf("%q is not a number a decimal column can hold", s)
	}
	whole, frac, _ := strings.Cut(s, ".")
	if strings.ContainsAny(s, "eE") {
		r, _ := new(big.Rat).SetString(s) // exactly, whatever its exponent
		r.Mul(r, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)))
		if !r.IsInt() {
			return nil, fmt.Errorf("%s has more than the %d decimal places of its column", s, scale)
		}
		return fits(new(big.Int).Set(r.Num()), s, precision)
	}
	if kept := strings.TrimRight(frac, "0"); len(kept) > int(scale) {
		return nil, fmt.Errorf("%s has more than the %d decimal places of its column", s, scale)
	} else {
		frac = kept
	}
	n, _ := new(big.Int).SetString(whole+frac+strings.Repeat("0", int(scale)-len(frac)), 10)
	return fits(n, s, precision)
}

func fits(n *big.Int, s string, precision int32) (*big.Int, error) {
	limit := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(precision)), nil)
	if new(big.Int).Abs(n).Cmp(limit) >= 0 {
		return nil, fmt.Errorf("%s has more than the %d digits of its column", s, precision)
	}
	return n, nil
}

// fixed is a signed number in exactly size bytes, most significant first.
func fixed(n *big.Int, size int) []byte {
	out := make([]byte, size)
	if n.Sign() >= 0 {
		n.FillBytes(out)
		return out
	}
	// Two's complement: the number, added to one past what the bytes hold.
	new(big.Int).Add(n, new(big.Int).Lsh(big.NewInt(1), uint(8*size))).FillBytes(out)
	return out
}
