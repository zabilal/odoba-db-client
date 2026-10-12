package transfer

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// DBF, the table file of dBase and of everything since that wrote one —
// FoxPro, Clipper, and every shapefile (FR-10.10).
//
// It is read by hand, as a workbook is (ADR-0044): the format is a header, a
// line a column, and then records of a fixed width, and there is nothing in it
// a library would do that forty lines do not.
//
// What it cannot be read without is the file beside it. A memo column holds
// not its text but where the text is, in a second file with the same name and
// another ending; an import is given one file, so a table with a memo in it is
// refused rather than read with that column empty. An empty column would look
// like data.

// dbfField is one column as the header describes it.
type dbfField struct {
	name     string
	kind     byte
	length   int
	decimals int
}

type dbf struct {
	r      *bufio.Reader
	fields []dbfField // every field of a record, in the order a record holds them
	cols   []model.ColumnDef
	left   uint32 // records the header says are still to come
	n      int    // records read, kept or not, for the errors
	record []byte
	enc    Encoding
}

// The fixed parts of the format.
const (
	dbfHeader     = 32
	dbfDescriptor = 32
	dbfEndFields  = 0x0D
	dbfEndFile    = 0x1A
	dbfDeleted    = '*'
	// dbfNullFlags is the type of the field FoxPro adds to a table with
	// columns that may be null: a bit a column, saying which are.
	dbfNullFlags = '0'
)

func openDBF(r io.ReaderAt, size int64, opt Options) (*dbf, error) {
	head := make([]byte, dbfHeader)
	if _, err := r.ReadAt(head, 0); err != nil {
		return nil, errors.New("transfer: this is not a DBF file: it is shorter than a DBF file's header")
	}
	count := binary.LittleEndian.Uint32(head[4:8])
	headerLen := int64(binary.LittleEndian.Uint16(head[8:10]))
	recordLen := int(binary.LittleEndian.Uint16(head[10:12]))
	if headerLen < dbfHeader+1 || headerLen > size || recordLen < 1 {
		return nil, errors.New("transfer: this is not a DBF file: its header does not describe one")
	}
	d := &dbf{left: count, record: make([]byte, recordLen), enc: opt.Encoding}
	descriptors := make([]byte, headerLen-dbfHeader)
	if _, err := r.ReadAt(descriptors, dbfHeader); err != nil {
		return nil, fmt.Errorf("transfer: the DBF file's columns could not be read: %w", err)
	}
	width := 1 // the mark that says whether a record was deleted
	for at := 0; at+dbfDescriptor <= len(descriptors) && descriptors[at] != dbfEndFields; at += dbfDescriptor {
		desc := descriptors[at : at+dbfDescriptor]
		f := dbfField{name: string(bytesBefore(desc[:11], 0)), kind: desc[11],
			length: int(desc[16]), decimals: int(desc[17])}
		if f.kind == 'C' {
			// A text column wider than a byte can say keeps the rest of its
			// width where a number keeps its decimal places.
			f.length, f.decimals = int(desc[16])|int(desc[17])<<8, 0
		}
		width += f.length
		d.fields = append(d.fields, f)
		if f.kind == dbfNullFlags {
			continue // in every record, and not a column of anybody's data
		}
		col, err := dbfColumn(f)
		if err != nil {
			return nil, err
		}
		d.cols = append(d.cols, col)
	}
	if len(d.cols) == 0 {
		return nil, errors.New("transfer: this DBF file describes no columns")
	}
	if width > recordLen {
		return nil, fmt.Errorf("transfer: this DBF file's columns are %d bytes wide and its records %d", width, recordLen)
	}
	d.cols = renamed(d.cols)
	d.r = bufio.NewReaderSize(io.NewSectionReader(r, headerLen, size-headerLen), 64<<10)
	return d, nil
}

// bytesBefore is b up to the first of a byte, or all of it.
func bytesBefore(b []byte, end byte) []byte {
	for i, c := range b {
		if c == end {
			return b[:i]
		}
	}
	return b
}

// renamed gives every column a name of its own, as named does for a
// delimited file's.
func renamed(cols []model.ColumnDef) []model.ColumnDef {
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	for i, c := range named(names) {
		cols[i].Name = c.Name
	}
	return cols
}

// dbfColumn is a column as rows hold it, or a refusal where what it holds
// cannot be read from this file.
func dbfColumn(f dbfField) (model.ColumnDef, error) {
	col := model.ColumnDef{Name: f.name, Type: model.DataType{Nullable: true, Native: string(f.kind), Length: -1}}
	switch f.kind {
	case 'C':
		col.Type.Class, col.Type.Length = model.TypeString, int64(f.length)
	case 'N', 'F':
		col.Type.Class, col.Type.Precision, col.Type.Scale = model.TypeDecimal, int32(f.length), int32(f.decimals)
	case 'I':
		col.Type.Class = model.TypeInteger
	case 'B', 'O':
		col.Type.Class = model.TypeFloat
	case 'Y':
		col.Type.Class, col.Type.Scale = model.TypeDecimal, 4
	case 'L':
		col.Type.Class = model.TypeBool
	case 'D':
		col.Type.Class = model.TypeDate
	case 'T', '@':
		col.Type.Class = model.TypeTimestamp
	case 'M', 'G', 'P', 'W':
		return col, fmt.Errorf("transfer: the column %s of this DBF file is a memo: its values are kept in a second file "+
			"beside this one, which an import is not given, so the column would be read as empty. "+
			"Export the table without that column, or as another format", f.name)
	default:
		return col, fmt.Errorf("transfer: the column %s of this DBF file is of a type, %q, that is not read", f.name, string(f.kind))
	}
	return col, nil
}

func (d *dbf) Columns() []model.ColumnDef { return d.cols }

func (d *dbf) Next(ctx context.Context) (model.Row, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if d.left == 0 {
			return nil, io.EOF
		}
		// The first byte alone, first: a file may end with a mark of its
		// own, and may simply end, before the count its header gave.
		mark, err := d.r.ReadByte()
		if err == io.EOF || (err == nil && mark == dbfEndFile) {
			return nil, io.EOF
		}
		if err != nil {
			return nil, err
		}
		d.n++
		d.left--
		d.record[0] = mark
		if _, err := io.ReadFull(d.r, d.record[1:]); err != nil {
			return nil, fmt.Errorf("transfer: record %d of this DBF file is cut short", d.n)
		}
		if mark == dbfDeleted {
			continue // deleted, and still in the file until somebody packs it
		}
		row := make(model.Row, 0, len(d.cols))
		at := 1
		for _, f := range d.fields {
			raw := d.record[at : at+f.length]
			at += f.length
			if f.kind == dbfNullFlags {
				continue
			}
			v, err := d.value(f, raw)
			if err != nil {
				return nil, fmt.Errorf("transfer: record %d, %s: %w", d.n, f.name, err)
			}
			row = append(row, v)
		}
		return row, nil
	}
}

func (d *dbf) Close() error { return nil }

// value is what a field of a record holds.
func (d *dbf) value(f dbfField, raw []byte) (any, error) {
	switch f.kind {
	case 'C':
		s := strings.TrimRight(d.decode(raw), " \x00")
		if s == "" {
			return nil, nil // a field left blank is NULL, as an empty CSV field is
		}
		return s, nil
	case 'N', 'F':
		s := strings.TrimSpace(string(bytesBefore(raw, 0)))
		switch {
		case s == "", strings.Trim(s, "*") == "", s == ".", s == "-":
			// Blank, or the row of stars dBase writes for a number that did
			// not fit its field: either way there is no number here.
			return nil, nil
		}
		if _, err := strconv.ParseFloat(s, 64); err != nil {
			return nil, fmt.Errorf("%q is not a number", s)
		}
		return model.Decimal(s), nil
	case 'L':
		switch raw[0] {
		case 'T', 't', 'Y', 'y':
			return true, nil
		case 'F', 'f', 'N', 'n':
			return false, nil
		}
		return nil, nil // '?' and blank: not yet said
	case 'D':
		s := strings.TrimSpace(string(bytesBefore(raw, 0)))
		if s == "" {
			return nil, nil
		}
		day, err := time.Parse("20060102", s)
		if err != nil {
			return nil, fmt.Errorf("%q is not a date", s)
		}
		return day, nil
	case 'I':
		if len(raw) != 4 {
			return nil, fmt.Errorf("an integer of %d bytes", len(raw))
		}
		return int64(int32(binary.LittleEndian.Uint32(raw))), nil
	case 'B', 'O':
		if len(raw) != 8 {
			return nil, fmt.Errorf("a double of %d bytes", len(raw))
		}
		return math.Float64frombits(binary.LittleEndian.Uint64(raw)), nil
	case 'Y':
		if len(raw) != 8 {
			return nil, fmt.Errorf("a currency of %d bytes", len(raw))
		}
		return model.Decimal(scaled4(int64(binary.LittleEndian.Uint64(raw)))), nil
	case 'T', '@':
		if len(raw) != 8 {
			return nil, fmt.Errorf("a time of %d bytes", len(raw))
		}
		day, ms := binary.LittleEndian.Uint32(raw[:4]), binary.LittleEndian.Uint32(raw[4:])
		if day == 0 {
			return nil, nil
		}
		// The day is a Julian day number, as astronomers count them, and the
		// rest is the milliseconds since that day's midnight.
		return time.Unix((int64(day)-julianUnixEpoch)*86400, 0).UTC().Add(time.Duration(ms) * time.Millisecond), nil
	}
	return nil, fmt.Errorf("a %q is not read", string(f.kind))
}

// julianUnixEpoch is the Julian day number of 1 January 1970.
const julianUnixEpoch = 2440588

// scaled4 is a count of ten-thousandths as the number it is, every place kept.
func scaled4(n int64) string {
	sign := ""
	if n < 0 {
		sign, n = "-", -n
	}
	return fmt.Sprintf("%s%d.%04d", sign, n/10000, n%10000)
}

// decode is a text field as UTF-8. A DBF file says which code page it was
// written in with one byte that most writers leave at nought, so unless a
// person said, text is read as text files are: as UTF-8 where it is UTF-8, and
// as Windows-1252 otherwise.
func (d *dbf) decode(raw []byte) string {
	if d.enc != Windows1252 && utf8.Valid(raw) {
		return string(raw)
	}
	out := make([]rune, 0, len(raw))
	for _, b := range raw {
		switch {
		case b < 0x80 || b >= 0xA0:
			out = append(out, rune(b))
		default:
			out = append(out, cp1252[b-0x80])
		}
	}
	return string(out)
}
