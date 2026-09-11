package export

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/testutil/race"
)

type rows struct {
	cols []model.ColumnDef
	data []model.Row
	i    int
}

func (r *rows) Columns() []model.ColumnDef { return r.cols }
func (r *rows) Close() error               { return nil }
func (r *rows) Next(ctx context.Context) (model.Row, error) {
	if r.i >= len(r.data) {
		return nil, io.EOF
	}
	r.i++
	return r.data[r.i-1], nil
}

func col(name string, class model.TypeClass, tz bool) model.ColumnDef {
	return model.ColumnDef{Name: name, Type: model.DataType{Class: class, TimeZone: tz}}
}

var at = time.Date(2026, 9, 10, 12, 30, 0, 0, time.UTC)

func sample() *rows {
	return &rows{
		cols: []model.ColumnDef{
			col("id", model.TypeInteger, false), col("name", model.TypeString, false),
			col("big", model.TypeDecimal, false), col("f", model.TypeFloat, false),
			col("at", model.TypeTimestamp, true), col("day", model.TypeDate, false),
			col("data", model.TypeBytes, false), col("doc", model.TypeJSON, false),
			col("tags", model.TypeArray, false), col("nothing", model.TypeString, false),
			col("id", model.TypeInteger, false),
		},
		data: []model.Row{{
			int64(1), `say "hi", <then> go` + "\nnow",
			model.Decimal("12345678901234567890.123456789"), math.NaN(),
			at, at, []byte{0xde, 0xad}, model.JSON("{\n  \"a\": [1, 2]\n}"),
			[]any{"x", model.Decimal("1.50")}, nil, int64(2),
		}},
	}
}

func export(t *testing.T, src model.RowStream, opt Options) string {
	t.Helper()
	var b bytes.Buffer
	if _, err := Copy(context.Background(), &b, src, opt, nil); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestCSVQuotesWhatNeedsIt(t *testing.T) {
	got := export(t, sample(), Options{Format: CSV, Header: true})
	want := "id,name,big,f,at,day,data,doc,tags,nothing,id\n" +
		`1,"say ""hi"", <then> go` + "\n" + `now",12345678901234567890.123456789,NaN,2026-09-10T12:30:00Z,2026-09-10,\xdead,"{` + "\n" +
		`  ""a"": [1, 2]` + "\n" + `}","[""x"",1.50]",,2` + "\n"
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

func TestTSVAndACustomNull(t *testing.T) {
	src := &rows{cols: []model.ColumnDef{col("a", model.TypeString, false), col("b", model.TypeString, false)},
		data: []model.Row{{"x\ty", nil}}}
	if got := export(t, src, Options{Format: TSV, Null: `\N`}); got != "\"x\ty\"\t\\N\n" {
		t.Errorf("%q", got)
	}
}

func TestJSONIsValidAndExact(t *testing.T) {
	out := export(t, sample(), Options{Format: JSON})
	dec := json.NewDecoder(strings.NewReader(out))
	dec.UseNumber()
	var got []map[string]any
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	r := got[0]
	if n, ok := r["big"].(json.Number); !ok || n.String() != "12345678901234567890.123456789" {
		t.Errorf("big = %#v; an exact numeric must keep every digit, as a number", r["big"])
	}
	if r["f"] != "NaN" {
		t.Errorf("f = %#v; NaN is not JSON, and must not become null", r["f"])
	}
	if r["data"] != "3q0=" || r["at"] != "2026-09-10T12:30:00Z" || r["day"] != "2026-09-10" {
		t.Errorf("data %v at %v day %v", r["data"], r["at"], r["day"])
	}
	if doc, ok := r["doc"].(map[string]any); !ok || len(doc["a"].([]any)) != 2 {
		t.Errorf("doc = %#v; JSON values are embedded, not quoted", r["doc"])
	}
	if tags := r["tags"].([]any); tags[1].(json.Number).String() != "1.50" {
		t.Errorf("tags = %#v; nested decimals stay exact too", tags)
	}
	if v, ok := r["nothing"]; !ok || v != nil {
		t.Errorf("nothing = %#v, want null", v)
	}
	if r["id"] != json.Number("1") || r["id_2"] != json.Number("2") {
		t.Errorf("id %v id_2 %v; duplicate columns need distinct keys", r["id"], r["id_2"])
	}
	if !strings.Contains(out, "<then>") {
		t.Error("< and > were escaped; exported text should read as it is")
	}
}

func TestNDJSONIsOneObjectALine(t *testing.T) {
	src := sample()
	src.data = append(src.data, src.data[0], src.data[0])
	out := export(t, src, Options{Format: NDJSON})
	sc := bufio.NewScanner(strings.NewReader(out))
	lines := 0
	for sc.Scan() {
		lines++
		if !json.Valid(sc.Bytes()) {
			t.Errorf("line %d is not JSON: %s", lines, sc.Text())
		}
	}
	if lines != 3 {
		t.Errorf("%d lines for 3 rows; a JSON value with newlines must be compacted", lines)
	}
}

func TestEmptyResults(t *testing.T) {
	empty := func() *rows { return &rows{cols: []model.ColumnDef{col("a", model.TypeString, false)}} }
	if got := export(t, empty(), Options{Format: JSON}); got != "[]\n" {
		t.Errorf("JSON %q", got)
	}
	if got := export(t, empty(), Options{Format: CSV, Header: true}); got != "a\n" {
		t.Errorf("CSV %q", got)
	}
	if got := export(t, empty(), Options{Format: NDJSON}); got != "" {
		t.Errorf("NDJSON %q", got)
	}
}

// endless generates rows until told to stop.
type endless struct {
	n      int64
	cancel func()
	at     int64
}

func (e *endless) Columns() []model.ColumnDef {
	return []model.ColumnDef{col("n", model.TypeInteger, false), col("s", model.TypeString, false)}
}
func (e *endless) Close() error { return nil }
func (e *endless) Next(ctx context.Context) (model.Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.n++
	if e.cancel != nil && e.n == e.at {
		e.cancel()
	}
	return model.Row{e.n, "some text to make the row a realistic size"}, nil
}

func TestCancelStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p, err := Copy(ctx, io.Discard, &endless{cancel: cancel, at: 1000}, Options{Format: CSV}, nil)
	if !errors.Is(err, context.Canceled) || p.Rows < 999 || p.Rows > 1001 {
		t.Errorf("rows %d, err %v", p.Rows, err)
	}
}

// limited ends an endless stream after n rows.
type limited struct {
	endless
	max int64
}

func (l *limited) Next(ctx context.Context) (model.Row, error) {
	if l.n >= l.max {
		return nil, io.EOF
	}
	return l.endless.Next(ctx)
}

func TestProgressEndsWithTheTotal(t *testing.T) {
	var last Progress
	calls := 0
	p, err := Copy(context.Background(), io.Discard, &limited{max: 50000}, Options{Format: NDJSON},
		func(p Progress) { calls++; last = p })
	if err != nil || calls == 0 || last.Rows != 50000 || last.Bytes != p.Bytes || p.Bytes == 0 {
		t.Errorf("calls %d, last %+v, result %+v, err %v", calls, last, p, err)
	}
}

func TestMemoryStaysFlat(t *testing.T) {
	if race.Enabled {
		t.Skip("the race detector's shadow memory distorts heap figures")
	}
	heap := func() uint64 {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapInuse
	}
	inserts := func([]model.ColumnDef, []model.Row) (string, error) { return "INSERT;\n", nil }
	before := heap()
	for _, f := range Formats() {
		if _, err := Copy(context.Background(), io.Discard, &limited{max: 500000}, Options{Format: f, Inserts: inserts}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if grew := int64(heap()) - int64(before); grew > 16<<20 {
		t.Errorf("heap grew %d MB over half a million rows in every format; export must stream", grew>>20)
	}
}
