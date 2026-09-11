package transfer

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

func detect(t *testing.T, data []byte, name string) Options {
	t.Helper()
	opt, err := Detect(bytes.NewReader(data), int64(len(data)), name)
	if err != nil {
		t.Fatal(err)
	}
	return opt
}

// utf16Bytes writes s as UTF-16, big-endian or not, with a byte-order mark
// or without.
func utf16Bytes(s string, big, mark bool) []byte {
	units := utf16.Encode([]rune(s))
	if mark {
		units = append([]uint16{0xFEFF}, units...)
	}
	var b bytes.Buffer
	for _, u := range units {
		if big {
			b.Write([]byte{byte(u >> 8), byte(u)})
		} else {
			b.Write([]byte{byte(u), byte(u >> 8)})
		}
	}
	return b.Bytes()
}

func TestAFilesEncodingIsFound(t *testing.T) {
	text := "name,price\ncafé,€3\n"
	for _, c := range []struct {
		name string
		data []byte
		want Encoding
	}{
		{"UTF-8", []byte(text), UTF8},
		{"UTF-8 with its mark", append([]byte{0xEF, 0xBB, 0xBF}, text...), UTF8},
		{"UTF-16 LE with its mark", utf16Bytes(text, false, true), UTF16LE},
		{"UTF-16 BE with its mark", utf16Bytes(text, true, true), UTF16BE},
		{"UTF-16 LE without", utf16Bytes(text, false, false), UTF16LE},
		{"UTF-16 BE without", utf16Bytes(text, true, false), UTF16BE},
		{"Windows-1252", []byte("name,price\ncaf\xe9,\x803\n"), Windows1252},
	} {
		if got := detect(t, c.data, "f.csv"); got.Encoding != c.want || got.Format != CSV || !got.Header {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
	// A first read that ends in the middle of a character is still UTF-8.
	if !validUTF8([]byte("caf\xc3")) || validUTF8([]byte("caf\xe9x")) {
		t.Error("a character cut short at the end is allowed, and nowhere else")
	}
}

func TestAnEncodedFileIsReadAsUTF8(t *testing.T) {
	text := "name,price\ncafé 😀,€3\n" // an emoji is two units of UTF-16
	for _, c := range []struct {
		data []byte
		want model.Row
	}{
		{utf16Bytes(text, false, true), model.Row{"café 😀", "€3"}},
		{utf16Bytes(text, true, false), model.Row{"café 😀", "€3"}},
		{[]byte("name,price\ncaf\xe9,\x803\n"), model.Row{"café", "€3"}},
	} {
		opt := detect(t, c.data, "f.csv")
		cols, rows, err := readAll(t, c.data, opt)
		if err != nil || !reflect.DeepEqual(names(cols), []string{"name", "price"}) || !reflect.DeepEqual(rows, []model.Row{c.want}) {
			t.Errorf("%v: %q %v %v", opt.Encoding, names(cols), rows, err)
		}
	}
	// JSON with a byte-order mark is read too.
	_, rows, err := readAll(t, append([]byte{0xEF, 0xBB, 0xBF}, `[{"a": 1}]`...), Options{Format: JSON})
	if err != nil || len(rows) != 1 {
		t.Errorf("%v %v", rows, err)
	}
}

func TestADelimiterIsFound(t *testing.T) {
	for _, c := range []struct {
		text   string
		format Format
		comma  rune
	}{
		{"a;b;c\n1;2,5;3\n4;5,5;6\n", CSV, ';'},
		{"a\tb\n1\t2\n", TSV, '\t'},
		{"a|b\n1|2\n", CSV, '|'},
		{"a,b\n\"1,5\",2\n", CSV, ','},
		{"a,b;c\n1,2;3\n", CSV, ','}, // a tie goes to the comma
		{"one column\nx\ny\n", CSV, ','},
	} {
		if got := detect(t, []byte(c.text), "f.txt"); got.Format != c.format || got.Comma != c.comma {
			t.Errorf("%q: %v %q", c.text, got.Format, got.Comma)
		}
	}
	data := []byte("a;b\n1;2\n")
	opt := detect(t, data, "f.csv")
	if _, rows, err := readAll(t, data, opt); err != nil || !reflect.DeepEqual(rows, []model.Row{{"1", "2"}}) {
		t.Errorf("read with the delimiter found: %v %v", rows, err)
	}
}

func TestAHeaderIsWeighed(t *testing.T) {
	for text, header := range map[string]bool{
		"id,name\n1,a\n2,b\n":                   true,  // a word over numbers
		"1,a\n2,b\n":                            false, // numbers all the way
		"when,what\n2024-01-02,x\n2024-02-03,y": true,  // a word over dates
		"code\nAB\nCD\nEF\n":                    true,  // a word of another length over codes
		"AB\nCD\nEF\n":                          false, // codes all the way
		"name,city\n":                           true,  // words alone, none repeated
		"a,a\n":                                 false, // a repeated word is data
		"when,note\n2024-1-2,abcd\n2024-12-25,efgh\n": true,  // a word over dates of other lengths
		"id,n\n1,a\n2,b\n":                            true,  // a word over numbers, though its length is theirs
		"1,abc\n2,cd\n3,ef\n":                         false, // a number over numbers outweighs a length
		"ab,cd\nxy,zw\n":                              true,  // one row under words: no length to go by, so words are names
		"":                                            false,
	} {
		if got := detect(t, []byte(text), "f.csv").Header; got != header {
			t.Errorf("%q: header %v", text, got)
		}
	}
}

func TestJSONAndWorkbooksAreFound(t *testing.T) {
	if got := detect(t, []byte("  [{\"a\":1}]"), "f.txt"); got.Format != JSON {
		t.Errorf("an array: %v", got.Format)
	}
	if got := detect(t, []byte("{\"a\":1}\n{\"a\":2}\n"), "f.txt"); got.Format != NDJSON {
		t.Errorf("objects a line: %v", got.Format)
	}
	wb := book(t, "", dataSheet)
	if got := detect(t, wb, "report.bin"); got.Format != XLSX || !got.Header {
		t.Errorf("a workbook by its bytes, its first row names: %+v", got)
	}
	if _, err := Detect(bytes.NewReader([]byte("a,b")), 3, "f.XLSX"); err == nil || !strings.Contains(err.Error(), "not a workbook") {
		t.Errorf("a name ending .xlsx is taken for a workbook: %v", err)
	}
}

func TestOnlyTheFilesStartIsRead(t *testing.T) {
	var b strings.Builder
	b.WriteString("id;name\n")
	for i := 0; b.Len() < sniffBytes*2; i++ {
		b.WriteString("1;row\n")
	}
	data := []byte(b.String())
	if got := detect(t, data, "big.csv"); got.Comma != ';' || !got.Header {
		t.Errorf("%+v", got)
	}
}
