package editor

import (
	"regexp"
	"strings"
)

// Search is a find query (FR-5.11, T1.60).
type Search struct {
	Text string
	// Regex treats Text as a regular expression (RE2 syntax). Otherwise it
	// is literal, and "(" is just a parenthesis.
	Regex bool
	// Case matches case exactly. Off by default, as in every macOS find bar.
	Case bool
	// Word matches whole words only. Word boundaries are ASCII (RE2's \b).
	Word bool
}

// Match is one occurrence.
type Match struct{ From, To Pos }

// compile turns a search into one regular expression, so literal and regex
// searches share a single matching engine.
func (s Search) compile() (*regexp.Regexp, error) {
	expr := s.Text
	if !s.Regex {
		expr = regexp.QuoteMeta(expr)
	}
	if s.Word {
		expr = `\b(?:` + expr + `)\b`
	}
	if !s.Case {
		expr = `(?i)` + expr
	}
	return regexp.Compile(`(?m)` + expr)
}

// matches is every non-empty match in the text, as byte offsets. Empty
// matches, as "a*" finds between letters, are skipped: they would have
// nothing to highlight and nothing to replace.
func (d *Document) matches(s Search, limit int) ([][]int, string, error) {
	if s.Text == "" {
		return nil, "", nil
	}
	re, err := s.compile()
	if err != nil {
		return nil, "", err
	}
	text := d.Text()
	var out [][]int
	for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
		if m[0] == m[1] {
			continue
		}
		out = append(out, m)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, text, nil
}

// FindAll is every match, in order, up to limit (0 for no limit): for
// highlighting them and saying "3 of 12".
func (d *Document) FindAll(s Search, limit int) ([]Match, error) {
	ms, _, err := d.matches(s, limit)
	out := make([]Match, len(ms))
	for i, m := range ms {
		out[i] = Match{d.PosAt(m[0]), d.PosAt(m[1])}
	}
	return out, err
}

// FindNext selects the next match after the selection, or before it when
// backward, wrapping around the document. ok is false when nothing matches.
func (d *Document) FindNext(s Search, backward bool) (ok bool, err error) {
	ms, _, err := d.matches(s, 0)
	if err != nil || len(ms) == 0 {
		return false, err
	}
	from, to, _ := d.Selection()
	start, end := d.Offset(from), d.Offset(to)
	pick := -1
	if backward {
		for i := len(ms) - 1; i >= 0; i-- {
			if ms[i][0] < start {
				pick = i
				break
			}
		}
		if pick < 0 {
			pick = len(ms) - 1
		}
	} else {
		for i, m := range ms {
			if m[0] >= end && !(m[0] == start && m[1] == end) {
				pick = i
				break
			}
		}
		if pick < 0 {
			pick = 0
		}
	}
	d.anchor, d.caret = d.PosAt(ms[pick][0]), d.PosAt(ms[pick][1])
	d.goal, d.sealed = -1, true
	return true, nil
}

// ReplaceSelection replaces the selection when it is exactly a match, then
// selects the next match: the find bar's Replace. It reports whether it
// replaced anything.
func (d *Document) ReplaceSelection(s Search, with string) (bool, error) {
	from, to, sel := d.Selection()
	if !sel {
		return d.FindNext(s, false)
	}
	re, err := s.compile()
	if err != nil {
		return false, err
	}
	selected := d.SelectedText()
	m := re.FindStringSubmatchIndex(selected)
	if m == nil || m[0] != 0 || m[1] != len(selected) {
		return d.FindNext(s, false) // the selection is not a match: just go to one
	}
	repl := with
	if s.Regex {
		repl = string(re.ExpandString(nil, with, selected, m))
	}
	d.sealed = true
	d.replace(from, to, repl, editOther)
	d.sealed = true
	d.FindNext(s, false)
	return true, nil
}

// ReplaceAll replaces every match as one undoable step and says how many it
// replaced. A regex replacement expands $1-style groups; a literal one is
// taken as written.
func (d *Document) ReplaceAll(s Search, with string) (int, error) {
	ms, text, err := d.matches(s, 0)
	if err != nil || len(ms) == 0 {
		return 0, err
	}
	re, _ := s.compile()
	var b strings.Builder
	last := 0
	for _, m := range ms {
		b.WriteString(text[last:m[0]])
		if s.Regex {
			b.Write(re.ExpandString(nil, with, text, m))
		} else {
			b.WriteString(with)
		}
		last = m[1]
	}
	b.WriteString(text[last:])
	caret := d.Offset(d.caret)
	d.sealed = true
	d.replace(Pos{}, d.end(), b.String(), editOther)
	d.sealed = true
	d.SetCaret(d.PosAt(min(caret, len(b.String()))), false)
	return len(ms), nil
}
