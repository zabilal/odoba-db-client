package model

// TypeClass is the canonical, engine-independent classification of a value's
// type. The grid selects renderers and in-place editors from this, so that
// FR-3.8 (type-honest rendering) and FR-4.1 (correct editor per type) need no
// per-engine knowledge.
type TypeClass uint8

const (
	TypeUnknown TypeClass = iota
	TypeBool
	TypeInteger
	TypeFloat
	TypeDecimal
	TypeString
	TypeBytes
	TypeDate
	TypeTime
	TypeTimestamp
	TypeInterval
	TypeUUID
	TypeJSON
	TypeXML
	TypeArray
	TypeStruct // composite type, document, or map
	TypeEnum
	TypeGeometry
	TypeNetwork // inet, cidr, macaddr
	TypeBit
)

var typeClassNames = map[TypeClass]string{
	TypeUnknown:   "unknown",
	TypeBool:      "bool",
	TypeInteger:   "integer",
	TypeFloat:     "float",
	TypeDecimal:   "decimal",
	TypeString:    "string",
	TypeBytes:     "bytes",
	TypeDate:      "date",
	TypeTime:      "time",
	TypeTimestamp: "timestamp",
	TypeInterval:  "interval",
	TypeUUID:      "uuid",
	TypeJSON:      "json",
	TypeXML:       "xml",
	TypeArray:     "array",
	TypeStruct:    "struct",
	TypeEnum:      "enum",
	TypeGeometry:  "geometry",
	TypeNetwork:   "network",
	TypeBit:       "bit",
}

func (c TypeClass) String() string {
	if n, ok := typeClassNames[c]; ok {
		return n
	}
	return "unknown"
}

// Numeric reports whether the class is right-aligned and chart-eligible.
func (c TypeClass) Numeric() bool {
	switch c {
	case TypeInteger, TypeFloat, TypeDecimal:
		return true
	}
	return false
}

// Temporal reports whether values carry a point or span in time.
func (c TypeClass) Temporal() bool {
	switch c {
	case TypeDate, TypeTime, TypeTimestamp, TypeInterval:
		return true
	}
	return false
}

// Structured reports whether the value expands into a nested viewer (FR-3.9).
func (c TypeClass) Structured() bool {
	switch c {
	case TypeJSON, TypeXML, TypeArray, TypeStruct:
		return true
	}
	return false
}

// DataType describes a column or field's type.
//
// Native is preserved verbatim and is what the UI shows the user — a Postgres
// user expects to see "timestamptz", not "timestamp". Class is what code
// branches on.
type DataType struct {
	Class  TypeClass
	Native string

	Nullable bool

	// Length is -1 when not applicable or unknown.
	Length    int64
	Precision int32
	Scale     int32

	// TimeZone reports whether a temporal type carries a zone.
	TimeZone bool

	// Element is the member type of TypeArray.
	Element *DataType

	// Fields are the members of TypeStruct.
	Fields []FieldDef

	// EnumValues are the permitted labels of TypeEnum.
	EnumValues []string
}

// FieldDef is a named member of a composite or document type.
type FieldDef struct {
	Name string
	Type DataType
}

// Unknown returns a permissive type for sources that cannot describe a value.
func Unknown(native string) DataType {
	return DataType{Class: TypeUnknown, Native: native, Nullable: true, Length: -1}
}
