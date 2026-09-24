package cockroach

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// resultFormats asks for every column in TEXT format.
//
// Every column, not the three named below: pgx looks each column's OID up
// in this map and reads a missing one as 0, which is TEXT. So what matters
// is that the map is passed at all — without it pgx asks for each type's
// preferred format, which is binary for most of them. The three are named
// because they are the ones it would go wrong for:
//
//   - numeric: the text is the exact decimal the server holds, padded to
//     the column's scale by the server itself, so a column of money reads
//     as 1.50 where the row holds 1.50. Binary decodes into pgtype.Numeric
//     and would have to be formatted back, with every chance of dropping
//     the zero somebody can see in the shell.
//   - json, jsonb: the text is the document itself, which is exactly
//     model.JSON. pgx's binary decoding unmarshals into Go maps, which
//     reorders keys and turns 1.0 into 1 — a document the database does
//     not hold.
//
// Text for the rest costs a little width on the wire and nothing else:
// pgx decodes a text bytea, timestamp or array as faithfully as a binary
// one, which the live tests read back column by column.
var resultFormats = pgx.QueryResultFormatsByOID{
	pgtype.NumericOID: pgtype.TextFormatCode,
	pgtype.JSONOID:    pgtype.TextFormatCode,
	pgtype.JSONBOID:   pgtype.TextFormatCode,
}

// rawText reports whether a column's value is taken from its raw text
// bytes rather than from what pgx decoded.
func rawText(oid uint32) bool {
	return oid == pgtype.NumericOID || oid == pgtype.JSONOID || oid == pgtype.JSONBOID
}

type typeInfo struct {
	class  model.TypeClass
	native string
}

// knownTypes is the wire's word for a column's type. CockroachDB answers
// with PostgreSQL's own OIDs, so these are the same numbers — but only the
// types this engine has are here, because a type it cannot hold is one
// nothing could ever prove.
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
	pgtype.InetOID:        {model.TypeNetwork, "inet"},
	pgtype.BitOID:         {model.TypeBit, "bit"},
	pgtype.VarbitOID:      {model.TypeBit, "bit varying"},
}

var arrayElements = map[uint32]uint32{
	pgtype.BoolArrayOID:        pgtype.BoolOID,
	pgtype.Int2ArrayOID:        pgtype.Int2OID,
	pgtype.Int4ArrayOID:        pgtype.Int4OID,
	pgtype.Int8ArrayOID:        pgtype.Int8OID,
	pgtype.TextArrayOID:        pgtype.TextOID,
	pgtype.VarcharArrayOID:     pgtype.VarcharOID,
	pgtype.Float4ArrayOID:      pgtype.Float4OID,
	pgtype.Float8ArrayOID:      pgtype.Float8OID,
	pgtype.NumericArrayOID:     pgtype.NumericOID,
	pgtype.UUIDArrayOID:        pgtype.UUIDOID,
	pgtype.JSONBArrayOID:       pgtype.JSONBOID,
	pgtype.ByteaArrayOID:       pgtype.ByteaOID,
	pgtype.DateArrayOID:        pgtype.DateOID,
	pgtype.TimestamptzArrayOID: pgtype.TimestamptzOID,
	pgtype.InetArrayOID:        pgtype.InetOID,
}

// dataType maps a result column's OID and type modifier to the canonical
// type.
//
// An OID outside this table — a user-defined enum, or one of the spatial
// types — gets TypeUnknown with the OID as its name, and the source
// resolves the real name from pg_type once per type, because a column
// labelled "oid 100123" instead of "order_status" tells nobody anything.
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

// classOf is a type's class from the name the catalogue gives it, for the
// types the OID table does not know: an enum is a string somebody bounded,
// and a spatial value is a geometry.
//
// There is no case here for a domain. CockroachDB has none — CREATE DOMAIN
// is not a statement it accepts — so a branch for one would be a branch
// nothing could ever reach.
func classOf(name, typtype string) model.TypeClass {
	if typtype == "e" {
		return model.TypeString
	}
	switch name {
	case "geometry", "geography":
		return model.TypeGeometry
	}
	return model.TypeUnknown
}

// normalize turns what pgx decoded into the closed set of values a row may
// hold (model.Row). Anything with no place in that set travels as its own
// text, which is what the grid shows and what a filter binds back.
func normalize(v any) any {
	switch x := v.(type) {
	case nil, bool, int64, float64, string, []byte, time.Time,
		model.Decimal, model.JSON:
		return v
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	case int:
		return int64(x)
	case float32:
		return float64(x)
	case [16]byte: // uuid
		return uuidText(x)
	case netip.Prefix:
		// An address with the whole of its mask is one address, and that
		// is how the server writes it: 192.168.0.1, not 192.168.0.1/32.
		// Adding the mask back would show a value the column does not
		// hold (FR-3.8).
		if x.Bits() == x.Addr().BitLen() {
			return x.Addr().String()
		}
		return x.String()
	case netip.Addr:
		return x.String()
	case *net.IPNet:
		return x.String()
	case net.IP:
		return x.String()
	case pgtype.Interval:
		return intervalText(x)
	case map[string]any:
		return x
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = normalize(x[i])
		}
		return out
	}
	return fmt.Sprint(v)
}

// uuidText writes a UUID the way everything else prints one.
func uuidText(b [16]byte) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for i, c := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out = append(out, '-')
		}
		out = append(out, hex[c>>4], hex[c&0x0f])
	}
	return string(out)
}

// intervalText writes an interval as the server's own text form, which is
// what a person reading the grid would have typed.
func intervalText(iv pgtype.Interval) string {
	if !iv.Valid {
		return ""
	}
	secs := iv.Microseconds / 1e6
	micros := iv.Microseconds % 1e6
	s := fmt.Sprintf("%d mons %d days %02d:%02d:%02d",
		iv.Months, iv.Days, secs/3600, (secs/60)%60, secs%60)
	if micros != 0 {
		s = fmt.Sprintf("%s.%06d", s, micros)
	}
	return s
}
