package export

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

func TestInsertsAreWrittenInTheSourcesDialect(t *testing.T) {
	var calls int
	opt := Options{Format: SQLInsert, Inserts: func(cols []model.ColumnDef, rows []model.Row) (string, error) {
		calls++
		return fmt.Sprintf("INSERT %d %v;\n", len(cols), rows), nil
	}}
	ids := []model.ColumnDef{col("id", model.TypeInteger, false)}
	if out := export(t, &rows{cols: ids, data: []model.Row{{int64(1)}, {int64(2)}}}, opt); out != "INSERT 1 [[1]];\nINSERT 1 [[2]];\n" || calls != 2 {
		t.Errorf("a statement a row, as the dialect writes it: %q, %d calls", out, calls)
	}
	if _, err := Copy(context.Background(), &bytes.Buffer{}, &rows{cols: ids}, Options{Format: SQLInsert}, nil); err == nil {
		t.Error("with no dialect to write them, no statements")
	}
	opt.Inserts = func([]model.ColumnDef, []model.Row) (string, error) { return "", errors.New("no literal for it") }
	if _, err := Copy(context.Background(), &bytes.Buffer{}, &rows{cols: ids, data: []model.Row{{int64(1)}}}, opt, nil); err == nil || err.Error() != "no literal for it" {
		t.Errorf("the dialect's error: %v", err)
	}
}

func TestHTMLIsAPageOfOneTable(t *testing.T) {
	out := export(t, sample(), Options{Format: HTML, Name: `items <all> & "more"`})
	for _, want := range []string{
		"<!doctype html>\n", `<meta charset="utf-8">`, "<title>items &lt;all&gt; &amp; &quot;more&quot;</title>",
		"prefers-color-scheme:dark", `<th class="num">id</th>`, "<th>name</th>",
		`<td class="num">1</td>`, "<td>say &quot;hi&quot;, &lt;then&gt; go<br>now</td>",
		`<td class="null"><i>NULL</i></td>`, `<td class="num">NaN</td>`, "</tbody>\n</table></body></html>\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no %s in\n%s", want, out)
		}
	}
	if strings.Contains(out, "<script") || strings.Contains(out, "http") {
		t.Error("a page that runs nothing and fetches nothing")
	}
	if out := export(t, &rows{cols: []model.ColumnDef{col("a<b", model.TypeString, false)}, data: []model.Row{{"x\r\ny"}}}, Options{Format: HTML}); !strings.Contains(out, "<title>Export</title>") ||
		!strings.Contains(out, "<td>x<br>y</td>") || !strings.Contains(out, "<th>a&lt;b</th>") {
		t.Errorf("untitled: %s", out)
	}
}

func TestXMLNamesEachFieldAndKeepsEachValue(t *testing.T) {
	out := export(t, sample(), Options{Format: XML})
	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>` + "\n<rows>\n<row>", `<field name="id">1</field>`,
		`<field name="name">say "hi", &lt;then&gt; go` + "\n" + `now</field>`, `<field name="data" encoding="base64">3q0=</field>`,
		`<field name="nothing" null="true"/>`, `<field name="f">NaN</field>`, "</row>\n</rows>\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no %s in\n%s", want, out)
		}
	}
	odd := &rows{cols: []model.ColumnDef{col("a\"b\tc\x01", model.TypeString, false)}, data: []model.Row{{"a\x01b"}, {"c\rd"}, {string([]byte{0xff})}, {"e\uffff"}}}
	out = export(t, odd, Options{Format: XML})
	for _, want := range []string{
		"<field name=\"a&quot;b&#9;c\uFFFD\" encoding=\"base64\">YQFi</field>", "<field name=\"a&quot;b&#9;c\uFFFD\">c&#13;d</field>",
		`encoding="base64">/w==</field>`, `encoding="base64">Ze+/vw==</field>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no %s in\n%s", want, out)
		}
	}
	dec := xml.NewDecoder(strings.NewReader(out))
	for {
		if _, err := dec.Token(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("not well-formed: %v", err)
		}
	}
}

func TestTheNewFormatsAreNamed(t *testing.T) {
	for f, want := range map[Format][2]string{SQLInsert: {"SQL INSERT", "sql"}, HTML: {"HTML", "html"}, XML: {"XML", "xml"}} {
		if f.String() != want[0] || f.Extension() != want[1] {
			t.Errorf("%v: %s %s", f, f.String(), f.Extension())
		}
	}
	if got := fmt.Sprint(Formats()); got != "[CSV TSV Excel JSON NDJSON XML HTML Markdown SQL INSERT]" {
		t.Errorf("offered in the order %s", got)
	}
}
