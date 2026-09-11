package transfer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// csvRows reads text as a CSV file whose first row names its columns.
func csvRows(t *testing.T, text string) model.RowStream {
	t.Helper()
	rs, err := Open(strings.NewReader(text), int64(len(text)), Options{Format: CSV, Header: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rs.Close() })
	return rs
}

func TestADryRunReadsEveryRowAndKeepsTheFirstProblems(t *testing.T) {
	var b strings.Builder
	b.WriteString("id,name\n")
	for i := 1; i <= 1200; i++ {
		switch {
		case i == 3:
			b.WriteString("x,\n") // two: not a number, and no name
		case i%400 == 0:
			fmt.Fprintf(&b, "%d.5,n\n", i)
		default:
			fmt.Fprintf(&b, "%d,n\n", i)
		}
	}
	to := columns(col("id", model.TypeInteger, false), col("name", model.TypeString, false))
	var reports []Checked
	c, err := Check(context.Background(), csvRows(t, b.String()), []Pair{{0, "id"}, {1, "name"}}, to, 3,
		func(p Checked) { reports = append(reports, p) })
	if err != nil {
		t.Fatal(err)
	}
	if c.Rows != 1200 || c.Bad != 4 || c.Values != 5 || c.Elapsed <= 0 {
		t.Errorf("every row read, and every problem counted: %+v", c)
	}
	var got []string
	for _, p := range c.Problems {
		got = append(got, fmt.Sprintf("%d %s", p.Row, p.Column))
	}
	if !reflect.DeepEqual(got, []string{"3 id", "3 name", "400 id"}) {
		t.Errorf("the first problems, by row and column: %v", got)
	}
	if s := c.Problems[0].Error(); s != "id: not a whole number (x)" {
		t.Errorf("each says why: %q", s)
	}
	if len(reports) != 2 || reports[0].Rows != 500 || reports[1].Rows != 1000 || reports[1].Bad != 3 ||
		reports[1].Values != 4 || reports[1].Problems != nil || reports[1].Elapsed <= 0 {
		t.Errorf("how far it has got, now and then: %+v", reports)
	}
}

func TestADryRunEndsWhereTheFileCannotBeRead(t *testing.T) {
	to := columns(col("id", model.TypeInteger, false))
	c, err := Check(context.Background(), csvRows(t, "id\n1\n2,3\n"), []Pair{{0, "id"}}, to, 10, nil)
	if err == nil || errors.Is(err, io.EOF) || c.Rows != 1 {
		t.Errorf("a row wider than the file's is an error, after the rows before it: %v %+v", err, c)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c, err := Check(ctx, csvRows(t, "id\n1\n"), []Pair{{0, "id"}}, to, 10, nil); !errors.Is(err, context.Canceled) || c.Rows != 0 {
		t.Errorf("a stopped dry run ends: %v %+v", err, c)
	}
	var many strings.Builder
	many.WriteString("id\n")
	for i := range reportEvery + 1 {
		fmt.Fprintf(&many, "%d\n", i)
	}
	if c, err := Check(context.Background(), csvRows(t, many.String()), []Pair{{0, "id"}}, to, 10, nil); err != nil || c.Rows != reportEvery+1 {
		t.Errorf("with no one to tell how far it has got: %v %+v", err, c)
	}
}

func TestTextLongerThanItsColumnIsSaid(t *testing.T) {
	code := col("code", model.TypeString, true)
	code.Type.Length = 3
	from := named(make([]string, 1))
	for text, fits := range map[string]bool{"abc": true, "abcd": false, "abc  ": true, "äöü": true, "äöüß": false} {
		if _, errs := Coerce(model.Row{text}, from, []Pair{{0, "code"}}, columns(code)); (len(errs) == 0) != fits {
			t.Errorf("%q fits in 3 characters: %v, said %v", text, fits, errs)
		}
	}
	if _, errs := Coerce(model.Row{"abcd"}, from, []Pair{{0, "code"}}, columns(code)); len(errs) != 1 || errs[0].Error() != "code: longer than 3 characters (abcd)" {
		t.Errorf("%v", errs)
	}
	code.Type.Length = -1
	if _, errs := Coerce(model.Row{"abcd"}, from, []Pair{{0, "code"}}, columns(code)); len(errs) != 0 {
		t.Errorf("a length not known holds anything: %v", errs)
	}
}

func TestAColumnThatNeedsAValueAndIsGivenNoneIsSaid(t *testing.T) {
	need := func(name string) model.Column {
		return model.Column{Name: name, Type: model.DataType{Class: model.TypeString}}
	}
	id, seq, total, made, note := need("id"), need("seq"), need("total"), need("made"), need("note")
	id.Identity, seq.AutoIncrement, total.Generated, made.HasDefault, note.Type.Nullable = true, true, "a + b", true, true
	cols := []model.Column{id, seq, total, made, note, need("name"), need("code")}
	if got := Unfilled(cols, []Pair{{0, "code"}}); !reflect.DeepEqual(got, []string{"name"}) {
		t.Errorf("only a column that cannot be NULL, has no default and the database does not fill: %v", got)
	}
	if got := Unfilled(cols, []Pair{{0, "code"}, {1, "name"}}); got != nil {
		t.Errorf("every column that needs a value is given one: %v", got)
	}
}
