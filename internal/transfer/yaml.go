package transfer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// YAML, read as a list of records (FR-10.10): a sequence of mappings, as
// export writes it, or a stream of documents that are each a mapping.
//
// The rules are JSON's, because the two are read into the same rows: a number
// keeps its digits (model.Decimal), a nested mapping or sequence is JSON, null
// is NULL, and the columns are the keys of the first sniffRecords records in
// the order first seen. What YAML has that JSON has not is kept as what it is:
// bytes as bytes, and the three numbers that are not numbers as themselves.
//
// It is read a record at a time, so memory does not grow with the file
// (FR-10.3), and that takes one thing on trust. A YAML parser reads a whole
// document, and a list of a million records is one document. So the file is
// cut into its records here, at the lines where one begins — a dash in the
// first column — and each is handed to the parser alone. That is exactly right
// for a list written the way every tool writes one, in block style, which is
// the only style in which a record can begin a line. A list written in flow
// style, in brackets like JSON, has no such lines and is read whole.
type yamlRecords struct {
	items items
	cols  []model.ColumnDef
	index map[string]int
	ahead []yamlObject
	n     int
}

// yamlObject is a record's keys and their values, in the order written.
type yamlObject struct {
	keys []string
	vals []any
}

func openYAML(r io.Reader) (*yamlRecords, error) {
	items, err := itemsOf(bufio.NewReader(r))
	if err != nil {
		return nil, err
	}
	rs := &yamlRecords{items: items, index: map[string]int{}}
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
			c := classOf(o.vals[i])
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

// classOf is the class of a value as a row holds it.
func classOf(v any) model.TypeClass {
	switch v.(type) {
	case nil:
		return model.TypeUnknown
	case string:
		return model.TypeString
	case bool:
		return model.TypeBool
	case model.Decimal:
		return model.TypeDecimal
	case float64:
		return model.TypeFloat
	case []byte:
		return model.TypeBytes
	case model.JSON:
		return model.TypeJSON
	}
	return model.TypeString
}

// read reads the next record, and reports false at the end.
func (rs *yamlRecords) read() (yamlObject, bool, error) {
	node, ok, err := rs.items.next()
	if err != nil || !ok {
		return yamlObject{}, false, err
	}
	rs.n++
	if node.Kind != yaml.MappingNode {
		return yamlObject{}, false, fmt.Errorf("transfer: record %d is not a mapping of names to values", rs.n)
	}
	var o yamlObject
	seen := map[string]bool{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		k, v := node.Content[i], node.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			return yamlObject{}, false, fmt.Errorf("transfer: record %d has a key that is not a name", rs.n)
		}
		if seen[k.Value] {
			// A parser may take the last, or the first; a row has one value
			// to a column, and which one is not this reader's to choose.
			return yamlObject{}, false, fmt.Errorf("transfer: record %d has the key %q twice", rs.n, k.Value)
		}
		seen[k.Value] = true
		val, err := yamlValue(v)
		if err != nil {
			return yamlObject{}, false, fmt.Errorf("transfer: record %d, %s: %w", rs.n, k.Value, err)
		}
		o.keys, o.vals = append(o.keys, k.Value), append(o.vals, val)
	}
	return o, true, nil
}

func (rs *yamlRecords) Columns() []model.ColumnDef { return rs.cols }

func (rs *yamlRecords) Next(ctx context.Context) (model.Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var o yamlObject
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
		row[at] = o.vals[i]
	}
	return row, nil
}

func (rs *yamlRecords) Close() error { return nil }

// plainNumber is a number written the way JSON writes one, which is the only
// way its digits can be kept as they are.
var plainNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// yamlValue is a YAML value as a row holds it: text as text, a number as its
// digits, true and false, bytes, a mapping or a sequence as JSON, and null as
// NULL.
func yamlValue(n *yaml.Node) (any, error) {
	for n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	switch n.Kind {
	case yaml.MappingNode, yaml.SequenceNode:
		var v any
		if err := n.Decode(&v); err != nil {
			return nil, err
		}
		b, err := json.Marshal(jsonSafe(v))
		if err != nil {
			return nil, err
		}
		return model.JSON(b), nil
	case yaml.ScalarNode:
	default:
		return nil, errors.New("is not a value")
	}
	switch n.ShortTag() {
	case "!!null":
		return nil, nil
	case "!!bool":
		var b bool
		if err := n.Decode(&b); err != nil {
			return nil, err
		}
		return b, nil
	case "!!int", "!!float":
		if plainNumber.MatchString(n.Value) {
			return model.Decimal(n.Value), nil
		}
		// Written some other way — in hexadecimal, with underscores, as
		// infinity — which the parser reads and this then writes plainly.
		var f float64
		if err := n.Decode(&f); err != nil {
			return nil, err
		}
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return f, nil
		}
		if n.ShortTag() == "!!int" {
			var i int64
			if err := n.Decode(&i); err == nil {
				return model.Decimal(fmt.Sprint(i)), nil
			}
		}
		return model.Decimal(fmt.Sprint(f)), nil
	case "!!binary":
		// Over several lines or one: the decoder passes over line ends.
		b, err := base64.StdEncoding.DecodeString(n.Value)
		if err != nil {
			return nil, fmt.Errorf("is marked as bytes and is not base64: %w", err)
		}
		return b, nil
	}
	// Text, and everything else that is written as text: a timestamp is kept
	// as it was written, for the column it goes into to read.
	return n.Value, nil
}

// jsonSafe makes a decoded YAML value one JSON can write: a mapping whose keys
// are not all text has them written as text, and the numbers JSON has no way
// to write become their names.
func jsonSafe(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = jsonSafe(e)
		}
		return x
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[fmt.Sprint(k)] = jsonSafe(e)
		}
		return out
	case []any:
		for i, e := range x {
			x[i] = jsonSafe(e)
		}
		return x
	case float64:
		switch {
		case math.IsNaN(x):
			return "NaN"
		case math.IsInf(x, 1):
			return "Infinity"
		case math.IsInf(x, -1):
			return "-Infinity"
		}
	case []byte:
		return base64.StdEncoding.EncodeToString(x)
	}
	return v
}

// items hands over a file's records one at a time.
type items interface {
	next() (*yaml.Node, bool, error)
}

// itemsOf looks at how a file begins and answers how its records are found.
func itemsOf(r *bufio.Reader) (items, error) {
	first, err := firstLine(r)
	if err != nil {
		return nil, err
	}
	switch {
	case startsItem(first):
		return &chunks{r: r, starts: startsItem}, nil
	case strings.HasPrefix(first, "["):
		return wholeOf(r)
	}
	// Anything else is a mapping, and a file of them is a stream of
	// documents: each is a record, and they are parted by a line of three
	// dashes. A file with nothing in it is a stream of none.
	return &chunks{r: r, starts: startsDocument, documents: true}, nil
}

// firstLine is the first line of a file that says something — not blank, not
// a comment, not the mark that begins a document — without taking it.
func firstLine(r *bufio.Reader) (string, error) {
	for {
		line, err := peekLine(r)
		if err != nil && line == "" {
			if errors.Is(err, io.EOF) {
				return "", nil
			}
			return "", err
		}
		t := strings.TrimRight(line, " \t\r\n")
		if t == "" || strings.HasPrefix(strings.TrimLeft(t, " \t"), "#") || t == "---" || strings.HasPrefix(t, "%") {
			if _, err := r.Discard(len(line)); err != nil {
				return "", err
			}
			continue
		}
		return t, nil
	}
}

// peekLine is the next line, with its ending, left where it is.
func peekLine(r *bufio.Reader) (string, error) {
	for n := 4096; ; n *= 2 {
		b, err := r.Peek(n)
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			return string(b[:i+1]), nil
		}
		if err != nil {
			return string(b), err
		}
		if n > maxLine {
			return "", fmt.Errorf("transfer: a line of this file is longer than %d bytes", maxLine)
		}
	}
}

// maxLine bounds one line, so that a file that is not YAML at all — one
// endless line of something else — is refused rather than read into memory.
const maxLine = 64 << 20

// startsItem reports whether a line begins a record of a list: a dash in the
// first column. Nothing else in a list written in block style can put one
// there — what a record holds is indented under it — except the mark between
// two documents, and a piece that is only that mark is a piece with nothing in
// it, which is passed over.
func startsItem(line string) bool { return strings.HasPrefix(line, "-") }

func startsDocument(line string) bool { return line == "---" || strings.HasPrefix(line, "--- ") }

// chunks cuts a file at the lines where a record begins and parses each piece
// alone.
type chunks struct {
	r         *bufio.Reader
	starts    func(line string) bool
	documents bool
	done      bool
	n         int
}

func (c *chunks) next() (*yaml.Node, bool, error) {
	for !c.done {
		var piece strings.Builder
		begun := false
		for {
			line, err := peekLine(c.r)
			if line == "" {
				if err != nil && !errors.Is(err, io.EOF) {
					return nil, false, err
				}
				c.done = true
				break
			}
			t := strings.TrimRight(line, "\r\n")
			if begun && c.starts(t) {
				break // the next record's; left for it
			}
			if _, err := c.r.Discard(len(line)); err != nil {
				return nil, false, err
			}
			if t == "---" {
				// The mark between two documents ends a piece. Where no
				// record was begun the piece has nothing in it, and is
				// passed over below. The mark that ends a document is left
				// in the piece, where the parser reads it.
				break
			}
			begun = begun || strings.TrimSpace(t) != ""
			piece.WriteString(line)
		}
		c.n++
		var doc yaml.Node
		if err := yaml.Unmarshal([]byte(piece.String()), &doc); err != nil {
			return nil, false, fmt.Errorf("transfer: record %d: %w", c.n, err)
		}
		if len(doc.Content) == 0 {
			c.n--
			continue // a piece with nothing in it: blank lines, or the end of the file
		}
		node := doc.Content[0]
		if !c.documents {
			if node.Kind != yaml.SequenceNode || len(node.Content) != 1 {
				return nil, false, fmt.Errorf("transfer: record %d is not one item of a list", c.n)
			}
			node = node.Content[0]
		}
		return node, true, nil
	}
	return nil, false, nil
}

// whole is a list read all at once, for a file written in the one style that
// cannot be cut.
type whole struct {
	nodes []*yaml.Node
}

func wholeOf(r io.Reader) (*whole, error) {
	var doc yaml.Node
	if err := yaml.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("transfer: %w", err)
	}
	// A list, because it began with a bracket and was read: what the document
	// holds is the list, and what the list holds are the records.
	w := &whole{}
	for _, list := range doc.Content {
		w.nodes = list.Content
	}
	return w, nil
}

func (w *whole) next() (*yaml.Node, bool, error) {
	if len(w.nodes) == 0 {
		return nil, false, nil
	}
	n := w.nodes[0]
	w.nodes = w.nodes[1:]
	return n, true, nil
}
