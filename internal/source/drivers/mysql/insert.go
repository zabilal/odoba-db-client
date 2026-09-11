package mysql

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
		return "", fmt.Errorf("mysql: %w", err)
	}
	return s, nil
}

var plainNumber = regexp.MustCompile(`^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$`)

// insertLiteral writes a value so that MySQL or MariaDB reads it back as the
// same value in a column of type t. Times are read in UTC (see the source's
// connection settings), so they are written as UTC wall-clock text.
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
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return "", errors.New("MySQL has no NaN or infinity")
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
		return "X'" + hex.EncodeToString(x) + "'", nil
	case time.Time:
		switch {
		case x.IsZero() && t.Class == model.TypeDate:
			return "'0000-00-00'", nil
		case x.IsZero():
			return "'0000-00-00 00:00:00'", nil
		case t.Class == model.TypeDate:
			return x.Format("'2006-01-02'"), nil
		}
		return x.UTC().Format("'2006-01-02 15:04:05.999999'"), nil
	case []any, map[string]any:
		b, err := json.Marshal(x)
		if err != nil {
			return "", err
		}
		return quoteText(string(b)), nil
	}
	return "", fmt.Errorf("a %s value cannot be written as a literal yet", t.Native)
}

// quoteText writes text as a string literal that means the same under every
// sql_mode. Doubling a quote does. What a backslash means depends on
// NO_BACKSLASH_ESCAPES, so text holding one, or a control character, is
// written in hex with its character set named instead.
func quoteText(s string) string {
	if strings.ContainsAny(s, "\\\x00\x1a") {
		return "_utf8mb4 X'" + hex.EncodeToString([]byte(s)) + "'"
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
