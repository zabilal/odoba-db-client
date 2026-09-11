package postgres

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// classOf classes a type found by name in pg_type. Enums and composites say
// what they are; PostGIS's geometry and geography are known by name, since
// their OIDs are given out when the extension is installed and so appear in
// no table here.
func classOf(name, typtype string) model.TypeClass {
	switch typtype {
	case "e":
		return model.TypeEnum
	case "c":
		return model.TypeStruct
	}
	switch name[strings.LastIndex(name, ".")+1:] {
	case "geometry", "geography":
		return model.TypeGeometry
	}
	return model.TypeUnknown
}

// pgGeometry reads a PostGIS value as it arrives: extended WKB, as hex text
// or as the bytes themselves. The SRID moves out of the WKB into the
// Geometry, so the WKB is plain and Geometry.String can name the SRID; ewkb
// puts it back for an INSERT.
func pgGeometry(raw []byte) (model.Geometry, error) {
	var b []byte
	if isHex(raw) {
		b = make([]byte, len(raw)/2)
		if _, err := hex.Decode(b, raw); err != nil {
			return model.Geometry{}, err
		}
	} else {
		b = append([]byte(nil), raw...) // pgx reuses its buffer
	}
	if len(b) < 5 {
		return model.Geometry{}, errors.New("postgres: a geometry shorter than its header")
	}
	var order binary.ByteOrder = binary.LittleEndian
	switch b[0] {
	case 0:
		order = binary.BigEndian
	case 1:
	default:
		return model.Geometry{}, fmt.Errorf("postgres: geometry byte order %d", b[0])
	}
	t := order.Uint32(b[1:5])
	if t&0x20000000 == 0 {
		return model.Geometry{WKB: b}, nil
	}
	if len(b) < 9 {
		return model.Geometry{}, errors.New("postgres: a geometry cut short in its SRID")
	}
	wkb := make([]byte, 5, len(b)-4)
	wkb[0] = b[0]
	order.PutUint32(wkb[1:5], t&^0x20000000)
	return model.Geometry{SRID: order.Uint32(b[5:9]), WKB: append(wkb, b[9:]...)}, nil
}

// isHex reports whether a value is hex text. Extended WKB in binary begins
// with a byte-order byte of 0 or 1, which no hex text does.
func isHex(b []byte) bool {
	if len(b) == 0 || len(b)%2 != 0 {
		return false
	}
	for _, c := range b {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}
