package export

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// unzipped is a workbook's parts, by name.
func unzipped(t *testing.T, data string) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(strings.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

// cellValue is the number a sheet's cell holds.
func cellValue(t *testing.T, sheet, ref string) float64 {
	t.Helper()
	m := regexp.MustCompile(`<c r="` + ref + `"[^>]*><v>([^<]*)</v>`).FindStringSubmatch(sheet)
	if m == nil {
		t.Fatalf("no number in %s", ref)
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestAWorkbookIsWrittenAsExcelReadsIt(t *testing.T) {
	parts := unzipped(t, export(t, sample(), Options{Format: XLSX, Header: true, Name: "items: all [2026]"}))
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/_rels/workbook.xml.rels", "xl/styles.xml", "xl/worksheets/sheet1.xml"} {
		if _, ok := parts[name]; !ok {
			t.Errorf("no part %s", name)
		}
	}
	if !strings.Contains(parts["xl/workbook.xml"], `<sheet name="items_ all _2026_" sheetId="1" r:id="rId1"/>`) {
		t.Errorf("the sheet named as Excel takes it: %s", parts["xl/workbook.xml"])
	}
	sheet := parts["xl/worksheets/sheet1.xml"]
	for _, want := range []string{
		`<pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/>`,
		`<c r="A1" s="4" t="inlineStr"><is><t xml:space="preserve">id</t></is></c>`,
		`<c r="K1" s="4" t="inlineStr"><is><t xml:space="preserve">id</t></is></c>`,
		`<c r="A2"><v>1</v></c>`,
		`<c r="B2" t="inlineStr"><is><t xml:space="preserve">say "hi", &lt;then&gt; go` + "\n" + `now</t></is></c>`,
		`<c r="C2" t="inlineStr"><is><t xml:space="preserve">12345678901234567890.123456789</t></is></c>`,
		`<c r="D2" t="inlineStr"><is><t xml:space="preserve">NaN</t></is></c>`,
		`<c r="G2" t="inlineStr"><is><t xml:space="preserve">\xdead</t></is></c>`,
		`<c r="K2"><v>2</v></c>`,
	} {
		if !strings.Contains(sheet, want) {
			t.Errorf("no %s in\n%s", want, sheet)
		}
	}
	if got := cellValue(t, sheet, "E2"); math.Abs(got-(46275+12.5/24)) > 1e-9 || !strings.Contains(sheet, `<c r="E2" s="2">`) {
		t.Errorf("an instant is its days and their fraction, shown as a timestamp: %v", got)
	}
	if got := cellValue(t, sheet, "F2"); got != 46275 || !strings.Contains(sheet, `<c r="F2" s="1">`) {
		t.Errorf("a date is its days, shown as a date: %v", got)
	}
	if strings.Contains(sheet, `r="J2"`) {
		t.Error("NULL is an empty cell")
	}
	if !strings.HasSuffix(sheet, "</row></sheetData></worksheet>") {
		t.Error("the sheet ends as XML does")
	}
	if !strings.Contains(parts["xl/styles.xml"], `formatCode="yyyy-mm-dd"`) || !strings.Contains(parts["xl/_rels/workbook.xml.rels"], `Target="worksheets/sheet1.xml"`) {
		t.Error("the styles and the sheet are named")
	}
	if wb := unzipped(t, export(t, sample(), Options{Format: XLSX, Name: `R&D "q"`}))["xl/workbook.xml"]; !strings.Contains(wb, `name="R&amp;D &quot;q&quot;"`) {
		t.Errorf("a sheet's name escaped as an attribute: %s", wb)
	}
	plain := unzipped(t, export(t, sample(), Options{Format: XLSX}))["xl/worksheets/sheet1.xml"]
	if strings.Contains(plain, "<pane") || !strings.Contains(plain, `<c r="A1"><v>1</v></c>`) {
		t.Errorf("without a header the rows start at the first: %s", plain[:200])
	}
}

func TestEachValueIsWrittenAsExcelKeepsIt(t *testing.T) {
	cases := []struct {
		v    any
		col  model.ColumnDef
		want string
	}{
		{true, col("b", model.TypeBool, false), `<c r="A1" t="b"><v>1</v></c>`},
		{false, col("b", model.TypeBool, false), `<c r="A1" t="b"><v>0</v></c>`},
		{int64(123456789012345), col("n", model.TypeInteger, false), `<c r="A1"><v>123456789012345</v></c>`},
		{int64(1234567890123456), col("n", model.TypeInteger, false), `<c r="A1" t="inlineStr"><is><t xml:space="preserve">1234567890123456</t></is></c>`},
		{int64(12000000000000000), col("n", model.TypeInteger, false), `<c r="A1"><v>12000000000000000</v></c>`},
		{model.Decimal("0.000000000000001"), col("d", model.TypeDecimal, false), `<c r="A1"><v>0.000000000000001</v></c>`},
		{model.Decimal("1.23456789012345e10"), col("d", model.TypeDecimal, false), `<c r="A1"><v>1.23456789012345e10</v></c>`},
		{model.Decimal("1e400"), col("d", model.TypeDecimal, false), `<c r="A1" t="inlineStr"><is><t xml:space="preserve">1e400</t></is></c>`},
		{model.Decimal("NaN"), col("d", model.TypeDecimal, false), `<c r="A1" t="inlineStr"><is><t xml:space="preserve">NaN</t></is></c>`},
		{1.5, col("f", model.TypeFloat, false), `<c r="A1"><v>1.5</v></c>`},
		{math.Inf(-1), col("f", model.TypeFloat, false), `<c r="A1" t="inlineStr"><is><t xml:space="preserve">-Infinity</t></is></c>`},
		{at, col("t", model.TypeTime, false), `<c r="A1" s="3"><v>0.5208333333333334</v></c>`},
		{at, col("t", model.TypeTime, true), `<c r="A1" t="inlineStr"><is><t xml:space="preserve">12:30:00Z</t></is></c>`},
		{time.Date(1900, 2, 28, 0, 0, 0, 0, time.UTC), col("d", model.TypeDate, false), `<c r="A1" t="inlineStr"><is><t xml:space="preserve">1900-02-28</t></is></c>`},
		{time.Date(1900, 3, 1, 0, 0, 0, 0, time.UTC), col("d", model.TypeDate, false), `<c r="A1" s="1"><v>61</v></c>`},
		{time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), col("d", model.TypeDate, false), `<c r="A1" t="inlineStr"><is><t xml:space="preserve">10000-01-01</t></is></c>`},
		{time.Date(2026, 9, 10, 14, 30, 0, 0, time.FixedZone("", 2*3600)), col("t", model.TypeTimestamp, false), `<c r="A1" s="2"><v>46275.604166666664</v></c>`},
		{time.Date(2026, 9, 10, 14, 30, 0, 0, time.FixedZone("", 2*3600)), col("t", model.TypeTimestamp, true), `<c r="A1" s="2"><v>46275.520833333336</v></c>`},
		{model.JSON(`{"a":1}`), col("j", model.TypeJSON, false), `<c r="A1" t="inlineStr"><is><t xml:space="preserve">{"a":1}</t></is></c>`},
		{nil, col("x", model.TypeString, false), `<row r="1"></row>`},
	}
	for _, c := range cases {
		out := export(t, &rows{cols: []model.ColumnDef{c.col}, data: []model.Row{{c.v}}}, Options{Format: XLSX})
		if sheet := unzipped(t, out)["xl/worksheets/sheet1.xml"]; !strings.Contains(sheet, c.want) {
			t.Errorf("%v as %v: want %s in %s", c.v, c.col.Type.Class, c.want, sheet[strings.Index(sheet, "<sheetData>"):])
		}
	}
}

func TestTextIsKeptWholeInACell(t *testing.T) {
	if got := string(appendXString(nil, "a&\x01b\r\nc_x0041_d\ufffe\te_x12_f_x0041xg_xZZZZ_")); got != "a&amp;_x0001_b_x000D_\nc_x005F_x0041_d_xFFFE_\te_x12_f_x0041xg_xZZZZ_" {
		t.Errorf("%q", got)
	}
	if excelChars("aé😀") != 4 {
		t.Error("Excel counts a character past the BMP as two")
	}
}

func TestWhatAWorkbookCannotHoldIsRefused(t *testing.T) {
	note := []model.ColumnDef{col("note", model.TypeString, false)}
	copyTo := func(src model.RowStream, opt Options) error {
		_, err := Copy(context.Background(), &bytes.Buffer{}, src, opt, nil)
		return err
	}
	long := strings.Repeat("é", cellChars)
	if err := copyTo(&rows{cols: note, data: []model.Row{{long}}}, Options{Format: XLSX}); err != nil {
		t.Errorf("a cell as full as it goes: %v", err)
	}
	err := copyTo(&rows{cols: note, data: []model.Row{{"ok"}, {long + "é"}}}, Options{Format: XLSX})
	if err == nil || !strings.Contains(err.Error(), "sheet row 2, column note: 32768 characters, more than the 32767 an Excel cell holds") {
		t.Errorf("a cell too long: %v", err)
	}
	defer func(n int) { sheetRows = n }(sheetRows)
	sheetRows = 2
	if err := copyTo(&rows{cols: note, data: []model.Row{{"a"}}}, Options{Format: XLSX, Header: true}); err != nil {
		t.Errorf("a sheet as full as it goes: %v", err)
	}
	if err := copyTo(&rows{cols: note, data: []model.Row{{"a"}, {"b"}}}, Options{Format: XLSX, Header: true}); err == nil || !strings.Contains(err.Error(), "more rows than an Excel sheet holds (2)") {
		t.Errorf("a sheet too long: %v", err)
	}
	wide := make([]model.ColumnDef, sheetCols+1)
	if err := copyTo(&rows{cols: wide}, Options{Format: XLSX}); err == nil || !strings.Contains(err.Error(), "16385 columns") {
		t.Errorf("a sheet too wide: %v", err)
	}
	if err := copyTo(&rows{cols: wide[:sheetCols]}, Options{Format: XLSX}); err != nil {
		t.Errorf("a sheet as wide as it goes: %v", err)
	}
}

func TestColumnsAndSheetsAreNamedAsExcelNamesThem(t *testing.T) {
	for i, want := range map[int]string{0: "A", 25: "Z", 26: "AA", 701: "ZZ", 702: "AAA", 16383: "XFD"} {
		if got := columnName(i); got != want {
			t.Errorf("%d: %s", i, got)
		}
	}
	for in, want := range map[string]string{"": "Sheet1", "  ": "Sheet1", "a/b*c\\d": "a_b_c_d", "'q'": "q", strings.Repeat("x", 40): strings.Repeat("x", 31), "tab\there": "tab_here",
		"'" + strings.Repeat("x", 40): strings.Repeat("x", 31), strings.Repeat("x", 30) + "'y": strings.Repeat("x", 30)} {
		if got := sheetName(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
	if Formats()[2] != XLSX || XLSX.String() != "Excel" || XLSX.Extension() != "xlsx" {
		t.Error("Excel is offered after TSV, as xlsx")
	}
}
