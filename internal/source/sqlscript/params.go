package sqlscript

import (
	"fmt"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Named parameters (FR-5.7) are written :name, whatever the engine. They are
// found through the lexer, so a ":name" inside a string or a comment is left
// alone and a PostgreSQL "::text" cast is not taken for one.

// Names are a statement's or a script's named parameters, in the order each
// is first used.
func Names(d *sqllex.Dialect, sql string) []string {
	var out []string
	seen := map[string]bool{}
	eachParam(d, sql, func(name string) string {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
		return ""
	})
	return out
}

// BindNamed rewrites a statement's named parameters to the engine's own
// placeholders, place(n) for the nth, and returns the values in order. With
// reuse, a name used twice is one parameter, as PostgreSQL's $n can be;
// without, each use is its own, as MySQL's ? must be. A name with no value
// is an error: nothing is bound in its place.
func BindNamed(d *sqllex.Dialect, sql string, named map[string]any, place func(n int) string, reuse bool) (string, []any, error) {
	var args []any
	index := map[string]int{}
	var missing string
	out := eachParam(d, sql, func(name string) string {
		n, seen := index[name]
		if !seen || !reuse {
			v, ok := named[name]
			if !ok {
				if missing == "" {
					missing = name
				}
				return ""
			}
			args = append(args, v)
			n = len(args)
			index[name] = n
		}
		return place(n)
	})
	if missing != "" {
		return "", nil, fmt.Errorf("no value supplied for :%s", missing)
	}
	return out, args, nil
}

// eachParam rebuilds sql with each named parameter replaced by what with
// says for its name.
func eachParam(d *sqllex.Dialect, sql string, with func(name string) string) string {
	lx := sqllex.NewLexer(d)
	var st sqllex.State
	var sb strings.Builder
	for li, line := range strings.Split(sql, "\n") {
		if li > 0 {
			sb.WriteByte('\n')
		}
		toks, next := lx.LexLine(line, st)
		for _, tk := range toks {
			text := line[tk.Start:tk.End]
			if tk.Kind == sqllex.TokParameter && len(text) > 1 && text[0] == ':' {
				sb.WriteString(with(text[1:]))
				continue
			}
			sb.WriteString(text)
		}
		st = next
	}
	return sb.String()
}
