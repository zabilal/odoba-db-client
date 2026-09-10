package editor

// Incremental highlighting.
//
// Two caches, with deliberately different lifetimes:
//
//   - states[i], the lexer state at the START of line i, is maintained for the
//     WHOLE buffer. Correctly colouring line 4 000 requires knowing whether it
//     begins inside a block comment or a dollar-quoted body, and that is only
//     knowable by scanning from the top.
//   - tokens[i] is computed only for lines actually drawn, and thrown away
//     freely. There is no reason to hold tokens for 5 000 lines when 60 are
//     visible.
//
// The state chain is what makes editing cheap. After an edit, only the edited
// line is re-lexed; the cascade continues to the next line only while the end
// state keeps changing, which for ordinary typing is never. Opening a block
// comment at the top of a file is the pathological case and cascades to the
// end — still around a millisecond over 5 000 lines.
//
// None of this is possible with Chroma, which cannot resume from a state. See
// the note at the top of lexer.go.

// Highlighter maintains per-line lexer state and tokens for a buffer.
type Highlighter struct {
	lexer  *Lexer
	buf    *Buffer
	states []State
	tokens map[int][]Token
	valid  map[int]bool

	// statesValid is the number of leading entries in states that were
	// actually computed. Without it the early-stop below compares against
	// zero-valued States, which read as StateNormal and are indistinguishable
	// from a genuinely computed normal state — so the very first build stops
	// at line 1 and the rest of the chain is never computed. Any file with a
	// block comment then mis-colours from construction.
	statesValid int
}

// NewHighlighter builds a highlighter and computes the initial state chain.
func NewHighlighter(buf *Buffer, d *Dialect) *Highlighter {
	h := &Highlighter{
		lexer:  NewLexer(d),
		buf:    buf,
		tokens: make(map[int][]Token),
		valid:  make(map[int]bool),
	}
	h.rebuildStates(0)
	return h
}

// SetDialect switches language, invalidating everything.
func (h *Highlighter) SetDialect(d *Dialect) {
	h.lexer = NewLexer(d)
	h.tokens = make(map[int][]Token)
	h.valid = make(map[int]bool)
	h.statesValid = 0
	h.rebuildStates(0)
}

// rebuildStates recomputes the state chain from line `from` to the end,
// stopping early once a recomputed state matches what was already cached.
func (h *Highlighter) rebuildStates(from int) {
	n := h.buf.LineCount()
	if cap(h.states) < n+1 {
		grown := make([]State, n+1, (n+1)*2)
		copy(grown, h.states)
		h.states = grown
	}
	h.states = h.states[:n+1]

	if from == 0 {
		h.states[0] = State{}
	}

	for i := from; i < n; i++ {
		_, end := h.lexer.LexLine(h.buf.Line(i), h.states[i])

		// Invalidate BEFORE the early-return check. This line was just
		// re-lexed, which means either its text changed (i == from) or its
		// start state did (i > from, reached only by cascading). Either way
		// its cached tokens are stale.
		//
		// Doing this after the check leaves exactly one line stale: the one
		// the cascade stops on. That line has a new start state but its old
		// tokens, so a block comment closing mid-file leaves a line coloured
		// as comment forever. TestHighlightMatchesFullRelex caught it.
		delete(h.valid, i)
		delete(h.tokens, i)

		// Stop only when the state we are comparing against was genuinely
		// computed (i+1 < statesValid), not merely zero-valued.
		if i > from && i+1 < h.statesValid && h.states[i+1].Equal(end) {
			// The chain below is already correct. This is what keeps an
			// ordinary keystroke O(1) instead of O(lines).
			return
		}
		h.states[i+1] = end
	}
	h.statesValid = n + 1
}

// Apply updates the caches after a buffer edit.
func (h *Highlighter) Apply(e Edit) {
	// Line count changed: shift the state and token caches rather than
	// discarding them, so inserting a line near the top does not invalidate
	// the rest of the file.
	if e.LinesInserted != e.LinesRemoved {
		delta := e.LinesInserted - e.LinesRemoved
		h.shiftCaches(e.Line, delta)
	}
	for i := e.Line; i < e.Line+e.LinesInserted; i++ {
		delete(h.valid, i)
		delete(h.tokens, i)
	}
	h.rebuildStates(e.Line)
}

func (h *Highlighter) shiftCaches(from, delta int) {
	// Shift the state chain alongside the token caches. Discarding it instead
	// would make every newline an O(lines) rebuild.
	if len(h.states) > from+1 {
		shifted := make([]State, len(h.states)+delta)
		copy(shifted, h.states[:from+1])
		for i := from + 1; i < len(h.states); i++ {
			if j := i + delta; j >= 0 && j < len(shifted) {
				shifted[j] = h.states[i]
			}
		}
		h.states = shifted
	}
	if h.statesValid > from {
		h.statesValid += delta
	}

	nt := make(map[int][]Token, len(h.tokens))
	nv := make(map[int]bool, len(h.valid))
	for i, t := range h.tokens {
		if i < from {
			nt[i] = t
		} else if h.valid[i] {
			nt[i+delta] = t
		}
	}
	for i := range h.valid {
		if i < from {
			nv[i] = true
		} else {
			nv[i+delta] = true
		}
	}
	h.tokens, h.valid = nt, nv
}

// Tokens returns the tokens for one line, lexing on demand.
//
// The returned slice is owned by the highlighter and is valid until the next
// edit; callers must not retain it across one.
func (h *Highlighter) Tokens(line int) []Token {
	if line < 0 || line >= h.buf.LineCount() {
		return nil
	}
	if h.valid[line] {
		return h.tokens[line]
	}

	src := h.buf.Line(line)
	toks, _ := h.lexer.LexLine(src, h.stateAt(line))

	// LexLine's slice aliases the lexer's buffer, so it must be copied before
	// being cached.
	cp := make([]Token, len(toks))
	copy(cp, toks)

	h.tokens[line] = cp
	h.valid[line] = true
	return cp
}

// TokensForRange returns tokens for a visible range, which is all a renderer
// ever needs.
func (h *Highlighter) TokensForRange(first, last int) [][]Token {
	if first < 0 {
		first = 0
	}
	if last >= h.buf.LineCount() {
		last = h.buf.LineCount() - 1
	}
	if last < first {
		return nil
	}
	out := make([][]Token, 0, last-first+1)
	for i := first; i <= last; i++ {
		out = append(out, h.Tokens(i))
	}
	return out
}

func (h *Highlighter) stateAt(line int) State {
	if line < len(h.states) {
		return h.states[line]
	}
	return State{}
}

// StateAt exposes the line's starting state, for tests.
func (h *Highlighter) StateAt(line int) State { return h.stateAt(line) }

// CachedLines reports how many lines currently hold tokens, so tests can prove
// the token cache stays bounded to what is drawn.
func (h *Highlighter) CachedLines() int { return len(h.tokens) }

// EvictOutside drops cached tokens outside a window, bounding memory on a
// large file (NFR-P6).
func (h *Highlighter) EvictOutside(first, last int) {
	const slack = 200
	lo, hi := first-slack, last+slack
	for i := range h.tokens {
		if i < lo || i > hi {
			delete(h.tokens, i)
			delete(h.valid, i)
		}
	}
}
