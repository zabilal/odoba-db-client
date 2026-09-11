// Package export writes rows out as CSV, TSV, JSON, NDJSON, Markdown, HTML,
// XML, SQL INSERT statements or an Excel workbook (FR-10.1). It
// streams, so memory stays flat however many rows there are (FR-10.3,
// NFR-P11).
//
// Values are written so that nothing is lost on the way out:
//
//   - An exact numeric stays exact. A Decimal is the server's own text, and
//     in JSON it is a number literal with every digit, never a float64.
//   - What JSON cannot represent — NaN, the infinities — becomes a string,
//     not null, so it cannot be mistaken for a missing value.
//   - Bytes are hex in CSV and TSV (PostgreSQL's \x form) and base64 in JSON.
//   - Times are written by column type: a date as a date, an instant in UTC
//     with its offset, a wall-clock timestamp without one.
package export

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Format is an export file format.
type Format int

const (
	CSV Format = iota
	TSV
	JSON
	NDJSON
	Markdown
	XLSX
	SQLInsert
	HTML
	XML
)

// Formats lists every format, in the order a menu offers them.
func Formats() []Format {
	return []Format{CSV, TSV, XLSX, JSON, NDJSON, XML, HTML, Markdown, SQLInsert}
}

func (f Format) String() string {
	switch f {
	case CSV:
		return "CSV"
	case TSV:
		return "TSV"
	case JSON:
		return "JSON"
	case NDJSON:
		return "NDJSON"
	case Markdown:
		return "Markdown"
	case XLSX:
		return "Excel"
	case SQLInsert:
		return "SQL INSERT"
	case HTML:
		return "HTML"
	case XML:
		return "XML"
	}
	return fmt.Sprintf("Format(%d)", int(f))
}

// Extension is the file extension, without the dot.
func (f Format) Extension() string {
	switch f {
	case TSV:
		return "tsv"
	case JSON:
		return "json"
	case NDJSON:
		return "ndjson"
	case Markdown:
		return "md"
	case XLSX:
		return "xlsx"
	case SQLInsert:
		return "sql"
	case HTML:
		return "html"
	case XML:
		return "xml"
	}
	return "csv"
}

// Options configures an export.
type Options struct {
	Format Format
	// Header writes the column names as the first CSV or TSV line, or as an
	// Excel sheet's first row.
	Header bool
	// Null is how CSV and TSV write NULL: empty unless set, which is what
	// spreadsheets expect. JSON always writes null.
	Null string
	// Name is what is exported: an Excel workbook's sheet, Sheet1 unless
	// given, and an HTML page's title.
	Name string
	// Inserts writes rows as INSERT statements into the table they came
	// from, in its source's dialect (ARCH-2); SQL INSERT needs it.
	Inserts func(cols []model.ColumnDef, rows []model.Row) (string, error)
}

// Progress is how far an export has got.
type Progress struct {
	Rows    int64
	Bytes   int64
	Elapsed time.Duration
}

// progressEvery bounds how often progress is reported: often enough for a
// steady rows-per-second figure, rarely enough to cost nothing.
const progressEvery = 100 * time.Millisecond

// Copy writes every row of src to dst, and reports progress as it goes and
// once at the end (progress may be nil). It stops when ctx is cancelled; what
// was written by then is incomplete, and removing it is the caller's call.
func Copy(ctx context.Context, dst io.Writer, src model.RowStream, opt Options, progress func(Progress)) (Progress, error) {
	cw := &countingWriter{w: dst}
	bw := bufio.NewWriterSize(cw, 64<<10)
	w, err := newWriter(bw, src.Columns(), opt)
	if err != nil {
		return Progress{}, err
	}
	var p Progress
	start := time.Now()
	last := start
	for {
		if err := ctx.Err(); err != nil {
			return p, err
		}
		row, err := src.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return p, err
		}
		if err := w.row(row); err != nil {
			return p, err
		}
		p.Rows++
		if progress != nil && p.Rows%256 == 0 {
			if now := time.Now(); now.Sub(last) >= progressEvery {
				last = now
				p.Bytes, p.Elapsed = cw.n+int64(bw.Buffered()), now.Sub(start)
				progress(p)
			}
		}
	}
	if err := w.end(); err != nil {
		return p, err
	}
	if err := bw.Flush(); err != nil {
		return p, err
	}
	p.Bytes, p.Elapsed = cw.n, time.Since(start)
	if progress != nil {
		progress(p)
	}
	return p, nil
}

// Write formats rows already in hand, as Copy formats a stream: for copying
// cells to the clipboard (FR-3.7), where the rows are few and in memory.
func Write(dst io.Writer, cols []model.ColumnDef, rows []model.Row, opt Options) error {
	bw := bufio.NewWriter(dst)
	w, err := newWriter(bw, cols, opt)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := w.row(r); err != nil {
			return err
		}
	}
	if err := w.end(); err != nil {
		return err
	}
	return bw.Flush()
}

type rowWriter interface {
	row(model.Row) error
	end() error
}

func newWriter(w *bufio.Writer, cols []model.ColumnDef, opt Options) (rowWriter, error) {
	switch opt.Format {
	case CSV, TSV:
		return newDelimited(w, cols, opt)
	case JSON, NDJSON:
		return newJSONWriter(w, cols, opt.Format == JSON), nil
	case Markdown:
		return newMarkdown(w, cols), nil
	case XLSX:
		return newXLSX(w, cols, opt)
	case SQLInsert:
		return newInserts(w, cols, opt)
	case HTML:
		return newHTML(w, cols, opt), nil
	case XML:
		return newXML(w, cols), nil
	}
	return nil, fmt.Errorf("export: unknown format %v", opt.Format)
}

// delimited writes CSV, or TSV with the same quoting rules, which is what
// spreadsheet applications read back.
type delimited struct {
	csv  *csv.Writer
	cols []model.ColumnDef
	null string
	rec  []string
}

func newDelimited(w io.Writer, cols []model.ColumnDef, opt Options) (*delimited, error) {
	c := csv.NewWriter(w)
	if opt.Format == TSV {
		c.Comma = '\t'
	}
	d := &delimited{csv: c, cols: cols, null: opt.Null, rec: make([]string, len(cols))}
	if opt.Header {
		names := make([]string, len(cols))
		for i, col := range cols {
			names[i] = col.Name
		}
		if err := c.Write(names); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func (d *delimited) row(r model.Row) error {
	for i := range d.rec {
		var v any
		if i < len(r) {
			v = r[i]
		}
		d.rec[i] = text(v, d.cols[i], d.null)
	}
	return d.csv.Write(d.rec)
}

func (d *delimited) end() error {
	d.csv.Flush()
	return d.csv.Error()
}

// Text writes one value as export writes it: exactly, with decimals as
// their digits and bytes as hex. NULL is empty.
func Text(v any, col model.ColumnDef) string { return text(v, col, "") }

// text renders a value for CSV and TSV.
func text(v any, col model.ColumnDef, null string) string {
	switch x := v.(type) {
	case nil:
		return null
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return formatFloat(x)
	case model.Decimal:
		return string(x)
	case model.JSON:
		return string(x)
	case model.Geometry:
		return x.String()
	case time.Time:
		return formatTime(x, col.Type)
	case []byte:
		return `\x` + hex.EncodeToString(x)
	case []any, map[string]any:
		if b, err := marshal(jsonable(x)); err == nil {
			return string(b)
		}
	}
	return fmt.Sprint(v)
}

// jsonWriter writes a JSON array of objects, or NDJSON: one object a line.
type jsonWriter struct {
	w     *bufio.Writer
	cols  []model.ColumnDef
	keys  [][]byte // `"name":`, with duplicate names made unique
	array bool
	n     int64
	buf   bytes.Buffer
}

func newJSONWriter(w *bufio.Writer, cols []model.ColumnDef, array bool) *jsonWriter {
	j := &jsonWriter{w: w, cols: cols, array: array, keys: make([][]byte, len(cols))}
	for i, name := range uniqueNames(cols) {
		b, _ := marshal(name)
		j.keys[i] = append(b, ':')
	}
	if array {
		w.WriteByte('[')
	}
	return j
}

func (j *jsonWriter) row(r model.Row) error {
	if j.array {
		if j.n > 0 {
			j.w.WriteByte(',')
		}
		j.w.WriteByte('\n')
	}
	j.w.WriteByte('{')
	for i := range j.cols {
		if i > 0 {
			j.w.WriteByte(',')
		}
		j.w.Write(j.keys[i])
		var v any
		if i < len(r) {
			v = r[i]
		}
		j.value(v, j.cols[i])
	}
	j.w.WriteByte('}')
	if !j.array {
		j.w.WriteByte('\n')
	}
	j.n++
	return nil
}

func (j *jsonWriter) end() error {
	if j.array {
		if j.n > 0 {
			j.w.WriteByte('\n')
		}
		j.w.WriteString("]\n")
	}
	return nil
}

func (j *jsonWriter) value(v any, col model.ColumnDef) {
	switch x := v.(type) {
	case nil:
		j.w.WriteString("null")
	case bool:
		j.w.WriteString(strconv.FormatBool(x))
	case int64:
		j.w.WriteString(strconv.FormatInt(x, 10))
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			j.str(formatFloat(x))
			return
		}
		j.w.WriteString(strconv.FormatFloat(x, 'g', -1, 64))
	case model.Decimal:
		if isJSONNumber(string(x)) {
			j.w.WriteString(string(x))
		} else {
			j.str(string(x))
		}
	case model.Geometry:
		j.str(x.String())
	case model.JSON:
		// Compacted: a json column keeps the newlines it was typed with, and
		// NDJSON is one record a line.
		j.buf.Reset()
		if json.Compact(&j.buf, x) == nil {
			j.w.Write(j.buf.Bytes())
		} else {
			j.str(string(x))
		}
	case string:
		j.str(x)
	case time.Time:
		j.str(formatTime(x, col.Type))
	case []byte:
		j.str(base64.StdEncoding.EncodeToString(x))
	default:
		if b, err := marshal(jsonable(v)); err == nil {
			j.w.Write(b)
		} else {
			j.str(fmt.Sprint(v))
		}
	}
}

func (j *jsonWriter) str(s string) {
	b, _ := marshal(s)
	j.w.Write(b)
}

// marshal is json.Marshal without HTML escaping: an exported "<" should read
// as "<", not <.
func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// jsonable converts nested values for encoding/json, applying the same
// rules as top-level ones: exact decimals, raw JSON, strings for NaN.
func jsonable(v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = jsonable(x[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = jsonable(e)
		}
		return out
	case model.Decimal:
		if isJSONNumber(string(x)) {
			return json.Number(x)
		}
		return string(x)
	case model.JSON:
		if json.Valid(x) {
			return json.RawMessage(x)
		}
		return string(x)
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return formatFloat(x)
		}
	}
	return v
}

var jsonNumber = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)

func isJSONNumber(s string) bool { return jsonNumber.MatchString(s) }

// formatFloat writes the shortest text that reads back as the same float,
// and PostgreSQL's spellings for the values that are not numbers.
func formatFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func formatTime(t time.Time, dt model.DataType) string {
	switch dt.Class {
	case model.TypeDate:
		return t.Format("2006-01-02")
	case model.TypeTime:
		if dt.TimeZone {
			return t.Format("15:04:05.999999999Z07:00")
		}
		return t.Format("15:04:05.999999999")
	case model.TypeTimestamp:
		if !dt.TimeZone {
			return t.Format("2006-01-02T15:04:05.999999999") // wall clock: no offset to invent
		}
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// uniqueNames makes column names usable as JSON keys: a join can return two
// columns called id, and a JSON object cannot hold both.
func uniqueNames(cols []model.ColumnDef) []string {
	seen := make(map[string]bool, len(cols))
	out := make([]string, len(cols))
	for i, c := range cols {
		name := c.Name
		for n := 2; seen[name]; n++ {
			name = c.Name + "_" + strconv.Itoa(n)
		}
		seen[name] = true
		out[i] = name
	}
	return out
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// markdown writes a table in GitHub's Markdown: a header, a rule that
// right-aligns the number columns, and a line a row. A '|' in a value is
// escaped and a line break becomes <br>, so each value stays in its cell.
// NULL is written _NULL_, so it reads apart from the text "NULL" (UX
// principle 7). A Markdown table always has its header, so Options.Header
// does not apply.
type markdown struct {
	w    *bufio.Writer
	cols []model.ColumnDef
}

func newMarkdown(w *bufio.Writer, cols []model.ColumnDef) *markdown {
	names, rule := make([]string, len(cols)), make([]string, len(cols))
	for i, c := range cols {
		names[i], rule[i] = markdownCell(c.Name), "---"
		switch c.Type.Class {
		case model.TypeInteger, model.TypeFloat, model.TypeDecimal:
			rule[i] = "---:"
		}
	}
	m := &markdown{w: w, cols: cols}
	m.line(names)
	m.line(rule)
	return m
}

func (m *markdown) line(cells []string) {
	m.w.WriteString("| " + strings.Join(cells, " | ") + " |\n")
}

func (m *markdown) row(r model.Row) error {
	cells := make([]string, len(m.cols))
	for i := range m.cols {
		var v any
		if i < len(r) {
			v = r[i]
		}
		if v == nil {
			cells[i] = "_NULL_"
			continue
		}
		cells[i] = markdownCell(text(v, m.cols[i], ""))
	}
	m.line(cells)
	return nil
}

func (m *markdown) end() error { return nil }

func markdownCell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "\r\n", "<br>")
	return strings.ReplaceAll(s, "\n", "<br>")
}
