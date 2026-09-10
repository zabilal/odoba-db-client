package editor

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Pos is a position in a document: a line, and a byte offset into it. Byte
// offsets are what the Buffer and the lexer use; a Pos is always on a rune
// boundary.
type Pos struct{ Line, Col int }

// Less reports whether p comes before q.
func (p Pos) Less(q Pos) bool { return p.Line < q.Line || p.Line == q.Line && p.Col < q.Col }

// Motion is a caret movement.
type Motion int

const (
	Left Motion = iota
	Right
	Up
	Down
	WordLeft
	WordRight
	// LineStart goes to the first non-blank character, and from there to
	// column 0: the "smart home" that makes indented code comfortable.
	LineStart
	LineEnd
	DocStart
	DocEnd
)

// DefaultIndent is what Tab inserts. Spaces rather than a tab character:
// result grids, diffs and server error positions all count columns, and a tab
// makes each of them disagree with what is on screen.
const DefaultIndent = "    "

type editKind uint8

const (
	editOther editKind = iota
	editType           // typing; a run of it undoes as one step
	editErase          // backspace or forward delete; likewise
)

// change is one undoable step: text removed and text inserted at one place,
// and where the caret and selection were on either side of it.
type change struct {
	at                        Pos
	removed, inserted         string
	caretBefore, anchorBefore Pos
	caretAfter, anchorAfter   Pos
	kind                      editKind
}

// Document is the editor's model: the text, the caret and selection, and the
// undo history. The widget draws it and feeds it input. Nothing here needs a
// window, so the editing behaviour is tested directly.
//
// A Document is not safe for concurrent use; the widget owns it on the UI
// goroutine.
type Document struct {
	buf *Buffer
	hl  *Highlighter

	caret, anchor Pos // the selection runs between them; empty when equal
	// goal is the visual column vertical moves aim for, so moving down
	// through a short line and on does not drift left. -1 when unset.
	goal int

	undo, redo []change
	rev        uint64 // counts edits; see Revision
	// sealed stops the next edit merging into the previous undo step. Any
	// caret movement seals, so typing after a click is a separate step.
	sealed bool

	// Indent is what Tab inserts, and one level of indentation.
	Indent string
	// TabWidth is how wide a tab character draws. Tab never inserts one, but
	// pasted text can contain them.
	TabWidth int
}

// NewDocument builds a document over text, highlighted for a dialect.
func NewDocument(text string, d *sqllex.Dialect) *Document {
	b := NewBuffer(normalise(text))
	return &Document{buf: b, hl: NewHighlighter(b, d), goal: -1, sealed: true,
		Indent: DefaultIndent, TabWidth: 4}
}

// Buffer is the document's text.
func (d *Document) Buffer() *Buffer { return d.buf }

// Highlighter colours the document. It is kept in step with every edit.
func (d *Document) Highlighter() *Highlighter { return d.hl }

// Text is the whole document.
func (d *Document) Text() string { return d.buf.Text() }

// Offset is p's byte offset into Text().
func (d *Document) Offset(p Pos) int {
	p = d.clamp(p)
	n := 0
	for i := 0; i < p.Line; i++ {
		n += len(d.buf.Line(i)) + 1
	}
	return n + p.Col
}

// Caret is where the caret is. With a selection, it is the end that moves.
func (d *Document) Caret() Pos { return d.caret }

// Selection is the selected range, in order. ok is false when nothing is
// selected.
func (d *Document) Selection() (from, to Pos, ok bool) {
	from, to = d.anchor, d.caret
	if to.Less(from) {
		from, to = to, from
	}
	return from, to, from != to
}

// SelectedText is the selected text, or "".
func (d *Document) SelectedText() string {
	from, to, ok := d.Selection()
	if !ok {
		return ""
	}
	return d.textRange(from, to)
}

// SetCaret places the caret. With extend the selection grows or shrinks to
// it; without, any selection is dropped.
func (d *Document) SetCaret(p Pos, extend bool) {
	d.caret = d.clamp(p)
	if !extend {
		d.anchor = d.caret
	}
	d.goal = -1
	d.sealed = true
}

// SelectAll selects the whole document.
func (d *Document) SelectAll() {
	d.anchor = Pos{}
	d.caret = d.end()
	d.goal = -1
	d.sealed = true
}

// SelectWord selects what a double-click at p selects: the word there, a run
// of spaces, or a single punctuation character.
func (d *Document) SelectWord(p Pos) {
	p = d.clamp(p)
	l := d.buf.Line(p.Line)
	if l == "" {
		d.SetCaret(p, false)
		return
	}
	at := p.Col
	if at == len(l) {
		_, n := utf8.DecodeLastRuneInString(l)
		at -= n
	}
	r, n := utf8.DecodeRuneInString(l[at:])
	start, end := at, at+n
	if c := class(r); c != classPunct {
		for start > 0 {
			r, n := utf8.DecodeLastRuneInString(l[:start])
			if class(r) != c {
				break
			}
			start -= n
		}
		for end < len(l) {
			r, n := utf8.DecodeRuneInString(l[end:])
			if class(r) != c {
				break
			}
			end += n
		}
	}
	d.anchor, d.caret = Pos{p.Line, start}, Pos{p.Line, end}
	d.goal = -1
	d.sealed = true
}

// Move moves the caret. With extend it drags the selection along; without,
// an arrow key first collapses a selection to the side it points at, as every
// macOS text field does.
func (d *Document) Move(m Motion, extend bool) {
	if from, to, ok := d.Selection(); ok && !extend {
		switch m {
		case Left:
			d.SetCaret(from, false)
			return
		case Right:
			d.SetCaret(to, false)
			return
		}
	}
	p := d.caret
	switch m {
	case Up:
		d.MoveLines(-1, extend)
		return
	case Down:
		d.MoveLines(1, extend)
		return
	case Left:
		p = d.prevRune(p)
	case Right:
		p = d.nextRune(p)
	case WordLeft:
		p = d.wordLeft(p)
	case WordRight:
		p = d.wordRight(p)
	case LineStart:
		if first := indentOf(d.buf.Line(p.Line)); p.Col == len(first) {
			p.Col = 0
		} else {
			p.Col = len(first)
		}
	case LineEnd:
		p.Col = len(d.buf.Line(p.Line))
	case DocStart:
		p = Pos{}
	case DocEnd:
		p = d.end()
	}
	d.SetCaret(p, extend)
}

// MoveLines moves the caret n lines, up when negative, keeping its visual
// column across short lines. Page Up and Page Down are a page of this.
func (d *Document) MoveLines(n int, extend bool) {
	if d.goal < 0 {
		d.goal = d.Column(d.caret)
	}
	goal := d.goal
	var p Pos
	switch line := d.caret.Line + n; {
	case line < 0:
		p = Pos{} // up from the first line goes to its start, as on macOS
	case line >= d.buf.LineCount():
		p = d.end()
	default:
		p = d.PosAtColumn(line, goal)
	}
	d.SetCaret(p, extend)
	d.goal = goal
}

// Column is p's visual column, counting runes and expanding tabs. The editor
// draws in a monospaced font, so this times the character width is the x
// position.
func (d *Document) Column(p Pos) int {
	col := 0
	for _, r := range d.buf.Line(p.Line)[:d.clamp(p).Col] {
		col = d.advance(col, r)
	}
	return col
}

// PosAtColumn is the position on line nearest to a visual column, rounding
// to the nearer side of a character: what a click at that x means.
func (d *Document) PosAtColumn(line, col int) Pos {
	line = clampInt(line, 0, d.buf.LineCount()-1)
	l := d.buf.Line(line)
	at, x := 0, 0
	for at < len(l) {
		r, n := utf8.DecodeRuneInString(l[at:])
		next := d.advance(x, r)
		if col < next {
			if col-x > next-col {
				at += n
			}
			return Pos{line, at}
		}
		at, x = at+n, next
	}
	return Pos{line, at}
}

func (d *Document) advance(col int, r rune) int {
	if r == '\t' && d.TabWidth > 0 {
		return (col/d.TabWidth + 1) * d.TabWidth
	}
	return col + 1
}

// Insert types or pastes text, replacing the selection.
func (d *Document) Insert(text string) {
	text = normalise(text)
	from, to, sel := d.Selection()
	kind := editOther
	if !sel && utf8.RuneCountInString(text) == 1 && text != "\n" {
		kind = editType
	}
	if kind == editOther {
		d.sealed = true
	}
	d.replace(from, to, text, kind)
	if kind == editOther {
		d.sealed = true
	}
}

// Newline breaks the line at the caret, carrying the current line's
// indentation onto the new one.
func (d *Document) Newline() {
	from, to, _ := d.Selection()
	indent := indentOf(d.buf.Line(from.Line))
	if from.Col < len(indent) {
		indent = indent[:from.Col]
	}
	d.sealed = true
	d.replace(from, to, "\n"+indent, editOther)
	d.sealed = true
}

// Backspace deletes the selection, or the character before the caret. In
// leading spaces it deletes back to the previous indent stop, so undoing a
// Tab by hand takes one keypress rather than four.
func (d *Document) Backspace() {
	if from, to, ok := d.Selection(); ok {
		d.sealed = true
		d.replace(from, to, "", editOther)
		d.sealed = true
		return
	}
	p := d.caret
	from := d.prevRune(p)
	if from == p {
		return
	}
	if w := len(d.Indent); w > 0 && p.Col > 0 && strings.TrimLeft(d.buf.Line(p.Line)[:p.Col], " ") == "" {
		n := p.Col % w
		if n == 0 {
			n = w
		}
		from = Pos{p.Line, p.Col - n}
	}
	d.replace(from, p, "", editErase)
}

// Delete deletes the selection, or the character after the caret.
func (d *Document) Delete() {
	if from, to, ok := d.Selection(); ok {
		d.sealed = true
		d.replace(from, to, "", editOther)
		d.sealed = true
		return
	}
	if to := d.nextRune(d.caret); to != d.caret {
		d.replace(d.caret, to, "", editErase)
	}
}

// Cut removes the selection and returns it.
func (d *Document) Cut() string {
	text := d.SelectedText()
	if text != "" {
		d.Delete()
	}
	return text
}

// SetText replaces the whole document as one undoable step.
func (d *Document) SetText(text string) {
	d.sealed = true
	d.replace(Pos{}, d.end(), normalise(text), editOther)
	d.sealed = true
}

// Tab indents. Within a line it inserts spaces to the next indent stop; with
// a selection across lines it indents every line the selection touches.
func (d *Document) Tab() {
	if from, to, ok := d.Selection(); ok && from.Line != to.Line {
		d.IndentLines()
		return
	}
	from, _, _ := d.Selection()
	if w := len(d.Indent); w > 0 {
		d.Insert(strings.Repeat(" ", w-d.Column(from)%w))
	}
}

// IndentLines indents every line the selection touches, or the caret's line,
// wherever the caret is in it: ⌘].
func (d *Document) IndentLines() {
	d.reindent(func(line string) (string, int) {
		if line == "" {
			return line, 0 // no trailing whitespace on blank lines
		}
		return d.Indent + line, len(d.Indent)
	})
}

// Revision changes whenever the text does, so a caller can tell an edit from
// a caret move without comparing text.
func (d *Document) Revision() uint64 { return d.rev }

// maxBracketLines bounds the bracket search. Past it no pair is shown, which
// costs a highlight; scanning a 50 000-line script on every caret move would
// cost frames.
const maxBracketLines = 2000

var bracketPairs = map[byte]byte{'(': ')', '[': ']', '{': '}', ')': '(', ']': '[', '}': '{'}

// MatchBracket is the bracket beside the caret and its partner: the one just
// before the caret, else the one just after. Brackets inside strings,
// comments and quoted identifiers are text, not structure, and do not count.
func (d *Document) MatchBracket() (at, partner Pos, ok bool) {
	c := d.caret
	cands := []Pos{c}
	if c.Col > 0 {
		cands = []Pos{d.prevRune(c), c}
	}
	for _, p := range cands {
		if b, ok := d.bracketAt(p); ok {
			if q, ok := d.partner(p, b); ok {
				return p, q, true
			}
		}
	}
	return Pos{}, Pos{}, false
}

// structuralBrackets lists the offsets on a line where a bracket is code.
func (d *Document) structuralBrackets(line int) []int {
	l := d.buf.Line(line)
	var out []int
	for _, t := range d.hl.Tokens(line) {
		switch t.Kind {
		case sqllex.TokString, sqllex.TokComment, sqllex.TokQuotedIdent:
			continue
		}
		for i := int(t.Start); i < int(t.End) && i < len(l); i++ {
			if _, ok := bracketPairs[l[i]]; ok {
				out = append(out, i)
			}
		}
	}
	return out
}

func (d *Document) bracketAt(p Pos) (byte, bool) {
	l := d.buf.Line(p.Line)
	if p.Col >= len(l) {
		return 0, false
	}
	if _, ok := bracketPairs[l[p.Col]]; !ok {
		return 0, false
	}
	for _, i := range d.structuralBrackets(p.Line) {
		if i == p.Col {
			return l[p.Col], true
		}
	}
	return 0, false
}

func (d *Document) partner(p Pos, b byte) (Pos, bool) {
	want, depth := bracketPairs[b], 0
	visit := func(line, i int) (Pos, bool) {
		switch d.buf.Line(line)[i] {
		case b:
			depth++
		case want:
			if depth == 0 {
				return Pos{line, i}, true
			}
			depth--
		}
		return Pos{}, false
	}
	if b == '(' || b == '[' || b == '{' {
		for line := p.Line; line < d.buf.LineCount() && line <= p.Line+maxBracketLines; line++ {
			for _, i := range d.structuralBrackets(line) {
				if line == p.Line && i <= p.Col {
					continue
				}
				if q, ok := visit(line, i); ok {
					return q, true
				}
			}
		}
		return Pos{}, false
	}
	for line := p.Line; line >= 0 && line >= p.Line-maxBracketLines; line-- {
		bs := d.structuralBrackets(line)
		for k := len(bs) - 1; k >= 0; k-- {
			if line == p.Line && bs[k] >= p.Col {
				continue
			}
			if q, ok := visit(line, bs[k]); ok {
				return q, true
			}
		}
	}
	return Pos{}, false
}

// Outdent removes one level of indentation from every line the selection
// touches, or from the caret's line.
func (d *Document) Outdent() {
	d.reindent(func(line string) (string, int) {
		n := 0
		for n < len(d.Indent) && n < len(line) && line[n] == ' ' {
			n++
		}
		if n == 0 && strings.HasPrefix(line, "\t") {
			n = 1
		}
		return line[n:], -n
	})
}

// reindent rewrites each line the selection touches, as one undoable step,
// shifting the caret and selection with their lines' text.
func (d *Document) reindent(fn func(line string) (string, int)) {
	from, to, _ := d.Selection()
	first, last := from.Line, to.Line
	if last > first && to.Col == 0 {
		last-- // a selection ending at a line's start does not include that line
	}
	lines := make([]string, 0, last-first+1)
	shift := map[int]int{}
	for i := first; i <= last; i++ {
		l, delta := fn(d.buf.Line(i))
		lines = append(lines, l)
		shift[i] = delta
	}
	block := strings.Join(lines, "\n")
	if block == d.textRange(Pos{first, 0}, Pos{last, len(d.buf.Line(last))}) {
		return
	}
	move := func(p Pos) Pos {
		if delta, ok := shift[p.Line]; ok && (p.Col > 0 || delta < 0) {
			p.Col = max(0, p.Col+delta)
		}
		return p
	}
	caret, anchor := move(d.caret), move(d.anchor)
	d.sealed = true
	d.replace(Pos{first, 0}, Pos{last, len(d.buf.Line(last))}, block, editOther)
	d.caret, d.anchor = d.clamp(caret), d.clamp(anchor)
	top := &d.undo[len(d.undo)-1]
	top.caretAfter, top.anchorAfter = d.caret, d.anchor
	d.sealed = true
}

// Undo reverts the last step. It reports whether there was one.
func (d *Document) Undo() bool {
	if len(d.undo) == 0 {
		return false
	}
	c := d.undo[len(d.undo)-1]
	d.undo = d.undo[:len(d.undo)-1]
	d.apply(c.at, c.inserted, c.removed)
	d.caret, d.anchor = c.caretBefore, c.anchorBefore
	d.redo = append(d.redo, c)
	d.goal = -1
	d.sealed = true
	return true
}

// Redo reapplies the last undone step. It reports whether there was one.
func (d *Document) Redo() bool {
	if len(d.redo) == 0 {
		return false
	}
	c := d.redo[len(d.redo)-1]
	d.redo = d.redo[:len(d.redo)-1]
	d.apply(c.at, c.removed, c.inserted)
	d.caret, d.anchor = c.caretAfter, c.anchorAfter
	d.undo = append(d.undo, c)
	d.goal = -1
	d.sealed = true
	return true
}

// CanUndo and CanRedo drive the Edit menu's enabled state.
func (d *Document) CanUndo() bool { return len(d.undo) > 0 }
func (d *Document) CanRedo() bool { return len(d.redo) > 0 }

// replace swaps the text in [from, to) for text, keeps the highlighter in
// step, records the change for undo, and leaves the caret after the text.
func (d *Document) replace(from, to Pos, text string, kind editKind) {
	c := change{at: from, removed: d.textRange(from, to), inserted: text,
		caretBefore: d.caret, anchorBefore: d.anchor, kind: kind}
	d.apply(from, c.removed, text)
	end := endOf(from, text)
	d.caret, d.anchor = end, end
	c.caretAfter, c.anchorAfter = end, end
	d.record(c)
	d.goal = -1
}

// apply performs a change on the buffer and tells the highlighter.
func (d *Document) apply(at Pos, removed, inserted string) {
	d.rev++
	if removed != "" {
		end := endOf(at, removed)
		d.hl.Apply(d.buf.DeleteRange(at.Line, at.Col, end.Line, end.Col))
	}
	if inserted != "" {
		_, _, e := d.buf.Insert(at.Line, at.Col, inserted)
		d.hl.Apply(e)
	}
}

// record pushes a change onto the undo stack, merging a run of typing or of
// deleting into one step. Typing breaks into a new step at each word, so
// undo takes back a word at a time rather than a whole paragraph.
func (d *Document) record(c change) {
	d.redo = d.redo[:0]
	if n := len(d.undo); n > 0 && !d.sealed && c.kind != editOther && d.undo[n-1].kind == c.kind {
		top := &d.undo[n-1]
		switch {
		case c.kind == editType && endOf(top.at, top.inserted) == c.at && !wordBreak(top.inserted, c.inserted):
			top.inserted += c.inserted
		case c.kind == editErase && endOf(c.at, c.removed) == top.at: // backspace
			top.at, top.removed = c.at, c.removed+top.removed
		case c.kind == editErase && c.at == top.at: // forward delete
			top.removed += c.removed
		default:
			d.undo = append(d.undo, c)
			d.sealed = false
			return
		}
		top.caretAfter, top.anchorAfter = c.caretAfter, c.anchorAfter
		return
	}
	d.undo = append(d.undo, c)
	d.sealed = false
}

// wordBreak reports whether typing next after prev starts a new word.
func wordBreak(prev, next string) bool {
	p, _ := utf8.DecodeLastRuneInString(prev)
	n, _ := utf8.DecodeRuneInString(next)
	return unicode.IsSpace(n) && !unicode.IsSpace(p)
}

func (d *Document) textRange(from, to Pos) string {
	if from.Line == to.Line {
		return d.buf.Line(from.Line)[from.Col:to.Col]
	}
	var b strings.Builder
	b.WriteString(d.buf.Line(from.Line)[from.Col:])
	for l := from.Line + 1; l < to.Line; l++ {
		b.WriteByte('\n')
		b.WriteString(d.buf.Line(l))
	}
	b.WriteByte('\n')
	b.WriteString(d.buf.Line(to.Line)[:to.Col])
	return b.String()
}

func (d *Document) end() Pos {
	last := d.buf.LineCount() - 1
	return Pos{last, len(d.buf.Line(last))}
}

// clamp moves p onto the document and back onto a rune boundary.
func (d *Document) clamp(p Pos) Pos {
	if p.Line < 0 {
		return Pos{}
	}
	if p.Line >= d.buf.LineCount() {
		return d.end()
	}
	l := d.buf.Line(p.Line)
	p.Col = clampInt(p.Col, 0, len(l))
	for p.Col > 0 && p.Col < len(l) && !utf8.RuneStart(l[p.Col]) {
		p.Col--
	}
	return p
}

func (d *Document) prevRune(p Pos) Pos {
	if p.Col > 0 {
		_, n := utf8.DecodeLastRuneInString(d.buf.Line(p.Line)[:p.Col])
		return Pos{p.Line, p.Col - n}
	}
	if p.Line > 0 {
		return Pos{p.Line - 1, len(d.buf.Line(p.Line - 1))}
	}
	return p
}

func (d *Document) nextRune(p Pos) Pos {
	l := d.buf.Line(p.Line)
	if p.Col < len(l) {
		_, n := utf8.DecodeRuneInString(l[p.Col:])
		return Pos{p.Line, p.Col + n}
	}
	if p.Line < d.buf.LineCount()-1 {
		return Pos{p.Line + 1, 0}
	}
	return p
}

// wordLeft skips back over spaces, then over one run of word characters or
// of punctuation. At a line's start it steps to the previous line's end.
func (d *Document) wordLeft(p Pos) Pos {
	if p.Col == 0 {
		return d.prevRune(p)
	}
	l := d.buf.Line(p.Line)
	at := p.Col
	for at > 0 {
		r, n := utf8.DecodeLastRuneInString(l[:at])
		if class(r) != classSpace {
			break
		}
		at -= n
	}
	if at > 0 {
		r, _ := utf8.DecodeLastRuneInString(l[:at])
		c := class(r)
		for at > 0 {
			r, n := utf8.DecodeLastRuneInString(l[:at])
			if class(r) != c {
				break
			}
			at -= n
		}
	}
	return Pos{p.Line, at}
}

// wordRight mirrors wordLeft.
func (d *Document) wordRight(p Pos) Pos {
	l := d.buf.Line(p.Line)
	if p.Col == len(l) {
		return d.nextRune(p)
	}
	at := p.Col
	for at < len(l) {
		r, n := utf8.DecodeRuneInString(l[at:])
		if class(r) != classSpace {
			break
		}
		at += n
	}
	if at < len(l) {
		r, _ := utf8.DecodeRuneInString(l[at:])
		c := class(r)
		for at < len(l) {
			r, n := utf8.DecodeRuneInString(l[at:])
			if class(r) != c {
				break
			}
			at += n
		}
	}
	return Pos{p.Line, at}
}

type charClass uint8

const (
	classSpace charClass = iota
	classWord
	classPunct
)

// class groups characters for word motion. An identifier character is a
// letter, digit, underscore or dollar sign ($ is legal in PostgreSQL and
// MySQL identifiers).
func class(r rune) charClass {
	switch {
	case unicode.IsSpace(r):
		return classSpace
	case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '$':
		return classWord
	}
	return classPunct
}

func indentOf(line string) string { return line[:len(line)-len(strings.TrimLeft(line, " \t"))] }

// endOf is where text inserted at at ends.
func endOf(at Pos, text string) Pos {
	if i := strings.LastIndexByte(text, '\n'); i >= 0 {
		return Pos{at.Line + strings.Count(text, "\n"), len(text) - i - 1}
	}
	return Pos{at.Line, at.Col + len(text)}
}

// normalise makes every line break a \n, whatever the text was pasted from.
func normalise(text string) string {
	if !strings.Contains(text, "\r") {
		return text
	}
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
}
