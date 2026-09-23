package sqlserver

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
)

// InsertRows writes rows as the INSERT statements that would add them to
// ref, one a row, for Copy as INSERT (source.RowScripter, FR-3.7).
func (d dialect) InsertRows(ref model.ObjectRef, cols []model.ColumnDef, rows []model.Row) (string, error) {
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = d.QuoteIdentifier(c.Name)
	}
	s, err := sqlscript.Inserts(d.QualifyRef(ref), names, cols, rows, insertLiteral)
	if err != nil {
		return "", fmt.Errorf("sqlserver: %w", err)
	}
	return s, nil
}

var plainNumber = regexp.MustCompile(`^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$`)

// insertLiteral writes a value so that SQL Server reads it back as the same
// value in a column of type t.
func insertLiteral(v any, t model.DataType) (string, error) {
	switch x := v.(type) {
	case nil:
		return "NULL", nil
	case bool:
		// A bit is a number: SQL Server has no boolean to write.
		if x {
			return "1", nil
		}
		return "0", nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return "", errors.New("SQL Server has no NaN or infinity")
		}
		return strconv.FormatFloat(x, 'g', -1, 64), nil
	case model.Decimal:
		if plainNumber.MatchString(string(x)) {
			return string(x), nil
		}
		return quoteText(string(x)), nil
	case string:
		return quoteText(x), nil
	case model.JSON:
		return quoteText(string(x)), nil
	case []byte:
		return "0x" + hex.EncodeToString(x), nil
	case time.Time:
		switch {
		case t.Class == model.TypeDate:
			return x.Format("'2006-01-02'"), nil
		case t.Class == model.TypeTime:
			return x.Format("'15:04:05.9999999'"), nil
		case t.TimeZone:
			// A datetimeoffset keeps the offset it was read with, which is
			// part of the value rather than a way of writing it.
			return x.Format("'2006-01-02 15:04:05.9999999 -07:00'"), nil
		}
		return x.Format("'2006-01-02 15:04:05.9999999'"), nil
	case []any, map[string]any:
		b, err := json.Marshal(x)
		if err != nil {
			return "", err
		}
		return quoteText(string(b)), nil
	}
	return "", fmt.Errorf("a %s value cannot be written as a literal yet", t.Native)
}

// quoteText writes text as a Unicode string literal, doubling the quote that
// would end it. T-SQL has no escape character inside one, so a newline is
// written as itself.
//
// Except for a NUL, which cannot be written at all: a literal is text the
// parser reads, and it ends there. Text holding one is written as the bytes
// it is made of instead — UTF-16, little-endian, which is what an nvarchar
// is — and converted back.
func quoteText(s string) string {
	if strings.ContainsRune(s, 0) {
		var b []byte
		for _, u := range utf16.Encode([]rune(s)) {
			b = append(b, byte(u), byte(u>>8))
		}
		return "CONVERT(nvarchar(max), 0x" + hex.EncodeToString(b) + ")"
	}
	return "N'" + strings.ReplaceAll(s, "'", "''") + "'"
}
