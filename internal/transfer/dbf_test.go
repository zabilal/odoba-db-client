package transfer

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/export"
	"github.com/ikigai-db/ikigai-db/internal/model"
)

// col is a column of a DBF file being put together by hand.
type dbfCol struct {
	name     string
	kind     byte
	length   int
	decimals int
}

// dbfFile puts a DBF file together: a header for the columns, and the records
// as they are given, each already the bytes it is. The count the header gives
// is the records', unless said.
func dbfFile(cols []dbfCol, records ...string) []byte {
	return dbfFileOf(0x03, uint32(len(records)), cols, records...)
}

func dbfFileOf(version byte, count uint32, cols []dbfCol, records ...string) []byte {
	width := 1
	for _, c := range cols {
		width += c.length
	}
	head := make([]byte, 32)
	head[0] = version
	binary.LittleEndian.PutUint32(head[4:], count)
	binary.LittleEndian.PutUint16(head[8:], uint16(32+32*len(cols)+1))
	binary.LittleEndian.PutUint16(head[10:], uint16(width))
	var b bytes.Buffer
	b.Write(head)
	for _, c := range cols {
		desc := make([]byte, 32)
		copy(desc, c.name)
		desc[11], desc[16], desc[17] = c.kind, byte(c.length), byte(c.decimals)
		if c.kind == 'C' {
			desc[16], desc[17] = byte(c.length), byte(c.length>>8)
		}
		b.Write(desc)
	}
	b.WriteByte(0x0D)
	for _, r := range records {
		b.WriteString(r)
	}
	b.WriteByte(0x1A)
	return b.Bytes()
}

func dbfRows(t *testing.T, data []byte, opt Options) ([]model.ColumnDef, []model.Row) {
	t.Helper()
	opt.Format = DBF
	cols, rows, err := readAll(t, data, opt)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	return cols, rows
}

var people = []dbfCol{{"ID", 'N', 4, 0}, {"NAME", 'C', 8, 0}, {"PRICE", 'N', 7, 2}, {"BORN", 'D', 8, 0}, {"OK", 'L', 1, 0}, {"RATIO", 'F', 6, 3}}

// A table is read as its columns and its records, each value as what it is.
func TestDBFIsReadAsWhatItHolds(t *testing.T) {
	cols, rows := dbfRows(t, dbfFile(people,
		" "+"   7"+"Ada     "+"  12.50"+"20260304"+"T"+" 0.125",
		" "+"-120"+"Grace H."+"   3.00"+"        "+"f"+"      ",
		" "+"    "+"        "+"       "+"19991231"+"?"+"-1.500",
		" "+"  42"+" padded "+"*******"+"20000229"+"Y"+"     .",
	), Options{})
	if !reflect.DeepEqual(names(cols), []string{"ID", "NAME", "PRICE", "BORN", "OK", "RATIO"}) {
		t.Fatalf("the columns are %q", names(cols))
	}
	day := func(y, m, d int) time.Time { return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC) }
	want := []model.Row{
		{model.Decimal("7"), "Ada", model.Decimal("12.50"), day(2026, 3, 4), true, model.Decimal("0.125")},
		{model.Decimal("-120"), "Grace H.", model.Decimal("3.00"), nil, false, nil},
		{nil, nil, nil, day(1999, 12, 31), nil, model.Decimal("-1.500")},
		// Text keeps the space it begins with and loses what it was padded
		// with; a number that did not fit its field is no number.
		{model.Decimal("42"), " padded", nil, day(2000, 2, 29), true, nil},
	}
	if !reflect.DeepEqual(rows, want) {
		for i := range rows {
			if i < len(want) && !reflect.DeepEqual(rows[i], want[i]) {
				t.Errorf("record %d is %#v\n want %#v", i+1, rows[i], want[i])
			}
		}
		if len(rows) != len(want) {
			t.Errorf("%d records", len(rows))
		}
	}
	types := map[string]model.DataType{}
	for _, c := range cols {
		types[c.Name] = c.Type
		if !c.Type.Nullable {
			t.Errorf("%s cannot be empty", c.Name)
		}
	}
	if p := types["PRICE"]; p.Class != model.TypeDecimal || p.Precision != 7 || p.Scale != 2 || p.Native != "N" {
		t.Errorf("PRICE is %+v", p)
	}
	if n := types["NAME"]; n.Class != model.TypeString || n.Length != 8 {
		t.Errorf("NAME is %+v", n)
	}
	if types["BORN"].Class != model.TypeDate || types["OK"].Class != model.TypeBool || types["RATIO"].Class != model.TypeDecimal {
		t.Errorf("the classes are %+v", types)
	}
}

// A record somebody deleted is still in the file, and is not a row; the
// records are read until the count the header gave, the mark that ends the
// file, or its end, whichever comes first.
func TestDBFReadsTheRecordsThatAreThere(t *testing.T) {
	cols := []dbfCol{{"N", 'N', 2, 0}}
	_, rows := dbfRows(t, dbfFile(cols, "  1", "* 2", "  3"), Options{})
	if !reflect.DeepEqual(rows, []model.Row{{model.Decimal("1")}, {model.Decimal("3")}}) {
		t.Errorf("with one deleted: %#v", rows)
	}
	// A header that counts more than there are.
	_, rows = dbfRows(t, dbfFileOf(0x03, 9, cols, "  1", "  2"), Options{})
	if len(rows) != 2 {
		t.Errorf("a count of nine and two records: %d rows", len(rows))
	}
	// And fewer: what lies past the count is not the table's.
	_, rows = dbfRows(t, dbfFileOf(0x03, 1, cols, "  1", "  2"), Options{})
	if len(rows) != 1 {
		t.Errorf("a count of one and two records: %d rows", len(rows))
	}
	// A file with no end mark at all.
	raw := dbfFile(cols, "  1", "  2")
	_, rows = dbfRows(t, raw[:len(raw)-1], Options{})
	if len(rows) != 2 {
		t.Errorf("with no end mark: %d rows", len(rows))
	}
	_, rows = dbfRows(t, dbfFile(cols), Options{})
	if len(rows) != 0 {
		t.Errorf("an empty table: %d rows", len(rows))
	}
	// Only a star says a record is deleted: one marked some other way is a
	// record, which is how a writer careless of the mark writes them all.
	_, rows = dbfRows(t, dbfFile(cols, "\x00 1", "x 2", "* 3"), Options{})
	if len(rows) != 2 {
		t.Errorf("two records marked oddly and one deleted: %d rows", len(rows))
	}
	// A record cut short is said to be, by its number, counting the deleted.
	raw = dbfFile(cols, "  1", "* 2", "  3")
	_, read, err := readAll(t, raw[:len(raw)-2], Options{Format: DBF})
	if err == nil || len(read) != 1 || !strings.Contains(err.Error(), "record 3 of this DBF file is cut short") {
		t.Errorf("a file cut short: %d rows, %v", len(read), err)
	}
}

// The types FoxPro added, and the field it keeps for itself.
func TestDBFReadsFoxProsTypes(t *testing.T) {
	le32 := func(v uint32) string { b := make([]byte, 4); binary.LittleEndian.PutUint32(b, v); return string(b) }
	le64 := func(v uint64) string { b := make([]byte, 8); binary.LittleEndian.PutUint64(b, v); return string(b) }
	cols := []dbfCol{{"I", 'I', 4, 0}, {"B", 'B', 8, 0}, {"Y", 'Y', 8, 4}, {"T", 'T', 8, 0}, {"_NullFlags", '0', 1, 0}, {"C", 'C', 2, 0}}
	const julian2026March4 = 2461104
	_, rows := dbfRows(t, dbfFileOf(0x30, 2, cols,
		" "+le32(uint32(0xFFFFFFF9))+le64(math.Float64bits(1.5))+le64(uint64(125000))+le32(julian2026March4)+le32(5*3600*1000+6*60*1000+7*1000+8)+"\x00"+"ab",
		" "+le32(7)+le64(math.Float64bits(-0.25))+le64(^uint64(0)-4)+le32(0)+le32(0)+"\x01"+"  ",
	), Options{})
	want := []model.Row{
		{int64(-7), 1.5, model.Decimal("12.5000"), time.Date(2026, 3, 4, 5, 6, 7, 8e6, time.UTC), "ab"},
		{int64(7), -0.25, model.Decimal("-0.0005"), nil, nil},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("\n got %#v\nwant %#v", rows, want)
	}
}

// Yes and no are said six ways each, and not said two.
func TestDBFReadsEveryWayOfSayingYes(t *testing.T) {
	cols := []dbfCol{{"OK", 'L', 1, 0}}
	for mark, want := range map[string]any{"T": true, "t": true, "Y": true, "y": true, "F": false, "f": false, "N": false, "n": false, "?": nil, " ": nil} {
		if _, rows := dbfRows(t, dbfFile(cols, " "+mark), Options{}); rows[0][0] != want {
			t.Errorf("%q is %#v", mark, rows[0][0])
		}
	}
	// A name a writer padded with spaces instead of noughts is the name.
	c, _ := dbfRows(t, dbfFile([]dbfCol{{"ID   ", 'C', 1, 0}}, " x"), Options{})
	if c[0].Name != "ID" {
		t.Errorf("a padded name is %q", c[0].Name)
	}
}

// Text is UTF-8 where it reads as UTF-8 and Windows-1252 otherwise, unless
// somebody said; and a text column wider than a byte can say is as wide as it
// is.
func TestDBFReadsItsText(t *testing.T) {
	cols := []dbfCol{{"V", 'C', 6, 0}}
	_, rows := dbfRows(t, dbfFile(cols, " caf\xe9  ", " café ", " \x80 5   "), Options{})
	if rows[0][0] != "café" || rows[1][0] != "café" || rows[2][0] != "€ 5" {
		t.Errorf("found as it is: %#v", rows)
	}
	// Said to be Windows-1252, bytes that happen to be UTF-8 are read as said.
	_, rows = dbfRows(t, dbfFile(cols, " café "), Options{Encoding: Windows1252})
	if rows[0][0] != "cafÃ©" {
		t.Errorf("said to be Windows-1252: %#v", rows[0][0])
	}
	long := strings.Repeat("x", 300)
	c, rows := dbfRows(t, dbfFile([]dbfCol{{"V", 'C', 300, 0}, {"N", 'N', 1, 0}}, " "+long+"7"), Options{})
	if rows[0][0] != long || rows[0][1] != model.Decimal("7") || c[0].Type.Length != 300 {
		t.Errorf("a text column of 300: %d characters, then %#v", len(rows[0][0].(string)), rows[0][1])
	}
	// A name the header gives twice, or not at all, is still a column's.
	c, _ = dbfRows(t, dbfFile([]dbfCol{{"A", 'C', 1, 0}, {"A", 'C', 1, 0}, {"", 'C', 1, 0}}, " xyz"), Options{})
	if !reflect.DeepEqual(names(c), []string{"A", "A 2", "column 3"}) {
		t.Errorf("the names are %q", names(c))
	}
}

func TestDBFThatCannotBeReadIsRefused(t *testing.T) {
	ok := []dbfCol{{"N", 'N', 2, 0}}
	for name, c := range map[string]struct {
		data []byte
		says string
	}{
		"too short":                     {[]byte("not a dbf"), "shorter than a DBF file's header"},
		"a header that is not one":      {bytes.Repeat([]byte{0}, 64), "does not describe one"},
		"a header longer than the file": {dbfFile(ok)[:40], "does not describe one"},
		"no columns":                    {dbfFile(nil), "describes no columns"},
		"a memo":                        {dbfFile([]dbfCol{{"ID", 'N', 2, 0}, {"NOTES", 'M', 10, 0}}, " 1         1"), "NOTES of this DBF file is a memo"},
		"a picture":                     {dbfFile([]dbfCol{{"PIC", 'P', 10, 0}}), "is a memo"},
		"a type nobody knows":           {dbfFile([]dbfCol{{"X", 'Q', 2, 0}}), `of a type, "Q", that is not read`},
		"only the null flags":           {dbfFile([]dbfCol{{"_NullFlags", '0', 1, 0}}), "describes no columns"},
	} {
		_, _, err := readAll(t, c.data, Options{Format: DBF})
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v, want it to mention %q", name, err, c.says)
		}
	}
	// Columns wider than the records they are in.
	wide := dbfFile(ok, "  1")
	binary.LittleEndian.PutUint16(wide[10:], 2)
	if _, _, err := readAll(t, wide, Options{Format: DBF}); err == nil || !strings.Contains(err.Error(), "3 bytes wide and its records 2") {
		t.Errorf("columns wider than a record: %v", err)
	}
	for name, c := range map[string]struct {
		cols   []dbfCol
		record string
		says   string
	}{
		"a number that is not":    {[]dbfCol{{"N", 'N', 3, 0}}, " 1x2", `record 1, N: "1x2" is not a number`},
		"a date that is not":      {[]dbfCol{{"D", 'D', 8, 0}}, " 20261340", `record 1, D: "20261340" is not a date`},
		"an integer of two bytes": {[]dbfCol{{"I", 'I', 2, 0}}, " ab", "record 1, I: an integer of 2 bytes"},
		"a double of four":        {[]dbfCol{{"B", 'B', 4, 0}}, " abcd", "a double of 4 bytes"},
		"a currency of four":      {[]dbfCol{{"Y", 'Y', 4, 0}}, " abcd", "a currency of 4 bytes"},
		"a time of four":          {[]dbfCol{{"T", 'T', 4, 0}}, " abcd", "a time of 4 bytes"},
	} {
		_, _, err := readAll(t, dbfFile(c.cols, c.record), Options{Format: DBF})
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v, want it to mention %q", name, err, c.says)
		}
	}
}

// What export writes is read back: every value DBF can hold as it was, and
// what it cannot as the nearest thing that reads back.
func TestDBFReadsBackWhatExportWrites(t *testing.T) {
	cols := []model.ColumnDef{
		{Name: "id", Type: model.DataType{Class: model.TypeInteger}}, {Name: "price", Type: model.DataType{Class: model.TypeDecimal}},
		{Name: "full name", Type: model.DataType{Class: model.TypeString}}, {Name: "born", Type: model.DataType{Class: model.TypeDate}},
		{Name: "ok", Type: model.DataType{Class: model.TypeBool}}, {Name: "big", Type: model.DataType{Class: model.TypeDecimal}},
	}
	day := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	rows := []model.Row{
		{int64(1), model.Decimal("12.50"), "Zoë – “quoted”", day, true, model.Decimal("123456789012345678901234567890")},
		{int64(-2), nil, nil, nil, nil, nil},
		{nil, model.Decimal("0.125"), " lead", day, false, model.Decimal("1")},
	}
	var b bytes.Buffer
	if err := export.Write(&b, cols, rows, export.Options{Format: export.DBF}); err != nil {
		t.Fatal(err)
	}
	opt, err := Detect(bytes.NewReader(b.Bytes()), int64(b.Len()), "people.DBF")
	if err != nil || opt.Format != DBF {
		t.Fatalf("told as %v, %v", opt.Format, err)
	}
	got, read, err := readAll(t, b.Bytes(), opt)
	if err != nil || !reflect.DeepEqual(names(got), []string{"ID", "PRICE", "FULL_NAME", "BORN", "OK", "BIG"}) {
		t.Fatalf("%q, %v", names(got), err)
	}
	want := []model.Row{
		{model.Decimal("1"), model.Decimal("12.500"), "Zoë – “quoted”", day, true, "123456789012345678901234567890"},
		{model.Decimal("-2"), nil, nil, nil, nil, nil},
		{nil, model.Decimal("0.125"), " lead", day, false, "1"},
	}
	if !reflect.DeepEqual(read, want) {
		t.Errorf("\n got %#v\nwant %#v", read, want)
	}
}

func TestDBFIsToldByItsName(t *testing.T) {
	data := dbfFile([]dbfCol{{"N", 'N', 2, 0}}, "  1")
	for name, want := range map[string]Format{"people.dbf": DBF, "PEOPLE.DBF": DBF, "people.csv": CSV} {
		opt, err := Detect(bytes.NewReader(data), int64(len(data)), name)
		if err != nil || opt.Format != want {
			t.Errorf("%s: %v, %v", name, opt.Format, err)
		}
	}
	if DBF.String() != "DBF" {
		t.Errorf("it is called %q", DBF.String())
	}
}

func TestDBFStopsWhenItIsToldTo(t *testing.T) {
	data := dbfFile([]dbfCol{{"N", 'N', 2, 0}}, "  1", "  2")
	rs, err := Open(bytes.NewReader(data), int64(len(data)), Options{Format: DBF})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := rs.Next(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := rs.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("a stopped read: %v", err)
	}
	if r, err := rs.Next(context.Background()); err != nil || r[0] != model.Decimal("2") {
		t.Errorf("and it had not moved on: %v, %v", r, err)
	}
	if _, err := rs.Next(context.Background()); !errors.Is(err, io.EOF) {
		t.Errorf("after the last: %v", err)
	}
	if rs.Close() != nil {
		t.Error("closing failed")
	}
}
