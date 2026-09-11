package transfer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Finding what a file is (FR-10.4, ADR-0045): its format, its encoding, its
// delimiter and whether its first row names its columns, from its name and
// its first bytes, for an import to start from and a person to correct.

// Encoding is how a text file's characters are written.
type Encoding int

const (
	UTF8        Encoding = iota
	UTF16LE              // with its byte-order mark, or without
	UTF16BE              // with its byte-order mark, or without
	Windows1252          // what a text that is not UTF-8 most often is: Excel's CSV on Windows
)

func (e Encoding) String() string {
	switch e {
	case UTF8:
		return "UTF-8"
	case UTF16LE:
		return "UTF-16 LE"
	case UTF16BE:
		return "UTF-16 BE"
	case Windows1252:
		return "Windows-1252"
	}
	return fmt.Sprintf("Encoding(%d)", int(e))
}

const (
	sniffBytes = 64 << 10 // how much of a file detection reads
	sniffRows  = 20       // how many records detection weighs
)

// Detect says what a file is, from its name and its first bytes. A zip, or
// a name ending .xlsx, is a workbook; text is JSON where it opens with [,
// NDJSON where it opens with {, and delimited otherwise.
func Detect(r io.ReaderAt, size int64, name string) (Options, error) {
	head := make([]byte, min(size, sniffBytes))
	n, err := r.ReadAt(head, 0)
	if err != nil && err != io.EOF {
		return Options{}, err
	}
	head, cut := head[:n], int64(n) < size
	if bytes.HasPrefix(head, []byte("PK\x03\x04")) || strings.EqualFold(filepath.Ext(name), ".xlsx") {
		return detectSheet(r, size)
	}
	enc := detectEncoding(head)
	b, err := io.ReadAll(textOf(bytes.NewReader(head), enc))
	if err != nil && err != io.ErrUnexpectedEOF {
		return Options{}, err
	}
	text := string(b)
	if cut { // the last line read may be only part of one
		if i := strings.LastIndexByte(text, '\n'); i >= 0 {
			text = text[:i+1]
		}
	}
	switch t := strings.TrimLeft(text, " \t\r\n"); {
	case strings.HasPrefix(t, "["):
		return Options{Format: JSON, Encoding: enc}, nil
	case strings.HasPrefix(t, "{"):
		return Options{Format: NDJSON, Encoding: enc}, nil
	}
	opt := Options{Format: CSV, Comma: detectComma(text), Encoding: enc}
	if opt.Comma == '\t' {
		opt.Format = TSV
	}
	opt.Header = detectHeader(sampleRecords(text, opt.Comma))
	return opt, nil
}

// detectEncoding is a text's encoding: its byte-order mark's, where it has
// one; UTF-16 where nearly every other byte is a zero, as ASCII is in
// UTF-16, the rest's zeros far fewer (a character past ASCII can have one);
// UTF-8 where it reads as UTF-8; and Windows-1252 otherwise.
func detectEncoding(b []byte) Encoding {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return UTF8
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}):
		return UTF16LE
	case bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		return UTF16BE
	}
	even, odd := 0, 0
	for i, c := range b {
		if c == 0 {
			if i%2 == 0 {
				even++
			} else {
				odd++
			}
		}
	}
	switch {
	case len(b) >= 2 && odd > len(b)/4 && even*10 < odd:
		return UTF16LE
	case len(b) >= 2 && even > len(b)/4 && odd*10 < even:
		return UTF16BE
	case validUTF8(b):
		return UTF8
	}
	return Windows1252
}

// validUTF8 reports whether b is UTF-8, allowing the last character to be
// cut short, as a file's first bytes may cut it.
func validUTF8(b []byte) bool {
	i := len(b) - 1
	for i >= 0 && len(b)-i < utf8.UTFMax && !utf8.RuneStart(b[i]) {
		i--
	}
	if i >= 0 && !utf8.FullRune(b[i:]) {
		b = b[:i]
	}
	return utf8.Valid(b)
}

// detectComma is the delimiter a text's records agree on: of tab, comma,
// semicolon and pipe, the one whose first records most often share a width
// of more than one field, ties going in that order. A text that none splits
// is one column, read as CSV.
func detectComma(text string) rune {
	best, score := ',', 0
	for _, c := range []rune{'\t', ',', ';', '|'} {
		if s := agreement(sampleRecords(text, c)); s > score {
			best, score = c, s
		}
	}
	return best
}

// agreement is how many records share the commonest width, where that
// width is more than one field.
func agreement(recs [][]string) int {
	counts := map[int]int{}
	for _, r := range recs {
		counts[len(r)]++
	}
	best := 0
	for w, n := range counts {
		if w > 1 && n > best {
			best = n
		}
	}
	return best
}

// sampleRecords are a text's first records, split at comma.
func sampleRecords(text string, comma rune) [][]string {
	c := csv.NewReader(strings.NewReader(text))
	c.Comma, c.LazyQuotes, c.FieldsPerRecord = comma, true, -1
	var out [][]string
	for len(out) < sniffRows {
		rec, err := c.Read()
		if err != nil {
			break
		}
		out = append(out, rec)
	}
	return out
}

// kind is what a field holds, for weighing a header.
type kind int

const (
	kindText kind = iota
	kindNumber
	kindDate
)

var (
	numberRE = regexp.MustCompile(`^[+-]?(\d+([.,]\d*)?|[.,]\d+)([eE][+-]?\d+)?$`)
	dateRE   = regexp.MustCompile(`^(\d{4}-\d{1,2}-\d{1,2}|\d{1,2}[/.]\d{1,2}[/.]\d{2,4})`)
)

func kindOf(s string) kind {
	s = strings.TrimSpace(s)
	switch {
	case numberRE.MatchString(s):
		return kindNumber
	case dateRE.MatchString(s):
		return kindDate
	}
	return kindText
}

// detectHeader weighs whether the first record names the columns. A column
// votes for it where its first value is not of the kind all the rest are
// (a word over numbers or dates), or, where two or more of the rest are
// words all of one length, is of another length; it votes against where
// the first value is of the rest's kind and length. Where the votes are
// even, none cast among them, a first record of words alone, none
// repeated, is taken for names.
func detectHeader(recs [][]string) bool {
	if len(recs) == 0 {
		return false
	}
	first, rest := recs[0], recs[1:]
	votes := 0
	for i, name := range first {
		kinds, lengths, values := map[kind]bool{}, map[int]bool{}, 0
		for _, r := range rest {
			if i < len(r) && strings.TrimSpace(r[i]) != "" {
				kinds[kindOf(r[i])] = true
				lengths[utf8.RuneCountInString(r[i])] = true
				values++
			}
		}
		if len(kinds) != 1 {
			continue // mixed, or empty: no vote
		}
		var k kind
		for k = range kinds {
		}
		switch {
		case k != kindText && kindOf(name) != k:
			votes++
		case k != kindText:
			votes--
		case len(lengths) == 1 && values >= 2: // one value has no length to share
			var n int
			for n = range lengths {
			}
			if utf8.RuneCountInString(name) != n {
				votes++
			} else {
				votes--
			}
		}
	}
	return votes > 0 || (votes == 0 && words(first))
}

// words reports whether every field is a word, none empty and none repeated.
func words(rec []string) bool {
	seen := map[string]bool{}
	for _, f := range rec {
		f = strings.TrimSpace(f)
		if f == "" || kindOf(f) != kindText || seen[f] {
			return false
		}
		seen[f] = true
	}
	return len(rec) > 0
}

// detectSheet weighs a workbook's first sheet's first rows for a header.
func detectSheet(r io.ReaderAt, size int64) (Options, error) {
	sh, err := openSheet(r, size, Options{Format: XLSX})
	if err != nil {
		return Options{}, err
	}
	defer sh.Close()
	var recs [][]string
	for len(recs) < sniffRows {
		row, err := sh.Next(context.Background())
		if err != nil {
			break
		}
		rec := make([]string, len(row))
		for i, v := range row {
			if v != nil {
				rec[i] = fmt.Sprint(v)
			}
		}
		recs = append(recs, rec)
	}
	return Options{Format: XLSX, Header: detectHeader(recs)}, nil
}

// textOf turns a text in enc into UTF-8 as it is read, without its
// byte-order mark.
func textOf(r io.Reader, enc Encoding) io.Reader {
	br := bufio.NewReader(r)
	switch enc {
	case UTF16LE, UTF16BE:
		return &utf16Reader{r: br, big: enc == UTF16BE}
	case Windows1252:
		return &cp1252Reader{r: br}
	}
	if b, _ := br.Peek(3); bytes.Equal(b, []byte{0xEF, 0xBB, 0xBF}) {
		_, _ = br.Discard(3) // a byte-order mark is not text
	}
	return br
}

// utf16Reader reads UTF-16 as UTF-8. A character cut in half at the end is
// io.ErrUnexpectedEOF.
type utf16Reader struct {
	r       *bufio.Reader
	big     bool
	out     []byte
	started bool
}

func (u *utf16Reader) unit() (uint16, error) {
	a, err := u.r.ReadByte()
	if err != nil {
		return 0, err
	}
	b, err := u.r.ReadByte()
	if err != nil {
		return 0, io.ErrUnexpectedEOF
	}
	if u.big {
		return uint16(a)<<8 | uint16(b), nil
	}
	return uint16(b)<<8 | uint16(a), nil
}

func (u *utf16Reader) Read(p []byte) (int, error) {
	for len(u.out) == 0 {
		c, err := u.unit()
		if err != nil {
			return 0, err
		}
		r := rune(c)
		if utf16.IsSurrogate(r) {
			c2, err := u.unit()
			if err != nil {
				return 0, io.ErrUnexpectedEOF
			}
			r = utf16.DecodeRune(r, rune(c2))
		}
		if !u.started {
			u.started = true
			if r == '\uFEFF' {
				continue // a byte-order mark is not text
			}
		}
		u.out = utf8.AppendRune(u.out, r)
	}
	n := copy(p, u.out)
	u.out = u.out[n:]
	return n, nil
}

// cp1252 is Windows-1252's characters from 0x80 to 0x9F, where it is not
// Latin-1; its five holes are U+FFFD.
var cp1252 = [32]rune{
	0x20AC, 0xFFFD, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021, 0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0xFFFD, 0x017D, 0xFFFD,
	0xFFFD, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014, 0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0xFFFD, 0x017E, 0x0178,
}

// cp1252Reader reads Windows-1252 as UTF-8.
type cp1252Reader struct {
	r   *bufio.Reader
	out []byte
}

func (c *cp1252Reader) Read(p []byte) (int, error) {
	for len(c.out) < len(p) {
		b, err := c.r.ReadByte()
		if err != nil {
			if len(c.out) > 0 {
				break
			}
			return 0, err
		}
		switch {
		case b < 0x80:
			c.out = append(c.out, b)
		case b < 0xA0:
			c.out = utf8.AppendRune(c.out, cp1252[b-0x80])
		default:
			c.out = utf8.AppendRune(c.out, rune(b))
		}
	}
	n := copy(p, c.out)
	c.out = c.out[n:]
	return n, nil
}
