package transfer

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/export"
	"github.com/ikigai-db/ikigai-db/internal/model"
)

// readAll opens data as rows, and reads them all.
func readAll(t *testing.T, data []byte, opt Options) ([]model.ColumnDef, []model.Row, error) {
	t.Helper()
	rs, err := Open(bytes.NewReader(data), int64(len(data)), opt)
	if err != nil {
		return nil, nil, err
	}
	defer rs.Close()
	var rows []model.Row
	for {
		r, err := rs.Next(context.Background())
		if errors.Is(err, io.EOF) {
			return rs.Columns(), rows, nil
		}
		if err != nil {
			return rs.Columns(), rows, err
		}
		rows = append(rows, r)
	}
}

func names(cols []model.ColumnDef) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Name
	}
	return out
}

func TestCSVIsReadAsExportWritesIt(t *testing.T) {
	cols, rows, err := readAll(t, []byte("\ufeffid,name,note\n1,\"a, b\",\n2,x\n"), Options{Format: CSV, Header: true})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names(cols), []string{"id", "name", "note"}) {
		t.Errorf("the header names the columns, its byte-order mark dropped: %q", names(cols))
	}
	want := []model.Row{{"1", "a, b", nil}, {"2", "x", nil}}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("an empty field is NULL, and a short row is padded with NULL: %v", rows)
	}
}

func TestAWiderRowIsAnErrorNamingItsLine(t *testing.T) {
	_, rows, err := readAll(t, []byte("a,b\n1,2\n1,2,3\n"), Options{Format: CSV, Header: true})
	if err == nil || !strings.Contains(err.Error(), "line 3 has 3 fields, the first had 2") || len(rows) != 1 {
		t.Errorf("%v after %d rows", err, len(rows))
	}
}

func TestColumnsAreNumberedWhereTheyHaveNoNames(t *testing.T) {
	cols, rows, err := readAll(t, []byte("1\tx\n2\ty\n"), Options{Format: TSV})
	if err != nil || !reflect.DeepEqual(names(cols), []string{"column 1", "column 2"}) || len(rows) != 2 || rows[0][1] != "x" {
		t.Errorf("without a header every line is a row: %q %v %v", names(cols), rows, err)
	}
	if _, rows, err := readAll(t, []byte("5\" screen,x\n"), Options{Format: CSV}); err != nil || !reflect.DeepEqual(rows, []model.Row{{`5" screen`, "x"}}) {
		t.Errorf("a quote inside a field is part of it: %v %v", rows, err)
	}
	cols, _, _ = readAll(t, []byte("a,,a\n"), Options{Format: CSV, Header: true})
	if !reflect.DeepEqual(names(cols), []string{"a", "column 2", "a 2"}) {
		t.Errorf("an empty name is numbered, a repeated one numbered after its first: %q", names(cols))
	}
}

func TestAnEmptyFileHasNoRows(t *testing.T) {
	for _, f := range []Format{CSV, TSV, JSON, NDJSON} {
		cols, rows, err := readAll(t, nil, Options{Format: f, Header: true})
		if err != nil || len(cols) != 0 || len(rows) != 0 {
			t.Errorf("%v: %v %v %v", f, cols, rows, err)
		}
	}
}

func TestJSONKeepsItsTypes(t *testing.T) {
	data := `[{"id": 1, "price": 12.50, "ok": true, "tags": ["a", {"b": 2}], "note": null, "name": "x", "code": 7, "n": null},
	          {"id": 2, "ok": false, "name": 3, "extra": "y", "code": "7a", "n": 5}]`
	cols, rows, err := readAll(t, []byte(data), Options{Format: JSON})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names(cols), []string{"id", "price", "ok", "tags", "note", "name", "code", "n", "extra"}) {
		t.Fatalf("every key of the records read ahead, in the order first seen: %q", names(cols))
	}
	classes := []model.TypeClass{model.TypeDecimal, model.TypeDecimal, model.TypeBool, model.TypeJSON, model.TypeUnknown,
		model.TypeString, model.TypeString, model.TypeDecimal, model.TypeString} // mixed is text, whichever came first; null says nothing
	for i, c := range cols {
		if c.Type.Class != classes[i] || !c.Type.Nullable {
			t.Errorf("%s is %v", c.Name, c.Type.Class)
		}
	}
	want := []model.Row{
		{model.Decimal("1"), model.Decimal("12.50"), true, model.JSON(`["a",{"b":2}]`), nil, "x", model.Decimal("7"), nil, nil},
		{model.Decimal("2"), nil, false, nil, nil, model.Decimal("3"), "7a", model.Decimal("5"), "y"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("digits kept, nested values as JSON, null as NULL:\n%v\n%v", rows, want)
	}
}

func TestNDJSONIsReadALineAtATime(t *testing.T) {
	_, rows, err := readAll(t, []byte("{\"a\":1}\n\n{\"a\":2}\n"), Options{Format: NDJSON})
	if err != nil || len(rows) != 2 || rows[1][0] != model.Decimal("2") {
		t.Errorf("%v %v", rows, err)
	}
}

func TestAKeyAfterTheRecordsReadAheadIsAnError(t *testing.T) {
	var b strings.Builder
	for i := range sniffRecords {
		fmt.Fprintf(&b, "{\"a\":%d}\n", i)
	}
	b.WriteString("{\"a\":0,\"late\":1}\n")
	_, rows, err := readAll(t, []byte(b.String()), Options{Format: NDJSON})
	if err == nil || !strings.Contains(err.Error(), `record 1001 has a key, "late", that the first 1000 records do not`) || len(rows) != sniffRecords {
		t.Errorf("%v after %d rows", err, len(rows))
	}
}

func TestJSONIsAnArrayOfObjects(t *testing.T) {
	if _, _, err := readAll(t, []byte(`{"a": 1}`), Options{Format: JSON}); err == nil || !strings.Contains(err.Error(), "array of objects") {
		t.Errorf("an object alone is not an array of them: %v", err)
	}
	if _, _, err := readAll(t, []byte(`[{"a": 1}, 2]`), Options{Format: JSON}); err == nil || !strings.Contains(err.Error(), "record 2 is not an object") {
		t.Errorf("an element that is not an object: %v", err)
	}
}

func TestWhatExportWritesIsReadBack(t *testing.T) {
	cols := []model.ColumnDef{
		{Name: "id", Type: model.DataType{Class: model.TypeInteger}}, {Name: "price", Type: model.DataType{Class: model.TypeDecimal}},
		{Name: "doc", Type: model.DataType{Class: model.TypeJSON}}, {Name: "name", Type: model.DataType{Class: model.TypeString}},
	}
	rows := []model.Row{{int64(1), model.Decimal("12.50"), model.JSON(`{"k": [1, 2]}`), "o'brien, \"jr\"\nsecond line"}, {int64(2), nil, nil, ""}}
	for _, c := range []struct {
		out  export.Format
		in   Format
		want []model.Row
	}{
		{export.JSON, JSON, []model.Row{{model.Decimal("1"), model.Decimal("12.50"), model.JSON(`{"k":[1,2]}`), rows[0][3]}, {model.Decimal("2"), nil, nil, ""}}},
		{export.NDJSON, NDJSON, []model.Row{{model.Decimal("1"), model.Decimal("12.50"), model.JSON(`{"k":[1,2]}`), rows[0][3]}, {model.Decimal("2"), nil, nil, ""}}},
		{export.CSV, CSV, []model.Row{{"1", "12.50", `{"k": [1, 2]}`, rows[0][3]}, {"2", nil, nil, nil}}}, // text; an empty string reads back as NULL
		{export.TSV, TSV, []model.Row{{"1", "12.50", `{"k": [1, 2]}`, rows[0][3]}, {"2", nil, nil, nil}}},
		{export.XLSX, XLSX, []model.Row{{model.Decimal("1"), model.Decimal("12.50"), `{"k": [1, 2]}`, rows[0][3]}, {model.Decimal("2"), nil, nil, ""}}},
	} {
		var b bytes.Buffer
		if err := export.Write(&b, cols, rows, export.Options{Format: c.out, Header: true}); err != nil {
			t.Fatal(err)
		}
		got, read, err := readAll(t, b.Bytes(), Options{Format: c.in, Header: true})
		if err != nil || !reflect.DeepEqual(names(got), []string{"id", "price", "doc", "name"}) || !reflect.DeepEqual(read, c.want) {
			t.Errorf("%v: %q %v %v", c.in, names(got), read, err)
		}
	}
}

func TestAStoppedReadEnds(t *testing.T) {
	data := []byte("a\n1\n")
	rs, err := Open(bytes.NewReader(data), int64(len(data)), Options{Format: CSV, Header: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := rs.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("a read stopped says so: %v", err)
	}
}

// workbook zips parts into an xlsx.
func workbook(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, body := range parts {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, body)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

const (
	mainNS = `xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	rels   = `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="worksheet" Target="worksheets/sheet1.xml"/>` +
		`<Relationship Id="rId2" Type="worksheet" Target="/xl/worksheets/sheet2.xml"/></Relationships>`
)

func book(t *testing.T, workbookPr, sheet1 string) []byte {
	return workbook(t, map[string]string{
		"xl/workbook.xml": `<workbook ` + mainNS + `>` + workbookPr + `<sheets><sheet name="Data" sheetId="1" r:id="rId1"/>` +
			`<sheet name="Other" sheetId="2" r:id="rId2"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": rels,
		"xl/sharedStrings.xml": `<sst ` + mainNS + `><si><t>name</t></si><si><t>n</t></si><si><t>when</t></si><si><t>ok</t></si>` +
			`<si><t>alpha</t></si><si><r><t>be</t></r><r><t>ta</t></r><rPh><t>left out</t></rPh></si></sst>`,
		"xl/styles.xml": `<styleSheet ` + mainNS + `><numFmts><numFmt numFmtId="164" formatCode="&quot;Day&quot; dd/mm/yyyy"/>` +
			`<numFmt numFmtId="165" formatCode="0.00&quot;h&quot;"/></numFmts>` +
			`<cellXfs><xf numFmtId="0"/><xf numFmtId="14"/><xf numFmtId="164"/><xf numFmtId="165"/></cellXfs></styleSheet>`,
		"xl/worksheets/sheet1.xml": sheet1,
		"xl/worksheets/sheet2.xml": `<worksheet ` + mainNS + `><sheetData><row r="1"><c r="B1"><v>7</v></c></row></sheetData></worksheet>`,
	})
}

const dataSheet = `<worksheet ` + mainNS + `><dimension ref="A1:E3"/><sheetData>` +
	`<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c><c r="C1" t="s"><v>2</v></c><c r="D1" t="s"><v>3</v></c></row>` +
	`<row r="2"><c r="A2" t="s"><v>4</v></c><c r="B2" s="3"><v>1.50</v></c><c r="C2" s="1"><v>44927</v></c><c r="D2" t="b"><v>1</v></c></row>` +
	`<row r="3"><c r="A3" t="inlineStr"><is><t>gamma</t></is></c><c r="C3" s="2"><v>44927.5</v></c><c r="D3" t="e"><v>#N/A</v></c><c r="E3" t="s"><v>5</v></c></row>` +
	`</sheetData></worksheet>`

func TestASheetIsRead(t *testing.T) {
	cols, rows, err := readAll(t, book(t, "", dataSheet), Options{Format: XLSX, Header: true})
	if err != nil {
		t.Fatal(err)
	}
	if cols[0].Type.Class != model.TypeUnknown || !cols[0].Type.Nullable {
		t.Errorf("a sheet's column may hold anything: %v", cols[0].Type)
	}
	if !reflect.DeepEqual(names(cols), []string{"name", "n", "when", "ok", "column 5"}) {
		t.Errorf("the first row names the columns, as wide as the sheet's dimension: %q", names(cols))
	}
	jan1 := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC) // 44927, as Excel counts from 1900
	want := []model.Row{
		{"alpha", model.Decimal("1.50"), jan1, true, nil},
		{"gamma", nil, jan1.Add(12 * time.Hour), "#N/A", "beta"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("strings, digits, dates by their format, booleans and errors, each in its column:\n%v\n%v", rows, want)
	}
}

func TestASheetIsPickedByName(t *testing.T) {
	cols, rows, err := readAll(t, book(t, "", dataSheet), Options{Format: XLSX, Sheet: "Other"})
	if err != nil || !reflect.DeepEqual(names(cols), []string{"column 1", "column 2"}) || !reflect.DeepEqual(rows, []model.Row{{nil, model.Decimal("7")}}) {
		t.Errorf("a sheet with no dimension is as wide as its first row: %q %v %v", names(cols), rows, err)
	}
	if _, _, err := readAll(t, book(t, "", dataSheet), Options{Format: XLSX, Sheet: "Nope"}); err == nil || !strings.Contains(err.Error(), `no sheet "Nope"`) {
		t.Errorf("a sheet the workbook has not: %v", err)
	}
}

func TestDatesCountFrom1904WhereTheWorkbookSays(t *testing.T) {
	sheet := `<worksheet ` + mainNS + `><sheetData><row r="1"><c r="A1" s="1"><v>0</v></c></row></sheetData></worksheet>`
	_, rows, err := readAll(t, book(t, `<workbookPr date1904="1"/>`, sheet), Options{Format: XLSX})
	if err != nil || len(rows) != 1 || !rows[0][0].(time.Time).Equal(time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("%v %v", rows, err)
	}
}

func TestACellPastTheSheetsWidthIsAnError(t *testing.T) {
	sheet := `<worksheet ` + mainNS + `><dimension ref="A1:B1"/><sheetData><row r="1"><c r="A1"><v>1</v></c></row>` +
		`<row r="2"><c r="C2"><v>2</v></c></row></sheetData></worksheet>`
	_, _, err := readAll(t, book(t, "", sheet), Options{Format: XLSX})
	if err == nil || !strings.Contains(err.Error(), "row 2 has a cell in column 3, past the last, 2") {
		t.Errorf("%v", err)
	}
}

func TestAFileThatIsNotAWorkbookIsRefused(t *testing.T) {
	if _, _, err := readAll(t, []byte("a,b\n"), Options{Format: XLSX}); err == nil || !strings.Contains(err.Error(), "not a workbook") {
		t.Errorf("%v", err)
	}
}

func TestADateFormatIsTold(t *testing.T) {
	for code, date := range map[string]bool{
		"yyyy-mm-dd": true, "h:mm": true, `"Day" dd`: true, `0.00"h"`: false, `[Red]0.00`: false, `0\h`: false, "General": false, "#,##0": false,
	} {
		if dateCode(code) != date {
			t.Errorf("%q: a date is %v", code, !date)
		}
	}
	if !builtInDate(14) || !builtInDate(22) || !builtInDate(47) || builtInDate(0) || builtInDate(23) {
		t.Error("the built-in date formats are 14 to 22 and 45 to 47")
	}
}

func TestAWorkbookIsReadBackWhole(t *testing.T) {
	cols := []model.ColumnDef{
		{Name: "note", Type: model.DataType{Class: model.TypeString}}, {Name: "day", Type: model.DataType{Class: model.TypeDate}},
		{Name: "at", Type: model.DataType{Class: model.TypeTimestamp, TimeZone: true}}, {Name: "ok", Type: model.DataType{Class: model.TypeBool}},
	}
	note := "a\x01b\r\nc_x0041_d  "
	day := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	at := time.Date(2026, 9, 10, 12, 30, 15, 250e6, time.UTC)
	var b bytes.Buffer
	if err := export.Write(&b, cols, []model.Row{{note, day, at, true}}, export.Options{Format: export.XLSX, Header: true}); err != nil {
		t.Fatal(err)
	}
	_, read, err := readAll(t, b.Bytes(), Options{Format: XLSX, Header: true})
	if err != nil || len(read) != 1 {
		t.Fatalf("%v %v", err, read)
	}
	r := read[0]
	if r[0] != note || !r[1].(time.Time).Equal(day) || !r[2].(time.Time).Equal(at) || r[3] != true {
		t.Errorf("text with what XML cannot hold, a date, an instant to the millisecond, a boolean: %q", r)
	}
}

func TestExcelsEscapesAreRead(t *testing.T) {
	if got := unescape("a_x0001_b_x005F_x0041_c_x12_d_xZZZZ_e_x0041xf"); got != "a\x01b_x0041_c_x12_d_xZZZZ_e_x0041xf" {
		t.Errorf("%q", got)
	}
}

func TestASharedStringsEscapesAreRead(t *testing.T) {
	data := workbook(t, map[string]string{
		"xl/workbook.xml":            `<workbook ` + mainNS + `><sheets><sheet name="Data" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": rels,
		"xl/sharedStrings.xml":       `<sst ` + mainNS + `><si><t>a_x0001_b_x005F_x0041_</t></si></sst>`,
		"xl/worksheets/sheet1.xml":   `<worksheet ` + mainNS + `><sheetData><row r="1"><c r="A1" t="s"><v>0</v></c></row></sheetData></worksheet>`,
	})
	if _, read, err := readAll(t, data, Options{Format: XLSX}); err != nil || len(read) != 1 || read[0][0] != "a\x01b_x0041_" {
		t.Errorf("a shared string's escapes: %v %q", err, read)
	}
}
