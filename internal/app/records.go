package app

import (
	"context"

	"github.com/ikigai-db/ikigai-db/internal/app/decode"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Exporting a consumed window of a topic (FR-10.8, FR-13.16).
//
// A record's key and value are bytes, and bytes are not one thing: the
// same bytes are text to whoever wrote them, JSON to the service that
// parses them, and a dump to whoever has to know what went over the wire
// (ADR-0098). The window reads them through a decoder, so an export of
// that window writes what the window is showing rather than the bytes it
// is showing them from — hex in a spreadsheet is nobody's export.

// A RecordReader says how one field of a record is read.
type RecordReader struct {
	// Chosen is the decoder somebody picked for this field. Nil reads each
	// record as its own bytes admit, most decoded first, which is the form
	// the record view offers first.
	Chosen source.Decoder
	// Extra is a registry's decoder for this field, offered ahead of the
	// ones that need nothing but the bytes: it knows more about them than
	// anything worked out from the bytes alone. Nil for a topic with no
	// schema.
	Extra source.Decoder
}

// read is one field's bytes as this reader reads them.
//
// Bytes no decoder will read come back as they are, and are written as
// bytes: an undecodable record is still worth exporting, and leaving it
// out would be a file that is quietly short of the window it is of.
func (r RecordReader) read(v any) any {
	b, ok := v.([]byte)
	if !ok {
		return v
	}
	if r.Chosen != nil {
		if out, err := r.Chosen.Decode(b); err == nil {
			return out
		}
		return v
	}
	// A registry's decoder first, then the ones that need nothing but the
	// bytes, most decoded first. Each is tried rather than asked, because
	// a decoder that cannot read these bytes says so by refusing them.
	for _, d := range append(r.extras(), decode.Local()...) {
		if out, err := d.Decode(b); err == nil {
			return out
		}
	}
	return v
}

func (r RecordReader) extras() []source.Decoder {
	if r.Extra == nil {
		return nil
	}
	return []source.Decoder{r.Extra}
}

// name is what the decoded column is said to hold.
func (r RecordReader) name() string {
	if r.Chosen != nil {
		return r.Chosen.Name()
	}
	return "decoded"
}

// DecodeRecords is a topic's rows with their key and value read the way
// the window is reading them.
//
// A stream with no such columns passes through untouched, so this is safe
// over any rows at all.
func DecodeRecords(rs model.RowStream, key, value RecordReader) model.RowStream {
	d := &decodedRows{RowStream: rs, keyAt: -1, valueAt: -1, key: key, value: value}
	d.cols = append([]model.ColumnDef(nil), rs.Columns()...)
	for i, c := range d.cols {
		switch c.Name {
		case DecodeKey:
			d.keyAt, d.cols[i].Type = i, decodedType(key, c.Type)
		case DecodeValue:
			d.valueAt, d.cols[i].Type = i, decodedType(value, c.Type)
		}
	}
	return d
}

// decodedType is what a decoded column holds: text rather than bytes,
// named after the decoder so that a file says how it was read.
func decodedType(r RecordReader, was model.DataType) model.DataType {
	if was.Class != model.TypeBytes {
		return was // not bytes, and so not something a decoder reads
	}
	return model.DataType{Class: model.TypeString, Native: r.name(), Nullable: was.Nullable}
}

type decodedRows struct {
	model.RowStream
	cols           []model.ColumnDef
	keyAt, valueAt int
	key, value     RecordReader
}

func (d *decodedRows) Columns() []model.ColumnDef { return d.cols }

func (d *decodedRows) Next(ctx context.Context) (model.Row, error) {
	row, err := d.RowStream.Next(ctx)
	if err != nil {
		return nil, err
	}
	// A copy: the row belongs to whoever made it, and a stream that reads
	// its caller's rows in place would leave them decoded for the next
	// reader — which is a record that cannot be read twice, once in the
	// window and once into a file.
	out := append(model.Row(nil), row...)
	if d.keyAt >= 0 && d.keyAt < len(out) {
		out[d.keyAt] = d.key.read(out[d.keyAt])
	}
	if d.valueAt >= 0 && d.valueAt < len(out) {
		out[d.valueAt] = d.value.read(out[d.valueAt])
	}
	return out, nil
}
