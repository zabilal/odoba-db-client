package export

import (
	"bytes"
	"context"
	"errors"

	"io"
	"math"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// pq is a written Parquet file read back by the library, as another program
// would read it: what each column says it is, and every value as the file
// keeps it.
type pq struct {
	file   *parquet.File
	fields []string // "name type", in the file's order
	rows   []parquet.Row
}

func readPQ(t *testing.T, raw string) pq {
	t.Helper()
	f, err := parquet.OpenFile(strings.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("what was written is not a Parquet file: %v", err)
	}
	p := pq{file: f}
	for _, field := range f.Schema().Fields() {
		if !field.Optional() {
			t.Errorf("%s cannot be absent, and any column of a result can", field.Name())
		}
		p.fields = append(p.fields, field.Name()+" "+field.Type().String())
	}
	r := parquet.NewGenericReader[any](f)
	defer r.Close()
	for {
		buf := make([]parquet.Row, 64)
		n, err := r.ReadRows(buf)
		for _, row := range buf[:n] {
			p.rows = append(p.rows, row.Clone())
		}
		if errors.Is(err, io.EOF) || n == 0 {
			return p
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func typedCol(name string, dt model.DataType) model.ColumnDef {
	return model.ColumnDef{Name: name, Type: dt}
}

func TestParquetGivesEachColumnItsType(t *testing.T) {
	zone := time.FixedZone("", -5*3600)
	when := time.Date(2024, 2, 29, 23, 14, 15, 123456000, zone)
	src := &rows{
		cols: []model.ColumnDef{
			col("zeta", model.TypeInteger, false), col("alpha", model.TypeString, false),
			typedCol("price", model.DataType{Class: model.TypeDecimal, Precision: 12, Scale: 3}),
			col("free", model.TypeDecimal, false), col("f", model.TypeFloat, false), col("ok", model.TypeBool, false),
			col("raw", model.TypeBytes, false), col("day", model.TypeDate, false),
			col("at", model.TypeTimestamp, true), col("wall", model.TypeTimestamp, false),
			typedCol("fine", model.DataType{Class: model.TypeTimestamp, Precision: 9, TimeZone: true}),
			typedCol("six", model.DataType{Class: model.TypeTimestamp, Precision: 6, TimeZone: true}),
			col("doc", model.TypeJSON, false), col("tags", model.TypeArray, false),
			col("clock", model.TypeTime, false), col("what", model.TypeUnknown, false),
		},
		data: []model.Row{{
			int64(-7), "héllo", model.Decimal("-12.5"), model.Decimal("12345678901234567890.123456789"), math.NaN(), true,
			[]byte{0xde, 0xad}, when, when, when, when.Add(789), when, model.JSON(`{"a": 1}`), []any{"x", model.Decimal("1.50")},
			"13:14:15", int64(5),
		}, {}},
	}
	p := readPQ(t, export(t, src, Options{Format: Parquet}))
	want := []string{
		"zeta INT(64,true)", "alpha STRING", "price DECIMAL(12,3)", "free STRING", "f DOUBLE", "ok BOOLEAN", "raw BYTE_ARRAY", "day DATE",
		"at TIMESTAMP(isAdjustedToUTC=true,unit=MICROS)", "wall TIMESTAMP(isAdjustedToUTC=false,unit=MICROS)",
		"fine TIMESTAMP(isAdjustedToUTC=true,unit=NANOS)", "six TIMESTAMP(isAdjustedToUTC=true,unit=MICROS)", "doc JSON", "tags JSON", "clock STRING", "what STRING",
	}
	// In the order they came in, and not the alphabet's.
	if got := strings.Join(p.fields, "\n"); got != strings.Join(want, "\n") {
		t.Fatalf("the columns:\n%s\nfor\n%s", got, strings.Join(want, "\n"))
	}
	if len(p.rows) != 2 || p.file.NumRows() != 2 {
		t.Fatalf("%d rows", len(p.rows))
	}
	r := p.rows[0]
	utc := when.UTC()
	for i, ok := range []bool{
		r[0].Int64() == -7,
		string(r[1].ByteArray()) == "héllo",
		// -12500, in the six bytes twelve digits need.
		bytes.Equal(r[2].ByteArray(), []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xCF, 0x2C}),
		string(r[3].ByteArray()) == "12345678901234567890.123456789",
		math.IsNaN(r[4].Double()),
		r[5].Boolean(),
		bytes.Equal(r[6].ByteArray(), []byte{0xde, 0xad}),
		// The day it says where it says it, 29 February, though it is already March in UTC.
		r[7].Int32() == int32(time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC).Unix()/86400),
		// A moment is the moment; a wall clock is what it reads.
		r[8].Int64() == utc.UnixMicro(),
		r[9].Int64() == time.Date(2024, 2, 29, 23, 14, 15, 123456000, time.UTC).UnixMicro(),
		r[10].Int64() == utc.UnixNano()+789,
		r[11].Int64() == utc.UnixMicro(),
		string(r[12].ByteArray()) == `{"a": 1}`,
		string(r[13].ByteArray()) == `["x",1.50]`,
		string(r[14].ByteArray()) == "13:14:15",
		string(r[15].ByteArray()) == "5",
	} {
		if !ok {
			t.Errorf("%s holds %v", p.fields[i], r[i])
		}
	}
	// A short row is filled with nothing, in every column.
	for i, v := range p.rows[1] {
		if !v.IsNull() || v.Column() != i {
			t.Errorf("%s of an empty row holds %v, as column %d", p.fields[i], v, v.Column())
		}
	}
	if by := p.file.Metadata().CreatedBy; !strings.HasPrefix(by, "ikigai-db") {
		t.Errorf("who wrote it: %q", by)
	}
	for _, c := range p.file.Metadata().RowGroups[0].Columns {
		if got := c.MetaData.Codec.String(); got != "SNAPPY" {
			t.Errorf("%v is %s, and not compressed as every reader reads", c.MetaData.PathInSchema, got)
		}
	}
	if Parquet.String() != "Parquet" || Parquet.Extension() != "parquet" {
		t.Errorf("its name and its file's: %s, %s", Parquet, Parquet.Extension())
	}
}

func TestParquetNamesEveryColumnOnce(t *testing.T) {
	src := &rows{cols: []model.ColumnDef{col("id", model.TypeInteger, false), col("", model.TypeString, false),
		col("id", model.TypeInteger, false), col("  ", model.TypeString, false)}, data: []model.Row{{int64(1), "a", int64(2), "b"}}}
	p := readPQ(t, export(t, src, Options{Format: Parquet}))
	if got := strings.Join(p.fields, ", "); got != "id INT(64,true), column 2 STRING, id_2 INT(64,true), column 4 STRING" {
		t.Errorf("the columns: %s", got)
	}
	if _, err := Copy(context.Background(), io.Discard, &rows{}, Options{Format: Parquet}, nil); err == nil || !strings.Contains(err.Error(), "at least one column") {
		t.Errorf("a file of no columns: %v", err)
	}
	// No rows is still a file, which says what its columns are.
	p = readPQ(t, export(t, &rows{cols: src.cols[:1]}, Options{Format: Parquet}))
	if len(p.rows) != 0 || len(p.fields) != 1 {
		t.Errorf("no rows: %d rows, %v", len(p.rows), p.fields)
	}
}

func TestParquetRefusesWhatAColumnCannotHold(t *testing.T) {
	dec := model.DataType{Class: model.TypeDecimal, Precision: 5, Scale: 2}
	far := time.Date(2300, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		col  model.ColumnDef
		v    any
		want string
	}{
		{col("n", model.TypeInteger, false), "seven", `column "n": the column is integer and this value is text; a Parquet column holds one type`},
		{col("n", model.TypeInteger, false), model.Decimal("18446744073709551615"), "this value is an exact number"},
		{col("f", model.TypeFloat, false), int64(1), "the column is float and this value is a whole number"},
		{col("ok", model.TypeBool, false), 1.5, "this value is a float"},
		{col("raw", model.TypeBytes, false), true, "the column is bytes and this value is a boolean"},
		{col("day", model.TypeDate, false), []byte{1}, "the column is date and this value is bytes"},
		{col("at", model.TypeTimestamp, true), model.JSON(`1`), "this value is JSON"},
		{typedCol("d", dec), far, "the column is decimal and this value is a time"},
		{typedCol("d", dec), struct{}{}, "this value is struct {}"},
		{typedCol("d", dec), model.Decimal("1.005"), "1.005 has more than the 2 decimal places of its column"},
		{typedCol("d", dec), model.Decimal("1000"), "1000 has more than the 5 digits of its column"},
		{typedCol("d", dec), int64(-1000), "-1000 has more than the 5 digits of its column"},
		{typedCol("d", dec), model.Decimal("NaN"), `"NaN" is not a number a decimal column can hold`},
		{col("at", model.TypeTimestamp, true), time.Date(2024, 1, 1, 0, 0, 0, 1, time.UTC), "2024-01-01T00:00:00.000000001Z is finer than the microseconds its column is written in"},
		{col("at", model.TypeTimestamp, true), time.Date(300000, 1, 1, 0, 0, 0, 0, time.UTC), "the year 300000 is outside what microseconds can count"},
		{typedCol("at", model.DataType{Class: model.TypeTimestamp, Precision: 9}), far, "2300-01-01T00:00:00Z is outside what nanoseconds can count, 1677 to 2262"},
	} {
		src := &rows{cols: []model.ColumnDef{c.col}, data: []model.Row{{nil}, {c.v}}}
		_, err := Copy(context.Background(), io.Discard, src, Options{Format: Parquet}, nil)
		if err == nil || !strings.HasPrefix(err.Error(), "export: row 2, column ") || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v in a %s column: %v", c.v, c.col.Type.Class, err)
		}
	}
}

func TestParquetKeepsADecimalsEveryDigit(t *testing.T) {
	for _, c := range []struct {
		s      string
		scale  int32
		digits int32
		want   string // the whole number kept, or nothing where it is refused
	}{
		{"0", 2, 5, "0"}, {"1.5", 2, 5, "150"}, {"-1.5", 2, 5, "-150"}, {"1.50", 1, 5, "15"}, {"1.500", 1, 5, "15"},
		{"999.99", 2, 5, "99999"}, {"-999.99", 2, 5, "-99999"}, {"1000", 2, 5, ""}, {"1.55", 1, 5, ""},
		{"1e2", 0, 5, "100"}, {"1E2", 0, 5, "100"}, {"1.5e-1", 2, 5, "15"}, {"1.5e-3", 2, 5, ""}, {"1e5", 0, 5, ""}, {"-1e5", 0, 5, ""},
		{".5", 1, 5, "5"}, {"-.5", 1, 5, "-5"}, {"+.5", 1, 5, "5"}, {"+1", 0, 5, "1"}, {"5.", 0, 5, "5"},
		{"NaN", 2, 5, ""}, {"Infinity", 2, 5, ""}, {"", 2, 5, ""}, {"1.2.3", 2, 5, ""}, {"12a", 0, 5, ""}, {"-", 0, 5, ""}, {"-", 2, 5, ""}, {".", 2, 5, ""}, {"+", 1, 5, ""},
		{"1/2", 1, 5, ""}, {"0x10", 0, 5, ""}, {"1e", 0, 5, ""}, {"1e99999", 0, 5, ""}, {"1e999999999", 0, 5, ""}, {"1e-999999999", 0, 5, ""}, {"1e9999", 0, 5, ""}, {" 1", 0, 5, ""}, {"1_0", 0, 5, ""}, {"-0.0", 1, 5, "0"},
		{"99999999999999999999999999999999999999", 0, 38, "99999999999999999999999999999999999999"},
		{"100000000000000000000000000000000000000", 0, 38, ""},
	} {
		n, err := unscaled(c.s, c.scale, c.digits)
		switch {
		case c.want == "" && err == nil:
			t.Errorf("%q in a column of %d digits and %d places is refused, and was kept as %s", c.s, c.digits, c.scale, n)
		case c.want != "" && (err != nil || n.String() != c.want):
			t.Errorf("%q in a column of %d digits and %d places is %s: %v, %v", c.s, c.digits, c.scale, c.want, n, err)
		}
	}
	// The fewest bytes that hold every number of so many digits, which is
	// what every reader expects a column to be.
	for digits, want := range map[int32]int{1: 1, 2: 1, 3: 2, 4: 2, 5: 3, 9: 4, 10: 5, 18: 8, 19: 9, 38: 16} {
		if got := decimalBytes(digits); got != want {
			t.Errorf("%d digits are kept in %d bytes, not %d", digits, want, got)
		}
	}
	for _, c := range []struct {
		n    int64
		size int
		want []byte
	}{
		{0, 2, []byte{0, 0}}, {1, 2, []byte{0, 1}}, {-1, 2, []byte{0xFF, 0xFF}}, {-1, 1, []byte{0xFF}},
		{127, 1, []byte{0x7F}}, {-128, 1, []byte{0x80}}, {-12500, 6, []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xCF, 0x2C}}, {256, 3, []byte{0, 1, 0}},
	} {
		if got := fixed(big.NewInt(c.n), c.size); !bytes.Equal(got, c.want) {
			t.Errorf("%d in %d bytes is % x, not % x", c.n, c.size, c.want, got)
		}
	}
	// The ends of what each count of a moment holds are held, and one past
	// them is not.
	for _, c := range []struct {
		t     time.Time
		nanos bool
		want  int64
		ok    bool
	}{
		{time.Unix(math.MaxInt64/1_000_000_000-1, 999999999), true, (math.MaxInt64/1_000_000_000-1)*1_000_000_000 + 999999999, true},
		{time.Unix(math.MinInt64/1_000_000_000+1, 0), true, (math.MinInt64/1_000_000_000 + 1) * 1_000_000_000, true},
		{time.Unix(0, 0).Add(-1), true, -1, true},
		{time.Unix(0, 0).Add(-1000), false, -1, true},
		{time.Unix(0, 999000), false, 999, true},
		{time.Unix(math.MaxInt64/1_000_000-1, 999999000), false, (math.MaxInt64/1_000_000-1)*1_000_000 + 999999, true},
		{time.Unix(math.MaxInt64/1_000_000, 0), false, 0, false},
		{time.Unix(math.MinInt64/1_000_000+1, 0), false, (math.MinInt64/1_000_000 + 1) * 1_000_000, true},
		{time.Unix(math.MinInt64/1_000_000, 0), false, 0, false},
		{time.Unix(math.MaxInt64/1_000_000_000, 0), true, 0, false},
		{time.Unix(math.MinInt64/1_000_000_000, 0), true, 0, false},
	} {
		v, err := parquetMoment(c.t, c.nanos)
		if (err == nil) != c.ok || (c.ok && v.Int64() != c.want) {
			t.Errorf("%d s, %d ns (nanos %v): %v, %v; for %d, %v", c.t.Unix(), c.t.Nanosecond(), c.nanos, v, err, c.want, c.ok)
		}
	}
}

func TestParquetWritesRowsInGroups(t *testing.T) {
	// By count: a group is let go of when it holds enough rows.
	const n = parquetGroupRows + 3*parquetBatch
	p := readPQ(t, export(t, &limited{max: n}, Options{Format: Parquet}))
	if p.file.NumRows() != n || len(p.rows) != n {
		t.Fatalf("%d rows written of %d", p.file.NumRows(), n)
	}
	groups := p.file.RowGroups()
	first := int64(parquetGroupRows+parquetBatch-1) / parquetBatch * parquetBatch
	if len(groups) != 2 || groups[0].NumRows() != first || groups[1].NumRows() != n-first {
		t.Errorf("%d groups, the first of %d rows", len(groups), groups[0].NumRows())
	}
	for i, r := range p.rows {
		if r[0].Int64() != int64(i+1) {
			t.Fatalf("row %d holds %v", i+1, r[0])
		}
	}
	// By size: a few very wide rows are a group before they are many.
	wide := strings.Repeat("x", parquetGroupBytes/4)
	src := &rows{cols: []model.ColumnDef{col("s", model.TypeString, false), col("b", model.TypeBytes, false)}}
	for range 5 {
		src.data = append(src.data, model.Row{wide, []byte(wide)})
	}
	p = readPQ(t, export(t, src, Options{Format: Parquet}))
	if got := len(p.file.RowGroups()); got != 3 || len(p.rows) != 5 {
		t.Errorf("five rows of %d MB in %d groups, %d rows read back", len(wide)>>19, got, len(p.rows))
	}

}
