package export

import (
	"archive/zip"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// An Excel workbook (FR-10.1, ADR-0055): a zip of XML parts, written with the
// standard library alone and streamed, so memory stays flat — the parts that
// name the sheet first, then the sheet, a row as it comes. Strings go inline,
// not in a table of shared strings that would hold them all. A number is a
// number where Excel keeps its every digit, and its text where it would not;
// a date, a time of day or a timestamp is Excel's count of days, shown by a
// format; NULL is an empty cell. What a workbook cannot hold is refused, not
// cut short.

// sheetRows is the most rows a sheet holds, as Excel says; a variable, for
// tests.
var sheetRows = 1 << 20

const (
	sheetCols   = 1 << 14 // the most columns a sheet holds
	cellChars   = 32767   // the most characters a cell holds
	exactDigits = 15      // the significant digits Excel keeps of a number
)

// The cell styles styles.xml defines, by their place among its cellXfs.
const (
	styleDate     = 1
	styleDateTime = 2
	styleTime     = 3
	styleHeader   = 4
)

// excelEpoch is the day Excel counts days from, in its 1900 date system.
var excelEpoch = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)

type xlsx struct {
	zw    *zip.Writer
	sheet io.Writer
	cols  []model.ColumnDef
	refs  []string // each column's letters
	rows  int      // the sheet's rows so far, its header among them
	buf   []byte
}

func newXLSX(w io.Writer, cols []model.ColumnDef, opt Options) (*xlsx, error) {
	if len(cols) > sheetCols {
		return nil, fmt.Errorf("export: %d columns, more than an Excel sheet holds (%d): export as CSV to keep them all", len(cols), sheetCols)
	}
	x := &xlsx{zw: zip.NewWriter(w), cols: cols, refs: make([]string, len(cols))}
	for i := range cols {
		x.refs[i] = columnName(i)
	}
	for _, p := range []struct{ name, body string }{
		{"[Content_Types].xml", contentTypes},
		{"_rels/.rels", packageRels},
		{"xl/workbook.xml", fmt.Sprintf(workbookPart, attrEscaper.Replace(sheetName(opt.Name)))},
		{"xl/_rels/workbook.xml.rels", workbookRels},
		{"xl/styles.xml", stylesPart},
	} {
		f, err := x.zw.Create(p.name)
		if err != nil {
			return nil, err
		}
		if _, err := io.WriteString(f, p.body); err != nil {
			return nil, err
		}
	}
	sheet, err := x.zw.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		return nil, err
	}
	x.sheet = sheet
	head := xmlHeader + `<worksheet xmlns="` + mainNS + `">`
	if opt.Header {
		head += `<sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews>`
	}
	if _, err := io.WriteString(sheet, head+"<sheetData>"); err != nil {
		return nil, err
	}
	if opt.Header {
		names := make(model.Row, len(cols))
		for i, c := range cols {
			names[i] = c.Name
		}
		if err := x.write(names, true); err != nil {
			return nil, err
		}
	}
	return x, nil
}

func (x *xlsx) row(r model.Row) error { return x.write(r, false) }

// write writes one row of the sheet: its column names, bold, or its values.
func (x *xlsx) write(r model.Row, header bool) error {
	if x.rows >= sheetRows {
		return fmt.Errorf("export: more rows than an Excel sheet holds (%d): export as CSV to keep them all", sheetRows)
	}
	x.rows++
	n := strconv.Itoa(x.rows)
	b := append(x.buf[:0], `<row r="`...)
	b = append(b, n...)
	b = append(b, `">`...)
	for i, c := range x.cols {
		var v any
		if i < len(r) {
			v = r[i]
		}
		var err error
		if header {
			b, err = inline(b, x.refs[i]+n, c.Name, styleHeader)
		} else {
			b, err = cell(b, x.refs[i]+n, v, c)
		}
		if err != nil {
			return fmt.Errorf("export: sheet row %d, column %s: %w", x.rows, c.Name, err)
		}
	}
	x.buf = append(b, "</row>"...)
	_, err := x.sheet.Write(x.buf)
	return err
}

func (x *xlsx) end() error {
	if _, err := io.WriteString(x.sheet, "</sheetData></worksheet>"); err != nil {
		return err
	}
	return x.zw.Close()
}

// cell writes one value's cell at ref, as its type says; NULL is no cell.
func cell(b []byte, ref string, v any, col model.ColumnDef) ([]byte, error) {
	switch t := v.(type) {
	case nil:
		return b, nil
	case bool:
		s := "0"
		if t {
			s = "1"
		}
		return append(b, `<c r="`+ref+`" t="b"><v>`+s+`</v></c>`...), nil
	case int64:
		if s := strconv.FormatInt(t, 10); exact(s) {
			return number(b, ref, s, 0), nil
		}
	case float64:
		if !math.IsNaN(t) && !math.IsInf(t, 0) {
			return number(b, ref, strconv.FormatFloat(t, 'g', -1, 64), 0), nil
		}
	case model.Decimal:
		if s := string(t); isJSONNumber(s) && exact(s) {
			if _, err := strconv.ParseFloat(s, 64); err == nil {
				return number(b, ref, s, 0), nil
			}
		}
	case time.Time:
		if serial, style, ok := excelTime(t, col.Type); ok {
			return number(b, ref, serial, style), nil
		}
	}
	return inline(b, ref, text(v, col, ""), 0)
}

// number writes a cell of a number, in a style where given.
func number(b []byte, ref, v string, style int) []byte {
	b = append(b, `<c r="`...)
	b = append(b, ref...)
	if style > 0 {
		b = append(b, `" s="`...)
		b = strconv.AppendInt(b, int64(style), 10)
	}
	b = append(b, `"><v>`...)
	b = append(b, v...)
	return append(b, "</v></c>"...)
}

// inline writes a cell of text, kept in the cell rather than in a table of
// shared strings, with its spaces.
func inline(b []byte, ref, s string, style int) ([]byte, error) {
	if n := excelChars(s); n > cellChars {
		return b, fmt.Errorf("%d characters, more than the %d an Excel cell holds; export as CSV or JSON to keep it whole", n, cellChars)
	}
	b = append(b, `<c r="`...)
	b = append(b, ref...)
	if style > 0 {
		b = append(b, `" s="`...)
		b = strconv.AppendInt(b, int64(style), 10)
	}
	b = append(b, `" t="inlineStr"><is><t xml:space="preserve">`...)
	b = appendXString(b, s)
	return append(b, "</t></is></c>"...), nil
}

// excelChars counts text as Excel does, in UTF-16's units.
func excelChars(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r >= 0x10000 {
			n++
		}
	}
	return n
}

// appendXString writes text as a cell holds it: XML's escapes, and Excel's
// _xHHHH_ for a character XML cannot hold — a carriage return among them,
// which XML would read as a line feed — and for an underscore that would
// read as the start of one.
func appendXString(b []byte, s string) []byte {
	for i, r := range s {
		switch {
		case r == '&':
			b = append(b, "&amp;"...)
		case r == '<':
			b = append(b, "&lt;"...)
		case r == '>':
			b = append(b, "&gt;"...)
		case r == '_' && xEscape(s[i:]):
			b = append(b, "_x005F_"...)
		case r == '\t' || r == '\n':
			b = append(b, byte(r))
		case r < 0x20 || r == 0xFFFE || r == 0xFFFF:
			b = fmt.Appendf(b, "_x%04X_", r)
		default:
			b = utf8.AppendRune(b, r)
		}
	}
	return b
}

// xEscape reports whether text starts as Excel's _xHHHH_ escape does.
func xEscape(s string) bool {
	if len(s) < 7 || s[1] != 'x' || s[6] != '_' {
		return false
	}
	_, err := strconv.ParseUint(s[2:6], 16, 16)
	return err == nil
}

// exact reports whether Excel keeps every digit of a number: at most 15 of
// them, leading and trailing zeros aside.
func exact(s string) bool {
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		s = s[:i]
	}
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
	return len(strings.Trim(digits, "0")) <= exactDigits
}

// excelTime is a time as Excel counts it — days from its epoch, and their
// fraction — with the style that shows it: a date, a time of day, or a
// timestamp, an instant as UTC's wall clock, as the other formats write it.
// A time of day with its zone, and a day before March 1900, where Excel
// counts a 29 February that was not, are written as text instead.
func excelTime(t time.Time, dt model.DataType) (string, int, bool) {
	if dt.Class == model.TypeTime {
		if dt.TimeZone {
			return "", 0, false
		}
		h, m, s := t.Clock()
		secs := float64(h*3600+m*60+s) + float64(t.Nanosecond())/1e9
		return strconv.FormatFloat(secs/86400, 'g', -1, 64), styleTime, true
	}
	if dt.Class != model.TypeDate && (dt.Class != model.TypeTimestamp || dt.TimeZone) {
		t = t.UTC()
	}
	wall := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
	if wall.Before(time.Date(1900, 3, 1, 0, 0, 0, 0, time.UTC)) || wall.Year() > 9999 {
		return "", 0, false
	}
	secs := wall.Unix() - excelEpoch.Unix()
	days := secs / 86400
	if dt.Class == model.TypeDate {
		return strconv.FormatInt(days, 10), styleDate, true
	}
	frac := (float64(secs%86400) + float64(wall.Nanosecond())/1e9) / 86400
	return strconv.FormatFloat(float64(days)+frac, 'g', -1, 64), styleDateTime, true
}

// columnName is a column's letters, from 0: A, then Z, AA and on to XFD.
func columnName(i int) string {
	var b []byte
	for i++; i > 0; i = (i - 1) / 26 {
		b = append([]byte{byte('A' + (i-1)%26)}, b...)
	}
	return string(b)
}

// sheetName is a name Excel takes for a sheet: at most 31 characters, none
// of []:*?/\ or a control character, no apostrophe at either end, and
// Sheet1 for none.
func sheetName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`[]:*?/\`, r) {
			return '_'
		}
		return r
	}, s)
	if r := []rune(strings.Trim(s, "'")); len(r) > 31 {
		s = string(r[:31])
	}
	if s = strings.Trim(s, "'"); strings.TrimSpace(s) == "" {
		return "Sheet1"
	}
	return s
}

var attrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

const (
	xmlHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"
	mainNS    = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
	relNS     = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	pkgRelNS  = "http://schemas.openxmlformats.org/package/2006/relationships"
	typePre   = "application/vnd.openxmlformats-officedocument.spreadsheetml."
)

const contentTypes = xmlHeader + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
	`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
	`<Default Extension="xml" ContentType="application/xml"/>` +
	`<Override PartName="/xl/workbook.xml" ContentType="` + typePre + `sheet.main+xml"/>` +
	`<Override PartName="/xl/worksheets/sheet1.xml" ContentType="` + typePre + `worksheet+xml"/>` +
	`<Override PartName="/xl/styles.xml" ContentType="` + typePre + `styles+xml"/>` +
	`</Types>`

const packageRels = xmlHeader + `<Relationships xmlns="` + pkgRelNS + `">` +
	`<Relationship Id="rId1" Type="` + relNS + `/officeDocument" Target="xl/workbook.xml"/>` +
	`</Relationships>`

const workbookPart = xmlHeader + `<workbook xmlns="` + mainNS + `" xmlns:r="` + relNS + `">` +
	`<sheets><sheet name="%s" sheetId="1" r:id="rId1"/></sheets></workbook>`

const workbookRels = xmlHeader + `<Relationships xmlns="` + pkgRelNS + `">` +
	`<Relationship Id="rId1" Type="` + relNS + `/worksheet" Target="worksheets/sheet1.xml"/>` +
	`<Relationship Id="rId2" Type="` + relNS + `/styles" Target="styles.xml"/>` +
	`</Relationships>`

// stylesPart shows a date as 2006-01-02, a timestamp with its time of day,
// and a time of day alone, each as ISO writes it; and the header in bold.
const stylesPart = xmlHeader + `<styleSheet xmlns="` + mainNS + `">` +
	`<numFmts count="3"><numFmt numFmtId="164" formatCode="yyyy-mm-dd"/>` +
	`<numFmt numFmtId="165" formatCode="yyyy-mm-dd hh:mm:ss"/><numFmt numFmtId="166" formatCode="hh:mm:ss"/></numFmts>` +
	`<fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><name val="Calibri"/></font></fonts>` +
	`<fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills>` +
	`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>` +
	`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
	`<cellXfs count="5"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>` +
	`<xf numFmtId="164" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>` +
	`<xf numFmtId="165" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>` +
	`<xf numFmtId="166" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>` +
	`<xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/></cellXfs>` +
	`<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles></styleSheet>`
