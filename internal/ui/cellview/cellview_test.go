package cellview

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

func TestJSONIsIndentedExactlyAndMarked(t *testing.T) {
	v := Prepare(model.JSON(`{"b":1.50,"a":[true,null,"x\"y"]}`), model.ColumnDef{}, time.UTC)
	want := []string{`{`, `  "b": 1.50,`, `  "a": [`, `    true,`, `    null,`, `    "x\"y"`, `  ]`, `}`}
	if v.Kind != KindCode || !reflect.DeepEqual(v.Lines, want) {
		t.Fatalf("lines %q", v.Lines)
	}
	role := func(line int, text string) Role {
		for _, s := range v.Spans {
			if s.Line == line && v.Lines[line][s.Start:s.End] == text {
				return s.Role
			}
		}
		return RolePlain
	}
	for _, c := range []struct {
		line int
		text string
		want Role
	}{
		{1, `"b"`, RoleKey}, {1, "1.50", RoleNumber}, {2, `"a"`, RoleKey},
		{3, "true", RoleLiteral}, {4, "null", RoleLiteral}, {5, `"x\"y"`, RoleString},
	} {
		if got := role(c.line, c.text); got != c.want {
			t.Errorf("%s on line %d: role %d, want %d", c.text, c.line, got, c.want)
		}
	}
	if v.Size != "33 bytes" {
		t.Errorf("size %q", v.Size)
	}
}

func TestBytesBecomeAHexDump(t *testing.T) {
	b := []byte("ABCDEFGHIJKLMNOP\x00\xffq")
	v := Prepare(b, model.ColumnDef{}, time.UTC)
	want := []string{
		"00000000  41 42 43 44 45 46 47 48  49 4a 4b 4c 4d 4e 4f 50  |ABCDEFGHIJKLMNOP|",
		"00000010  00 ff 71                                          |..q|",
	}
	if !reflect.DeepEqual(v.Lines, want) || v.Size != "19 bytes" {
		t.Errorf("dump\n%s\nsize %q", strings.Join(v.Lines, "\n"), v.Size)
	}
	big := Prepare(make([]byte, MaxHex+1), model.ColumnDef{}, time.UTC)
	if !big.Cut || len(big.Lines) != MaxHex/16 || big.Size != "65,537 bytes" {
		t.Errorf("a dump past the limit: cut %v, %d lines, size %q", big.Cut, len(big.Lines), big.Size)
	}
}

func TestOtherValuesAreShownWhole(t *testing.T) {
	if v := Prepare(nil, model.ColumnDef{}, time.UTC); v.Kind != KindNull {
		t.Error("NULL is shown as NULL, not as empty text")
	}
	when := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	plus2 := time.FixedZone("", 7200)
	instant := model.ColumnDef{Type: model.DataType{Class: model.TypeTimestamp, TimeZone: true}}
	if v := Prepare(when, instant, plus2); v.Text != "2024-05-06 09:08:09 +02:00\n2024-05-06 07:08:09 UTC" {
		t.Errorf("an instant is shown local and in UTC: %q", v.Text)
	}
	wall := model.ColumnDef{Type: model.DataType{Class: model.TypeTimestamp}}
	if v := Prepare(when, wall, plus2); v.Text != "2024-05-06 07:08:09" {
		t.Errorf("a time with no zone is shown as stored: %q", v.Text)
	}
	date := model.ColumnDef{Type: model.DataType{Class: model.TypeDate}}
	if v := Prepare(when, date, plus2); v.Text != "2024-05-06" {
		t.Errorf("a date is a date: %q", v.Text)
	}
	if v := Prepare(model.Decimal("12345678901234567890.1234567890"), model.ColumnDef{}, time.UTC); v.Text != "12345678901234567890.1234567890" {
		t.Errorf("decimal %q", v.Text)
	}
	xml := model.ColumnDef{Type: model.DataType{Class: model.TypeXML}}
	if v := Prepare("<a>\n<b/>\n</a>", xml, time.UTC); v.Kind != KindCode || len(v.Lines) != 3 {
		t.Errorf("XML is kept in its lines: %+v", v)
	}
	long := strings.Repeat("é", MaxText+5)
	if v := Prepare(long, model.ColumnDef{}, time.UTC); !v.Cut || len([]rune(v.Text)) != MaxText || v.Size != "1,048,581 characters" {
		t.Errorf("long text: cut %v, %d runes, size %q", v.Cut, len([]rune(v.Text)), v.Size)
	}
}
