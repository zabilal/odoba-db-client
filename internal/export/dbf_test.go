package export

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// table is a DBF file taken apart: its header's figures, its columns and its
// records as the bytes they are.
type table struct {
	version, codepage byte
	count             uint32
	headerLen, recLen int
	fields            []field
	records           []string
	end               byte
}

func parse(t *testing.T, raw string) table {
	t.Helper()
	b := []byte(raw)
	if len(b) < 33 {
		t.Fatalf("%d bytes is not a DBF file", len(b))
	}
	tb := table{version: b[0], codepage: b[29], count: binary.LittleEndian.Uint32(b[4:]),
		headerLen: int(binary.LittleEndian.Uint16(b[8:])), recLen: int(binary.LittleEndian.Uint16(b[10:]))}
	at := 32
	for b[at] != 0x0D {
		tb.fields = append(tb.fields, field{name: string(bytes.TrimRight(b[at:at+11], "\x00")), kind: b[at+11],
			length: int(b[at+16]), decimals: int(b[at+17])})
		at += 32
	}
	if at+1 != tb.headerLen {
		t.Fatalf("the header says it is %d bytes and is %d", tb.headerLen, at+1)
	}
	body := b[tb.headerLen:]
	for i := uint32(0); i < tb.count; i++ {
		tb.records = append(tb.records, string(body[:tb.recLen]))
		body = body[tb.recLen:]
	}
	if len(body) != 1 {
		t.Fatalf("%d bytes after the last record", len(body))
	}
	tb.end = body[0]
	return tb
}

func TestDBFIsATableEveryReaderReads(t *testing.T) {
	cols := []model.ColumnDef{
		col("id", model.TypeInteger, false), col("Full Name", model.TypeString, false), col("price", model.TypeDecimal, false),
		col("born", model.TypeDate, false), col("ok", model.TypeBool, false), col("seen_at", model.TypeTimestamp, true),
	}
	day := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	got := parse(t, export(t, &rows{cols: cols, data: []model.Row{
		{int64(7), "Ada", model.Decimal("12.5"), day, true, at},
		{int64(-1200), "Grace Hopper", model.Decimal("3"), nil, false, nil},
		{nil, nil, model.Decimal("0.125"), day, nil, at},
	}}, Options{Format: DBF}))

	if got.version != 0x03 || got.count != 3 || got.end != 0x1A || got.codepage != 0 {
		t.Errorf("dBase III, three records, an end mark, plain text: %+v", got)
	}
	want := []field{
		{"ID", 'N', 5, 0},         // as wide as its widest, the sign counted
		{"FULL_NAME", 'C', 12, 0}, // a name a header can hold
		{"PRICE", 'N', 6, 3},      // the widest whole part and the most places, which are two rows'
		{"BORN", 'D', 8, 0},
		{"OK", 'L', 1, 0},
		{"SEEN_AT", 'C', 20, 0}, // no time of day in dBase III: written as text
	}
	if len(got.fields) != len(want) {
		t.Fatalf("the columns are %+v", got.fields)
	}
	for i, f := range got.fields {
		if f != want[i] {
			t.Errorf("column %d is %+v, want %+v", i+1, f, want[i])
		}
	}
	if got.recLen != 1+5+12+6+8+1+20 {
		t.Errorf("a record is %d bytes", got.recLen)
	}
	for i, want := range []string{
		" " + "    7" + "Ada         " + "12.500" + "20260304" + "T" + "2026-09-10T12:30:00Z",
		" " + "-1200" + "Grace Hopper" + " 3.000" + "        " + "F" + "                    ",
		" " + "     " + "            " + " 0.125" + "20260304" + "?" + "2026-09-10T12:30:00Z",
	} {
		if got.records[i] != want {
			t.Errorf("record %d is\n %q, want\n %q", i+1, got.records[i], want)
		}
	}
}

// A name is made to fit a header, and made different from the others where
// fitting made it the same.
func TestDBFNamesAreMadeToFit(t *testing.T) {
	names := func(given ...string) []string {
		cols := make([]model.ColumnDef, len(given))
		for i, g := range given {
			cols[i] = col(g, model.TypeString, false)
		}
		return dbfNames(cols)
	}
	for name, c := range map[string]struct{ given, want []string }{
		"as they are":          {[]string{"ID", "NAME_2"}, []string{"ID", "NAME_2"}},
		"in capitals":          {[]string{"id", "Name"}, []string{"ID", "NAME"}},
		"what is not a letter": {[]string{"first name", "a.b", "é"}, []string{"FIRST_NAME", "A_B", "_"}},
		"cut to ten":           {[]string{"a_very_long_column_name"}, []string{"A_VERY_LON"}},
		"cut to the same":      {[]string{"customer_name", "customer_number", "customer_note"}, []string{"CUSTOMER_N", "CUSTOMER_2", "CUSTOMER_3"}},
		"the same":             {[]string{"id", "ID", "Id"}, []string{"ID", "ID_2", "ID_3"}},
		"no name at all":       {[]string{"", "x", ""}, []string{"FIELD1", "X", "FIELD3"}},
		"a made name taken":    {[]string{"id", "id", "ID_2"}, []string{"ID", "ID_2", "ID_2_2"}},
	} {
		got := names(c.given...)
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: %q became %q, want %q", name, c.given, got, c.want)
				break
			}
		}
	}
	// Twelve of the same long name are still twelve names of ten.
	many := make([]string, 12)
	for i := range many {
		many[i] = "abcdefghijkl"
	}
	seen := map[string]bool{}
	for _, n := range names(many...) {
		if len(n) > 10 || seen[n] {
			t.Errorf("of twelve, %q", n)
		}
		seen[n] = true
	}
}

// What a number column holds decides whether it is one: a number DBF cannot
// write as a number is written as text with every digit, not as a number
// with some of them gone.
func TestDBFKeepsEveryDigitOrSaysItIsText(t *testing.T) {
	one := func(class model.TypeClass, vals ...any) field {
		data := make([]model.Row, len(vals))
		for i, v := range vals {
			data[i] = model.Row{v}
		}
		return parse(t, export(t, &rows{cols: []model.ColumnDef{col("v", class, false)}, data: data}, Options{Format: DBF})).fields[0]
	}
	for name, c := range map[string]struct {
		got  field
		want field
	}{
		"integers":                           {one(model.TypeInteger, int64(1), int64(-22)), field{"V", 'N', 3, 0}},
		"exact numbers":                      {one(model.TypeDecimal, model.Decimal("1.5"), model.Decimal("10.25")), field{"V", 'N', 5, 2}},
		"twenty characters":                  {one(model.TypeDecimal, model.Decimal("12345678901234567890")), field{"V", 'N', 20, 0}},
		"twenty-one":                         {one(model.TypeDecimal, model.Decimal("123456789012345678901")), field{"V", 'C', 21, 0}},
		"a float":                            {one(model.TypeFloat, 1.5), field{"V", 'N', 3, 1}},
		"a float too large to write plainly": {one(model.TypeFloat, 1e21), field{"V", 'C', 5, 0}},
		"not a number at all":                {one(model.TypeFloat, 1.5, math.NaN()), field{"V", 'C', 3, 0}},
		"an exponent":                        {one(model.TypeDecimal, model.Decimal("1e3")), field{"V", 'C', 3, 0}},
		"nothing but nothing":                {one(model.TypeInteger, nil, nil), field{"V", 'C', 1, 0}},
		"text that is digits":                {one(model.TypeString, "007"), field{"V", 'C', 3, 0}},
		"a date column holding text":         {one(model.TypeDate, "soon"), field{"V", 'C', 4, 0}},
		"a logical column holding text":      {one(model.TypeBool, true, "maybe"), field{"V", 'C', 5, 0}},
		"a date column holding yes and no":   {one(model.TypeDate, true), field{"V", 'C', 1, 0}},
		"a logical column holding a date":    {one(model.TypeBool, time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)), field{"V", 'C', 20, 0}},
		"bytes":                              {one(model.TypeBytes, []byte{0xde, 0xad}), field{"V", 'C', 6, 0}},
		"a document":                         {one(model.TypeJSON, model.JSON(`{"a":1}`)), field{"V", 'C', 7, 0}},
	} {
		if c.got != c.want {
			t.Errorf("%s: %+v, want %+v", name, c.got, c.want)
		}
	}
	// Fitted together: the longest whole part and the most places are two
	// values', and the column is wide enough for both at once.
	got := parse(t, export(t, &rows{cols: []model.ColumnDef{col("v", model.TypeDecimal, false)},
		data: []model.Row{{model.Decimal("123456")}, {model.Decimal("0.123")}}}, Options{Format: DBF}))
	if got.fields[0] != (field{"V", 'N', 10, 3}) || got.records[0] != " 123456.000" || got.records[1] != "      0.123" {
		t.Errorf("fitted: %+v %q", got.fields[0], got.records)
	}
}

// Text is written in the code page a reader will assume, where all of it can
// be, and the reader is told; where some of it cannot, it is written as it
// is.
func TestDBFSaysHowItsTextIsWritten(t *testing.T) {
	cols := []model.ColumnDef{col("v", model.TypeString, false)}
	plain := parse(t, export(t, &rows{cols: cols, data: []model.Row{{"cafe"}}}, Options{Format: DBF}))
	if plain.codepage != 0 || plain.records[0] != " cafe" {
		t.Errorf("plain text: %+v", plain)
	}
	latin := parse(t, export(t, &rows{cols: cols, data: []model.Row{{"café"}, {"€5 – “ok”"}}}, Options{Format: DBF}))
	if latin.codepage != 0x03 || latin.fields[0].length != 9 || latin.records[0] != " caf\xe9     " || latin.records[1] != " \x805 \x96 \x93ok\x94" {
		t.Errorf("text Windows-1252 has: page %#x, %+v, %q", latin.codepage, latin.fields[0], latin.records)
	}
	// A character Latin-1 has and Windows-1252 has not — a control from the
	// range it gave to quotation marks and the euro — is one it cannot write.
	control := parse(t, export(t, &rows{cols: cols, data: []model.Row{{"caf\u00e9"}, {"next\u0085line"}}}, Options{Format: DBF}))
	if control.codepage != 0 || control.records[0] != " caf\u00e9     " {
		t.Errorf("text with a control in it: page %#x, %q", control.codepage, control.records)
	}
	wide := parse(t, export(t, &rows{cols: cols, data: []model.Row{{"café"}, {"日本"}}}, Options{Format: DBF}))
	if wide.codepage != 0 || wide.fields[0].length != 6 || wide.records[0] != " café " || wide.records[1] != " 日本" {
		t.Errorf("text it has not: page %#x, %+v, %q", wide.codepage, wide.fields[0], wide.records)
	}
}

func TestDBFRefusesWhatItCannotHold(t *testing.T) {
	cols := []model.ColumnDef{col("id", model.TypeInteger, false), col("notes", model.TypeString, false)}
	write := func(src *rows) error {
		_, err := Copy(context.Background(), &bytes.Buffer{}, src, Options{Format: DBF}, nil)
		return err
	}
	long := strings.Repeat("x", 255)
	if err := write(&rows{cols: cols, data: []model.Row{{int64(1), "short"}, {int64(2), long}}}); err == nil ||
		!strings.Contains(err.Error(), "notes") || !strings.Contains(err.Error(), "255 bytes") || !strings.Contains(err.Error(), "nothing was written") {
		t.Errorf("text too long for a field: %v", err)
	}
	if err := write(&rows{cols: cols, data: []model.Row{{int64(1), strings.Repeat("x", 254)}}}); err != nil {
		t.Errorf("text as long as a field holds: %v", err)
	}
	// As long as a field holds in the bytes it is written in, which for
	// accented text is fewer than it takes here.
	if err := write(&rows{cols: cols, data: []model.Row{{int64(1), strings.Repeat("é", 254)}}}); err != nil {
		t.Errorf("254 accented letters: %v", err)
	}
	if err := write(&rows{}); err == nil || !strings.Contains(err.Error(), "at least one column") {
		t.Errorf("no columns: %v", err)
	}
	many := make([]model.ColumnDef, 256)
	for i := range many {
		many[i] = col("c", model.TypeString, false)
	}
	if err := write(&rows{cols: many}); err == nil || !strings.Contains(err.Error(), "at most 255 columns") {
		t.Errorf("too many columns: %v", err)
	}
	wide := make([]model.ColumnDef, 255)
	row := make(model.Row, 255)
	for i := range wide {
		wide[i], row[i] = col("c", model.TypeString, false), strings.Repeat("x", 254)
	}
	if err := write(&rows{cols: wide[:255], data: []model.Row{row}}); err != nil {
		t.Errorf("255 columns of 254: %v", err) // 64,771 bytes: inside what a record holds
	}
}

// A row shorter than its columns has nothing in the rest.
func TestDBFFillsAShortRowWithNothing(t *testing.T) {
	got := parse(t, export(t, &rows{cols: []model.ColumnDef{col("a", model.TypeString, false), col("b", model.TypeString, false)},
		data: []model.Row{{"x"}, {"y", "z"}}}, Options{Format: DBF}))
	if got.records[0] != " x " || got.records[1] != " yz" {
		t.Errorf("%q", got.records)
	}
}

// No rows is still a table: its columns, and nothing in them.
func TestDBFWithNoRows(t *testing.T) {
	got := parse(t, export(t, &rows{cols: []model.ColumnDef{col("id", model.TypeInteger, false), col("born", model.TypeDate, false)}}, Options{Format: DBF}))
	if got.count != 0 || len(got.fields) != 2 || got.fields[0] != (field{"ID", 'C', 1, 0}) || got.fields[1] != (field{"BORN", 'D', 8, 0}) || got.end != 0x1A {
		t.Errorf("%+v", got)
	}
}

// failing is rows that stop with an error.
type failing struct {
	rows
	after int
}

func (f *failing) Next(ctx context.Context) (model.Row, error) {
	if f.i >= f.after {
		return nil, errors.New("the connection dropped")
	}
	return f.rows.Next(ctx)
}

// The file the rows are kept in while a table is written is gone afterwards,
// however the writing ended.
func TestDBFLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	left := func() []string {
		t.Helper()
		got, err := filepath.Glob(filepath.Join(dir, "ikigai-dbf-*"))
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if os.TempDir() != dir {
		t.Skipf("temporary files go to %s here, not where the test can look", os.TempDir())
	}
	cols := []model.ColumnDef{col("v", model.TypeString, false)}
	data := []model.Row{{"a"}, {"b"}, {strings.Repeat("x", 300)}}

	export(t, &rows{cols: cols, data: data[:2]}, Options{Format: DBF})
	if got := left(); len(got) != 0 {
		t.Errorf("after a table was written: %v", got)
	}
	if _, err := Copy(context.Background(), &bytes.Buffer{}, &rows{cols: cols, data: data}, Options{Format: DBF}, nil); err == nil {
		t.Fatal("text too long was written")
	}
	if got := left(); len(got) != 0 {
		t.Errorf("after a table was refused: %v", got)
	}
	if _, err := Copy(context.Background(), &bytes.Buffer{}, &failing{rows: rows{cols: cols, data: data}, after: 1}, Options{Format: DBF}, nil); err == nil {
		t.Fatal("rows that failed were written")
	}
	if got := left(); len(got) != 0 {
		t.Errorf("after the rows stopped coming: %v", got)
	}
	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Copy(stopped, &bytes.Buffer{}, &rows{cols: cols, data: data[:2]}, Options{Format: DBF}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("a stopped export: %v", err)
	}
	if got := left(); len(got) != 0 {
		t.Errorf("after an export was stopped: %v", got)
	}
	if err := Write(&bytes.Buffer{}, cols, data, Options{Format: DBF}); err == nil {
		t.Fatal("text too long was written")
	}
	if got := left(); len(got) != 0 {
		t.Errorf("after a selection was refused: %v", got)
	}
}
