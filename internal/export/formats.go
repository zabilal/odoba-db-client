package export

import (
	"bufio"
	"encoding/base64"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// SQL INSERT statements, an HTML page and XML (FR-10.1, ADR-0056), each
// streamed as the other formats are.

// inserts writes rows as INSERT statements into the table they came from, in
// its source's dialect, which Options.Inserts writes: the export holds no
// dialect of its own (ARCH-2). It asks for a row at a time, so it keeps no
// row a stream might go on to reuse.
type inserts struct {
	w     *bufio.Writer
	cols  []model.ColumnDef
	write func([]model.ColumnDef, []model.Row) (string, error)
}

func newInserts(w *bufio.Writer, cols []model.ColumnDef, opt Options) (*inserts, error) {
	if opt.Inserts == nil {
		return nil, errors.New("export: SQL INSERT needs the table's source to write its statements")
	}
	return &inserts{w: w, cols: cols, write: opt.Inserts}, nil
}

func (s *inserts) row(r model.Row) error {
	text, err := s.write(s.cols, []model.Row{r})
	if err != nil {
		return err
	}
	_, err = s.w.WriteString(text)
	return err
}

func (s *inserts) end() error { return nil }

// page writes an HTML document: a table whose head names the columns, then a
// row a line, every value escaped and its line breaks kept. NULL reads apart
// from the text "NULL" with no style at all (UX principle 7), and numbers
// align right. Its little style follows the reader's light or dark
// appearance; it runs no script and fetches nothing.
type page struct {
	w    *bufio.Writer
	cols []model.ColumnDef
	num  []bool
}

func newHTML(w *bufio.Writer, cols []model.ColumnDef, opt Options) *page {
	p := &page{w: w, cols: cols, num: make([]bool, len(cols))}
	title := opt.Name
	if title == "" {
		title = "Export"
	}
	w.WriteString("<!doctype html>\n<html><head><meta charset=\"utf-8\"><title>" + htmlText(title) + "</title>\n")
	w.WriteString("<style>" + pageStyle + "</style></head>\n<body><table>\n<thead><tr>")
	for i, c := range cols {
		switch c.Type.Class {
		case model.TypeInteger, model.TypeFloat, model.TypeDecimal:
			p.num[i] = true
		}
		p.cell("th", htmlText(c.Name), p.num[i])
	}
	w.WriteString("</tr></thead>\n<tbody>\n")
	return p
}

func (p *page) cell(tag, text string, num bool) {
	p.w.WriteString("<" + tag)
	if num {
		p.w.WriteString(` class="num"`)
	}
	p.w.WriteString(">" + text + "</" + tag + ">")
}

func (p *page) row(r model.Row) error {
	p.w.WriteString("<tr>")
	for i := range p.cols {
		var v any
		if i < len(r) {
			v = r[i]
		}
		if v == nil {
			p.w.WriteString(`<td class="null"><i>NULL</i></td>`)
			continue
		}
		p.cell("td", htmlText(text(v, p.cols[i], "")), p.num[i])
	}
	p.w.WriteString("</tr>\n")
	return nil
}

func (p *page) end() error {
	_, err := p.w.WriteString("</tbody>\n</table></body></html>\n")
	return err
}

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "\r\n", "<br>", "\n", "<br>")

func htmlText(s string) string { return htmlEscaper.Replace(s) }

// pageStyle is the page's style: rules between cells, a head that stays in
// view, numbers in figures of one width, and NULL in a grey that keeps
// WCAG AA's contrast on either ground.
const pageStyle = `:root{color-scheme:light dark;--rule:#c8c8c8;--null:#5f5f5f}` +
	`@media (prefers-color-scheme:dark){:root{--rule:#4a4a4a;--null:#b0b0b0}}` +
	`body{font:14px -apple-system,system-ui,sans-serif;margin:16px}` +
	`table{border-collapse:collapse}` +
	`th,td{border:1px solid var(--rule);padding:4px 8px;text-align:left;vertical-align:top}` +
	`th{position:sticky;top:0;background:Canvas}` +
	`.num{text-align:right;font-variant-numeric:tabular-nums}.null{color:var(--null)}`

// xmlRows writes rows as XML: a <row> of <field>s, each naming its column in
// an attribute, as a column's name is not always an XML name. NULL is a
// field marked null. Bytes, and text holding a character XML 1.0 cannot hold
// at all, go as base64 and are marked so: nothing is lost, and the document
// stays well-formed. A carriage return is kept as a reference, which XML
// would otherwise read as a line feed.
type xmlRows struct {
	w     *bufio.Writer
	cols  []model.ColumnDef
	names []string // each column's name, as an attribute holds it
}

func newXML(w *bufio.Writer, cols []model.ColumnDef) *xmlRows {
	x := &xmlRows{w: w, cols: cols, names: make([]string, len(cols))}
	for i, c := range cols {
		x.names[i] = xmlAttrEscaper.Replace(strings.Map(func(r rune) rune {
			if !xmlChar(r) {
				return utf8.RuneError
			}
			return r
		}, c.Name))
	}
	w.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n<rows>\n")
	return x
}

func (x *xmlRows) row(r model.Row) error {
	x.w.WriteString("<row>")
	for i, c := range x.cols {
		var v any
		if i < len(r) {
			v = r[i]
		}
		x.w.WriteString(`<field name="` + x.names[i] + `"`)
		var s string
		switch t := v.(type) {
		case nil:
			x.w.WriteString(` null="true"/>`)
			continue
		case []byte:
			x.base64(t)
			continue
		default:
			s = text(v, c, "")
		}
		if !xmlSafe(s) {
			x.base64([]byte(s))
			continue
		}
		x.w.WriteString(">" + xmlEscaper.Replace(s) + "</field>")
	}
	x.w.WriteString("</row>\n")
	return nil
}

func (x *xmlRows) base64(b []byte) {
	x.w.WriteString(` encoding="base64">` + base64.StdEncoding.EncodeToString(b) + "</field>")
}

func (x *xmlRows) end() error {
	_, err := x.w.WriteString("</rows>\n")
	return err
}

// xmlSafe reports whether XML 1.0 can hold text as it is: valid UTF-8, with
// no character it cannot hold.
func xmlSafe(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if !xmlChar(r) {
			return false
		}
	}
	return true
}

// xmlChar reports whether XML 1.0 can hold a character.
func xmlChar(r rune) bool {
	return r >= 0x20 && r != 0xFFFE && r != 0xFFFF || r == '\t' || r == '\n' || r == '\r'
}

var (
	xmlEscaper     = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\r", "&#13;")
	xmlAttrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "\t", "&#9;", "\n", "&#10;", "\r", "&#13;")
)
