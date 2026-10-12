package export

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// YAML, as a list of records (FR-10.10): one mapping to a row, its keys the
// columns in their order.
//
// Written by hand rather than through a library, for the reason every writer
// here is: a row is written as it arrives and then forgotten, so memory does
// not grow with the rows (FR-10.3), and a library that builds a document
// builds all of it.
//
// What keeps a hand-written emitter honest is writing only the forms that
// cannot be misread. A string is always in double quotes, written as JSON
// writes one — which is YAML's own double-quoted form — so that "no", "1.0"
// and "2026-03-04" stay text whoever reads the file, and not a boolean, a
// number and a date. A number is plain, an exact one with every digit it had.
// What is made of other values is written as JSON too, which YAML reads as a
// flow collection. And the three numbers JSON has no way to write are written
// as YAML's own: .nan, .inf and -.inf.
type yamlWriter struct {
	w    *bufio.Writer
	cols []model.ColumnDef
	keys [][]byte
	n    int64
	buf  bytes.Buffer
}

func newYAML(w *bufio.Writer, cols []model.ColumnDef) *yamlWriter {
	y := &yamlWriter{w: w, cols: cols, keys: make([][]byte, len(cols))}
	for i, name := range uniqueNames(cols) {
		y.keys[i] = append(yamlKey(name), ':', ' ')
	}
	return y
}

// plainKey is a key that reads as itself with no quotes round it: a word,
// which cannot be taken for a number, a boolean or nothing.
var plainKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// reserved are the words YAML reads as something other than text, in one
// version of it or another. As a key each would still be a key, but of the
// wrong type: a column called "no" would come back as the boolean false.
var reserved = map[string]bool{
	"null": true, "Null": true, "NULL": true,
	"true": true, "True": true, "TRUE": true, "false": true, "False": true, "FALSE": true,
	"yes": true, "Yes": true, "YES": true, "no": true, "No": true, "NO": true,
	"on": true, "On": true, "ON": true, "off": true, "Off": true, "OFF": true,
	"y": true, "Y": true, "n": true, "N": true,
}

func yamlKey(name string) []byte {
	if plainKey.MatchString(name) && !reserved[name] {
		return []byte(name)
	}
	b, _ := marshal(name)
	return b
}

func (y *yamlWriter) row(r model.Row) error {
	if len(y.cols) == 0 {
		y.w.WriteString("- {}\n")
		y.n++
		return nil
	}
	for i := range y.cols {
		if i == 0 {
			y.w.WriteString("- ")
		} else {
			y.w.WriteString("  ")
		}
		y.w.Write(y.keys[i])
		var v any
		if i < len(r) {
			v = r[i]
		}
		y.value(v, y.cols[i])
		y.w.WriteByte('\n')
	}
	y.n++
	return nil
}

// end writes what a file with no rows has to say: an empty list, which an
// empty file is not.
func (y *yamlWriter) end() error {
	if y.n == 0 {
		y.w.WriteString("[]\n")
	}
	return nil
}

func (y *yamlWriter) value(v any, col model.ColumnDef) {
	switch x := v.(type) {
	case nil:
		y.w.WriteString("null")
	case bool:
		y.w.WriteString(strconv.FormatBool(x))
	case int64:
		y.w.WriteString(strconv.FormatInt(x, 10))
	case float64:
		switch {
		case math.IsNaN(x):
			y.w.WriteString(".nan")
		case math.IsInf(x, 1):
			y.w.WriteString(".inf")
		case math.IsInf(x, -1):
			y.w.WriteString("-.inf")
		default:
			y.w.WriteString(yamlFloat(x))
		}
	case model.Decimal:
		if isJSONNumber(string(x)) {
			y.w.WriteString(string(x))
		} else {
			y.str(string(x))
		}
	case model.Geometry:
		y.str(x.String())
	case model.JSON:
		y.buf.Reset()
		if json.Compact(&y.buf, x) == nil {
			y.w.Write(y.buf.Bytes())
		} else {
			y.str(string(x))
		}
	case string:
		y.str(x)
	case time.Time:
		y.str(formatTime(x, col.Type))
	case []byte:
		// YAML's own type for bytes, so that they come back as bytes and
		// not as the text of their encoding.
		y.w.WriteString("!!binary ")
		y.str(base64.StdEncoding.EncodeToString(x))
	default:
		if b, err := marshal(jsonable(v)); err == nil {
			y.w.Write(b)
		} else {
			y.str(fmt.Sprint(v))
		}
	}
}

// yamlFloat is a float that reads back as a float: the shortest text that is
// the same number, with a point in it where there would be none, because 3
// read back is an integer and the column was not one.
func yamlFloat(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	for i := 0; i < len(s); i++ {
		if s[i] == '.' || s[i] == 'e' {
			return s
		}
	}
	return s + ".0"
}

func (y *yamlWriter) str(s string) {
	b, _ := marshal(s)
	y.w.Write(b)
}
