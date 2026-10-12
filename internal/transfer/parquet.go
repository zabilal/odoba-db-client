package transfer

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"strconv"
	"time"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/format"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Parquet, read as rows (FR-10.10).
//
// A Parquet file is columns, kept apart and compressed, with a description of
// each at its end. So unlike every other file an import takes, it says what it
// holds: a column is a number, a date, a moment, an exact decimal of so many
// places, and none of it has to be worked out from the values. What a column
// says it is, is what its values are read as — an exact number as its digits,
// a moment as a moment, bytes as bytes.
//
// It is read through a library and not by hand, unlike a workbook and a DBF
// table: the format is a dozen encodings, half a dozen compressions and a
// description written in a serialisation of its own, and a hand-written reader
// of that would be a library, less tested.
//
// A column that is itself made of columns — a list, a map, a group — is read
// as JSON, which is what a row has for something with parts. That one path
// goes through the library's own reading of a row, in which an exact decimal
// inside such a column becomes a float; a decimal that is a column of its own
// does not.
type parquetRows struct {
	reader *parquet.GenericReader[any]
	schema *parquet.Schema
	cols   []model.ColumnDef
	leaves []leaf // one to a column of the rows; nested where index is -1
	buf    []parquet.Row
	at, n  int
	read   int64
	done   bool
}

// leaf is how one column of the rows is found in a row of the file.
type leaf struct {
	name   string
	index  int // the file's column of that name, or -1 where the field has parts
	typ    parquet.Type
	nested bool
}

func openParquet(r io.ReaderAt, size int64) (*parquetRows, error) {
	f, err := parquet.OpenFile(r, size)
	if err != nil {
		return nil, fmt.Errorf("transfer: this is not a Parquet file that can be read: %w", err)
	}
	schema := f.Schema()
	index := map[string]int{}
	for i, path := range schema.Columns() {
		if len(path) == 1 {
			index[path[0]] = i
		}
	}
	p := &parquetRows{reader: parquet.NewGenericReader[any](f), schema: schema, buf: make([]parquet.Row, 256)}
	for _, field := range schema.Fields() {
		l := leaf{name: field.Name(), index: -1, typ: field.Type()}
		col := model.ColumnDef{Name: field.Name(), Type: model.DataType{Nullable: field.Optional(), Length: -1}}
		if at, ok := index[field.Name()]; ok && !field.Repeated() {
			l.index = at
			col.Type = parquetType(field.Type(), field.Optional())
		} else {
			l.nested = true
			col.Type.Class, col.Type.Native = model.TypeJSON, field.Type().String()
		}
		p.leaves, p.cols = append(p.leaves, l), append(p.cols, col)
	}
	if len(p.cols) == 0 {
		return nil, errors.New("transfer: this Parquet file describes no columns")
	}
	p.cols = renamed(p.cols)
	return p, nil
}

// logical is what a column says its values mean, beyond the bytes they are.
func logical(t parquet.Type) format.LogicalTypeValue {
	if lt := t.LogicalType(); lt != nil {
		return lt.Value
	}
	return nil
}

// parquetType is a column's type as rows hold it.
func parquetType(t parquet.Type, optional bool) model.DataType {
	dt := model.DataType{Native: t.String(), Nullable: optional, Length: -1}
	switch lt := logical(t).(type) {
	case *format.StringType, *format.EnumType:
		dt.Class = model.TypeString
	case *format.JsonType:
		dt.Class = model.TypeJSON
	case *format.UUIDType:
		dt.Class = model.TypeUUID
	case *format.DateType:
		dt.Class = model.TypeDate
	case *format.TimeType:
		dt.Class, dt.TimeZone = model.TypeTime, lt.IsAdjustedToUTC
	case *format.TimestampType:
		dt.Class, dt.TimeZone = model.TypeTimestamp, lt.IsAdjustedToUTC
		dt.Precision = digitsOf(lt.Unit)
	case *format.DecimalType:
		dt.Class, dt.Precision, dt.Scale = model.TypeDecimal, lt.Precision, lt.Scale
	default:
		switch t.Kind() {
		case parquet.Boolean:
			dt.Class = model.TypeBool
		case parquet.Int32, parquet.Int64:
			dt.Class = model.TypeInteger
		case parquet.Int96:
			// The moment older writers kept in twelve bytes, before the
			// format had a type for one.
			dt.Class, dt.Precision = model.TypeTimestamp, 9
		case parquet.Float, parquet.Double:
			dt.Class = model.TypeFloat
		default:
			dt.Class = model.TypeBytes
		}
	}
	return dt
}

// digitsOf is how many places of a second a unit keeps.
func digitsOf(u format.TimeUnit) int32 {
	if u.Value == nil {
		return 0
	}
	switch u.Value.Duration() {
	case time.Millisecond:
		return 3
	case time.Microsecond:
		return 6
	}
	return 9
}

func (p *parquetRows) Columns() []model.ColumnDef { return p.cols }

func (p *parquetRows) Next(ctx context.Context) (model.Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for p.at >= p.n {
		if p.done {
			return nil, io.EOF
		}
		n, err := p.reader.ReadRows(p.buf)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("transfer: after row %d of this Parquet file: %w", p.read, err)
		}
		p.done = errors.Is(err, io.EOF) || n == 0
		p.at, p.n = 0, n
	}
	raw := p.buf[p.at]
	p.at++
	p.read++

	row := make(model.Row, len(p.leaves))
	var parts map[string]any
	for i, l := range p.leaves {
		if l.nested {
			if parts == nil {
				parts = map[string]any{}
				if err := p.schema.Reconstruct(&parts, raw); err != nil {
					return nil, fmt.Errorf("transfer: row %d of this Parquet file: %w", p.read, err)
				}
			}
			b, err := json.Marshal(jsonSafe(parts[l.name]))
			if err != nil {
				return nil, fmt.Errorf("transfer: row %d, %s: %w", p.read, l.name, err)
			}
			if string(b) != "null" {
				row[i] = model.JSON(b)
			}
			continue
		}
		for _, v := range raw {
			if v.Column() != l.index {
				continue
			}
			val, err := parquetValue(v, l.typ)
			if err != nil {
				return nil, fmt.Errorf("transfer: row %d, %s: %w", p.read, l.name, err)
			}
			row[i] = val
			break
		}
	}
	return row, nil
}

func (p *parquetRows) Close() error { return p.reader.Close() }

// parquetValue is one value of a column, read as what the column says it is.
func parquetValue(v parquet.Value, t parquet.Type) (any, error) {
	if v.IsNull() {
		return nil, nil
	}
	switch lt := logical(t).(type) {
	case *format.StringType, *format.EnumType:
		return string(v.ByteArray()), nil
	case *format.JsonType:
		b := v.ByteArray()
		if json.Valid(b) {
			return model.JSON(append([]byte(nil), b...)), nil
		}
		return string(b), nil
	case *format.UUIDType:
		b := v.ByteArray()
		if len(b) != 16 {
			return nil, fmt.Errorf("a UUID of %d bytes", len(b))
		}
		h := hex.EncodeToString(b)
		return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
	case *format.DateType:
		return time.Unix(int64(v.Int32())*86400, 0).UTC(), nil
	case *format.TimeType:
		unit := time.Nanosecond
		if lt.Unit.Value != nil {
			unit = lt.Unit.Value.Duration()
		}
		since := v.Int64()
		if since < 0 || since >= int64(24*time.Hour/unit) {
			return nil, fmt.Errorf("%d is not a time of day in units of %v", since, unit)
		}
		return time.Unix(0, 0).UTC().Add(time.Duration(since) * unit).Format("15:04:05.999999999"), nil
	case *format.TimestampType:
		unit := time.Microsecond
		if lt.Unit.Value != nil {
			unit = lt.Unit.Value.Duration()
		}
		return moment(v.Int64(), unit), nil
	case *format.DecimalType:
		switch v.Kind() {
		case parquet.Int32, parquet.Int64:
			return model.Decimal(scaledBy(big.NewInt(v.Int64()), lt.Scale)), nil
		}
		return model.Decimal(scaledBy(twosComplement(v.ByteArray()), lt.Scale)), nil
	case *format.IntType:
		if !lt.IsSigned {
			if u := v.Uint64(); u > math.MaxInt64 {
				return model.Decimal(strconv.FormatUint(u, 10)), nil // past what an int64 holds
			} else {
				return int64(u), nil
			}
		}
	}
	switch v.Kind() {
	case parquet.Boolean:
		return v.Boolean(), nil
	case parquet.Int32, parquet.Int64:
		return v.Int64(), nil // either width: a value is kept as the wider
	case parquet.Int96:
		// Nanoseconds into the day, then the day as astronomers number them.
		at := v.Int96()
		nanos, day := uint64(at[0])|uint64(at[1])<<32, at[2]
		return time.Unix((int64(day)-julianUnixEpoch)*86400, 0).UTC().Add(time.Duration(nanos)), nil
	case parquet.Float:
		return float64(v.Float()), nil
	case parquet.Double:
		return v.Double(), nil
	}
	// Its own, not the page's it was read from; and no bytes is not nothing.
	return append([]byte{}, v.ByteArray()...), nil
}

// moment is a count of units since 1970 as the moment it is, without
// multiplying the count out and losing the far ends of what it can hold.
func moment(n int64, unit time.Duration) time.Time {
	per := int64(time.Second / unit)
	return time.Unix(n/per, n%per*int64(unit)).UTC() // which carries a negative remainder itself
}

// twosComplement is a signed number written as bytes, most significant first.
func twosComplement(b []byte) *big.Int {
	n := new(big.Int).SetBytes(b)
	if len(b) > 0 && b[0]&0x80 != 0 {
		n.Sub(n, new(big.Int).Lsh(big.NewInt(1), uint(8*len(b))))
	}
	return n
}

// scaledBy is a whole number with a point put so many places from its end:
// every digit, and every place the column has.
func scaledBy(n *big.Int, scale int32) string {
	if scale <= 0 {
		return n.String()
	}
	sign := ""
	if n.Sign() < 0 {
		sign, n = "-", new(big.Int).Neg(n)
	}
	digits := n.String()
	for len(digits) <= int(scale) {
		digits = "0" + digits
	}
	return sign + digits[:len(digits)-int(scale)] + "." + digits[len(digits)-int(scale):]
}
