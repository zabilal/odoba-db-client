package postgres

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
)

// InsertRows writes rows as INSERT statements into ref, one a row, for Copy
// as INSERT (source.RowScripter, FR-3.7).
func (d dialect) InsertRows(ref model.ObjectRef, cols []model.ColumnDef, rows []model.Row) (string, error) {
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = d.QuoteIdentifier(c.Name)
	}
	s, err := sqlscript.Inserts(d.QualifyRef(ref), names, cols, rows, insertLiteral)
	if err != nil {
		return "", fmt.Errorf("postgres: %w", err)
	}
	return s, nil
}

var plainNumber = regexp.MustCompile(`^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$`)

// insertLiteral writes a value so that PostgreSQL reads it back as the same
// value in a column of type t. Text goes quoted, with quotes doubled; a
// literal of no stated type takes on the column's, so a uuid, an interval or
// a network address written as text arrives as itself.
func insertLiteral(v any, t model.DataType) (string, error) {
	switch x := v.(type) {
	case nil:
		return "NULL", nil
	case bool:
		if x {
			return "TRUE", nil
		}
		return "FALSE", nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case float64:
		switch {
		case math.IsNaN(x):
			return "'NaN'::float8", nil
		case math.IsInf(x, 1):
			return "'Infinity'::float8", nil
		case math.IsInf(x, -1):
			return "'-Infinity'::float8", nil
		}
		return strconv.FormatFloat(x, 'g', -1, 64), nil
	case model.Decimal:
		if plainNumber.MatchString(string(x)) {
			return string(x), nil
		}
		return quoteText(string(x)) // NaN, Infinity
	case string:
		return quoteText(x)
	case model.JSON:
		return quoteText(string(x))
	case []byte:
		return `'\x` + hex.EncodeToString(x) + `'::bytea`, nil
	case time.Time:
		switch {
		case t.Class == model.TypeDate:
			return x.Format("'2006-01-02'"), nil
		case t.TimeZone:
			return x.Format("'2006-01-02 15:04:05.999999-07:00'"), nil
		}
		return x.Format("'2006-01-02 15:04:05.999999'"), nil
	case []any:
		elem, cast := model.DataType{}, ""
		if t.Element != nil {
			elem = *t.Element
		}
		if t.Native != "" {
			cast = "::" + t.Native
		}
		if len(x) == 0 {
			return "'{}'" + cast, nil
		}
		parts := make([]string, len(x))
		for i, e := range x {
			lit, err := insertLiteral(e, elem)
			if err != nil {
				return "", err
			}
			parts[i] = lit
		}
		return "ARRAY[" + strings.Join(parts, ", ") + "]" + cast, nil
	}
	return "", fmt.Errorf("a %s value cannot be written as a literal yet", t.Native)
}

func quoteText(s string) (string, error) {
	if strings.ContainsRune(s, 0) {
		return "", errors.New("PostgreSQL text cannot hold a NUL character")
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'", nil
}
