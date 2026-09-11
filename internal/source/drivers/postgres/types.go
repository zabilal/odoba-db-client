package postgres

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// resultFormats asks for three types in TEXT format rather than binary.
//
//   - numeric: text is the exact decimal PostgreSQL holds. Binary decodes into
//     pgtype.Numeric and would have to be formatted back, with every chance of
//     losing a trailing zero the user can see in psql.
//   - json, jsonb: text is the document itself, which is exactly model.JSON.
//     Binary jsonb carries a version byte, and pgx's default decoding
//     unmarshals into Go maps — which reorders keys and turns 1.0 into 1, so
//     the grid would show a document the database does not hold.
var resultFormats = pgx.QueryResultFormatsByOID{
	pgtype.NumericOID: pgtype.TextFormatCode,
	pgtype.JSONOID:    pgtype.TextFormatCode,
	pgtype.JSONBOID:   pgtype.TextFormatCode,
}

// rawText reports whether a column's value is taken from its raw text bytes.
func rawText(oid uint32) bool {
	return oid == pgtype.NumericOID || oid == pgtype.JSONOID || oid == pgtype.JSONBOID
}

type typeInfo struct {
	class  model.TypeClass
	native string
}

var knownTypes = map[uint32]typeInfo{
	pgtype.BoolOID:        {model.TypeBool, "boolean"},
	pgtype.Int2OID:        {model.TypeInteger, "smallint"},
	pgtype.Int4OID:        {model.TypeInteger, "integer"},
	pgtype.Int8OID:        {model.TypeInteger, "bigint"},
	pgtype.OIDOID:         {model.TypeInteger, "oid"},
	pgtype.Float4OID:      {model.TypeFloat, "real"},
	pgtype.Float8OID:      {model.TypeFloat, "double precision"},
	pgtype.NumericOID:     {model.TypeDecimal, "numeric"},
	pgtype.TextOID:        {model.TypeString, "text"},
	pgtype.VarcharOID:     {model.TypeString, "character varying"},
	pgtype.BPCharOID:      {model.TypeString, "character"},
	pgtype.NameOID:        {model.TypeString, "name"},
	pgtype.QCharOID:       {model.TypeString, "\"char\""},
	pgtype.ByteaOID:       {model.TypeBytes, "bytea"},
	pgtype.DateOID:        {model.TypeDate, "date"},
	pgtype.TimeOID:        {model.TypeTime, "time"},
	pgtype.TimetzOID:      {model.TypeTime, "time with time zone"},
	pgtype.TimestampOID:   {model.TypeTimestamp, "timestamp"},
	pgtype.TimestamptzOID: {model.TypeTimestamp, "timestamptz"},
	pgtype.IntervalOID:    {model.TypeInterval, "interval"},
	pgtype.UUIDOID:        {model.TypeUUID, "uuid"},
	pgtype.JSONOID:        {model.TypeJSON, "json"},
	pgtype.JSONBOID:       {model.TypeJSON, "jsonb"},
	pgtype.XMLOID:         {model.TypeXML, "xml"},
	pgtype.InetOID:        {model.TypeNetwork, "inet"},
	pgtype.CIDROID:        {model.TypeNetwork, "cidr"},
	pgtype.MacaddrOID:     {model.TypeNetwork, "macaddr"},
	pgtype.BitOID:         {model.TypeBit, "bit"},
	pgtype.VarbitOID:      {model.TypeBit, "bit varying"},
	pgtype.PointOID:       {model.TypeGeometry, "point"},
}

var arrayElements = map[uint32]uint32{
	pgtype.BoolArrayOID:    pgtype.BoolOID,
	pgtype.Int2ArrayOID:    pgtype.Int2OID,
	pgtype.Int4ArrayOID:    pgtype.Int4OID,
	pgtype.Int8ArrayOID:    pgtype.Int8OID,
	pgtype.TextArrayOID:    pgtype.TextOID,
	pgtype.VarcharArrayOID: pgtype.VarcharOID,
	pgtype.Float8ArrayOID:  pgtype.Float8OID,
	pgtype.NumericArrayOID: pgtype.NumericOID,
	pgtype.UUIDArrayOID:    pgtype.UUIDOID,
	pgtype.JSONBArrayOID:   pgtype.JSONBOID,
}

// dataType maps a result column's OID and type modifier to the canonical type.
//
// OIDs outside this table — enums, domains, composites, extension types — get
// TypeUnknown with the OID as the native name. The source resolves the real
// name from pg_type once per type (see source.go), because a column labelled
// "oid 16394" instead of "order_status" tells the user nothing.
func dataType(oid uint32, typmod int32) model.DataType {
	if elem, ok := arrayElements[oid]; ok {
		e := dataType(elem, -1)
		return model.DataType{Class: model.TypeArray, Native: e.Native + "[]",
			Nullable: true, Length: -1, Element: &e}
	}
	info, ok := knownTypes[oid]
	if !ok {
		return model.DataType{Class: model.TypeUnknown,
			Native: "oid " + strconv.FormatUint(uint64(oid), 10), Nullable: true, Length: -1}
	}

	dt := model.DataType{Class: info.class, Native: info.native, Nullable: true, Length: -1}
	switch oid {
	case pgtype.TimestamptzOID, pgtype.TimetzOID:
		dt.TimeZone = true
	case pgtype.NumericOID:
		// typmod packs (precision << 16 | scale) + 4; -1 means unconstrained.
		if typmod >= 4 {
			dt.Precision = int32(((typmod - 4) >> 16) & 0xffff)
			dt.Scale = int32((typmod - 4) & 0xffff)
			dt.Native = fmt.Sprintf("numeric(%d,%d)", dt.Precision, dt.Scale)
		}
	case pgtype.VarcharOID, pgtype.BPCharOID:
		if typmod >= 4 {
			dt.Length = int64(typmod - 4)
			dt.Native = fmt.Sprintf("%s(%d)", info.native, dt.Length)
		}
	}
	return dt
}

// normalize narrows a decoded value to the closed set model.Row permits:
//
//	bool, int64, float64, model.Decimal, string, []byte, time.Time,
//	model.JSON, []any, map[string]any
//
// The grid type-switches only on that set, so anything outside it — a pgx
// interval, an inet prefix, a UUID byte array — is rendered here, once, in the
// form PostgreSQL itself would print. The fallback is fmt.Sprint rather than a
// panic: an unfamiliar type should draw as legible text, never as a crash.
func normalize(v any) any {
	switch x := v.(type) {
	case nil, bool, int64, float64, string, time.Time, model.Decimal, model.JSON:
		return x
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	case int:
		return int64(x)
	case uint32:
		return int64(x)
	case float32:
		return float64(x)
	case []byte:
		return append([]byte(nil), x...)
	case [16]byte:
		return formatUUID(x)
	case pgtype.UUID:
		if !x.Valid {
			return nil
		}
		return formatUUID(x.Bytes)
	case netip.Prefix:
		return x.String()
	case netip.Addr:
		return x.String()
	case net.HardwareAddr:
		return x.String()
	case pgtype.Interval:
		if !x.Valid {
			return nil
		}
		return formatInterval(x)
	case pgtype.Time:
		if !x.Valid {
			return nil
		}
		d := time.Duration(x.Microseconds) * time.Microsecond
		s := fmt.Sprintf("%02d:%02d:%02d", int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60)
		if us := x.Microseconds % 1_000_000; us != 0 {
			s += strings.TrimRight(fmt.Sprintf(".%06d", us), "0") // exact, as psql shows it
		}
		return s
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalize(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = normalize(e)
		}
		return out
	case map[string]*string: // hstore
		out := make(map[string]any, len(x))
		for k, e := range x {
			if e == nil {
				out[k] = nil
			} else {
				out[k] = *e
			}
		}
		return out
	case fmt.Stringer:
		return x.String()
	}
	return fmt.Sprint(v)
}

func formatUUID(b [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// formatInterval renders an interval the way psql does by default.
func formatInterval(iv pgtype.Interval) string {
	var parts []string
	if y, m := iv.Months/12, iv.Months%12; y != 0 || m != 0 {
		if y != 0 {
			parts = append(parts, plural(int64(y), "year"))
		}
		if m != 0 {
			parts = append(parts, plural(int64(m), "mon"))
		}
	}
	if iv.Days != 0 {
		parts = append(parts, plural(int64(iv.Days), "day"))
	}
	if us := iv.Microseconds; us != 0 || len(parts) == 0 {
		sign := ""
		if us < 0 {
			sign, us = "-", -us
		}
		h, rem := us/3_600_000_000, us%3_600_000_000
		m, rem := rem/60_000_000, rem%60_000_000
		s, frac := rem/1_000_000, rem%1_000_000
		clock := fmt.Sprintf("%s%02d:%02d:%02d", sign, h, m, s)
		if frac != 0 {
			clock += strings.TrimRight(fmt.Sprintf(".%06d", frac), "0")
		}
		parts = append(parts, clock)
	}
	return strings.Join(parts, " ")
}

func plural(n int64, unit string) string {
	s := strconv.FormatInt(n, 10) + " " + unit
	if n != 1 && n != -1 {
		s += "s"
	}
	return s
}
