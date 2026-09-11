package sqlite

import (
	"encoding/hex"
	"encoding/json"
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
		return "", fmt.Errorf("sqlite: %w", err)
	}
	return s, nil
}

var plainNumber = regexp.MustCompile(`^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$`)

// insertLiteral writes a value so that SQLite reads it back as the same
// value in a column of type t. SQLite has no booleans, so they are 1 and 0;
// it stores NaN as NULL, so NaN is written so; and a time is written in the
// layout its date functions read, as a date alone when it is one.
func insertLiteral(v any, t model.DataType) (string, error) {
	switch x := v.(type) {
	case nil:
		return "NULL", nil
	case bool:
		if x {
			return "1", nil
		}
		return "0", nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case float64:
		switch {
		case math.IsNaN(x):
			return "NULL", nil
		case math.IsInf(x, 1):
			return "9e999", nil
		case math.IsInf(x, -1):
			return "-9e999", nil
		}
		s := strconv.FormatFloat(x, 'g', -1, 64)
		if !strings.ContainsAny(s, ".e") {
			s += ".0" // stays a real, even in a column of no type
		}
		return s, nil
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
		return "X'" + hex.EncodeToString(x) + "'", nil
	case time.Time:
		_, offset := x.Zone()
		midnight := x.Hour() == 0 && x.Minute() == 0 && x.Second() == 0 && x.Nanosecond() == 0
		switch {
		case t.Class == model.TypeDate && midnight && offset == 0:
			return x.Format("'2006-01-02'"), nil
		case offset == 0:
			return x.Format("'2006-01-02 15:04:05.999999999'"), nil
		}
		return x.Format("'2006-01-02 15:04:05.999999999-07:00'"), nil
	case []any, map[string]any:
		b, err := json.Marshal(x)
		if err != nil {
			return "", err
		}
		return quoteText(string(b)), nil
	}
	return "", fmt.Errorf("a %T value cannot be written as a literal yet", v)
}

// quoteText writes text as a quoted literal with its quotes doubled. A NUL
// would end a quoted literal early, so text holding one goes in hex.
func quoteText(s string) string {
	if strings.ContainsRune(s, 0) {
		return "CAST(X'" + hex.EncodeToString([]byte(s)) + "' AS TEXT)"
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
