// Package cellview prepares one value for the grid's cell viewer (FR-3.9):
// the whole of it, where a grid cell shows one line. JSON is indented and
// its tokens marked for colouring, bytes become a hex dump, a time is shown
// in both local time and UTC, and a value too long to show is cut at a
// stated limit rather than drawn until the application hangs.
//
// It draws nothing; the shell's viewer does. That keeps what the viewer
// says about a value testable without a window.
package cellview

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ikigai-db/ikigai-db/internal/export"
	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Limits on what is shown. The value itself is never changed: Copy Value
// copies all of it.
const (
	MaxText = 1 << 20  // characters of text
	MaxHex  = 64 << 10 // bytes in a hex dump
)

// Kind is how a value is shown.
type Kind uint8

const (
	// KindText is prose: wrapped, in the body font.
	KindText Kind = iota
	// KindCode is lines kept as they are, in a monospaced font: JSON, XML
	// and hex dumps.
	KindCode
	// KindNull is SQL NULL, shown as the word, distinct from empty text.
	KindNull
)

// Role is what a run of JSON is, for colouring.
type Role uint8

const (
	RolePlain Role = iota
	RoleKey
	RoleString
	RoleNumber
	RoleLiteral // true, false, null
)

// Span marks a run of one line with a role; Start and End are byte offsets.
type Span struct {
	Line, Start, End int
	Role             Role
}

// View is a value made ready to show.
type View struct {
	Kind  Kind
	Text  string   // KindText and KindNull
	Lines []string // KindCode
	Spans []Span   // KindCode, for JSON
	Size  string   // "1,204 characters", "16 bytes"
	Cut   bool     // longer than is shown
}

// Prepare makes a value ready to show. loc is the local time zone.
func Prepare(v any, col model.ColumnDef, loc *time.Location) View {
	switch x := v.(type) {
	case nil:
		return View{Kind: KindNull, Text: "NULL"}
	case model.JSON:
		return jsonView(x, len(x))
	case []any, map[string]any:
		b, err := json.Marshal(x)
		if err != nil {
			return textView(fmt.Sprint(x))
		}
		return jsonView(b, -1)
	case []byte:
		return hexView(x)
	case time.Time:
		local := x.In(loc).Format("2006-01-02 15:04:05.999999999 -07:00")
		return View{Kind: KindText, Text: local + "\n" + x.UTC().Format("2006-01-02 15:04:05.999999999") + " UTC"}
	case string:
		switch col.Type.Class {
		case model.TypeJSON:
			if json.Valid([]byte(x)) {
				return jsonView([]byte(x), -1)
			}
		case model.TypeXML:
			v := textView(x)
			v.Kind, v.Lines, v.Text = KindCode, strings.Split(v.Text, "\n"), ""
			return v
		}
		return textView(x)
	}
	return textView(export.Text(v, col))
}

func textView(s string) View {
	v := View{Kind: KindText, Text: s, Size: count(utf8.RuneCountInString(s), "character")}
	if utf8.RuneCountInString(s) > MaxText {
		cut := 0
		for i := range s {
			if cut == MaxText {
				v.Text, v.Cut = s[:i], true
				break
			}
			cut++
		}
	}
	return v
}

// jsonView indents JSON without re-encoding it, so every number keeps its
// digits and every key its place. size is the stored length, or -1 when the
// value was not stored as JSON text.
func jsonView(raw []byte, size int) View {
	var b bytes.Buffer
	if err := json.Indent(&b, raw, "", "  "); err != nil {
		return textView(string(raw))
	}
	v := View{Kind: KindCode}
	if size >= 0 {
		v.Size = count(size, "byte")
	}
	text := b.String()
	if len(text) > MaxText {
		text, v.Cut = text[:MaxText], true
	}
	v.Lines = strings.Split(text, "\n")
	for i, line := range v.Lines {
		v.Spans = append(v.Spans, jsonSpans(i, line)...)
	}
	return v
}

// jsonSpans marks the tokens of one line of indented JSON. A string
// followed by a colon is a key. Indented JSON never breaks a token across
// lines, so each line can be read alone.
func jsonSpans(n int, line string) []Span {
	var out []Span
	for i := 0; i < len(line); {
		c := line[i]
		switch {
		case c == '"':
			j := i + 1
			for j < len(line) && line[j] != '"' {
				if line[j] == '\\' {
					j++
				}
				j++
			}
			j = min(j+1, len(line))
			role := RoleString
			if k := strings.TrimLeft(line[j:], " "); strings.HasPrefix(k, ":") {
				role = RoleKey
			}
			out = append(out, Span{n, i, j, role})
			i = j
		case c == '-' || (c >= '0' && c <= '9'):
			j := i + 1
			for j < len(line) && strings.IndexByte("0123456789.eE+-", line[j]) >= 0 {
				j++
			}
			out = append(out, Span{n, i, j, RoleNumber})
			i = j
		case c == 't' || c == 'f' || c == 'n':
			j := i
			for j < len(line) && line[j] >= 'a' && line[j] <= 'z' {
				j++
			}
			out = append(out, Span{n, i, j, RoleLiteral})
			i = j
		default:
			i++
		}
	}
	return out
}

// hexView writes bytes as a hex dump: an offset, sixteen bytes in two groups
// of eight, and the printable ones as text.
func hexView(b []byte) View {
	v := View{Kind: KindCode, Size: count(len(b), "byte")}
	if len(b) > MaxHex {
		b, v.Cut = b[:MaxHex], true
	}
	for off := 0; off < len(b); off += 16 {
		row := b[off:min(off+16, len(b))]
		var sb strings.Builder
		fmt.Fprintf(&sb, "%08x ", off)
		for i := 0; i < 16; i++ {
			if i == 8 {
				sb.WriteByte(' ')
			}
			if i < len(row) {
				sb.WriteString(" " + hex.EncodeToString(row[i:i+1]))
			} else {
				sb.WriteString("   ")
			}
		}
		sb.WriteString("  |")
		for _, c := range row {
			if c >= 0x20 && c < 0x7f {
				sb.WriteByte(c)
			} else {
				sb.WriteByte('.')
			}
		}
		sb.WriteByte('|')
		v.Lines = append(v.Lines, sb.String())
	}
	return v
}

// count writes a size for a person: "1 byte", "1,204 characters".
func count(n int, noun string) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if n == 1 {
		return s + " " + noun
	}
	return s + " " + noun + "s"
}
