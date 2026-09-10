package postgres

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

func TestDataTypeDecodesTypmods(t *testing.T) {
	// numeric(12,2) arrives as typmod (12<<16 | 2) + 4.
	n := dataType(pgtype.NumericOID, (12<<16|2)+4)
	if n.Precision != 12 || n.Scale != 2 || n.Native != "numeric(12,2)" {
		t.Errorf("numeric: %+v", n)
	}
	if u := dataType(pgtype.NumericOID, -1); u.Native != "numeric" {
		t.Errorf("unconstrained numeric: %q", u.Native)
	}
	if v := dataType(pgtype.VarcharOID, 64+4); v.Length != 64 || v.Native != "character varying(64)" {
		t.Errorf("varchar: %+v", v)
	}
	if ts := dataType(pgtype.TimestamptzOID, -1); !ts.TimeZone || ts.Class != model.TypeTimestamp {
		t.Errorf("timestamptz: %+v", ts)
	}
}

func TestDataTypeArraysAndUnknown(t *testing.T) {
	a := dataType(pgtype.Int4ArrayOID, -1)
	if a.Class != model.TypeArray || a.Element == nil || a.Element.Class != model.TypeInteger || a.Native != "integer[]" {
		t.Errorf("int4[]: %+v", a)
	}
	u := dataType(16394, -1)
	if u.Class != model.TypeUnknown || u.Native != "oid 16394" {
		t.Errorf("unknown: %+v", u)
	}
}

// allowed is the closed set model.Row documents.
func allowed(v any) bool {
	switch x := v.(type) {
	case nil, bool, int64, float64, string, []byte, time.Time, model.Decimal, model.JSON:
		return true
	case []any:
		for _, e := range x {
			if !allowed(e) {
				return false
			}
		}
		return true
	case map[string]any:
		for _, e := range x {
			if !allowed(e) {
				return false
			}
		}
		return true
	}
	return false
}

func TestNormalizeProducesOnlyTheClosedSet(t *testing.T) {
	inputs := []any{
		nil, true, int16(1), int32(2), int64(3), uint32(4), float32(1.5), 2.5, "s",
		[]byte{1, 2}, time.Now(), [16]byte{1},
		pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
		netip.MustParsePrefix("10.0.0.0/8"), netip.MustParseAddr("::1"),
		net.HardwareAddr{1, 2, 3, 4, 5, 6},
		pgtype.Interval{Months: 14, Days: 3, Microseconds: 3723_500_000, Valid: true},
		pgtype.Time{Microseconds: 3723_000_000, Valid: true},
		[]any{int32(1), nil, pgtype.UUID{Valid: true}},
		map[string]any{"a": int16(1)},
		map[string]*string{"k": nil},
		struct{ X int }{1}, // an unfamiliar type must still become legible text
	}
	for _, in := range inputs {
		if out := normalize(in); !allowed(out) {
			t.Errorf("normalize(%T) = %T, outside the closed set", in, out)
		}
	}
}

func TestNormalizeRendersLikePsql(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{[16]byte{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0, 1, 2, 3, 4, 5, 6, 7, 8},
			"12345678-9abc-def0-0102-030405060708"},
		{pgtype.Interval{Months: 14, Days: 3, Microseconds: 3723_500_000, Valid: true},
			"1 year 2 mons 3 days 01:02:03.5"},
		{pgtype.Interval{Valid: true}, "00:00:00"},
		{pgtype.Interval{Days: 1, Valid: true}, "1 day"},
		{pgtype.Interval{Microseconds: -90_000_000, Valid: true}, "-00:01:30"},
	}
	for _, c := range cases {
		if got := normalize(c.in); got != c.want {
			t.Errorf("normalize(%#v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeCopiesBytes(t *testing.T) {
	// pgx reuses its row buffer, so a []byte that is not copied changes under
	// the grid when the next row is read.
	src := []byte{1, 2, 3}
	out := normalize(src).([]byte)
	src[0] = 9
	if out[0] != 1 {
		t.Error("normalize aliased the input buffer")
	}
}

func TestInvalidPgtypeValuesAreNull(t *testing.T) {
	for _, in := range []any{pgtype.UUID{}, pgtype.Interval{}, pgtype.Time{}} {
		if normalize(in) != nil {
			t.Errorf("invalid %T should be NULL", in)
		}
	}
}
