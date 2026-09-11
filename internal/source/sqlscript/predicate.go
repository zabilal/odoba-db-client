package sqlscript

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Predicate checks a condition a person typed for a WHERE clause (FR-3.6),
// and returns it ready to follow WHERE or AND: in brackets, on lines of its
// own, so that a line comment at its end ends there and not at the end of
// the statement it is put in.
//
// It is the person's own SQL, so it is not parsed, only held to being one
// condition: no ';', no string, comment or quoted name left open, brackets
// that balance, and no parameters, since the statement's own parameters are
// numbered around it. Everything else is the server's to judge.
func Predicate(d *sqllex.Dialect, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("the WHERE clause is empty")
	}
	lx := sqllex.NewLexer(d)
	var st sqllex.State
	depth := 0
	for _, line := range strings.Split(text, "\n") {
		var toks []sqllex.Token
		toks, st = lx.LexLine(line, st)
		for _, tk := range toks {
			lexeme := line[tk.Start:tk.End]
			switch tk.Kind {
			case sqllex.TokPunctuation:
				switch lexeme {
				case "(":
					depth++
				case ")":
					if depth--; depth < 0 {
						return "", errors.New("the WHERE clause closes a bracket it never opened")
					}
				case ";":
					return "", errors.New("a WHERE clause is one condition: it cannot end the statement or start another")
				}
			case sqllex.TokParameter:
				return "", fmt.Errorf("a WHERE clause cannot take parameters (%s); write the value itself", lexeme)
			case sqllex.TokQuotedIdent:
				if !closedQuote(lexeme) {
					return "", errors.New("the WHERE clause leaves a quoted name open")
				}
			}
		}
	}
	if st.Kind != sqllex.StateNormal {
		return "", errors.New("the WHERE clause leaves a string or comment open")
	}
	if depth != 0 {
		return "", errors.New("the WHERE clause leaves a bracket open")
	}
	return "(\n" + text + "\n)", nil
}

// closedQuote reports whether a quoted name ends with its closing quote. A
// doubled quote inside is an escaped one, so a closed name holds an even
// number of them.
func closedQuote(lexeme string) bool {
	if len(lexeme) < 2 {
		return false
	}
	if q := lexeme[0]; q != '[' {
		return lexeme[len(lexeme)-1] == q && strings.Count(lexeme, string(q))%2 == 0
	}
	return lexeme[len(lexeme)-1] == ']'
}
