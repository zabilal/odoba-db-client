package elasticsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
)

// What a search server holds, and how it is read (T5.15, FR-2.1, FR-2.5).
//
// One level: indices, and nothing above them. A search engine has no databases
// and no schemas — an index is the whole of a namespace — so the tree is the
// Collections class with every index in it rather than an invented level with
// one thing in it.
//
// An index is a collection here and not a table, which is the honest word: its
// documents have no declared columns, two of them need not have the same fields,
// and what a mapping says is which fields have been *seen* rather than which
// exist. That is what ParadigmDocument means, and it is why the grid draws these
// through the same code that draws MongoDB's (ADR-0070).

// hidden are the indices a search engine keeps for itself. They are not shown,
// for the reason a catalogue's own tables are not: somebody browsing their data
// did not ask about the engine's bookkeeping, and there are dozens of them.
func hidden(name string) bool {
	return strings.HasPrefix(name, ".")
}

// index is one index as _cat/indices answers it.
type index struct {
	Name   string `json:"index"`
	Size   string `json:"store.size"`
	State  string `json:"status"`
	Health string `json:"health"`
}

// indices are the indices the server holds, in name order.
//
// _cat rather than _aliases or the cluster state, because _cat answers what is
// wanted of an index — its name, how much room it takes and how it is — in one
// request, and answers for every index in one line each.
func (s *esSource) indices(ctx context.Context) ([]index, error) {
	var all []index
	q := url.Values{"format": {"json"}, "h": {"index,store.size,status,health"}}
	if err := s.c.get(ctx, "/_cat/indices", q, &all); err != nil {
		return nil, err
	}
	out := make([]index, 0, len(all))
	for _, i := range all {
		if !hidden(i.Name) {
			out = append(out, i)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out, nil
}

// Root is the one class this holds: its indices.
func (s *esSource) Root(ctx context.Context) (_ []model.Node, err error) {
	defer panics.Recover(&err, "listing the indices")
	all, err := s.indices(ctx)
	if err != nil {
		return nil, err
	}
	// The class even where it holds nothing: a server with no indices is a
	// server with no indices, which an empty tree does not say.
	return []model.Node{model.ClassNode(model.ObjectRef{}, model.KindCollection, int64(len(all)))}, nil
}

// Children are the indices under that class, and nothing under an index: its
// fields are the columns of a browse rather than a level of the tree, because a
// document's fields are not objects of their own (ADR-0070).
func (s *esSource) Children(ctx context.Context, ref model.ObjectRef) (_ []model.Node, err error) {
	defer panics.Recover(&err, "listing the indices")
	if k, ok := model.ClassOf(ref); !ok || k != model.KindCollection {
		return nil, nil
	}
	all, err := s.indices(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.Node, 0, len(all))
	for _, i := range all {
		// No badge from what _cat says, though it says a number: that number
		// counts every object in a nested field as a document of its own, so
		// an index of a hundred documents with two notes on a third of them
		// answers 166. An estimate that is wrong by what the data looks like
		// is not an estimate. Badge counts, when the tree asks.
		out = append(out, model.Node{Ref: model.NewRef(model.KindCollection, i.Name), Label: i.Name,
			Browsable: true, Describable: true})
	}
	return out, nil
}

// Badge is what the tree writes beside an index: how many documents it holds, as
// the engine last counted them.
func (s *esSource) Badge(ctx context.Context, ref model.ObjectRef) (_ model.Badge, _ bool, err error) {
	defer panics.Recover(&err, "counting the documents")
	name, err := indexOf(ref)
	if err != nil {
		return model.Badge{}, false, nil
	}
	n, err := s.count(ctx, name, map[string]any{"match_all": map[string]any{}})
	if err != nil {
		return model.Badge{}, false, err
	}
	return model.Badge{Text: strconv.FormatInt(n, 10), Exact: true}, true, nil
}

// indexOf reads the index a ref names.
func indexOf(ref model.ObjectRef) (string, error) {
	if ref.Kind != model.KindCollection || len(ref.Path) != 1 || ref.Path[0] == "" {
		return "", fmt.Errorf("elasticsearch: %s is not an index", ref)
	}
	return ref.Path[0], nil
}

// Describe is what one index is: the fields its mapping knows, how many
// documents it holds, how much room it takes, and the settings somebody chose.
func (s *esSource) Describe(ctx context.Context, ref model.ObjectRef) (_ any, err error) {
	defer panics.Recover(&err, "reading the structure")
	name, err := indexOf(ref)
	if err != nil {
		return nil, err
	}
	fields, err := s.fields(ctx, name)
	if err != nil {
		return nil, err
	}
	out := &model.Collection{Name: name, Attrs: map[string]string{}}
	// The mapping is what this has instead of inference: a search engine already
	// knows which fields it has seen and what it decided each one is, so there is
	// nothing to sample (FR-12.4). What the mapping does not say is how many
	// documents hold each, and the engine can be asked that of every document
	// rather than of a sample — so the shape is of the whole index, and says so.
	total, holding, err := s.presence(ctx, name, fields)
	if err != nil {
		return nil, err
	}
	out.DocumentsEstimate = total
	shape := &model.DocumentShape{Sampled: total}
	for i, f := range fields {
		in := model.InferredField{Name: f.name, Types: []model.ObservedType{{Type: f.typ, Count: holding[i]}}}
		if total > 0 {
			in.Presence = float64(holding[i]) / float64(total)
		}
		shape.Fields = append(shape.Fields, in)
	}
	out.Shape = shape

	for _, i := range mustIndices(ctx, s, name) {
		if i.Size != "" {
			out.Attrs["size"] = i.Size
		}
		if i.State != "" {
			out.Attrs["status"] = i.State
		}
		if i.Health != "" {
			out.Attrs["health"] = i.Health
		}
	}
	settings, err := s.settings(ctx, name)
	if err == nil {
		for k, v := range settings {
			out.Attrs[k] = v
		}
	}
	return out, nil
}

// mustIndices is the _cat line for one index, or none: what it is for is the
// figures beside a structure, and a structure without them is still a structure.
func mustIndices(ctx context.Context, s *esSource, name string) []index {
	var out []index
	q := url.Values{"format": {"json"}, "h": {"index,store.size,status,health"}}
	if err := s.c.get(ctx, "/_cat/indices/"+segment(name), q, &out); err != nil {
		return nil
	}
	return out
}

// settings are the ones somebody chose, told from the hundreds a server
// defaults: shards, replicas and the window a search may page through, which are
// the three that change what this application can do.
func (s *esSource) settings(ctx context.Context, name string) (map[string]string, error) {
	var raw map[string]struct {
		Settings map[string]json.RawMessage `json:"settings"`
	}
	if err := s.c.get(ctx, "/"+segment(name)+"/_settings",
		url.Values{"flat_settings": {"true"}}, &raw); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, held := range raw {
		for _, want := range []string{
			"index.number_of_shards", "index.number_of_replicas", "index.max_result_window",
		} {
			if v, ok := held.Settings[want]; ok {
				out[strings.TrimPrefix(want, "index.")] = strings.Trim(string(v), `"`)
			}
		}
	}
	return out, nil
}

// presenceBatch is how many fields are asked about in one request. An index may
// have a thousand, and a request that asked about all of them at once is one a
// server is entitled to refuse.
const presenceBatch = 200

// presence is how many documents the index holds and how many of them hold each
// field, in the fields' order.
//
// Counted by the engine over every document, one filter to a field, with no
// documents read: it is the same question a not-null filter asks, which is what
// makes the figure beside a field agree with what filtering on it finds.
func (s *esSource) presence(ctx context.Context, name string, fields []field) (total int64, holding []int64, err error) {
	holding = make([]int64, len(fields))
	for start := 0; start == 0 || start < len(fields); start += presenceBatch {
		end := min(start+presenceBatch, len(fields))
		aggs := map[string]any{}
		for i := start; i < end; i++ {
			aggs["f"+strconv.Itoa(i)] = map[string]any{"filter": holds(fields[i])}
		}
		body := map[string]any{"size": 0, "track_total_hits": true}
		if len(aggs) > 0 {
			body["aggs"] = aggs
		}
		var res struct {
			Hits struct {
				Total struct {
					Value int64 `json:"value"`
				} `json:"total"`
			} `json:"hits"`
			Aggregations map[string]struct {
				Count int64 `json:"doc_count"`
			} `json:"aggregations"`
		}
		if err := s.c.post(ctx, "/"+segment(name)+"/_search", body, &res); err != nil {
			return 0, nil, err
		}
		total = res.Hits.Total.Value
		for i := start; i < end; i++ {
			holding[i] = res.Aggregations["f"+strconv.Itoa(i)].Count
		}
	}
	return total, holding, nil
}

// holds is the query matching the documents that hold a field. One inside a
// nested object is asked for where it is, or nothing would hold it.
func holds(f field) map[string]any {
	q := map[string]any{"exists": map[string]any{"field": f.name}}
	if f.nested != "" {
		q = map[string]any{"nested": map[string]any{"path": f.nested, "query": q}}
	}
	return q
}
