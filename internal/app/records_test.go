package app

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/app/decode"
	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Exporting a consumed window of a topic (FR-10.8, FR-13.16).

// records is a topic's rows: the columns a Kafka browse gives.
type records struct {
	rows []model.Row
	i    int
	cols []model.ColumnDef
}

func newRecords(rows ...model.Row) *records {
	return &records{rows: rows, cols: []model.ColumnDef{
		{Name: "offset", Type: model.DataType{Class: model.TypeInteger, Native: "int64"}},
		{Name: "key", Type: model.DataType{Class: model.TypeBytes, Native: "bytes", Nullable: true}},
		{Name: "value", Type: model.DataType{Class: model.TypeBytes, Native: "bytes", Nullable: true}},
	}}
}

func (r *records) Columns() []model.ColumnDef { return r.cols }
func (r *records) Close() error               { return nil }
func (r *records) Next(context.Context) (model.Row, error) {
	if r.i >= len(r.rows) {
		return nil, io.EOF
	}
	r.i++
	return r.rows[r.i-1], nil
}

// readAll takes every row of a stream.
func readAll(t *testing.T, rs model.RowStream) []model.Row {
	t.Helper()
	var out []model.Row
	for {
		row, err := rs.Next(context.Background())
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
}

// A record's key and value arrive as the bytes admit: JSON as JSON, text
// as text, so that an export is what the window shows.
func TestRecordsAreDecodedAsTheirBytesAdmit(t *testing.T) {
	rows := newRecords(
		model.Row{int64(1), []byte("k1"), []byte(`{"id":7}`)},
		model.Row{int64(2), nil, []byte("plain text")},
	)
	got := readAll(t, DecodeRecords(rows, RecordReader{}, RecordReader{}))
	if len(got) != 2 {
		t.Fatalf("it read %d rows", len(got))
	}
	if s, ok := got[0][1].(string); !ok || s != "k1" {
		t.Errorf("the key arrived as %#v", got[0][1])
	}
	// JSON keeps its own text, so that a file of them is a file of JSON
	// and every number keeps its digits.
	if j, ok := got[0][2].(model.JSON); !ok || string(j) != `{"id":7}` {
		t.Errorf("the value arrived as %T (%#v)", got[0][2], got[0][2])
	}
	if got[1][1] != nil {
		t.Errorf("a record with no key arrived with %#v", got[1][1])
	}
	if s, ok := got[1][2].(string); !ok || s != "plain text" {
		t.Errorf("the value arrived as %#v", got[1][2])
	}
}

// The decoder somebody picked is the one used, whatever the bytes admit:
// bytes that are JSON read as text where text is what was chosen.
func TestTheChosenDecoderIsUsed(t *testing.T) {
	text, ok := decode.ByName(decode.NameText)
	if !ok {
		t.Fatal("there is no text decoder")
	}
	rows := newRecords(model.Row{int64(1), []byte("k1"), []byte(`{"id":7}`)})
	got := readAll(t, DecodeRecords(rows, RecordReader{}, RecordReader{Chosen: text}))
	if s, isText := got[0][2].(string); !isText || s != `{"id":7}` {
		t.Errorf("the value arrived as %T (%#v), and text was chosen", got[0][2], got[0][2])
	}
	// And with nothing chosen those same bytes read as JSON.
	plain := readAll(t, DecodeRecords(newRecords(model.Row{int64(1), nil, []byte(`{"id":7}`)}),
		RecordReader{}, RecordReader{}))
	if _, isJSON := plain[0][2].(model.JSON); !isJSON {
		t.Errorf("with nothing chosen the value arrived as %T", plain[0][2])
	}
}

// Bytes no decoder will read come back as bytes: an undecodable record is
// still worth exporting, and leaving it out would be a file quietly short
// of the window it is of.
func TestWhatCannotBeDecodedStaysBytes(t *testing.T) {
	rows := newRecords(model.Row{int64(1), nil, []byte{0xff, 0xfe}})
	got := readAll(t, DecodeRecords(rows, RecordReader{}, RecordReader{Chosen: refusing{}}))
	if _, ok := got[0][2].([]byte); !ok {
		t.Errorf("the value arrived as %T", got[0][2])
	}
}

// refusing is a decoder that will not read anything.
type refusing struct{}

func (refusing) Name() string               { return "Refusing" }
func (refusing) Decode([]byte) (any, error) { return nil, errors.New("not for me") }

// A registry's decoder is offered ahead of the ones that need nothing but
// the bytes: it knows more about them than anything worked out from them.
func TestARegistrysDecoderComesFirst(t *testing.T) {
	rows := newRecords(model.Row{int64(1), nil, []byte(`{"id":7}`)})
	got := readAll(t, DecodeRecords(rows, RecordReader{}, RecordReader{Extra: shouting{}}))
	if s, ok := got[0][2].(string); !ok || s != "SHOUTED" {
		t.Errorf("the value arrived as %#v", got[0][2])
	}
}

// A registry's decoder that will not read these bytes is passed over
// rather than taken for the answer: a schema says what most of a topic is,
// not what every record in it is.
func TestARegistrysDecoderThatRefusesIsPassedOver(t *testing.T) {
	rows := newRecords(model.Row{int64(1), nil, []byte(`{"id":7}`)})
	got := readAll(t, DecodeRecords(rows, RecordReader{}, RecordReader{Extra: refusing{}}))
	if j, ok := got[0][2].(model.JSON); !ok || string(j) != `{"id":7}` {
		t.Errorf("the value arrived as %T (%#v)", got[0][2], got[0][2])
	}
}

// shouting stands in for a schema registry's decoder.
type shouting struct{}

func (shouting) Name() string               { return "Shouting" }
func (shouting) Decode([]byte) (any, error) { return "SHOUTED", nil }

// The decoded columns say they hold text, and are named after the decoder,
// so that a file says how it was read.
func TestTheDecodedColumnsSayWhatTheyHold(t *testing.T) {
	rows := newRecords(model.Row{int64(1), nil, []byte("x")})
	got := DecodeRecords(rows, RecordReader{}, RecordReader{Chosen: shouting{}}).Columns()
	if len(got) != 3 {
		t.Fatalf("the columns are %+v", got)
	}
	if got[0].Type.Class != model.TypeInteger {
		t.Errorf("the offset column became %+v", got[0].Type)
	}
	if got[1].Type.Class != model.TypeString || got[1].Type.Native != "decoded" {
		t.Errorf("the key column is %+v", got[1].Type)
	}
	if got[2].Type.Class != model.TypeString || got[2].Type.Native != "Shouting" {
		t.Errorf("the value column is %+v", got[2].Type)
	}
}

// A key or value column that is not bytes is left as it is: nothing
// decodes what was never encoded.
func TestAColumnThatIsNotBytesIsLeftAlone(t *testing.T) {
	rows := &records{cols: []model.ColumnDef{
		{Name: "offset", Type: model.DataType{Class: model.TypeInteger}},
		{Name: "key", Type: model.DataType{Class: model.TypeString, Native: "text"}},
		{Name: "value", Type: model.DataType{Class: model.TypeString, Native: "text"}},
	}}
	got := DecodeRecords(rows, RecordReader{}, RecordReader{Chosen: shouting{}}).Columns()
	for i, c := range got[1:] {
		if c.Type.Native != "text" {
			t.Errorf("column %d became %+v", i+1, c.Type)
		}
	}
}

// The columns a stream reports are its own, and come back unchanged: two
// readers of the same rows read the same columns.
func TestDecodingLeavesTheColumnsItWasGiven(t *testing.T) {
	rows := newRecords()
	DecodeRecords(rows, RecordReader{}, RecordReader{}).Columns()
	if got := rows.Columns()[2].Type.Class; got != model.TypeBytes {
		t.Errorf("the stream it wrapped now says its value column holds %v", got)
	}
}

// Rows with no key or value pass through untouched, so this is safe over
// any rows at all.
func TestRowsThatAreNotRecordsPassThrough(t *testing.T) {
	rows := &records{rows: []model.Row{{int64(1), "a"}}, cols: []model.ColumnDef{
		{Name: "id", Type: model.DataType{Class: model.TypeInteger}},
		{Name: "name", Type: model.DataType{Class: model.TypeString}},
	}}
	out := DecodeRecords(rows, RecordReader{}, RecordReader{})
	if got := readAll(t, out); len(got) != 1 || got[0][1] != "a" {
		t.Errorf("it read %+v", got)
	}
	if got := out.Columns(); got[1].Type.Class != model.TypeString {
		t.Errorf("the columns are %+v", got)
	}
}

// The rows a reader is given are its caller's, and come back unchanged: a
// record read once into a window and once into a file is the same record
// both times.
func TestDecodingLeavesTheRowsItWasGiven(t *testing.T) {
	rows := []model.Row{{int64(1), []byte("k"), []byte(`{"id":7}`)}}
	src := &records{rows: rows, cols: newRecords().cols}
	readAll(t, DecodeRecords(src, RecordReader{}, RecordReader{}))
	if _, ok := rows[0][2].([]byte); !ok {
		t.Errorf("the row it was given now holds %T", rows[0][2])
	}
}
