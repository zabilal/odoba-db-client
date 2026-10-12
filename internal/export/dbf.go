package export

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// DBF: a dBase III table, which is the one every reader of the format reads
// (FR-10.10).
//
// Everything about the format is decided before the first record is written.
// The header says how many records there are and how wide each column is, in
// bytes, and a record is exactly that wide. A stream of rows knows neither
// until its last. So the rows are written twice: once to a file of this
// program's own beside the system's temporary ones, as they arrive, while each
// column's widest value is noted; and then, at the end, out — header first,
// then every record at the widths that turned out to be needed. Memory does not
// grow with the rows (FR-10.3). Disk does, for as long as the export takes.
//
// What the format cannot hold is refused or written as the nearest thing that
// can be read back, and never cut to fit:
//
//   - A column's name is at most ten letters, digits and underscores. Names
//     are made to fit, and made different from each other where fitting made
//     them the same. This is the one thing an export to DBF changes that a
//     person did not ask it to, and there is no DBF in which it would not.
//   - Text is at most 254 bytes. Longer text is refused, naming the column:
//     DBF's own answer is a memo, in a second file, and this writes one file.
//   - A number is at most twenty characters. A column holding a longer one is
//     written as text, every digit kept, rather than as a number with some of
//     them gone.
//   - There is no time of day, so a timestamp is written as text.
type dbfWriter struct {
	w     *bufio.Writer
	cols  []model.ColumnDef
	spool *os.File
	sw    *bufio.Writer
	rows  uint32

	width    []int  // each column's widest value, in bytes
	decimals []int  // each numeric column's most decimal places
	numeric  []bool // whether every value so far could be a DBF number
	typed    []bool // whether every value so far is what a date or a logical column holds
	ascii    bool   // whether every byte of text so far is ASCII
	latin    bool   // whether all text so far can be written in Windows-1252
}

// The limits of a dBase III table.
const (
	dbfMaxText   = 254
	dbfMaxNumber = 20
	dbfMaxFields = 255
	dbfNameLen   = 10
)

func newDBF(w *bufio.Writer, cols []model.ColumnDef) (*dbfWriter, error) {
	if len(cols) == 0 {
		return nil, fmt.Errorf("export: a DBF table needs at least one column")
	}
	if len(cols) > dbfMaxFields {
		return nil, fmt.Errorf("export: a DBF table holds at most %d columns, and this has %d", dbfMaxFields, len(cols))
	}
	spool, err := os.CreateTemp("", "ikigai-dbf-*")
	if err != nil {
		return nil, fmt.Errorf("export: a DBF table is written through a temporary file, which could not be made: %w", err)
	}
	d := &dbfWriter{w: w, cols: cols, spool: spool, sw: bufio.NewWriterSize(spool, 64<<10),
		width: make([]int, len(cols)), decimals: make([]int, len(cols)), numeric: make([]bool, len(cols)),
		typed: make([]bool, len(cols)), ascii: true, latin: true}
	for i, c := range cols {
		switch c.Type.Class {
		case model.TypeInteger, model.TypeFloat, model.TypeDecimal:
			d.numeric[i] = true
		case model.TypeDate, model.TypeBool:
			d.typed[i] = true
		}
	}
	return d, nil
}

// abandon is what a writer that will not be finished is told, so that it
// leaves nothing behind.
func (d *dbfWriter) abandon() { d.discard() }

// discard lets go of the temporary file. More than once is the same as once.
func (d *dbfWriter) discard() {
	name := d.spool.Name()
	_ = d.spool.Close()
	_ = os.Remove(name)
}

func (d *dbfWriter) row(r model.Row) error {
	if d.rows == math.MaxUint32 {
		d.discard()
		return fmt.Errorf("export: a DBF table holds at most %d records", uint32(math.MaxUint32))
	}
	for i, c := range d.cols {
		var v any
		if i < len(r) {
			v = r[i]
		}
		text, null := dbfText(v, c)
		if !null {
			if d.numeric[i] {
				if decimals, ok := dbfNumber(text); ok {
					d.decimals[i] = max(d.decimals[i], decimals)
				} else {
					d.numeric[i] = false
				}
			}
			for j := 0; j < len(text); j++ {
				if text[j] >= 0x80 {
					d.ascii = false
					break
				}
			}
			if !d.ascii && d.latin && !latin1252(text) {
				d.latin = false
			}
			// A column said to hold dates or yes-and-no is written as one
			// only while that is what it holds.
			switch v.(type) {
			case bool:
				d.typed[i] = d.typed[i] && c.Type.Class == model.TypeBool
			case time.Time:
				d.typed[i] = d.typed[i] && c.Type.Class == model.TypeDate
			default:
				d.typed[i] = false
			}
			d.width[i] = max(d.width[i], len(text))
		}
		// A value is spooled as its length and its bytes; no value at all as
		// a length no value has.
		n := uint32(len(text))
		if null {
			n = math.MaxUint32
		}
		var head [4]byte
		binary.LittleEndian.PutUint32(head[:], n)
		if _, err := d.sw.Write(head[:]); err != nil {
			d.discard()
			return err
		}
		if _, err := d.sw.WriteString(text); err != nil {
			d.discard()
			return err
		}
	}
	d.rows++
	return nil
}

// dbfText is a value as a DBF field holds it, before it is fitted to a width,
// and whether it is no value at all.
func dbfText(v any, col model.ColumnDef) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", true
	case bool:
		if x {
			return "T", false
		}
		return "F", false
	case time.Time:
		if col.Type.Class == model.TypeDate {
			return x.Format("20060102"), false
		}
		return formatTime(x, col.Type), false
	case []byte:
		return `\x` + hex.EncodeToString(x), false
	case float64:
		return formatFloat(x), false
	}
	return text(v, col, ""), false
}

// dbfNumber reports whether text is a number a DBF field can hold — digits, a
// sign, a point, and no exponent — and how many decimal places it has.
func dbfNumber(s string) (int, bool) {
	if s == "" || len(s) > dbfMaxNumber {
		return 0, false
	}
	digits, point := 0, -1
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			digits++
		case c == '-' && i == 0:
		case c == '.' && point < 0:
			point = i
		default:
			return 0, false
		}
	}
	if digits == 0 {
		return 0, false
	}
	if point < 0 {
		return 0, true
	}
	return len(s) - point - 1, true
}

// latin1252 reports whether text can be written in Windows-1252, which is the
// code page a DBF reader assumes of text that is not plain ASCII.
func latin1252(s string) bool {
	for _, r := range s {
		if _, ok := toCP1252(r); !ok {
			return false
		}
	}
	return true
}

// cp1252High is Windows-1252's characters from 0x80 to 0x9F, where it is not
// Latin-1; a nought is a byte it leaves unused.
var cp1252High = [32]rune{
	0x20AC, 0, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021, 0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0, 0x017D, 0,
	0, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014, 0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0, 0x017E, 0x0178,
}

func toCP1252(r rune) (byte, bool) {
	switch {
	case r < 0x80, r >= 0xA0 && r <= 0xFF:
		return byte(r), true
	}
	for i, c := range cp1252High {
		if c == r {
			return byte(0x80 + i), true
		}
	}
	return 0, false
}

// field is a column as the header describes it.
type field struct {
	name     string
	kind     byte
	length   int
	decimals int
}

// fields decides what each column is, now that every value has been seen.
func (d *dbfWriter) fields() []field {
	names := dbfNames(d.cols)
	out := make([]field, len(d.cols))
	for i, c := range d.cols {
		f := field{name: names[i]}
		switch {
		case d.typed[i] && c.Type.Class == model.TypeBool:
			f.kind, f.length = 'L', 1
		case d.typed[i] && c.Type.Class == model.TypeDate:
			f.kind, f.length = 'D', 8
		case d.numeric[i] && d.width[i] > 0:
			// Wide enough for the longest whole part and the most decimal
			// places together, which may be two different values'.
			f.kind, f.decimals = 'N', d.decimals[i]
			f.length = d.width[i]
		default:
			f.kind, f.length = 'C', 1 // and as wide as its widest value, found below
		}
		out[i] = f
	}
	return out
}

// dbfNames makes the columns' names ones a DBF header can hold: at most ten
// letters, digits and underscores, in capitals, each different from the rest.
func dbfNames(cols []model.ColumnDef) []string {
	seen := map[string]bool{}
	out := make([]string, len(cols))
	for i, c := range cols {
		var b strings.Builder
		for _, r := range strings.ToUpper(c.Name) {
			switch {
			case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
				b.WriteRune(r)
			default:
				b.WriteByte('_') // an underscore among them: it becomes itself
			}
		}
		base := b.String()
		if base == "" {
			base = "FIELD" + strconv.Itoa(i+1)
		}
		if len(base) > dbfNameLen {
			base = base[:dbfNameLen]
		}
		name := base
		for n := 2; seen[name]; n++ {
			suffix := "_" + strconv.Itoa(n)
			name = base[:min(len(base), dbfNameLen-len(suffix))] + suffix
		}
		seen[name] = true
		out[i] = name
	}
	return out
}

// end writes the table: the header, now that it can be, and every record.
func (d *dbfWriter) end() error {
	defer d.discard()
	if err := d.sw.Flush(); err != nil {
		return err
	}
	fields := d.fields()
	// A column's width is found by going through it once more: a number is
	// fitted by its widest whole part and its most decimal places, which may
	// be two different values', and text is as wide as it is in the bytes it
	// will be written in, which is not known until all of it has been seen.
	if err := d.replay(func(i int, text string, null bool) error {
		switch f := &fields[i]; {
		case null:
		case f.kind == 'N':
			f.length = max(f.length, len(dbfFit(text, f.decimals)))
		case f.kind == 'C':
			f.length = max(f.length, len(d.encoded(text)))
		}
		return nil
	}); err != nil {
		return err
	}
	recordLen := 1
	for i, f := range fields {
		limit := dbfMaxText
		if f.kind == 'N' {
			limit = dbfMaxNumber
		}
		if f.length > limit {
			if f.kind == 'N' {
				return fmt.Errorf("export: the column %s needs %d characters to write its numbers, and a DBF number has %d",
					d.cols[i].Name, f.length, dbfMaxNumber)
			}
			return fmt.Errorf("export: the column %s holds text of %d bytes, and a DBF field holds %d: "+
				"nothing was cut to fit, and nothing was written", d.cols[i].Name, f.length, dbfMaxText)
		}
		recordLen += f.length
	}
	if recordLen > math.MaxUint16 {
		return fmt.Errorf("export: a row of this table is %d bytes, and a DBF record holds at most %d", recordLen, math.MaxUint16)
	}

	now := time.Now()
	head := make([]byte, 32)
	head[0] = 0x03 // dBase III, with no memo file
	head[1], head[2], head[3] = byte(now.Year()-1900), byte(now.Month()), byte(now.Day())
	binary.LittleEndian.PutUint32(head[4:], d.rows)
	binary.LittleEndian.PutUint16(head[8:], uint16(32+32*len(fields)+1))
	binary.LittleEndian.PutUint16(head[10:], uint16(recordLen))
	if !d.ascii && d.latin {
		head[29] = 0x03 // Windows-1252, which is what a reader is told the text is
	}
	d.w.Write(head)
	for _, f := range fields {
		desc := make([]byte, 32)
		copy(desc, f.name)
		desc[11], desc[16], desc[17] = f.kind, byte(f.length), byte(f.decimals)
		d.w.Write(desc)
	}
	d.w.WriteByte(0x0D)

	pad := make([]byte, dbfMaxText)
	for i := range pad {
		pad[i] = ' '
	}
	err := d.replay(func(i int, text string, null bool) error {
		f := fields[i]
		if i == 0 {
			d.w.WriteByte(' ') // a record that has not been deleted
		}
		switch {
		case null && f.kind == 'L':
			d.w.WriteByte('?')
		case null:
			d.w.Write(pad[:f.length])
		case f.kind == 'N':
			fitted := dbfFit(text, f.decimals)
			d.w.Write(pad[:f.length-len(fitted)]) // a number sits at the right of its field
			d.w.WriteString(fitted)
		default:
			out := d.encoded(text)
			d.w.WriteString(out)
			d.w.Write(pad[:f.length-len(out)])
		}
		return nil
	})
	if err != nil {
		return err
	}
	return d.w.WriteByte(0x1A)
}

// dbfFit is a number with the decimal places its column has.
func dbfFit(s string, decimals int) string {
	if decimals == 0 {
		return s
	}
	whole, frac, _ := strings.Cut(s, ".")
	return whole + "." + frac + strings.Repeat("0", decimals-len(frac))
}

// encoded is text in the bytes this table is written in: Windows-1252 where
// all of the table's text can be, which is what a reader is then told it is,
// and UTF-8 where some of it cannot.
func (d *dbfWriter) encoded(s string) string {
	if d.ascii || !d.latin {
		return s
	}
	out := make([]byte, 0, len(s))
	for _, r := range s {
		b, _ := toCP1252(r)
		out = append(out, b)
	}
	return string(out)
}

// replay goes through the spooled values again, in the order they were
// written, a column at a time and a row at a time.
func (d *dbfWriter) replay(each func(col int, text string, null bool) error) error {
	if _, err := d.spool.Seek(0, io.SeekStart); err != nil {
		return err
	}
	r := bufio.NewReaderSize(d.spool, 64<<10)
	var head [4]byte
	var buf []byte
	for row := uint32(0); row < d.rows; row++ {
		for i := range d.cols {
			if _, err := io.ReadFull(r, head[:]); err != nil {
				return err
			}
			n := binary.LittleEndian.Uint32(head[:])
			if n == math.MaxUint32 {
				if err := each(i, "", true); err != nil {
					return err
				}
				continue
			}
			if cap(buf) < int(n) {
				buf = make([]byte, n)
			}
			buf = buf[:n]
			if _, err := io.ReadFull(r, buf); err != nil {
				return err
			}
			if err := each(i, string(buf), false); err != nil {
				return err
			}
		}
	}
	return nil
}
