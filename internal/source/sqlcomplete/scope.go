package sqlcomplete

import (
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Relation is a table a statement reads, under the name the rest of the
// statement calls it by.
type Relation struct {
	// Name is what a column is qualified with: the alias where the table
	// has one, its own name where it has not.
	Name string

	// Database, Schema and Table locate the table in the catalog. Table is
	// "" for a derived table — a bracketed subquery under an alias — whose
	// columns are not the catalog's to know.
	Database, Schema, Table string
}

// Scope lists the tables in scope at a cursor, in the order the statement
// names them.
//
// This is what makes `o.` mean the orders table in `select o.| from orders
// o`, and what an unqualified column is offered from. It reads the whole
// statement, not the part before the cursor: a column is typed before the
// FROM clause that says where it comes from.
//
// A bracketed subquery's tables are in scope only inside those brackets; the
// statement's own are in scope within them too, which is what a correlated
// subquery reads.
func Scope(d *sqllex.Dialect, text string, cursor int) []Relation {
	sig, stack := significant(d, text, cursor)
	inScope := map[int]bool{-1: true}
	for _, b := range stack {
		inScope[b] = true
	}

	var rels []Relation
	for i := 0; i < len(sig); i++ {
		if sig[i].text == ";" && sig[i].bracket == -1 {
			// Another statement. The one the cursor is in is the only one
			// whose tables it can name.
			if sig[i].start < cursor {
				rels = rels[:0]
				continue
			}
			break
		}
		if !relationIntroducer[strings.ToLower(sig[i].text)] {
			continue
		}
		var found []Relation
		next := readRelations(sig, i+1, &found)
		for _, r := range found {
			if inScope[sig[i].bracket] {
				rels = append(rels, r)
			}
		}
		i = next - 1
	}
	return rels
}

// relationIntroducer holds the words a table's name follows in the clauses
// that put a table in scope. TABLE and TRUNCATE are not among them: their
// table is the statement's object, not something columns are read from.
var relationIntroducer = words(`from join into update`)

// readRelations reads the comma-separated list of tables at j, each with its
// alias, and returns where it stopped.
func readRelations(sig []sigToken, j int, out *[]Relation) int {
	for j < len(sig) {
		if sig[j].text == "(" {
			// A derived table. Its columns are the subquery's, which this
			// does not read; the alias is still recorded, so that it is
			// offered and does not resolve to some table of the same name.
			depth := 1
			j++
			for ; j < len(sig) && depth > 0; j++ {
				switch sig[j].text {
				case "(":
					depth++
				case ")":
					depth--
				}
			}
			alias, next := readAlias(sig, j)
			if alias != "" {
				*out = append(*out, Relation{Name: alias})
			}
			j = next
		} else {
			if !isName(sig[j].kind) {
				return j
			}
			parts := []string{unquote(sig[j].text)}
			j++
			for j+1 < len(sig) && sig[j].text == "." && isName(sig[j+1].kind) {
				parts = append(parts, unquote(sig[j+1].text))
				j += 2
			}
			alias, next := readAlias(sig, j)
			j = next
			*out = append(*out, relation(parts, alias))
		}
		if j < len(sig) && sig[j].text == "," {
			j++
			continue
		}
		return j
	}
	return j
}

// readAlias reads the name a table is given after it, with or without AS,
// and returns where it stopped. A keyword is never an alias: it is the next
// clause.
func readAlias(sig []sigToken, j int) (string, int) {
	if j < len(sig) && strings.EqualFold(sig[j].text, "as") {
		j++
	}
	if j < len(sig) && (sig[j].kind == sqllex.TokIdentifier || sig[j].kind == sqllex.TokQuotedIdent) {
		return unquote(sig[j].text), j + 1
	}
	return "", j
}

// relation builds a relation from a dotted name, innermost last.
func relation(parts []string, alias string) Relation {
	r := Relation{}
	switch n := len(parts); n {
	case 1:
		r.Table = parts[0]
	case 2:
		r.Schema, r.Table = parts[0], parts[1]
	default:
		r.Database, r.Schema, r.Table = parts[n-3], parts[n-2], parts[n-1]
	}
	r.Name = alias
	if r.Name == "" {
		r.Name = r.Table
	}
	return r
}

// sigToken is a token that carries meaning — neither whitespace nor comment —
// with its text and the bracket group it is in.
type sigToken struct {
	kind       sqllex.TokenKind
	start, end int
	text       string

	// bracket is the index in the list of the '(' that opened the group this
	// token is in, or -1 at the top.
	bracket int
}

// significant tokenises the text and returns its meaningful tokens, together
// with the brackets that are open at the cursor — the groups whose tables the
// cursor can name.
func significant(d *sqllex.Dialect, text string, cursor int) ([]sigToken, []int) {
	var sig []sigToken
	var stack []int
	var atCursor []int
	seen := false
	for _, t := range lexAll(d, text) {
		if t.kind == sqllex.TokText || t.kind == sqllex.TokComment {
			continue
		}
		if !seen && t.start >= cursor {
			atCursor = append([]int(nil), stack...)
			seen = true
		}
		s := text[t.start:t.end]
		bracket := -1
		if len(stack) > 0 {
			bracket = stack[len(stack)-1]
		}
		if s == ")" && len(stack) > 0 {
			stack = stack[:len(stack)-1]
			if len(stack) > 0 {
				bracket = stack[len(stack)-1]
			} else {
				bracket = -1
			}
		}
		sig = append(sig, sigToken{kind: t.kind, start: t.start, end: t.end, text: s, bracket: bracket})
		if s == "(" {
			stack = append(stack, len(sig)-1)
		}
	}
	if !seen {
		atCursor = stack
	}
	return sig, atCursor
}
