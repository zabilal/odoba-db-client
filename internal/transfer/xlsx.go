package transfer

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// A workbook (xlsx) is a zip of XML parts, read here with the standard
// library alone. Shared and inline strings are text; a number keeps its
// digits (model.Decimal), unless its cell's format is a date's, when it is
// the instant it counts, from 1900 or from 1904 where the workbook says so;
// a boolean is true or false; an error cell (#N/A) is its text. The sheet's
// rows are read from its XML as they are asked for.

type workbookXML struct {
	Pr struct {
		Date1904 string `xml:"date1904,attr"`
	} `xml:"workbookPr"`
	Sheets []struct {
		Name string `xml:"name,attr"`
		RID  string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
	} `xml:"sheets>sheet"`
}

type relsXML struct {
	Rels []struct {
		ID     string `xml:"Id,attr"`
		Target string `xml:"Target,attr"`
	} `xml:"Relationship"`
}

type stylesXML struct {
	Formats []struct {
		ID   int    `xml:"numFmtId,attr"`
		Code string `xml:"formatCode,attr"`
	} `xml:"numFmts>numFmt"`
	Xfs []struct {
		Format int `xml:"numFmtId,attr"`
	} `xml:"cellXfs>xf"`
}

// sheet is one sheet's rows.
type sheet struct {
	rc      io.ReadCloser
	dec     *xml.Decoder
	strings []string // the workbook's shared strings
	dates   []bool   // which cell styles are dates
	epoch   time.Time
	cols    []model.ColumnDef
	first   model.Row // the first row, when it is a row rather than names
	width   int
	row     int // the sheet's row number, for errors
}

func openSheet(r io.ReaderAt, size int64, opt Options) (*sheet, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("transfer: not a workbook: %w", err)
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[strings.TrimPrefix(f.Name, "/")] = f
	}
	var wb workbookXML
	if err := readXML(files, "xl/workbook.xml", &wb); err != nil {
		return nil, err
	}
	var rels relsXML
	if err := readXML(files, "xl/_rels/workbook.xml.rels", &rels); err != nil {
		return nil, err
	}
	target := ""
	for _, s := range wb.Sheets {
		if opt.Sheet == "" || s.Name == opt.Sheet {
			for _, rel := range rels.Rels {
				if rel.ID == s.RID {
					target = rel.Target
				}
			}
			break
		}
	}
	if target == "" {
		return nil, fmt.Errorf("transfer: the workbook has no sheet %q", opt.Sheet)
	}
	if !strings.HasPrefix(target, "/") {
		target = path.Join("xl", target)
	}
	sh := &sheet{epoch: time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)}
	if wb.Pr.Date1904 == "1" || wb.Pr.Date1904 == "true" {
		sh.epoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	if sh.strings, err = sharedStrings(files); err != nil {
		return nil, err
	}
	if sh.dates, err = dateStyles(files); err != nil {
		return nil, err
	}
	f := files[strings.TrimPrefix(target, "/")]
	if f == nil {
		return nil, fmt.Errorf("transfer: the workbook has no part %s", target)
	}
	if sh.rc, err = f.Open(); err != nil {
		return nil, err
	}
	sh.dec = xml.NewDecoder(sh.rc)
	first, err := sh.next()
	if err == io.EOF {
		return sh, nil // an empty sheet: no columns, no rows
	}
	if err != nil {
		sh.rc.Close()
		return nil, err
	}
	sh.width = max(sh.width, len(first))
	if opt.Header {
		names := make([]string, sh.width)
		for i, v := range first {
			if v != nil {
				names[i] = fmt.Sprint(v)
			}
		}
		sh.cols = named(names)
	} else {
		sh.cols, sh.first = named(make([]string, sh.width)), first
	}
	for i := range sh.cols {
		sh.cols[i].Type.Class = model.TypeUnknown // a sheet's column may hold anything
	}
	return sh, nil
}

func readXML(files map[string]*zip.File, name string, v any) error {
	f := files[name]
	if f == nil {
		return fmt.Errorf("transfer: the workbook has no part %s", name)
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	return xml.NewDecoder(rc).Decode(v)
}

// sharedStrings are the workbook's strings, each the text of its runs; a
// workbook with none has no part for them.
func sharedStrings(files map[string]*zip.File) ([]string, error) {
	f := files["xl/sharedStrings.xml"]
	if f == nil {
		return nil, nil
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var out []string
	dec := xml.NewDecoder(rc)
	for {
		t, err := dec.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if se, ok := t.(xml.StartElement); ok && se.Name.Local == "si" {
			s, err := text(dec, "si")
			if err != nil {
				return nil, err
			}
			out = append(out, s)
		}
	}
}

// text is the text of the <t> elements inside the element being read, up
// to its end, leaving out phonetic runs (<rPh>).
func text(dec *xml.Decoder, end string) (string, error) {
	var b strings.Builder
	inT, skip := false, 0
	for {
		t, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch x := t.(type) {
		case xml.StartElement:
			switch {
			case x.Name.Local == "rPh" || skip > 0:
				skip++
			case x.Name.Local == "t":
				inT = true
			}
		case xml.EndElement:
			switch {
			case skip > 0:
				skip--
			case x.Name.Local == "t":
				inT = false
			case x.Name.Local == end:
				return b.String(), nil
			}
		case xml.CharData:
			if inT && skip == 0 {
				b.Write(x)
			}
		}
	}
}

// dateStyles says which of the workbook's cell styles show a date or a
// time: a built-in date format, or one of its own with a date's letters.
func dateStyles(files map[string]*zip.File) ([]bool, error) {
	if files["xl/styles.xml"] == nil {
		return nil, nil
	}
	var st stylesXML
	if err := readXML(files, "xl/styles.xml", &st); err != nil {
		return nil, err
	}
	custom := map[int]string{}
	for _, f := range st.Formats {
		custom[f.ID] = f.Code
	}
	out := make([]bool, len(st.Xfs))
	for i, xf := range st.Xfs {
		code, ok := custom[xf.Format]
		out[i] = (!ok && builtInDate(xf.Format)) || (ok && dateCode(code))
	}
	return out, nil
}

// builtInDate reports whether one of Excel's built-in formats shows a date
// or a time.
func builtInDate(id int) bool {
	return (id >= 14 && id <= 22) || (id >= 45 && id <= 47)
}

// dateCode reports whether a format code shows a date or a time: a y, m, d,
// h or s outside its quoted text, its [brackets] and its escaped letters.
func dateCode(code string) bool {
	quoted, bracket := false, false
	for i := 0; i < len(code); i++ {
		switch c := code[i]; {
		case c == '"':
			quoted = !quoted
		case quoted:
		case c == '\\':
			i++
		case c == '[':
			bracket = true
		case c == ']':
			bracket = false
		case bracket:
		case strings.ContainsRune("ymdhsYMDHS", rune(c)):
			return true
		}
	}
	return false
}

func (sh *sheet) Columns() []model.ColumnDef { return sh.cols }

func (sh *sheet) Next(ctx context.Context) (model.Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r := sh.first; r != nil {
		sh.first = nil
		return pad(r, len(sh.cols)), nil
	}
	if sh.dec == nil || len(sh.cols) == 0 {
		return nil, io.EOF
	}
	r, err := sh.next()
	if err != nil {
		return nil, err
	}
	if len(r) > len(sh.cols) {
		return nil, fmt.Errorf("transfer: row %d has a cell in column %d, past the last, %d", sh.row, len(r), len(sh.cols))
	}
	return pad(r, len(sh.cols)), nil
}

func (sh *sheet) Close() error {
	if sh.rc == nil {
		return nil
	}
	return sh.rc.Close()
}

func pad(r model.Row, n int) model.Row {
	for len(r) < n {
		r = append(r, nil)
	}
	return r
}

// next reads the sheet's next row, as wide as its last cell; io.EOF ends
// the rows. A sheet's dimension, where it says one, sets the width of the
// rows before any is read.
func (sh *sheet) next() (model.Row, error) {
	for {
		t, err := sh.dec.Token()
		if err != nil {
			return nil, err
		}
		switch x := t.(type) {
		case xml.StartElement:
			switch x.Name.Local {
			case "dimension":
				if w := dimensionWidth(attr(x, "ref")); w > 0 {
					sh.width = w
				}
			case "row":
				sh.row++
				if n, err := strconv.Atoi(attr(x, "r")); err == nil {
					sh.row = n
				}
				return sh.cells()
			}
		case xml.EndElement:
			if x.Name.Local == "sheetData" {
				return nil, io.EOF
			}
		}
	}
}

// cells reads a row's cells, each at its column.
func (sh *sheet) cells() (model.Row, error) {
	var row model.Row
	col := 0
	for {
		t, err := sh.dec.Token()
		if err != nil {
			return nil, err
		}
		switch x := t.(type) {
		case xml.StartElement:
			if x.Name.Local != "c" {
				continue
			}
			if at := column(attr(x, "r")); at >= 0 {
				col = at
			}
			v, err := sh.cell(x)
			if err != nil {
				return nil, err
			}
			for len(row) <= col {
				row = append(row, nil)
			}
			row[col] = v
			col++
		case xml.EndElement:
			if x.Name.Local == "row" {
				return row, nil
			}
		}
	}
}

// cell reads one cell's value, as its type and style say.
func (sh *sheet) cell(c xml.StartElement) (any, error) {
	kind, style := attr(c, "t"), -1
	if s, err := strconv.Atoi(attr(c, "s")); err == nil {
		style = s
	}
	var v string
	has := false
	for {
		t, err := sh.dec.Token()
		if err != nil {
			return nil, err
		}
		switch x := t.(type) {
		case xml.StartElement:
			switch x.Name.Local {
			case "v":
				if v, err = text2(sh.dec, "v"); err != nil {
					return nil, err
				}
				has = true
			case "is":
				if v, err = text(sh.dec, "is"); err != nil {
					return nil, err
				}
				has = true
			}
		case xml.EndElement:
			if x.Name.Local == "c" {
				if !has {
					return nil, nil
				}
				return sh.convert(kind, style, v)
			}
		}
	}
}

// text2 is the character data of the element being read, up to its end.
func text2(dec *xml.Decoder, end string) (string, error) {
	var b strings.Builder
	for {
		t, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch x := t.(type) {
		case xml.CharData:
			b.Write(x)
		case xml.EndElement:
			if x.Name.Local == end {
				return b.String(), nil
			}
		}
	}
}

func (sh *sheet) convert(kind string, style int, v string) (any, error) {
	switch kind {
	case "s":
		i, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || i < 0 || i >= len(sh.strings) {
			return nil, fmt.Errorf("transfer: row %d refers to shared string %q, which the workbook has not", sh.row, v)
		}
		return sh.strings[i], nil
	case "str", "inlineStr", "e":
		return v, nil
	case "b":
		return strings.TrimSpace(v) == "1", nil
	}
	v = strings.TrimSpace(v)
	if style >= 0 && style < len(sh.dates) && sh.dates[style] {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, fmt.Errorf("transfer: row %d has a date that is not a number, %q", sh.row, v)
		}
		days, frac := math.Modf(f)
		ms := math.Round(frac * 24 * 60 * 60 * 1000)
		return sh.epoch.AddDate(0, 0, int(days)).Add(time.Duration(ms) * time.Millisecond), nil
	}
	return model.Decimal(v), nil
}

func attr(e xml.StartElement, name string) string {
	for _, a := range e.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// column is the column a cell reference names, from 0: C7 is 2. It is -1
// where the reference names none.
func column(ref string) int {
	n := 0
	for i := 0; i < len(ref); i++ {
		c := ref[i]
		if c < 'A' || c > 'Z' {
			if i == 0 {
				return -1
			}
			break
		}
		n = n*26 + int(c-'A'+1)
	}
	if n == 0 {
		return -1
	}
	return n - 1
}

// dimensionWidth is how many columns a sheet's dimension spans: A1:D10 is 4.
func dimensionWidth(ref string) int {
	_, last, ok := strings.Cut(ref, ":")
	if !ok {
		last = ref
	}
	return column(last) + 1
}
