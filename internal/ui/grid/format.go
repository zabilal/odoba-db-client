package grid

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Cell formatting.
//
// This is the hottest code in the application: it runs for every visible cell
// of every frame. It is also where UX principle 7 is enforced — NULL, empty
// string, zero and false must never be confusable.
//
// Deliberately free of any Fyne dependency so it can be benchmarked in
// isolation and so both renderer candidates share exactly the same formatting
// cost, making their comparison meaningful.

// CellKind selects the colour and style a renderer draws a value with.
type CellKind uint8

const (
	// CellNormal is ordinary text.
	CellNormal CellKind = iota
	// CellNull is SQL NULL. Drawn in the Null colour, italic, as the literal
	// word — never blank, which would be indistinguishable from an empty
	// string.
	CellNull
	// CellNumber is right-aligned and tabular.
	CellNumber
	// CellBool renders true/false.
	CellBool
	// CellTemporal is a date, time or timestamp.
	CellTemporal
	// CellStructured is JSON, XML, an array or a document — shown compacted
	// in the cell and expandable in the viewer (FR-3.9).
	CellStructured
	// CellBinary is a byte string, shown as a size and hex preview.
	CellBinary
	// CellPending marks a row that has not arrived yet.
	CellPending
)

// RightAligned reports whether the kind is drawn flush right. Numeric columns
// align on the decimal point by convention, which is most of what makes a
// numeric column scannable.
func (k CellKind) RightAligned() bool { return k == CellNumber }

// NullText is the literal drawn for a NULL. It is a word rather than a symbol
// so that it survives copy/paste and screen readers.
const NullText = "NULL"

// pendingText is drawn for a row still being fetched. An em-dash reads as
// "not here yet" rather than as data.
const pendingText = "—"

// MaxCellRunes caps how much of a long value is measured and drawn. Without a
// cap, one 40 KB text column would stall a frame; the full value is always
// available in the cell viewer.
const MaxCellRunes = 200

// Cell is a formatted value ready to draw.
type Cell struct {
	Text string
	Kind CellKind
	// Truncated reports that Text was cut at MaxCellRunes, so the renderer can
	// draw an ellipsis affordance rather than implying the value ends there.
	Truncated bool
}

// PendingCell is what a renderer draws for a row that has not loaded.
func PendingCell() Cell { return Cell{Text: pendingText, Kind: CellPending} }

// Format renders one value for display.
//
// col supplies the declared type, which matters because a source may hand back
// a nil interface for a typed NULL, and because a numeric may arrive as a
// string to preserve precision.
func Format(v any, col model.ColumnDef, loc *time.Location) Cell {
	if v == nil {
		return Cell{Text: NullText, Kind: CellNull}
	}

	switch x := v.(type) {
	case string:
		return textCell(x, col)
	case bool:
		if x {
			return Cell{Text: "true", Kind: CellBool}
		}
		return Cell{Text: "false", Kind: CellBool}
	case int64:
		return Cell{Text: strconv.FormatInt(x, 10), Kind: CellNumber}
	case int32:
		return Cell{Text: strconv.FormatInt(int64(x), 10), Kind: CellNumber}
	case int:
		return Cell{Text: strconv.Itoa(x), Kind: CellNumber}
	case float64:
		return Cell{Text: strconv.FormatFloat(x, 'g', -1, 64), Kind: CellNumber}
	case float32:
		return Cell{Text: strconv.FormatFloat(float64(x), 'g', -1, 32), Kind: CellNumber}
	case model.Decimal:
		// Carried as text precisely so no precision is lost; do not parse it.
		return Cell{Text: string(x), Kind: CellNumber}
	case time.Time:
		return temporalCell(x, col, loc)
	case model.JSON:
		return structuredCell(x)
	case []byte:
		return binaryCell(x)
	case []any:
		return compactCell(x)
	case map[string]any:
		return compactCell(x)
	}

	// An unexpected dynamic type is a driver bug, but the grid must still draw
	// something legible rather than blank.
	return textCell(stringify(v), col)
}

func textCell(s string, col model.ColumnDef) Cell {
	kind := CellNormal
	switch col.Type.Class {
	case model.TypeJSON, model.TypeXML:
		kind = CellStructured
	case model.TypeDecimal, model.TypeInteger, model.TypeFloat:
		// Numerics delivered as text keep their precision and their alignment.
		kind = CellNumber
	}
	return clip(s, kind)
}

func temporalCell(t time.Time, col model.ColumnDef, loc *time.Location) Cell {
	if loc != nil {
		t = t.In(loc)
	}
	var layout string
	switch col.Type.Class {
	case model.TypeDate:
		layout = "2006-01-02"
	case model.TypeTime:
		layout = "15:04:05"
	default:
		// Seconds resolution by default: sub-second digits are noise in a
		// grid, and the full value is in the cell viewer.
		layout = "2006-01-02 15:04:05"
	}
	return Cell{Text: t.Format(layout), Kind: CellTemporal}
}

func structuredCell(raw []byte) Cell {
	// Collapse whitespace so a pretty-printed document occupies one line. The
	// expanded form belongs in the cell viewer, not the grid.
	s := strings.Join(strings.Fields(string(raw)), " ")
	return clip(s, CellStructured)
}

func compactCell(v any) Cell {
	b, err := json.Marshal(v)
	if err != nil {
		return clip(stringify(v), CellStructured)
	}
	return clip(string(b), CellStructured)
}

func binaryCell(b []byte) Cell {
	const preview = 8
	n := len(b)
	if n > preview {
		b = b[:preview]
	}

	var sb strings.Builder
	sb.Grow(preview*3 + 16)
	const hexdigits = "0123456789abcdef"
	for i, c := range b {
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteByte(hexdigits[c>>4])
		sb.WriteByte(hexdigits[c&0x0F])
	}
	if n > preview {
		sb.WriteString(" … ")
		sb.WriteString(strconv.Itoa(n))
		sb.WriteString(" bytes")
	}
	return Cell{Text: sb.String(), Kind: CellBinary}
}

// clip caps a value at MaxCellRunes without splitting a rune.
func clip(s string, kind CellKind) Cell {
	// Fast path: ASCII shorter than the cap, which is the overwhelming
	// majority of cells.
	if len(s) <= MaxCellRunes {
		return Cell{Text: s, Kind: kind}
	}
	if utf8.RuneCountInString(s) <= MaxCellRunes {
		return Cell{Text: s, Kind: kind}
	}

	count := 0
	for i := range s {
		if count == MaxCellRunes {
			return Cell{Text: s[:i], Kind: kind, Truncated: true}
		}
		count++
	}
	return Cell{Text: s, Kind: kind}
}

func stringify(v any) string {
	if s, ok := v.(interface{ String() string }); ok {
		return s.String()
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "?"
	}
	return string(b)
}
