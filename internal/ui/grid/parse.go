package grid

import (
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/export"
	"github.com/ikigai-db/ikigai-db/internal/model"
)

// A cell's editor (FR-4.1) starts from EditText, the value whole and in the
// form Parse reads, and what is typed is read back by Parse as the column's
// type.

const (
	dateLayout = "2006-01-02"
	timeLayout = "15:04:05.999999999"
	whenLayout = dateLayout + " " + timeLayout
)

// EditText is a value as its editor starts with it: whole, and in the form
// Parse reads. NULL is empty. An instant is in local time, as the grid shows
// it; a time with no zone is as stored.
func EditText(v any, col model.ColumnDef, loc *time.Location) string {
	t, ok := v.(time.Time)
	if !ok {
		return export.Text(v, col)
	}
	if col.Type.TimeZone && loc != nil {
		t = t.In(loc)
	}
	switch col.Type.Class {
	case model.TypeDate:
		return t.Format(dateLayout)
	case model.TypeTime:
		return t.Format(timeLayout)
	}
	return t.Format(whenLayout)
}

// Editable reports whether a column's cells are edited by typing. Bytes,
// arrays, composites and shapes are not: typed text is no way to write them.
func Editable(col model.ColumnDef) bool {
	switch col.Type.Class {
	case model.TypeBytes, model.TypeArray, model.TypeStruct, model.TypeGeometry:
		return false
	}
	return true
}

// textual reports whether a class holds text as typed, where empty text is
// the empty string rather than NULL.
func textual(c model.TypeClass) bool {
	return c == model.TypeString || c == model.TypeUnknown || c == model.TypeXML
}

var (
	decimalRE = regexp.MustCompile(`^[+-]?(\d+(\.\d*)?|\.\d+)([eE][+-]?\d+)?$`)
	uuidRE    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// Parse reads text typed into a cell as its column's type. Empty text is
// NULL, except in a column of text, where it is the empty string; a column
// that cannot hold NULL refuses it. Text is kept as typed; anything else is
// read with the spaces around it trimmed.
func Parse(text string, col model.ColumnDef, loc *time.Location) (any, error) {
	class := col.Type.Class
	if textual(class) {
		return text, nil
	}
	s := strings.TrimSpace(text)
	if s == "" {
		if !col.Type.Nullable {
			return nil, errors.New("needs a value")
		}
		return nil, nil
	}
	switch class {
	case model.TypeBool:
		switch strings.ToLower(s) {
		case "true", "t", "yes", "y", "on", "1":
			return true, nil
		case "false", "f", "no", "n", "off", "0":
			return false, nil
		}
		return nil, errors.New("not true or false")
	case model.TypeInteger:
		n, err := strconv.ParseInt(s, 10, 64)
		if errors.Is(err, strconv.ErrRange) {
			return nil, errors.New("too large for a whole number")
		}
		if err != nil {
			return nil, errors.New("not a whole number")
		}
		return n, nil
	case model.TypeFloat:
		f, err := strconv.ParseFloat(s, 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return nil, errors.New("not a number")
		}
		if err != nil {
			return nil, errors.New("too large a number")
		}
		return f, nil
	case model.TypeDecimal:
		if !decimalRE.MatchString(s) {
			return nil, errors.New("not a number")
		}
		return model.Decimal(s), nil
	case model.TypeDate:
		t, err := time.Parse(dateLayout, s)
		if err != nil {
			return nil, errors.New("not a date, as 2006-01-02")
		}
		return t, nil
	case model.TypeTime:
		for _, l := range []string{"15:04:05", "15:04"} {
			if t, err := time.Parse(l, s); err == nil {
				return t, nil
			}
		}
		return nil, errors.New("not a time, as 15:04:05")
	case model.TypeTimestamp:
		return parseWhen(s, col, loc)
	case model.TypeUUID:
		if !uuidRE.MatchString(s) {
			return nil, errors.New("not a UUID")
		}
		return s, nil
	case model.TypeJSON:
		if !json.Valid([]byte(s)) {
			return nil, errors.New("not valid JSON")
		}
		return model.JSON(s), nil
	case model.TypeEnum:
		if vals := col.Type.EnumValues; len(vals) > 0 && !slices.Contains(vals, s) {
			return nil, errors.New("not one of " + strings.Join(vals, ", "))
		}
	}
	return s, nil
}

// parseWhen reads a date and time. An instant's may give its offset; one
// that does not is in local time, as the grid shows it. A time with no zone
// is read as written.
func parseWhen(s string, col model.ColumnDef, loc *time.Location) (any, error) {
	in := time.UTC
	if col.Type.TimeZone {
		for _, l := range []string{time.RFC3339, "2006-01-02 15:04:05Z07:00", "2006-01-02 15:04:05 Z07:00"} {
			if t, err := time.Parse(l, s); err == nil {
				return t, nil
			}
		}
		if loc != nil {
			in = loc
		}
	}
	for _, l := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02 15:04", "2006-01-02T15:04", dateLayout} {
		if t, err := time.ParseInLocation(l, s, in); err == nil {
			return t, nil
		}
	}
	return nil, errors.New("not a date and time, as 2006-01-02 15:04:05")
}
