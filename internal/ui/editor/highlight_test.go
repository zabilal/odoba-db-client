package editor

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/testutil/race"
)

func loadBuffer(tb testing.TB) *Buffer {
	tb.Helper()
	b, err := os.ReadFile("testdata/workload.sql")
	if err != nil {
		tb.Skipf("workload not generated: %v", err)
	}
	return NewBuffer(string(b))
}

func TestBufferInsertAndDelete(t *testing.T) {
	b := NewBuffer("hello\nworld")

	line, col, e := b.Insert(0, 5, " there")
	if b.Line(0) != "hello there" || line != 0 || col != 11 {
		t.Fatalf("insert: line %q, caret (%d,%d)", b.Line(0), line, col)
	}
	if e.LinesInserted != 1 || e.LinesRemoved != 1 {
		t.Errorf("single-line insert reported a line-count change: %+v", e)
	}

	line, col, e = b.Insert(0, 5, "\nnew\n")
	if b.LineCount() != 4 {
		t.Fatalf("multi-line insert gave %d lines, want 4: %q", b.LineCount(), b.Text())
	}
	if e.LinesInserted != 3 {
		t.Errorf("LinesInserted = %d, want 3", e.LinesInserted)
	}

	b = NewBuffer("one\ntwo\nthree")
	b.DeleteRange(0, 1, 2, 2)
	if got := b.Text(); got != "oree" {
		t.Errorf("cross-line delete = %q, want %q", got, "oree")
	}
}

func TestBufferAlwaysHasALine(t *testing.T) {
	// The caret must always have somewhere to be.
	b := NewBuffer("")
	if b.LineCount() != 1 {
		t.Errorf("empty buffer has %d lines, want 1", b.LineCount())
	}
	b.DeleteRange(0, 0, 0, 0)
	if b.LineCount() != 1 {
		t.Error("delete emptied the buffer of lines")
	}
}

func TestBufferNormalisesCRLF(t *testing.T) {
	b := NewBuffer("a\r\nb\r\nc")
	if b.LineCount() != 3 {
		t.Fatalf("CRLF gave %d lines, want 3", b.LineCount())
	}
	if strings.Contains(b.Text(), "\r") {
		t.Error("carriage returns survived into the buffer")
	}
}

func TestHighlightMatchesFullRelex(t *testing.T) {
	// The incremental result must equal what a from-scratch lex produces.
	// This is the property that makes incremental highlighting trustworthy;
	// everything else is an optimisation on top of it.
	buf := loadBuffer(t)
	h := NewHighlighter(buf, PostgreSQL)

	// Edit inside a function body, near a block comment, and at the very top.
	for _, edit := range []struct {
		line int
		text string
	}{
		{0, "-- prefix"},
		{2500, " AND 1=1"},
		{4999, ";"},
	} {
		l, c, _ := buf.Insert(edit.line, 0, edit.text)
		_ = l
		_ = c
		h.Apply(Edit{Line: edit.line, LinesRemoved: 1, LinesInserted: 1})
	}

	// The oracle is a direct sequential lex from line 0, NOT another
	// Highlighter. Comparing two Highlighters hid a construction-time bug
	// behind itself: both were wrong in the same way, so they agreed.
	oracle := NewLexer(PostgreSQL)
	st := State{}
	want := make([][]Token, buf.LineCount())
	for i := 0; i < buf.LineCount(); i++ {
		var toks []Token
		toks, st = oracle.LexLine(buf.Line(i), st)
		cp := make([]Token, len(toks))
		copy(cp, toks)
		want[i] = cp
	}

	for i := 0; i < buf.LineCount(); i++ {
		got, want := h.Tokens(i), want[i]
		if len(got) != len(want) {
			t.Fatalf("line %d: %d tokens incrementally, %d from scratch\n  %q",
				i, len(got), len(want), buf.Line(i))
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("line %d token %d: %+v incrementally, %+v from scratch",
					i, j, got[j], want[j])
			}
		}
	}
}

func TestOpeningBlockCommentCascades(t *testing.T) {
	// The pathological edit: opening a comment at the top must recolour
	// everything below it.
	buf := NewBuffer("SELECT 1;\nSELECT 2;\nSELECT 3;")
	h := NewHighlighter(buf, PostgreSQL)

	if h.StateAt(2).Kind != StateNormal {
		t.Fatal("precondition: line 2 should start normal")
	}

	buf.Insert(0, 0, "/*")
	h.Apply(Edit{Line: 0, LinesRemoved: 1, LinesInserted: 1})

	if h.StateAt(2).Kind != StateBlockComment {
		t.Errorf("line 2 state = %v, want StateBlockComment; the cascade stopped short",
			h.StateAt(2).Kind)
	}
	toks := h.Tokens(2)
	if len(toks) != 1 || toks[0].Kind != TokComment {
		t.Errorf("line 2 should be entirely comment, got %+v", toks)
	}
}

func TestOrdinaryEditDoesNotCascade(t *testing.T) {
	// The common case must stay O(1). If a keystroke re-lexed the whole file
	// the editor would be unusable on a large script.
	buf := loadBuffer(t)
	h := NewHighlighter(buf, PostgreSQL)

	// Touch the far end of the file so its state is cached and valid.
	_ = h.Tokens(buf.LineCount() - 1)
	before := h.StateAt(buf.LineCount() - 1)

	buf.Insert(2500, 0, "x")
	h.Apply(Edit{Line: 2500, LinesRemoved: 1, LinesInserted: 1})

	if after := h.StateAt(buf.LineCount() - 1); !after.Equal(before) {
		t.Errorf("an ordinary edit changed the end-of-file state: %+v -> %+v", before, after)
	}
}

func TestInsertingLinesShiftsCachesRatherThanDiscarding(t *testing.T) {
	buf := loadBuffer(t)
	h := NewHighlighter(buf, PostgreSQL)

	// Warm a window far below the edit.
	h.TokensForRange(4000, 4060)
	warm := h.CachedLines()
	if warm == 0 {
		t.Fatal("precondition: nothing cached")
	}

	buf.Insert(100, 0, "SELECT 1;\n")
	h.Apply(Edit{Line: 100, LinesRemoved: 1, LinesInserted: 2})

	if h.CachedLines() == 0 {
		t.Error("inserting a line discarded the whole token cache; " +
			"it should shift and keep distant lines")
	}
}

func TestTokenCacheStaysBounded(t *testing.T) {
	// NFR-P6: holding tokens for every line of a large file is exactly the
	// kind of unbounded growth the memory budget forbids.
	buf := loadBuffer(t)
	h := NewHighlighter(buf, PostgreSQL)

	for first := 0; first+60 < buf.LineCount(); first += 60 {
		h.TokensForRange(first, first+60)
		h.EvictOutside(first, first+60)
	}
	if got := h.CachedLines(); got > 600 {
		t.Errorf("token cache holds %d lines after scrolling the file; "+
			"eviction is not bounding it", got)
	}
}

// --- GATE G0-2 -------------------------------------------------------------

// TestGateG0_2 is spike W2's gate (TASKS.md T0.48).
//
// NFR-P5 allows 16ms from keystroke to glyph. This measures the part the
// editor owns: apply the edit, repair the highlight state, and produce tokens
// for the visible viewport. Painting is Fyne's and is measured separately.
func TestGateG0_2(t *testing.T) {
	race.SkipTimingGate(t)
	buf := loadBuffer(t)
	if buf.LineCount() < 4000 {
		t.Skipf("workload is only %d lines", buf.LineCount())
	}
	h := NewHighlighter(buf, PostgreSQL)

	const viewFirst, viewLast = 2470, 2530 // 60 visible lines around the edit
	h.TokensForRange(viewFirst, viewLast)

	type scenario struct {
		name string
		edit func(i int)
	}
	scenarios := []scenario{
		{"typing in the middle", func(i int) {
			buf.Insert(2500, 0, "x")
			h.Apply(Edit{Line: 2500, LinesRemoved: 1, LinesInserted: 1})
		}},
		{"typing at the top", func(i int) {
			buf.Insert(0, 0, "x")
			h.Apply(Edit{Line: 0, LinesRemoved: 1, LinesInserted: 1})
		}},
		{"newline in the middle", func(i int) {
			buf.Insert(2500, 0, "\n")
			h.Apply(Edit{Line: 2500, LinesRemoved: 1, LinesInserted: 2})
		}},
	}

	const iterations = 200
	for _, s := range scenarios {
		var worst, total time.Duration
		for i := 0; i < iterations; i++ {
			start := time.Now()
			s.edit(i)
			h.TokensForRange(viewFirst, viewLast)
			d := time.Since(start)
			total += d
			if d > worst {
				worst = d
			}
		}
		mean := total / iterations
		t.Logf("%-24s mean %-12v worst %v", s.name, mean.Round(time.Nanosecond), worst.Round(time.Microsecond))

		// A quarter of the frame for the editor's own work leaves ample room
		// for layout and paint.
		const budget = 4 * time.Millisecond
		if mean > budget {
			t.Errorf("G0-2: %q mean %v exceeds %v", s.name, mean, budget)
		}
	}

	// The pathological edit: opening a block comment at the top of the file
	// invalidates every line below it. It must still be survivable.
	fresh := NewBuffer(buf.Text())
	fh := NewHighlighter(fresh, PostgreSQL)
	start := time.Now()
	fresh.Insert(0, 0, "/*")
	fh.Apply(Edit{Line: 0, LinesRemoved: 1, LinesInserted: 1})
	fh.TokensForRange(viewFirst, viewLast)
	cascade := time.Since(start)

	t.Logf("%-24s %v  (worst case: invalidates all %d lines)",
		"opening a block comment", cascade.Round(time.Microsecond), fresh.LineCount())

	if cascade > 16*time.Millisecond {
		t.Errorf("G0-2: worst-case cascade %v exceeds the 16ms keystroke budget", cascade)
	}
}
