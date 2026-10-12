package transfer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/export"
	"github.com/ikigai-db/ikigai-db/internal/model"
)

func yamlRows(t *testing.T, text string) ([]model.ColumnDef, []model.Row) {
	t.Helper()
	cols, rows, err := readAll(t, []byte(text), Options{Format: YAML})
	if err != nil {
		t.Fatalf("reading\n%s\n%v", text, err)
	}
	return cols, rows
}

// A list of mappings is read as rows, each value as what it is.
func TestYAMLKeepsItsTypes(t *testing.T) {
	cols, rows := yamlRows(t, `# what was exported
- id: 1
  price: 12.50
  name: "o'brien"
  plain: just words
  ok: true
  nothing: null
  tilde: ~
  doc: {a: [1, 2]}
  list:
    - x
    - 2
  bytes: !!binary 3q0=
  when: 2026-03-04
  quoted: "007"
- id: 9007199254740993
  price: 1e3
  name: |
    two
    lines
  hex: 0x20000000000001
  inf: .inf
  folded: !!binary |
    3q2+
    7w==
`)
	want := []string{"id", "price", "name", "plain", "ok", "nothing", "tilde", "doc", "list", "bytes", "when", "quoted", "hex", "inf", "folded"}
	if !reflect.DeepEqual(names(cols), want) {
		t.Fatalf("the columns are %q", names(cols))
	}
	first := model.Row{model.Decimal("1"), model.Decimal("12.50"), "o'brien", "just words", true, nil, nil,
		model.JSON(`{"a":[1,2]}`), model.JSON(`["x",2]`), []byte{0xde, 0xad}, "2026-03-04", "007", nil, nil, nil}
	if !reflect.DeepEqual(rows[0], first) {
		t.Errorf("the first row is %#v", rows[0])
	}
	// Every digit of a number too long for a float, a number written another
	// way as the number it is, and infinity as itself.
	second := rows[1]
	if second[0] != model.Decimal("9007199254740993") || second[1] != model.Decimal("1e3") || second[2] != "two\nlines\n" ||
		second[12] != model.Decimal("9007199254740993") { // every digit of it, which a float would not keep
		t.Errorf("the second row is %#v", second)
	}
	// Bytes written over several lines are the bytes, without the line ends.
	if !reflect.DeepEqual(second[14], []byte{0xde, 0xad, 0xbe, 0xef}) {
		t.Errorf("bytes over two lines are %#v", second[14])
	}
	if f, ok := second[13].(float64); !ok || !math.IsInf(f, 1) {
		t.Errorf("infinity is %#v", second[13])
	}
	classes := map[string]model.TypeClass{}
	for _, c := range cols {
		classes[c.Name] = c.Type.Class
		if !c.Type.Nullable {
			t.Errorf("%s cannot be empty", c.Name)
		}
	}
	for name, want := range map[string]model.TypeClass{
		"id": model.TypeDecimal, "name": model.TypeString, "ok": model.TypeBool, "nothing": model.TypeUnknown,
		"doc": model.TypeJSON, "bytes": model.TypeBytes, "inf": model.TypeFloat, "quoted": model.TypeString,
	} {
		if classes[name] != want {
			t.Errorf("%s is a %v, want %v", name, classes[name], want)
		}
	}
}

// What export writes is read back as it was, every kind of value.
func TestYAMLReadsBackWhatExportWrites(t *testing.T) {
	cols := []model.ColumnDef{
		{Name: "id", Type: model.DataType{Class: model.TypeInteger}}, {Name: "price", Type: model.DataType{Class: model.TypeDecimal}},
		{Name: "doc", Type: model.DataType{Class: model.TypeJSON}}, {Name: "name", Type: model.DataType{Class: model.TypeString}},
		{Name: "raw", Type: model.DataType{Class: model.TypeBytes}}, {Name: "f", Type: model.DataType{Class: model.TypeFloat}},
		{Name: "at", Type: model.DataType{Class: model.TypeTimestamp, TimeZone: true}}, {Name: "no", Type: model.DataType{Class: model.TypeString}},
		{Name: "ok", Type: model.DataType{Class: model.TypeBool}},
	}
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	rows := []model.Row{
		{int64(1), model.Decimal("12.50"), model.JSON(`{"k": [1, 2]}`), "o'brien, \"jr\"\nsecond: line\n- third", []byte{0, 0xff}, 3.0, at, "no", true},
		{int64(2), nil, nil, "", nil, math.Inf(-1), nil, "1.0", false},
	}
	var b bytes.Buffer
	if err := export.Write(&b, cols, rows, export.Options{Format: export.YAML}); err != nil {
		t.Fatal(err)
	}
	got, read, err := readAll(t, b.Bytes(), Options{Format: YAML})
	if err != nil || !reflect.DeepEqual(names(got), []string{"id", "price", "doc", "name", "raw", "f", "at", "no", "ok"}) {
		t.Fatalf("%q, %v\n%s", names(got), err, b.String())
	}
	want := []model.Row{
		{model.Decimal("1"), model.Decimal("12.50"), model.JSON(`{"k":[1,2]}`), rows[0][3], []byte{0, 0xff}, model.Decimal("3.0"), "2026-03-04T05:06:07Z", "no", true},
		{model.Decimal("2"), nil, nil, "", nil, math.Inf(-1), nil, "1.0", false},
	}
	if !reflect.DeepEqual(read, want) {
		t.Errorf("\n got %#v\nwant %#v\nfrom\n%s", read, want, b.String())
	}
	// And a result with no rows reads back as no rows.
	b.Reset()
	if err := export.Write(&b, cols, nil, export.Options{Format: export.YAML}); err != nil {
		t.Fatal(err)
	}
	if got, read, err := readAll(t, b.Bytes(), Options{Format: YAML}); err != nil || len(got) != 0 || len(read) != 0 {
		t.Errorf("no rows read back as %v, %v, %v", got, read, err)
	}
}

// The ways a list is written, each read as the same two records.
func TestYAMLIsReadHoweverItIsLaidOut(t *testing.T) {
	want := []model.Row{{model.Decimal("1"), "x"}, {model.Decimal("2"), "y"}}
	for name, text := range map[string]string{
		"a list":                                  "- a: 1\n  b: x\n- a: 2\n  b: y\n",
		"with a document mark":                    "---\n- a: 1\n  b: x\n- a: 2\n  b: y\n",
		"with a directive":                        "%YAML 1.2\n---\n- a: 1\n  b: x\n- a: 2\n  b: y\n",
		"with comments and gaps":                  "# first\n\n- a: 1\n  b: x\n\n# between\n- a: 2\n  b: y\n# after\n",
		"the dash on a line of its own":           "-\n  a: 1\n  b: x\n-\n  a: 2\n  b: y\n",
		"in windows lines":                        "- a: 1\r\n  b: x\r\n- a: 2\r\n  b: y\r\n",
		"with no last line ending":                "- a: 1\n  b: x\n- a: 2\n  b: y",
		"each a flow mapping":                     "- {a: 1, b: x}\n- {a: 2, b: y}\n",
		"in flow style, as JSON":                  `[{"a": 1, "b": "x"}, {"a": 2, "b": "y"}]`,
		"in flow style over lines":                "[\n  {a: 1, b: x},\n  {a: 2, b: y}\n]\n",
		"a stream of documents":                   "a: 1\nb: x\n---\na: 2\nb: y\n",
		"a stream, each begun and ended":          "---\na: 1\nb: x\n...\n---\na: 2\nb: y\n...\n",
		"a stream in windows lines":               "a: 1\r\nb: x\r\n---\r\na: 2\r\nb: y\r\n",
		"a stream with gaps":                      "a: 1\nb: x\n\n---\n\n---\na: 2\nb: y\n\n",
		"a list inside a record":                  "- a: 1\n  b: x\n  c:\n  - not a record\n  - nor this\n- a: 2\n  b: y\n",
		"text over lines that begins with dashes": "- a: 1\n  b: x\n  c: |\n    - not a record\n    --- nor a document\n- a: 2\n  b: y\n",
	} {
		_, rows := yamlRows(t, text)
		if len(rows) != 2 {
			t.Errorf("%s: %d records: %#v", name, len(rows), rows)
			continue
		}
		for i, r := range rows {
			if !reflect.DeepEqual(r[:2], want[i]) {
				t.Errorf("%s: record %d is %#v", name, i+1, r)
			}
		}
	}
	// One mapping alone is one record.
	if _, rows := yamlRows(t, "a: 1\nb: x\n"); len(rows) != 1 || !reflect.DeepEqual(rows[0], want[0]) {
		t.Errorf("one mapping: %#v", rows)
	}
	for name, text := range map[string]string{"nothing": "", "only comments": "# nothing here\n\n", "an empty list": "[]\n", "only a mark": "---\n"} {
		if cols, rows := yamlRows(t, text); len(cols) != 0 || len(rows) != 0 {
			t.Errorf("%s: %v, %v", name, cols, rows)
		}
	}
}

// A record is numbered as the record it is, whatever lay between it and the
// last: a gap is not a record.
func TestYAMLNumbersRecordsAndNotGaps(t *testing.T) {
	_, _, err := readAll(t, []byte("a: 1\n---\n\n---\n# nothing\n---\njust text\n"), Options{Format: YAML})
	if err == nil || !strings.Contains(err.Error(), "record 2 is not a mapping") {
		t.Errorf("the second record, after two gaps: %v", err)
	}
	// And so is one that cannot be read at all.
	_, _, err = readAll(t, []byte("a: 1\n---\n\n---\n# nothing\n---\nb: [left open\n"), Options{Format: YAML})
	if err == nil || !strings.Contains(err.Error(), "record 2: ") {
		t.Errorf("the second record, unreadable, after two gaps: %v", err)
	}
	// An empty pair of brackets is a list of no records.
	if _, _, err := readAll(t, []byte("[ ]"), Options{Format: YAML}); err != nil {
		t.Errorf("an empty list: %v", err)
	}
}

func TestYAMLThatIsNotRecordsIsRefused(t *testing.T) {
	for name, c := range map[string]struct{ text, says string }{
		"a list of values":           {"- 1\n- 2\n", "record 1 is not a mapping"},
		"a list of lists":            {"- [1, 2]\n", "record 1 is not a mapping"},
		"a value alone":              {"just text\n", "record 1 is not a mapping"},
		"a flow list of values":      {"[1, 2]", "record 1 is not a mapping"},
		"a flow mapping, not a list": {"[", "transfer:"},
		"a key that is a list":       {"- [a, b]: 1\n", "a key that is not a name"},
		"a key twice":                {"- a: 1\n  a: 2\n", `record 1 has the key "a" twice`},
		"bytes that are not":         {"- a: !!binary \"***\"\n", "record 1, a: is marked as bytes and is not base64"},
		"a record left open":         {"- a: 1\n- b: [1, 2\n", "record 2"},
		"tabs for indentation":       {"- a: 1\n\tb: 2\n", "record 1"},
	} {
		_, _, err := readAll(t, []byte(c.text), Options{Format: YAML})
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v, want it to mention %q", name, err, c.says)
		}
	}
}

// The columns are the keys of the records read ahead, in the order first
// seen; a record without one has nothing there, and one with a key they lack,
// after them, is an error naming it rather than a value dropped.
func TestYAMLColumnsAreTheKeysReadAhead(t *testing.T) {
	cols, rows := yamlRows(t, "- a: 1\n- b: x\n  a: 2\n- c: true\n")
	if !reflect.DeepEqual(names(cols), []string{"a", "b", "c"}) ||
		!reflect.DeepEqual(rows, []model.Row{{model.Decimal("1"), nil, nil}, {model.Decimal("2"), "x", nil}, {nil, nil, true}}) {
		t.Errorf("%q %#v", names(cols), rows)
	}
	// Nothing says nothing of a column: one that is empty and then a number is
	// a column of numbers.
	cols, _ = yamlRows(t, "- a: null\n- a: 1\n- a: ~\n")
	if cols[0].Type.Class != model.TypeDecimal {
		t.Errorf("empty, then a number: a %v", cols[0].Type.Class)
	}
	// A column of numbers and text is text, each value kept as it was.
	cols, rows = yamlRows(t, "- a: 1\n- a: x\n")
	if cols[0].Type.Class != model.TypeString || rows[0][0] != model.Decimal("1") || rows[1][0] != "x" {
		t.Errorf("mixed: %v %#v", cols[0].Type.Class, rows)
	}
	var b strings.Builder
	for i := 0; i < sniffRecords; i++ {
		fmt.Fprintf(&b, "- a: %d\n", i)
	}
	b.WriteString("- a: 1\n  late: x\n")
	_, read, err := readAll(t, []byte(b.String()), Options{Format: YAML})
	if err == nil || len(read) != sniffRecords || !strings.Contains(err.Error(), `"late"`) ||
		!strings.Contains(err.Error(), fmt.Sprintf("record %d", sniffRecords+1)) {
		t.Errorf("a late key: %d rows, %v", len(read), err)
	}
}

// A list is read a record at a time: a record that is wrong is found when it
// is reached, after the ones before it were handed over.
func TestYAMLIsReadARecordAtATime(t *testing.T) {
	var b strings.Builder
	for i := 0; i < sniffRecords+5; i++ {
		fmt.Fprintf(&b, "- a: %d\n", i)
	}
	b.WriteString("- a: [left open\n")
	data := []byte(b.String())
	rs, err := Open(bytes.NewReader(data), int64(len(data)), Options{Format: YAML})
	if err != nil {
		t.Fatalf("opening a file whose last record is wrong: %v", err)
	}
	defer rs.Close()
	n := 0
	for {
		if _, err = rs.Next(context.Background()); err != nil {
			break
		}
		n++
	}
	if n != sniffRecords+5 || errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), fmt.Sprintf("record %d", sniffRecords+6)) {
		t.Errorf("%d records, then %v", n, err)
	}
	// And a read that is stopped stops.
	rs2, _ := Open(bytes.NewReader(data), int64(len(data)), Options{Format: YAML})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := rs2.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("a stopped read: %v", err)
	}
	if rs2.Close() != nil {
		t.Error("closing failed")
	}
}

func TestYAMLIsToldByItsNameOrItsFirstLine(t *testing.T) {
	for name, c := range map[string]struct {
		file, text string
		want       Format
	}{
		"by .yaml":             {"people.yaml", "- a: 1\n", YAML},
		"by .yml":              {"people.YML", "- a: 1\n", YAML},
		"by a document mark":   {"people.txt", "---\n- a: 1\n", YAML},
		"by a directive":       {"people", "%YAML 1.2\n---\n- a: 1\n", YAML},
		"a dash in a CSV file": {"people.csv", "- a,b\n1,2\n", CSV},
		"JSON still":           {"people.json", "[{\"a\":1}]", JSON},
	} {
		opt, err := Detect(strings.NewReader(c.text), int64(len(c.text)), c.file)
		if err != nil || opt.Format != c.want {
			t.Errorf("%s: %v, %v", name, opt.Format, err)
		}
	}
	if YAML.String() != "YAML" {
		t.Errorf("it is called %q", YAML.String())
	}
}

// YAML in another encoding is read as text is.
func TestYAMLIsReadInItsEncoding(t *testing.T) {
	text := "- name: caf\xe9\n"
	cols, rows, err := readAll(t, []byte(text), Options{Format: YAML, Encoding: Windows1252})
	if err != nil || len(cols) != 1 || len(rows) != 1 || rows[0][0] != "café" {
		t.Errorf("%v %#v %v", cols, rows, err)
	}
}

// A name given to a value is good within its record, and not beyond it: a
// record is read alone, and one that points at something in another is refused
// with what it pointed at rather than read as nothing.
func TestYAMLAliasesReachNoFurtherThanTheirRecord(t *testing.T) {
	_, rows := yamlRows(t, "- a: &x 1\n  b: *x\n")
	if len(rows) != 1 || rows[0][0] != model.Decimal("1") || rows[0][1] != model.Decimal("1") {
		t.Errorf("an alias within a record: %#v", rows)
	}
	_, _, err := readAll(t, []byte("- a: &x 1\n- a: *x\n"), Options{Format: YAML})
	if err == nil || !strings.Contains(err.Error(), "record 2") || !strings.Contains(err.Error(), "unknown anchor 'x'") {
		t.Errorf("an alias to another record's value: %v", err)
	}
}
