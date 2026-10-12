package transfer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/deprecated"

	"github.com/ikigai-db/ikigai-db/internal/export"
	"github.com/ikigai-db/ikigai-db/internal/model"
)

// parquetOf is a Parquet file of these records, written by the library as
// another program would write one.
func parquetOf[T any](t *testing.T, records ...T) []byte {
	t.Helper()
	var b bytes.Buffer
	w := parquet.NewGenericWriter[T](&b)
	if _, err := w.Write(records); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// readParquet is a file's columns and every row of it.
func readParquet(t *testing.T, file []byte) ([]model.ColumnDef, []model.Row) {
	t.Helper()
	s, err := Open(bytes.NewReader(file), int64(len(file)), Options{Format: Parquet})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var rows []model.Row
	for {
		r, err := s.Next(context.Background())
		if errors.Is(err, io.EOF) {
			return s.Columns(), rows
		}
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, r)
	}
}

// typed is every kind of column another program writes that is one value.
type typed struct {
	ID      int64            `parquet:"id"`
	Small   int32            `parquet:"small"`
	U32     uint32           `parquet:"u32"`
	U64     uint64           `parquet:"u64"`
	Name    *string          `parquet:"name,optional"`
	Ratio   float32          `parquet:"ratio"`
	Double  float64          `parquet:"double"`
	Yes     bool             `parquet:"yes"`
	Raw     []byte           `parquet:"raw"`
	Price   int64            `parquet:"price,decimal(2:12)"`
	Tenth   int32            `parquet:"tenth,decimal(1:5)"`
	Wide    [16]byte         `parquet:"wide,decimal(4:38)"`
	Day     int32            `parquet:"day,date"`
	Millis  time.Time        `parquet:"millis,timestamp(millisecond)"`
	Micros  int64            `parquet:"micros,timestamp(microsecond:local)"`
	Nanos   int64            `parquet:"nanos,timestamp(nanosecond)"`
	Clock   int64            `parquet:"clock,time(microsecond)"`
	Shift   int32            `parquet:"shift,time(millisecond)"`
	Ident   [16]byte         `parquet:"ident,uuid"`
	Kind    string           `parquet:"kind,enum"`
	Doc     string           `parquet:"doc,json"`
	Legacy  deprecated.Int96 `parquet:"legacy"`
	Fixed   [3]byte          `parquet:"fixed"`
	Missing *int64           `parquet:"missing,optional"`
}

func TestParquetReadsEachColumnAsWhatItSaysItIs(t *testing.T) {
	name := "Åsa"
	wide := [16]byte{}
	for i := range wide {
		wide[i] = 0xFF
	}
	wide[14], wide[15] = 0xCF, 0xC7 // -12345, in sixteen bytes
	// 2001-09-09T01:46:40Z, as older writers kept a moment: nanoseconds into
	// the day and the Julian day.
	var legacy deprecated.Int96
	nanos := uint64((time.Hour + 46*time.Minute + 40*time.Second + 5).Nanoseconds())
	legacy[0], legacy[1], legacy[2] = uint32(nanos), uint32(nanos>>32), 2452162
	file := parquetOf(t, typed{
		ID: math.MinInt64, Small: -5, U32: math.MaxUint32, U64: math.MaxUint64, Name: &name, Ratio: 0.5, Double: -2.25,
		Yes: true, Raw: []byte{0, 0xFF}, Price: -1999, Tenth: 5, Wide: wide, Day: 19782,
		Millis: time.Date(2024, 2, 29, 13, 14, 15, 123e6, time.FixedZone("", 3600)),
		Micros: -1, Nanos: 1_000_000_000_000_000_001, Clock: 47655000250, Shift: 86399999,
		Ident: [16]byte{0x12, 0x3e, 0x45, 0x67, 0xe8, 0x9b, 0x12, 0xd3, 0xa4, 0x56, 0x42, 0x66, 0x14, 0x17, 0x40, 0x00},
		Kind:  "open", Doc: `{"a": [1, 2]}`, Legacy: legacy, Fixed: [3]byte{1, 2, 3},
	}, typed{U64: 7, Doc: "not json"})

	cols, rows := readParquet(t, file)
	want := []struct {
		name  string
		class model.TypeClass
		value any
	}{
		{"id", model.TypeInteger, int64(math.MinInt64)},
		{"small", model.TypeInteger, int64(-5)},
		{"u32", model.TypeInteger, int64(math.MaxUint32)},
		{"u64", model.TypeInteger, model.Decimal("18446744073709551615")},
		{"name", model.TypeString, "Åsa"},
		{"ratio", model.TypeFloat, 0.5},
		{"double", model.TypeFloat, -2.25},
		{"yes", model.TypeBool, true},
		{"raw", model.TypeBytes, []byte{0, 0xFF}},
		{"price", model.TypeDecimal, model.Decimal("-19.99")},
		{"tenth", model.TypeDecimal, model.Decimal("0.5")},
		{"wide", model.TypeDecimal, model.Decimal("-1.2345")},
		{"day", model.TypeDate, time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)},
		{"millis", model.TypeTimestamp, time.Date(2024, 2, 29, 12, 14, 15, 123e6, time.UTC)},
		{"micros", model.TypeTimestamp, time.Date(1969, 12, 31, 23, 59, 59, 999999000, time.UTC)},
		{"nanos", model.TypeTimestamp, time.Date(2001, 9, 9, 1, 46, 40, 1, time.UTC)},
		{"clock", model.TypeTime, "13:14:15.00025"},
		{"shift", model.TypeTime, "23:59:59.999"},
		{"ident", model.TypeUUID, "123e4567-e89b-12d3-a456-426614174000"},
		{"kind", model.TypeString, "open"},
		{"doc", model.TypeJSON, model.JSON(`{"a": [1, 2]}`)},
		{"legacy", model.TypeTimestamp, time.Date(2001, 9, 9, 1, 46, 40, 5, time.UTC)},
		{"fixed", model.TypeBytes, []byte{1, 2, 3}},
		{"missing", model.TypeInteger, nil},
	}
	if len(cols) != len(want) || len(rows) != 2 {
		t.Fatalf("%d columns and %d rows, for %d and 2", len(cols), len(rows), len(want))
	}
	for i, w := range want {
		if cols[i].Name != w.name || cols[i].Type.Class != w.class {
			t.Errorf("column %d is %s, %v; it is %s, %v in the file", i, cols[i].Name, cols[i].Type.Class, w.name, w.class)
		}
		if !reflect.DeepEqual(rows[0][i], w.value) {
			t.Errorf("%s: %#v, and the file holds %#v", w.name, rows[0][i], w.value)
		}
	}
	by := map[string]model.ColumnDef{}
	for _, c := range cols {
		by[c.Name] = c
	}
	// What a column says of itself beyond its class is kept: an exact
	// number's digits and places, a moment's places and whether it is one
	// moment everywhere, whether it may be absent.
	if c := by["price"].Type; c.Precision != 12 || c.Scale != 2 || c.Native != "DECIMAL(12,2)" || c.Nullable {
		t.Errorf("price: %+v", c)
	}
	if c := by["wide"].Type; c.Precision != 38 || c.Scale != 4 {
		t.Errorf("wide: %+v", c)
	}
	for name, w := range map[string]struct {
		digits int32
		zone   bool
	}{"millis": {3, true}, "micros": {6, false}, "nanos": {9, true}, "legacy": {9, false}} {
		if c := by[name].Type; c.Precision != w.digits || c.TimeZone != w.zone {
			t.Errorf("%s keeps %d places, zone %v: it keeps %d, %v", name, c.Precision, c.TimeZone, w.digits, w.zone)
		}
	}
	if !by["name"].Type.Nullable || !by["missing"].Type.Nullable || by["id"].Type.Nullable {
		t.Error("a column that may be absent says so, and one that may not does not")
	}
	// The second row: a number that fits is a number, text that is not JSON
	// in a column said to be is still its text, and what is absent is absent.
	if rows[1][3] != int64(7) || rows[1][20] != "not json" || rows[1][4] != nil || rows[1][16] != "00:00:00" {
		t.Errorf("the second row: %#v, %#v, %#v", rows[1][3], rows[1][19], rows[1][4])
	}
}

// nested is a file whose columns have parts, among ones that do not.
type nested struct {
	Tags  []string `parquet:"tags,list"`
	ID    int64    `parquet:"id"`
	Where struct {
		City string  `parquet:"city"`
		Lat  float64 `parquet:"lat"`
	} `parquet:"where"`
	Counts map[string]int64 `parquet:"counts"`
	Plain  []int32          `parquet:"plain"`
	Name   string           `parquet:"name"`
	Maybe  *struct {
		N int64 `parquet:"n"`
	} `parquet:"maybe,optional"`
}

func TestParquetReadsAColumnWithPartsAsJSON(t *testing.T) {
	a := nested{Tags: []string{"x", "y"}, ID: 1, Counts: map[string]int64{"n": 3}, Plain: []int32{4, 5}, Name: "first"}
	a.Where.City, a.Where.Lat = "Oslo", 59.9
	a.Maybe = &struct {
		N int64 `parquet:"n"`
	}{9}
	cols, rows := readParquet(t, parquetOf(t, a, nested{ID: 2, Name: "second"}))
	var names []string
	for _, c := range cols {
		names = append(names, c.Name+":"+c.Type.Class.String())
	}
	if got := strings.Join(names, " "); got != "tags:json id:integer where:json counts:json plain:json name:string maybe:json" {
		t.Fatalf("the columns, in the file's order: %s", got)
	}
	want := model.Row{model.JSON(`["x","y"]`), int64(1), model.JSON(`{"city":"Oslo","lat":59.9}`), model.JSON(`{"n":3}`), model.JSON(`[4,5]`), "first", model.JSON(`{"n":9}`)}
	if !reflect.DeepEqual(rows[0], want) {
		t.Errorf("the first row:\n%s\nfor\n%s", show(rows[0]), show(want))
	}
	// The columns after a column with parts are still found, in a row whose
	// parts are empty.
	if rows[1][1] != int64(2) || rows[1][5] != "second" {
		t.Errorf("the second row: %s", show(rows[1]))
	}
	// Parts that are absent are nothing, and not JSON's word for it; and a
	// column says whether its parts may be.
	if rows[1][6] != nil || !cols[6].Type.Nullable || cols[2].Type.Nullable {
		t.Errorf("absent parts: %#v, nullable %v and %v", rows[1][6], cols[6].Type.Nullable, cols[2].Type.Nullable)
	}
}

func show(r model.Row) string {
	var b strings.Builder
	for _, v := range r {
		if j, ok := v.(model.JSON); ok {
			b.WriteString(string(j) + " | ")
			continue
		}
		b.WriteString(export.Text(v, model.ColumnDef{}) + " | ")
	}
	return b.String()
}

// What export writes, import reads back as it was: the two halves of a
// transfer between databases through a file (FR-10.10).
func TestParquetReadsBackWhatExportWrote(t *testing.T) {
	cols := []model.ColumnDef{
		{Name: "z", Type: model.DataType{Class: model.TypeInteger}},
		{Name: "a", Type: model.DataType{Class: model.TypeString}},
		{Name: "amount", Type: model.DataType{Class: model.TypeDecimal, Precision: 20, Scale: 3}},
		{Name: "day", Type: model.DataType{Class: model.TypeDate}},
		{Name: "at", Type: model.DataType{Class: model.TypeTimestamp, TimeZone: true}},
		{Name: "wall", Type: model.DataType{Class: model.TypeTimestamp, Precision: 9}},
		{Name: "doc", Type: model.DataType{Class: model.TypeJSON}},
		{Name: "raw", Type: model.DataType{Class: model.TypeBytes}},
		{Name: "f", Type: model.DataType{Class: model.TypeFloat}},
		{Name: "ok", Type: model.DataType{Class: model.TypeBool}},
		{Name: "free", Type: model.DataType{Class: model.TypeDecimal}},
	}
	when := time.Date(2024, 2, 29, 23, 14, 15, 123456000, time.FixedZone("", 3600))
	in := []model.Row{
		{int64(-7), "héllo\n", model.Decimal("-12345678901234567.5"), when, when, when.Add(789), model.JSON(`{"a":[1,2]}`),
			[]byte{0, 255}, math.Inf(-1), true, model.Decimal("123456789012345678901234567890.000000001")},
		make(model.Row, len(cols)),
		{int64(0), "", model.Decimal("0"), time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC),
			time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), model.JSON(`null`), []byte{}, 0.0, false, model.Decimal("-0.1")},
	}
	var file bytes.Buffer
	if err := export.Write(&file, cols, in, export.Options{Format: export.Parquet}); err != nil {
		t.Fatal(err)
	}
	opt, err := Detect(bytes.NewReader(file.Bytes()), int64(file.Len()), "rows")
	if err != nil || opt.Format != Parquet {
		t.Fatalf("a file that opens with PAR1 is Parquet whatever it is called: %v, %v", opt.Format, err)
	}
	got, rows := readParquet(t, file.Bytes())
	for i, c := range cols {
		// A decimal of no stated precision goes out as its digits, in text.
		want := c.Type.Class
		if c.Name == "free" {
			want = model.TypeString
		}
		if got[i].Name != c.Name || got[i].Type.Class != want {
			t.Errorf("column %d went out %s, %v and came back %s, %v", i, c.Name, want, got[i].Name, got[i].Type.Class)
		}
	}
	want := []model.Row{
		{int64(-7), "héllo\n", model.Decimal("-12345678901234567.500"), time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC),
			time.Date(2024, 2, 29, 22, 14, 15, 123456000, time.UTC), time.Date(2024, 2, 29, 23, 14, 15, 123456789, time.UTC),
			model.JSON(`{"a":[1,2]}`), []byte{0, 255}, math.Inf(-1), true, "123456789012345678901234567890.000000001"},
		make(model.Row, len(cols)),
		{int64(0), "", model.Decimal("0.000"), time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC),
			time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), model.JSON(`null`), []byte{}, 0.0, false, "-0.1"},
	}
	if len(rows) != len(want) {
		t.Fatalf("%d rows came back of %d", len(rows), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(rows[i], want[i]) {
			t.Errorf("row %d came back\n%#v\nfor\n%#v", i+1, rows[i], want[i])
		}
	}
	if !got[4].Type.TimeZone || got[5].Type.TimeZone || got[4].Type.Precision != 6 || got[5].Type.Precision != 9 {
		t.Errorf("a moment and a wall clock stay what they were: %+v, %+v", got[4].Type, got[5].Type)
	}
	if got[2].Type.Precision != 20 || got[2].Type.Scale != 3 {
		t.Errorf("an exact number's digits and places: %+v", got[2].Type)
	}
}

func TestParquetRefusesATimeOfDayThatIsNotOne(t *testing.T) {
	type c struct {
		ID    int64 `parquet:"id"`
		Clock int64 `parquet:"clock,time(microsecond)"`
	}
	for _, bad := range []int64{-1, 86_400_000_000, math.MaxInt64} {
		file := parquetOf(t, c{1, 0}, c{2, bad})
		s, err := Open(bytes.NewReader(file), int64(len(file)), Options{Format: Parquet})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Next(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Next(context.Background()); err == nil || !strings.Contains(err.Error(), "row 2, clock") || !strings.Contains(err.Error(), "not a time of day") {
			t.Errorf("%d microseconds into a day: %v", bad, err)
		}
		s.Close()
	}
}

// A file another program wrote: ClickHouse 26.9, through the Arrow library,
// compressed with Zstandard. Three rows of
//
//	SELECT toInt64(number)-2 AS id, toInt32(number) AS small, toUInt64(18446744073709551615) AS big,
//	  if(number=1, NULL, concat('n', toString(number))) AS name, toDecimal64(number*1.5-2, 3) AS price,
//	  toDecimal128('-12345678901234567890.12345', 5) AS wide, toDate('2024-02-29')+number AS day,
//	  toDateTime64('2024-02-29 13:14:15.123456', 6, 'UTC') AS at,
//	  toDateTime64('1969-12-31 23:59:59.999', 3, 'UTC') AS early, [number, 7] AS list, map('k', number) AS m,
//	  toFloat32(0.5) AS f, number%2=0 AS ok, toUUID('123e4567-e89b-12d3-a456-426614174000') AS u,
//	  CAST((number, 'x') AS Tuple(a UInt64, b String)) AS tup FROM numbers(3)
func TestParquetReadsAFileClickHouseWrote(t *testing.T) {
	file, err := os.ReadFile(filepath.Join("testdata", "clickhouse.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	cols, rows := readParquet(t, file)
	var names []string
	for _, c := range cols {
		names = append(names, c.Name+":"+c.Type.Class.String())
	}
	if got := strings.Join(names, " "); got != "id:integer small:integer big:integer name:string price:decimal wide:decimal day:date "+
		"at:timestamp early:timestamp list:json m:json f:float ok:integer u:uuid tup:json" {
		t.Fatalf("the columns: %s", got)
	}
	want := []string{
		`-2 | 0 | 18446744073709551615 | n0 | -2.000 | -12345678901234567890.12345 | 2024-02-29T00:00:00Z | 2024-02-29T13:14:15.123456Z | 1969-12-31T23:59:59.999Z | [0,7] | {"k":0} | 0.5 | 1 | 123e4567-e89b-12d3-a456-426614174000 | {"a":0,"b":"x"} | `,
		`-1 | 1 | 18446744073709551615 |  | -0.500 | -12345678901234567890.12345 | 2024-03-01T00:00:00Z | 2024-02-29T13:14:15.123456Z | 1969-12-31T23:59:59.999Z | [1,7] | {"k":1} | 0.5 | 0 | 123e4567-e89b-12d3-a456-426614174000 | {"a":1,"b":"x"} | `,
		`0 | 2 | 18446744073709551615 | n2 | 1.000 | -12345678901234567890.12345 | 2024-03-02T00:00:00Z | 2024-02-29T13:14:15.123456Z | 1969-12-31T23:59:59.999Z | [2,7] | {"k":2} | 0.5 | 1 | 123e4567-e89b-12d3-a456-426614174000 | {"a":2,"b":"x"} | `,
	}
	if len(rows) != len(want) {
		t.Fatalf("%d rows of %d", len(rows), len(want))
	}
	for i, w := range want {
		if got := show(rows[i]); got != w {
			t.Errorf("row %d:\n%s\nfor\n%s", i+1, got, w)
		}
	}
	// Kept as what they are, and not only printed alike: the number past an
	// int64 exact, the absent name absent, the decimal its digits.
	if rows[0][2] != model.Decimal("18446744073709551615") || rows[1][3] != nil || rows[0][5] != model.Decimal("-12345678901234567890.12345") {
		t.Errorf("%#v, %#v, %#v", rows[0][2], rows[1][3], rows[0][5])
	}
}

func TestParquetReadsMoreRowsThanItHoldsAtOnce(t *testing.T) {
	type n struct {
		N int64 `parquet:"n"`
	}
	records := make([]n, 1000)
	for i := range records {
		records[i].N = int64(i)
	}
	_, rows := readParquet(t, parquetOf(t, records...))
	if len(rows) != len(records) {
		t.Fatalf("%d rows of %d", len(rows), len(records))
	}
	for i, r := range rows {
		if r[0] != int64(i) {
			t.Fatalf("row %d holds %v", i, r[0])
		}
	}
}

func TestParquetIsFoundAndRefused(t *testing.T) {
	type n struct {
		N int64 `parquet:"n"`
	}
	file := parquetOf(t, n{1})
	for name, w := range map[string]Format{"data.parquet": Parquet, "DATA.PARQUET": Parquet} {
		// By its name, where the bytes say nothing.
		if opt, err := Detect(strings.NewReader("a,b\n1,2\n"), 8, name); err != nil || opt.Format != w {
			t.Errorf("%s: %v, %v", name, opt.Format, err)
		}
	}
	if opt, _ := Detect(strings.NewReader("PAR,b\n1,2\n"), 10, "data.csv"); opt.Format != CSV {
		t.Errorf("three letters of four is not Parquet: %v", opt.Format)
	}
	if Parquet.String() != "Parquet" {
		t.Errorf("its name: %s", Parquet)
	}
	// Not a Parquet file, and one cut short: said, not guessed at.
	for _, bad := range [][]byte{[]byte("a,b\n1,2\n"), file[:len(file)-10], {}} {
		if _, err := Open(bytes.NewReader(bad), int64(len(bad)), Options{Format: Parquet}); err == nil || !strings.Contains(err.Error(), "not a Parquet file that can be read") {
			t.Errorf("%d bytes that are no Parquet file: %v", len(bad), err)
		}
	}
	// A file that describes no columns has no rows to give.
	var empty bytes.Buffer
	if err := parquet.NewGenericWriter[any](&empty, parquet.NewSchema("none", parquet.Group{})).Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(bytes.NewReader(empty.Bytes()), int64(empty.Len()), Options{Format: Parquet}); err == nil || !strings.Contains(err.Error(), "describes no columns") {
		t.Errorf("a file of no columns: %v", err)
	}
	// A file whose rows are damaged says so where it finds it, and does not
	// end early as though it were whole.
	damaged := append([]byte(nil), file...)
	for i := 8; i < 24; i++ {
		damaged[i] ^= 0xFF
	}
	s, err := Open(bytes.NewReader(damaged), int64(len(damaged)), Options{Format: Parquet})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Next(context.Background()); err == nil || errors.Is(err, io.EOF) || !strings.Contains(err.Error(), "after row 0 of this Parquet file") {
		t.Errorf("damaged rows: %v", err)
	}
	s.Close()
	s, err = Open(bytes.NewReader(file), int64(len(file)), Options{Format: Parquet})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("a read that was cancelled: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Error(err)
	}
}

func TestParquetPutsThePointWhereTheColumnSays(t *testing.T) {
	for _, c := range []struct {
		bytes []byte
		scale int32
		want  string
	}{
		{[]byte{0x00}, 0, "0"},
		{[]byte{0x00}, 3, "0.000"},
		{[]byte{0x05}, 3, "0.005"},
		{[]byte{0xFB}, 3, "-0.005"},
		{[]byte{0x04, 0xD2}, 1, "123.4"},
		{[]byte{0x04, 0xD2}, 4, "0.1234"},
		{[]byte{0x80}, 0, "-128"},
		{[]byte{0x7F}, 0, "127"},
		{[]byte{0xFF, 0xFF, 0xFF}, 2, "-0.01"},
		{nil, 2, "0.00"},
		{[]byte{0x04, 0xD2}, -2, "1234"},
	} {
		if got := scaledBy(twosComplement(c.bytes), c.scale); got != c.want {
			t.Errorf("% x with %d places is %s, not %s", c.bytes, c.scale, c.want, got)
		}
	}
	for _, c := range []struct {
		n    int64
		unit time.Duration
		want string
	}{
		{0, time.Millisecond, "1970-01-01T00:00:00Z"},
		{-1, time.Millisecond, "1969-12-31T23:59:59.999Z"},
		{-1000, time.Millisecond, "1969-12-31T23:59:59Z"},
		{1500, time.Millisecond, "1970-01-01T00:00:01.5Z"},
		{-1, time.Nanosecond, "1969-12-31T23:59:59.999999999Z"},
		{253402300799999999, time.Microsecond, "9999-12-31T23:59:59.999999Z"},
		{-62135596800000000, time.Microsecond, "0001-01-01T00:00:00Z"},
	} {
		if got := moment(c.n, c.unit).Format(time.RFC3339Nano); got != c.want {
			t.Errorf("%d of %v since 1970 is %s, not %s", c.n, c.unit, c.want, got)
		}
	}
}
