package elasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Reading an index (T5.15, FR-3.1, FR-12.1).
//
// A document is not a row until something decides which of its fields are
// columns, and here the mapping decides: it is the engine's own record of every
// field it has seen and what it took each one to be. So the columns of a browse
// are the mapping's leaves, in order, with _id first — and a document missing one
// of them has a nought there rather than a different shape, which is what lets a
// grid draw a thousand documents that do not agree with each other.
//
// What the mapping does not know cannot be shown as a column. A field written
// into a document after the mapping was fixed, or one the index was told to
// ignore, is in the source and not in the columns; that is the engine's answer
// about its own data rather than something to paper over, and the whole document
// is one column away — _source is there for exactly that.

// idColumn is the one column every index has, and the only one a document is
// told from another by.
const idColumn = "_id"

// sourceColumn is the document as it was indexed, which is the only place a
// field the mapping does not know can be seen.
const sourceColumn = "_source"

// field is one mapped field, flattened to the name a document holds it under.
type field struct {
	name string
	typ  model.DataType

	// exact is the name to ask about the field's whole value — to match it,
	// order by it or run a pattern across it. It is the field itself for most
	// types, the keyword kept beside an analysed text where there is one, and
	// empty where the engine holds nothing but the words, or nothing that can
	// be compared at all.
	exact string

	// analysed says the engine holds the field as words, which can be
	// searched for where a whole value cannot.
	analysed bool

	// nested is the nested object the field is inside, if it is in one: the
	// engine keeps such an object's documents apart from the one that holds
	// them, and they are asked for by where they are.
	nested string
}

// fields are an index's mapped fields, in name order.
//
// Flattened: a mapping nests objects, and a document holds them nested too, but a
// grid has columns and not trees. So "author.name" is the column and the value is
// fetched by walking that path, which is what the engine itself calls a field in
// every query it takes.
func (s *esSource) fields(ctx context.Context, name string) ([]field, error) {
	var raw map[string]struct {
		Mappings struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"mappings"`
	}
	if err := s.c.get(ctx, "/"+segment(name)+"/_mapping", nil, &raw); err != nil {
		return nil, err
	}
	var out []field
	for _, held := range raw {
		out = append(out, flatten("", "", held.Mappings.Properties)...)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].name < out[b].name })
	return out, nil
}

// property is one entry in a mapping: a type, or an object with its own
// properties, or both — a field can be an object and still have a type.
type property struct {
	Type       string                     `json:"type"`
	Properties map[string]json.RawMessage `json:"properties"`
	Fields     map[string]json.RawMessage `json:"fields"`
}

// flatten turns a mapping's properties into columns. A sub-field — the "keyword"
// beside a "text", which is the same value indexed a second way — is not a column
// of its own: it is the same data, and a grid showing both would show everything
// twice. It is where the column's whole value is asked for instead.
func flatten(prefix, nested string, props map[string]json.RawMessage) []field {
	var out []field
	for name, raw := range props {
		var p property
		if err := json.Unmarshal(raw, &p); err != nil {
			continue
		}
		full := name
		if prefix != "" {
			full = prefix + "." + name
		}
		if len(p.Properties) > 0 {
			inside := nested
			if p.Type == "nested" {
				inside = full
			}
			out = append(out, flatten(full, inside, p.Properties)...)
			continue
		}
		f := field{name: full, typ: typeOf(p.Type), nested: nested}
		switch {
		case analysed[p.Type]:
			f.analysed = true
			f.exact = keywordBeside(full, p.Fields)
		case !opaque[p.Type] && p.Type != "":
			f.exact = full
		}
		out = append(out, f)
	}
	return out
}

// analysed are the types the engine holds as words rather than as a value.
var analysed = map[string]bool{
	"text": true, "match_only_text": true, "annotated_text": true, "search_as_you_type": true,
}

// opaque are the types that hold something with no equal and no order: a place,
// a shape, a vector, bytes the engine does not index, an object nothing was ever
// written into.
var opaque = map[string]bool{
	"object": true, "nested": true, "binary": true,
	"geo_point": true, "geo_shape": true, "point": true, "shape": true,
	"dense_vector": true, "sparse_vector": true, "knn_vector": true,
	"rank_feature": true, "rank_features": true,
}

// keywordBeside is the keyword an analysed field keeps beside itself, or none.
// The first by name where there are several, so that the answer is the same
// every time it is asked.
func keywordBeside(full string, subs map[string]json.RawMessage) string {
	var names []string
	for name, raw := range subs {
		var p property
		if json.Unmarshal(raw, &p) == nil && p.Type == "keyword" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return full + "." + names[0]
}

// typeOf is what a mapped type is as this application holds it. A search engine
// has more types than a grid has classes — half of them are about how a value is
// indexed rather than what it is — so what is kept is the distinction a grid acts
// on: numbers align right, times sort as times, and everything else is text.
func typeOf(mapped string) model.DataType {
	t := model.DataType{Native: mapped, Length: -1}
	switch mapped {
	case "long", "integer", "short", "byte", "unsigned_long":
		t.Class = model.TypeInteger
	case "double", "float", "half_float", "scaled_float":
		t.Class = model.TypeFloat
	case "boolean":
		t.Class = model.TypeBool
	case "date", "date_nanos":
		t.Class = model.TypeTimestamp
	case "object", "nested", "flattened", "geo_point", "geo_shape":
		// Something with parts. A place is among them: it arrives as a pair or
		// a shape and is held as what it arrived as, which the map reads by
		// name where a result has one (ADR-0163).
		t.Class = model.TypeJSON
	case "":
		t.Class = model.TypeUnknown
	default:
		// text, keyword, ip, version, and everything a later engine adds.
		t.Class = model.TypeString
	}
	return t
}

// Browse reads an index's documents.
func (s *esSource) Browse(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (_ model.RowStream, err error) {
	defer panics.Recover(&err, "reading the documents")

	name, err := indexOf(ref)
	if err != nil {
		return nil, err
	}
	if opt.Follow {
		// A search engine keeps no log of what was written to it: there is
		// nothing to follow, and an index that answered a following read would
		// be answering a different question every time it was asked.
		return nil, errors.New("elasticsearch: an index cannot be followed: " +
			"a search engine keeps no record of what was written to it, only what is in it now")
	}
	mapped, err := s.fields(ctx, name)
	if err != nil {
		return nil, err
	}
	cols, err := columns(mapped, opt.Columns)
	if err != nil {
		return nil, err
	}
	body, err := searchBody(mapped, opt)
	if err != nil {
		return nil, err
	}
	var res searchResult
	if err := s.c.post(ctx, "/"+segment(name)+"/_search", body, &res); err != nil {
		return nil, deepPaging(err, opt.Offset, body["size"].(int64))
	}
	return &hitStream{cols: cols, hits: res.Hits.Hits}, nil
}

// defaultPage is how many documents a browse reads when nobody says. The grid
// always says; this is for everything else that asks.
const defaultPage = 200

// searchBody is the request a browse makes.
func searchBody(mapped []field, opt source.BrowseOptions) (map[string]any, error) {
	q, err := query(mapped, opt)
	if err != nil {
		return nil, err
	}
	by, err := order(mapped, opt.Sorts)
	if err != nil {
		return nil, err
	}
	limit := opt.Limit
	if limit <= 0 {
		limit = defaultPage
	}
	return map[string]any{
		"from": max(opt.Offset, 0), "size": limit, "query": q, "sort": by,
		// The page is all that is wanted; how many there are is Count's to
		// say, and it says it exactly.
		"track_total_hits": false,
	}, nil
}

// columns are the columns of a browse: the identity, the mapped fields, and the
// document itself.
func columns(mapped []field, asked []string) ([]column, error) {
	text := func(name string) model.DataType {
		return model.DataType{Class: model.TypeString, Native: name, Length: -1}
	}
	all := make([]column, 0, len(mapped)+2)
	all = append(all, column{def: model.ColumnDef{Name: idColumn, Type: text(idColumn), ReadOnly: true}})
	for _, f := range mapped {
		all = append(all, column{def: model.ColumnDef{Name: f.name, Type: f.typ, ReadOnly: true},
			path: strings.Split(f.name, ".")})
	}
	all = append(all, column{def: model.ColumnDef{Name: sourceColumn,
		Type: model.DataType{Class: model.TypeJSON, Native: sourceColumn, Length: -1}, ReadOnly: true}})
	if len(asked) == 0 {
		return all, nil
	}
	out := make([]column, 0, len(asked))
next:
	for _, want := range asked {
		for _, c := range all {
			if c.def.Name == want {
				out = append(out, c)
				continue next
			}
		}
		// Left out in silence, a column asked for and not there is a grid one
		// column short with nothing to say why (REQ-DRV-3).
		return nil, fmt.Errorf("elasticsearch: %q is not a field the mapping knows", want)
	}
	return out, nil
}

// column is one column of a browse and where in a document its value is.
type column struct {
	def  model.ColumnDef
	path []string
}

// searchResult is what _search answers, of what it answers.
type searchResult struct {
	Hits struct {
		Hits []hit `json:"hits"`
	} `json:"hits"`
}

type hit struct {
	ID     string          `json:"_id"`
	Source json.RawMessage `json:"_source"`
}

// hitStream is a page of documents.
type hitStream struct {
	cols []column
	hits []hit
	at   int
}

func (h *hitStream) Columns() []model.ColumnDef {
	out := make([]model.ColumnDef, len(h.cols))
	for i, c := range h.cols {
		out[i] = c.def
	}
	return out
}

func (h *hitStream) Next(ctx context.Context) (_ model.Row, err error) {
	defer panics.Recover(&err, "reading a document")
	// The page is already here, so there is nothing to interrupt — but a read
	// somebody has stopped answers that it was stopped (NFR-P9).
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if h.at >= len(h.hits) {
		return nil, io.EOF
	}
	d := h.hits[h.at]
	h.at++
	held, err := document(d.Source)
	if err != nil {
		return nil, err
	}
	row := make(model.Row, len(h.cols))
	for i, c := range h.cols {
		switch c.def.Name {
		case idColumn:
			row[i] = d.ID
		case sourceColumn:
			if held != nil {
				row[i] = narrow(held, model.DataType{})
			}
		default:
			row[i] = narrow(at(held, c.path), c.def.Type)
		}
	}
	return row, nil
}

func (*hitStream) Close() error { return nil }

// document is a document's source, its numbers kept as their digits. None where
// the index keeps no source, which an index may be told not to.
func document(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var held map[string]any
	if err := dec.Decode(&held); err != nil {
		return nil, fmt.Errorf("elasticsearch: a document could not be read: %w", err)
	}
	return held, nil
}

// Count is how many documents a browse would read.
func (s *esSource) Count(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (_ int64, err error) {
	defer panics.Recover(&err, "counting the documents")
	name, err := indexOf(ref)
	if err != nil {
		return 0, err
	}
	var mapped []field
	if len(opt.Filters) > 0 {
		// Only a filter needs to know what its field is.
		if mapped, err = s.fields(ctx, name); err != nil {
			return 0, err
		}
	}
	q, err := query(mapped, opt)
	if err != nil {
		return 0, err
	}
	return s.count(ctx, name, q)
}

// count asks the engine, which counts exactly rather than estimating: _count is
// a real count of matching documents, unlike the total a search answers, which
// stops at ten thousand and says so.
func (s *esSource) count(ctx context.Context, name string, q map[string]any) (int64, error) {
	var res struct {
		Count int64 `json:"count"`
	}
	if err := s.c.post(ctx, "/"+segment(name)+"/_count",
		map[string]any{"query": q}, &res); err != nil {
		return 0, err
	}
	return res.Count, nil
}

// deepPaging says what a refusal past the result window means, because the
// engine's own words are about a setting rather than about what was asked.
//
// A search engine will not page arbitrarily deep: past index.max_result_window —
// ten thousand documents by default — it refuses, because answering would mean
// every shard sorting everything before it. That is a real limit and not one this
// can work around from here, so it is said in those terms.
func deepPaging(err error, offset, limit int64) error {
	var he *httpError
	if !errors.As(err, &he) || !strings.Contains(strings.ToLower(he.reason), "result window") {
		return err
	}
	return fmt.Errorf("elasticsearch: this index will not answer documents %d to %d: "+
		"a search engine refuses to page deeper than its result window, which is ten thousand "+
		"documents unless the index was told otherwise (index.max_result_window). Narrow the "+
		"filters rather than paging further: %w", offset+1, offset+limit, err)
}
