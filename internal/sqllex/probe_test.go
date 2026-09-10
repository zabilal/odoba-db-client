package sqllex

import (
	"os"
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// Architecture probe for spike W2 (T0.45).
//
// The design of the highlighter depends entirely on how fast Chroma is. If a
// full-buffer lex fits inside NFR-P5's 16ms keystroke budget, a debounced
// full re-lex is the simplest thing that works. If it does not, highlighting
// has to become incremental, which is a substantially larger build.
//
// Measure first, then design.

func loadWorkload(tb testing.TB) string {
	tb.Helper()
	b, err := os.ReadFile("testdata/workload.sql")
	if err != nil {
		tb.Skipf("workload not generated: %v", err)
	}
	return string(b)
}

func BenchmarkChromaFullBuffer(b *testing.B) {
	src := loadWorkload(b)
	lexer := chroma.Coalesce(lexers.Get("postgresql"))

	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	b.ResetTimer()

	var tokens int
	for i := 0; i < b.N; i++ {
		it, err := lexer.Tokenise(nil, src)
		if err != nil {
			b.Fatal(err)
		}
		tokens = 0
		for t := it(); t != chroma.EOF; t = it() {
			tokens++
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/1e6, "ms/lex")
	b.ReportMetric(float64(tokens), "tokens")
}

func BenchmarkChromaSingleLine(b *testing.B) {
	// If only visible lines are lexed, this is the per-line cost that matters.
	line := "SELECT c.name, sum(r.total) AS lifetime_value FROM recent_1 r " +
		"JOIN customers c ON c.id = r.customer_id WHERE r.rn = 1;"
	lexer := chroma.Coalesce(lexers.Get("postgresql"))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		it, err := lexer.Tokenise(nil, line)
		if err != nil {
			b.Fatal(err)
		}
		for t := it(); t != chroma.EOF; t = it() {
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/1e3, "us/line")
}

func BenchmarkChromaViewport(b *testing.B) {
	// 60 visible lines: what a full-screen editor actually needs per frame.
	src := loadWorkload(b)
	lines := strings.Split(src, "\n")
	if len(lines) > 60 {
		lines = lines[1000:1060]
	}
	viewport := strings.Join(lines, "\n")
	lexer := chroma.Coalesce(lexers.Get("postgresql"))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		it, err := lexer.Tokenise(nil, viewport)
		if err != nil {
			b.Fatal(err)
		}
		for t := it(); t != chroma.EOF; t = it() {
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/1e6, "ms/viewport")
}

func TestChromaHasSQLDialects(t *testing.T) {
	// FR-5.1 needs SQL and CQL. Find out now what Chroma actually ships,
	// rather than discovering a gap in Phase 2.
	for _, name := range []string{"postgresql", "sql", "mysql", "cassandra", "cql", "transactsql"} {
		l := lexers.Get(name)
		if l == nil {
			t.Logf("%-14s NOT available", name)
			continue
		}
		t.Logf("%-14s -> %s", name, l.Config().Name)
	}
}

// --- our lexer, for comparison with Chroma above ---

func BenchmarkOurLexerFullBuffer(b *testing.B) {
	src := loadWorkload(b)
	lines := strings.Split(src, "\n")
	l := NewLexer(PostgreSQL)

	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	b.ResetTimer()

	var tokens int
	for i := 0; i < b.N; i++ {
		st := State{}
		tokens = 0
		for _, line := range lines {
			var toks []Token
			toks, st = l.LexLine(line, st)
			tokens += len(toks)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/1e6, "ms/lex")
	b.ReportMetric(float64(tokens), "tokens")
}

func BenchmarkOurLexerSingleLine(b *testing.B) {
	line := "SELECT c.name, sum(r.total) AS lifetime_value FROM recent_1 r " +
		"JOIN customers c ON c.id = r.customer_id WHERE r.rn = 1;"
	l := NewLexer(PostgreSQL)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.LexLine(line, State{})
	}
	b.StopTimer()
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/1e3, "us/line")
}

func BenchmarkOurLexerViewport(b *testing.B) {
	src := loadWorkload(b)
	lines := strings.Split(src, "\n")[1000:1060]
	l := NewLexer(PostgreSQL)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		st := State{}
		for _, line := range lines {
			_, st = l.LexLine(line, st)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/1e6, "ms/viewport")
}
