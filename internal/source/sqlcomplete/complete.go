// Package sqlcomplete offers what can be typed next at a point in a SQL
// statement: the dialect's keywords and functions, and the server's
// databases, schemas, tables, views and columns (FR-5.2).
//
// It is shared rather than per-driver because the grammar it reads is the one
// every SQL engine has. A driver implements source.Completer by handing its
// dialect and a catalog to an Engine; what differs between engines — the
// keyword list, the identifier quote, whether a schema is a database — is
// already data (sqllex.Dialect, source.Dialect.QuoteIdentifier).
//
// Nothing here reaches the server. The catalog answers from memory, so a
// keystroke never waits on a round trip (NFR-P5).
package sqlcomplete

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ikigai-db/ikigai-db/internal/fuzzy"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// DefaultLimit is how many candidates are returned when a request asks for no
// particular number: a popup a person can look down, not a listing.
const DefaultLimit = 50

// Base scores, by what a candidate is. What the cursor is in decides which
// kinds are offered at all (Want), so these only order the kinds that share a
// position: a column above a table above a keyword, in a WHERE clause.
const (
	scoreColumn   = 600
	scoreTable    = 500
	scoreView     = 480
	scoreSchema   = 400
	scoreDatabase = 380
	scoreRoutine  = 300
	scoreFunction = 200
	scoreKeyword  = 100
	scoreType     = 90

	// bonusPrefix goes to a candidate the typed letters start. It is smaller
	// than the gap between two kinds: a column is still offered above a
	// keyword the letters happen to start, because in a column's position
	// that is what was meant. Within a kind it is what puts a name being
	// typed from its start above one whose letters are only scattered
	// through it.
	bonusPrefix = 60
)

// Engine answers completion requests for one connection.
//
// It holds no state between requests: the catalog is the state, and it
// belongs to the connection.
type Engine struct {
	dialect *sqllex.Dialect
	catalog Catalog
	quote   func(string) string
}

// New builds an engine. The quoter is the source's
// Dialect.QuoteIdentifier — the only sanctioned way an identifier reaches
// statement text (ARCH-2); a nil one falls back to the lexer dialect's quote
// character. A nil catalog offers the dialect's own words and nothing else,
// which is what an unconnected editor has.
func New(d *sqllex.Dialect, cat Catalog, quote func(string) string) *Engine {
	if d == nil {
		d = sqllex.PostgreSQL
	}
	if quote == nil {
		quote = DefaultQuoter(d)
	}
	if cat == nil {
		cat = NewStatic()
	}
	return &Engine{dialect: d, catalog: cat, quote: quote}
}

// DefaultQuoter renders an identifier with a dialect's quote character,
// doubling any inside it.
func DefaultQuoter(d *sqllex.Dialect) func(string) string {
	q := d.QuoteIdent
	if q == 0 {
		q = '"'
	}
	s := string(q)
	return func(name string) string {
		return s + strings.ReplaceAll(name, s, s+s) + s
	}
}

// Complete returns what can be typed at a request's cursor, best first.
func (e *Engine) Complete(req source.CompletionRequest) source.CompletionResult {
	cursor := byteOffset(req.Text, req.Cursor)
	ctx := Analyze(e.dialect, req.Text, cursor)
	res := source.CompletionResult{
		Start: utf8.RuneCountInString(req.Text[:ctx.Start]),
		End:   utf8.RuneCountInString(req.Text[:ctx.End]),
	}
	if ctx.Want == 0 {
		return res
	}

	cands := e.gather(ctx, req)
	lower := strings.ToLower(ctx.Prefix)
	out := make([]source.Completion, 0, len(cands))
	for _, c := range cands {
		score, ok := rank(ctx.Prefix, lower, c.Label)
		if !ok {
			continue
		}
		c.Score += score
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return strings.ToLower(out[i].Label) < strings.ToLower(out[j].Label)
	})
	limit := req.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if len(out) > limit {
		out = out[:limit]
	}
	res.Candidates = out
	return res
}

// rank scores a candidate against what has been typed, and reports whether it
// matches at all. An empty prefix matches everything, which is what asking
// for completion on an empty word means: show me what goes here.
func rank(prefix, lowerPrefix, label string) (int, bool) {
	if prefix == "" {
		return 0, true
	}
	score, _, ok := fuzzy.Match(prefix, label)
	if !ok {
		return 0, false
	}
	if strings.HasPrefix(strings.ToLower(label), lowerPrefix) {
		score += bonusPrefix
	}
	return score, true
}

// gather collects every candidate the context allows, unranked.
func (e *Engine) gather(ctx Context, req source.CompletionRequest) []source.Completion {
	var out []source.Completion
	db, schema := req.Database, req.Schema

	// A word can be in more than one of the dialect's lists — SET is a
	// keyword and a type, EXISTS a keyword and a function — and the popup
	// must not show it twice. The first list it is in names it.
	upper := keywordUpper(ctx.Prefix)
	seen := map[string]bool{}
	addWords := func(ws map[string]bool, kind source.CompletionKind, score int) {
		for w := range ws {
			if seen[w] {
				continue
			}
			seen[w] = true
			out = append(out, word(w, kind, upper, score))
		}
	}
	if ctx.Want.Has(WantKeyword) {
		addWords(e.dialect.Keywords, source.CompletionKeyword, scoreKeyword)
		addWords(e.dialect.Types, source.CompletionKeyword, scoreType)
	}
	if ctx.Want.Has(WantFunction) {
		if len(ctx.Qualifier) == 0 {
			addWords(e.dialect.Functions, source.CompletionFunction, scoreFunction)
		}
		for _, name := range e.catalog.Routines(e.qualDatabase(db, ctx), e.qualSchema(schema, ctx)) {
			out = append(out, source.Completion{
				Kind: source.CompletionFunction, Label: name,
				Insert: e.insert(name, ctx), Score: scoreRoutine,
			})
		}
	}
	if ctx.Want.Has(WantDatabase) {
		for _, name := range e.catalog.Databases() {
			out = append(out, source.Completion{
				Kind: source.CompletionDatabase, Label: name,
				Insert: e.insert(name, ctx), Score: scoreDatabase,
			})
		}
	}
	if ctx.Want.Has(WantSchema) {
		for _, name := range e.catalog.Schemas(db) {
			out = append(out, source.Completion{
				Kind: source.CompletionSchema, Label: name,
				Insert: e.insert(name, ctx), Score: scoreSchema,
			})
		}
	}
	if ctx.Want.Has(WantTable) {
		qdb, qschema := e.qualDatabase(db, ctx), e.qualSchema(schema, ctx)
		for _, obj := range e.catalog.Objects(qdb, qschema) {
			kind, score := source.CompletionTable, scoreTable
			if obj.Kind == model.KindView || obj.Kind == model.KindMaterializedView {
				kind, score = source.CompletionView, scoreView
			}
			out = append(out, source.Completion{
				Kind: kind, Label: obj.Name, Insert: e.insert(obj.Name, ctx),
				Detail: qschema, Score: score,
			})
		}
	}
	if ctx.Want.Has(WantColumn) && len(ctx.Qualifier) > 0 {
		// The chain before the dot, read as a table: `orders.` in the
		// session's schema, `public.orders.`, `sales.public.orders.`. Where
		// the catalog knows such a table, its columns go there. A name that
		// is an alias for one is T2.26's work.
		cdb, cschema, table := columnPath(db, schema, ctx.Qualifier)
		for _, col := range e.catalog.Columns(cdb, cschema, table) {
			out = append(out, source.Completion{
				Kind: source.CompletionColumn, Label: col.Name,
				Insert: e.insert(col.Name, ctx), Detail: col.Type, Score: scoreColumn,
			})
		}
	}
	return out
}

// columnPath reads a dotted chain as the table whose columns are wanted,
// innermost last: {"orders"}, {"public", "orders"}, {"sales", "public",
// "orders"}.
func columnPath(db, schema string, qual []string) (string, string, string) {
	switch n := len(qual); n {
	case 1:
		return db, schema, qual[0]
	case 2:
		return db, qual[0], qual[1]
	default:
		return qual[n-3], qual[n-2], qual[n-1]
	}
}

// qualDatabase and qualSchema read the dotted chain before the cursor as the
// place to look in: `sales.public.` names both, `public.` only a schema.
func (e *Engine) qualDatabase(db string, ctx Context) string {
	if len(ctx.Qualifier) >= 2 {
		return ctx.Qualifier[0]
	}
	return db
}

func (e *Engine) qualSchema(schema string, ctx Context) string {
	switch len(ctx.Qualifier) {
	case 0:
		return schema
	case 1:
		return ctx.Qualifier[0]
	default:
		return ctx.Qualifier[1]
	}
}

// word renders one of the dialect's own words — a keyword, a type name or a
// built-in function. The label is upper case, which is how SQL words are
// read; what is written follows the case being typed, so that someone writing
// lower-case SQL is not given upper-case words.
func word(w string, kind source.CompletionKind, upper bool, score int) source.Completion {
	insert := w
	if upper {
		insert = strings.ToUpper(w)
	}
	return source.Completion{
		Kind:   kind,
		Label:  strings.ToUpper(w),
		Insert: insert,
		Score:  score,
	}
}

// keywordUpper decides the case a keyword is written in: lower once a
// lower-case letter has been typed in the word, upper otherwise.
func keywordUpper(prefix string) bool {
	for _, r := range prefix {
		if unicode.IsLower(r) {
			return false
		}
	}
	return true
}

// insert renders an identifier as it must be written: quoted where the name
// is not a plain one, where it is a keyword the server would read as one, or
// where the person has already typed an opening quote.
func (e *Engine) insert(name string, ctx Context) string {
	if ctx.Quoted || e.needsQuote(name) {
		return e.quote(name)
	}
	return name
}

func (e *Engine) needsQuote(name string) bool {
	if name == "" {
		return true
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c == '_':
		case (c >= '0' && c <= '9' || c == '$') && i > 0:
		default:
			return true
		}
	}
	return e.dialect.Keywords[name] || e.dialect.Types[name]
}

// byteOffset converts a rune offset into the byte offset the lexer works in.
func byteOffset(text string, runes int) int {
	if runes <= 0 {
		return 0
	}
	n := 0
	for i := range text {
		if n == runes {
			return i
		}
		n++
	}
	return len(text)
}
