package cockroach

import (
	"net/netip"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// The wire says which type a column is, and CockroachDB answers with
// PostgreSQL's own numbers.
func TestAColumnsTypeIsReadFromTheWire(t *testing.T) {
	for _, c := range []struct {
		oid    uint32
		typmod int32
		class  model.TypeClass
		native string
	}{
		{pgtype.BoolOID, -1, model.TypeBool, "boolean"},
		{pgtype.Int8OID, -1, model.TypeInteger, "bigint"},
		{pgtype.Float8OID, -1, model.TypeFloat, "double precision"},
		{pgtype.TextOID, -1, model.TypeString, "text"},
		{pgtype.ByteaOID, -1, model.TypeBytes, "bytea"},
		{pgtype.DateOID, -1, model.TypeDate, "date"},
		{pgtype.UUIDOID, -1, model.TypeUUID, "uuid"},
		{pgtype.JSONBOID, -1, model.TypeJSON, "jsonb"},
		{pgtype.InetOID, -1, model.TypeNetwork, "inet"},
		{pgtype.IntervalOID, -1, model.TypeInterval, "interval"},
		// The width is part of the name, because a column of ten places is
		// not the same column as one of thirty.
		{pgtype.NumericOID, (30<<16 | 10) + 4, model.TypeDecimal, "numeric(30,10)"},
		{pgtype.VarcharOID, 20 + 4, model.TypeString, "character varying(20)"},
	} {
		got := dataType(c.oid, c.typmod)
		if got.Class != c.class || got.Native != c.native {
			t.Errorf("oid %d reads as %v %q, want %v %q", c.oid, got.Class, got.Native, c.class, c.native)
		}
	}
}

// A time that carries a zone says so, and one that does not says nothing.
func TestATimeSaysWhetherItCarriesAZone(t *testing.T) {
	if !dataType(pgtype.TimestamptzOID, -1).TimeZone {
		t.Error("a timestamptz does not say it carries a zone")
	}
	if dataType(pgtype.TimestampOID, -1).TimeZone {
		t.Error("a plain timestamp claims to carry a zone")
	}
}

// An unconstrained number keeps the plain name: numeric, not numeric(0,0).
func TestAnUnconstrainedNumberIsJustANumber(t *testing.T) {
	if got := dataType(pgtype.NumericOID, -1).Native; got != "numeric" {
		t.Errorf("an unconstrained number reads as %q", got)
	}
	if got := dataType(pgtype.VarcharOID, -1).Native; got != "character varying" {
		t.Errorf("an unbounded string reads as %q", got)
	}
}

// A list says what it is a list of.
func TestAListSaysWhatItHolds(t *testing.T) {
	got := dataType(pgtype.TextArrayOID, -1)
	if got.Class != model.TypeArray || got.Native != "text[]" {
		t.Fatalf("a list of text reads as %v %q", got.Class, got.Native)
	}
	if got.Element == nil || got.Element.Class != model.TypeString {
		t.Errorf("its element reads as %+v", got.Element)
	}
}

// A type the table does not know keeps its number, so that the source can
// ask the catalogue for its real name rather than showing nothing.
func TestAnUnknownTypeKeepsItsNumber(t *testing.T) {
	got := dataType(100123, -1)
	if got.Class != model.TypeUnknown || got.Native != "oid 100123" {
		t.Errorf("an unknown type reads as %v %q", got.Class, got.Native)
	}
}

// An enum is a string somebody bounded; a spatial value is a geometry.
func TestATypeTheCatalogueNames(t *testing.T) {
	for _, c := range []struct {
		name, typtype string
		want          model.TypeClass
	}{
		{"order_status", "e", model.TypeString},
		{"geometry", "b", model.TypeGeometry},
		{"geography", "b", model.TypeGeometry},
		{"something", "b", model.TypeUnknown},
		// An enum is a string whatever it is called, and a type merely
		// called geometry is not one unless the engine says so.
		{"geometry", "e", model.TypeString},
	} {
		if got := classOf(c.name, c.typtype); got != c.want {
			t.Errorf("classOf(%q, %q) = %v, want %v", c.name, c.typtype, got, c.want)
		}
	}
}

// An exact number and a document are read from their text, because
// decoding either loses something somebody can see.
func TestWhatIsReadFromItsText(t *testing.T) {
	for _, oid := range []uint32{pgtype.NumericOID, pgtype.JSONOID, pgtype.JSONBOID} {
		if !rawText(oid) {
			t.Errorf("oid %d is decoded rather than read as text", oid)
		}
	}
	for _, oid := range []uint32{pgtype.Int8OID, pgtype.TextOID, pgtype.TimestamptzOID} {
		if rawText(oid) {
			t.Errorf("oid %d is read as text where it should be decoded", oid)
		}
	}
	// The bytes are copied: pgx reuses that buffer on the next row.
	raw := []byte(`{"a":1}`)
	got := decode(nil, raw, pgtype.JSONBOID)
	doc, ok := got.(model.JSON)
	if !ok || string(doc) != `{"a":1}` {
		t.Fatalf("a document reads as %#v", got)
	}
	raw[0] = 'X'
	if string(doc) != `{"a":1}` {
		t.Errorf("the document moved when the buffer was reused: %s", doc)
	}
	if got := decode(nil, []byte("1.50"), pgtype.NumericOID); got != model.Decimal("1.50") {
		t.Errorf("an exact number reads as %#v", got)
	}
	if got := decode(nil, nil, pgtype.NumericOID); got != nil {
		t.Errorf("nothing reads as %#v", got)
	}
}

// Every value the grid holds is one of a closed set, and anything else
// travels as its own text (model.Row).
func TestAValueIsNarrowedToWhatARowMayHold(t *testing.T) {
	for _, c := range []struct {
		in   any
		want any
	}{
		{nil, nil},
		{true, true},
		{int16(1), int64(1)},
		{int32(1), int64(1)},
		{int(1), int64(1)},
		{int64(1), int64(1)},
		{float32(1.5), float64(1.5)},
		{float64(1.5), float64(1.5)},
		{"x", "x"},
		{[16]byte{0x6f, 0x96, 0x19, 0xff, 0x8b, 0x86, 0xd0, 0x11,
			0xb4, 0x2d, 0x00, 0xc0, 0x4f, 0xc9, 0x64, 0xff},
			"6f9619ff-8b86-d011-b42d-00c04fc964ff"},
	} {
		if got := normalize(c.in); got != c.want {
			t.Errorf("normalize(%#v) = %#v, want %#v", c.in, got, c.want)
		}
	}
	if got := normalize(time.Unix(0, 0).UTC()); got != time.Unix(0, 0).UTC() {
		t.Errorf("a time was changed on the way through: %#v", got)
	}
	// A list is narrowed all the way down, not only at its surface.
	got := normalize([]any{int32(1), int16(2)})
	list, ok := got.([]any)
	if !ok || len(list) != 2 || list[0] != int64(1) || list[1] != int64(2) {
		t.Errorf("a list reads as %#v", got)
	}
}

// An address with the whole of its mask is one address, and that is how
// the server writes it (FR-3.8).
func TestAnAddressKeepsOnlyTheMaskItHas(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"192.168.0.1/32", "192.168.0.1"},
		{"192.168.0.0/24", "192.168.0.0/24"},
		{"2001:db8::1/128", "2001:db8::1"},
		{"2001:db8::/32", "2001:db8::/32"},
	} {
		p := netip.MustParsePrefix(c.in)
		if got := normalize(p); got != c.want {
			t.Errorf("%s reads as %v, want %s", c.in, got, c.want)
		}
	}
	if got := normalize(netip.MustParseAddr("10.0.0.1")); got != "10.0.0.1" {
		t.Errorf("a bare address reads as %v", got)
	}
}

// An interval is written the way the server writes one, because that is
// what whoever reads the grid would have typed.
func TestAnIntervalIsWrittenAsTheServerWritesIt(t *testing.T) {
	for _, c := range []struct {
		in   pgtype.Interval
		want string
	}{
		{pgtype.Interval{Months: 1, Days: 2, Microseconds: 3723000000, Valid: true},
			"1 mons 2 days 01:02:03"},
		{pgtype.Interval{Microseconds: 1500000, Valid: true}, "0 mons 0 days 00:00:01.500000"},
		{pgtype.Interval{}, ""},
	} {
		if got := normalize(c.in); got != c.want {
			t.Errorf("%+v reads as %q, want %q", c.in, got, c.want)
		}
	}
}
