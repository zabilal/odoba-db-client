package transfer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// sniffRecords is how many records are read ahead for the columns.
const sniffRecords = 1000

// records reads JSON: an array of objects, as export writes it, or NDJSON,
// an object a line. A number keeps its digits (model.Decimal), a nested
// object or array is JSON, and null is NULL. The columns are the keys of
// the first sniffRecords records, in the order first seen; a later record
// with a key they lack is an error naming it, not a value dropped.
type records struct {
	dec   *json.Decoder
	array bool
	cols  []model.ColumnDef
	index map[string]int
	ahead []object // read ahead for the columns, not yet handed out
	n     int      // records read, for the errors
	done  bool
}

// object is a record's keys and their values, in the order written.
type object struct {
	keys []string
	vals []json.RawMessage
}

func openRecords(r io.Reader, array bool) (*records, error) {
	rs := &records{dec: json.NewDecoder(r), array: array, index: map[string]int{}}
	if array {
		t, err := rs.dec.Token()
		if err == io.EOF {
			rs.done = true
			return rs, nil
		}
		if err != nil {
			return nil, err
		}
		if t != json.Delim('[') {
			return nil, errors.New("transfer: JSON is read as an array of objects")
		}
	}
	classes := map[string]model.TypeClass{}
	for len(rs.ahead) < sniffRecords {
		o, ok, err := rs.read()
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		rs.ahead = append(rs.ahead, o)
		for i, k := range o.keys {
			if _, seen := rs.index[k]; !seen {
				rs.index[k] = len(rs.cols)
				rs.cols = append(rs.cols, model.ColumnDef{Name: k, Type: model.DataType{Nullable: true}})
			}
			_, c := jsonValue(o.vals[i])
			switch prev, ok := classes[k]; {
			case c == model.TypeUnknown: // null says nothing of the column
			case !ok:
				classes[k] = c
			case prev != c:
				classes[k] = model.TypeString // mixed: the values are kept as they are
			}
		}
	}
	for k, c := range classes {
		rs.cols[rs.index[k]].Type.Class = c
	}
	return rs, nil
}

// read reads the next record, and reports false at the end.
func (rs *records) read() (object, bool, error) {
	if rs.done {
		return object{}, false, nil
	}
	if rs.array && !rs.dec.More() {
		rs.done = true
		_, err := rs.dec.Token() // the closing ]
		return object{}, false, err
	}
	t, err := rs.dec.Token()
	if err == io.EOF && !rs.array {
		rs.done = true
		return object{}, false, nil
	}
	if err != nil {
		return object{}, false, err
	}
	rs.n++
	if t != json.Delim('{') {
		return object{}, false, fmt.Errorf("transfer: record %d is not an object", rs.n)
	}
	var o object
	for rs.dec.More() {
		t, err := rs.dec.Token()
		if err != nil {
			return object{}, false, err
		}
		var raw json.RawMessage
		if err := rs.dec.Decode(&raw); err != nil {
			return object{}, false, err
		}
		o.keys, o.vals = append(o.keys, t.(string)), append(o.vals, raw)
	}
	if _, err := rs.dec.Token(); err != nil { // the closing }
		return object{}, false, err
	}
	return o, true, nil
}

func (rs *records) Columns() []model.ColumnDef { return rs.cols }

func (rs *records) Next(ctx context.Context) (model.Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var o object
	if len(rs.ahead) > 0 {
		o, rs.ahead = rs.ahead[0], rs.ahead[1:]
	} else {
		var ok bool
		var err error
		if o, ok, err = rs.read(); err != nil {
			return nil, err
		} else if !ok {
			return nil, io.EOF
		}
	}
	row := make(model.Row, len(rs.cols))
	for i, k := range o.keys {
		at, ok := rs.index[k]
		if !ok {
			return nil, fmt.Errorf("transfer: record %d has a key, %q, that the first %d records do not", rs.n, k, sniffRecords)
		}
		row[at], _ = jsonValue(o.vals[i])
	}
	return row, nil
}

func (rs *records) Close() error { return nil }

// jsonValue is a JSON value as a row holds it, and its class: a string as text,
// a number as its digits, true and false, an object or array as JSON, and
// null as NULL (of no class).
func jsonValue(raw json.RawMessage) (any, model.TypeClass) {
	t := bytes.TrimSpace(raw)
	switch {
	case len(t) == 0 || string(t) == "null":
		return nil, model.TypeUnknown
	case t[0] == '"':
		var s string
		_ = json.Unmarshal(t, &s)
		return s, model.TypeString
	case t[0] == '{' || t[0] == '[':
		var b bytes.Buffer
		_ = json.Compact(&b, t)
		return model.JSON(b.String()), model.TypeJSON
	case string(t) == "true", string(t) == "false":
		return string(t) == "true", model.TypeBool
	}
	return model.Decimal(t), model.TypeDecimal
}
