package sqlfmt

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// The one thing a formatter must never do (FR-5.12).
//
// Laying a statement out may move whitespace and change the case of SQL's
// own words, and may do nothing else. Whatever is thrown at it — a bracket
// that never closes, a comment that swallows a quote, a dollar-quoted body
// with a semicolon in it — what comes back says what went in.

// differs says how two token streams differ, or nothing where they do not.
func differs(a, b []Tok) string {
	if len(a) != len(b) {
		return fmt.Sprintf("%d tokens became %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Kind != b[i].Kind {
			return fmt.Sprintf("token %d is a %d where it was a %d", i, b[i].Kind, a[i].Kind)
		}
		if a[i].Text == b[i].Text {
			continue
		}
		switch a[i].Kind {
		case sqllex.TokKeyword, sqllex.TokType, sqllex.TokFunction:
			if strings.EqualFold(a[i].Text, b[i].Text) {
				continue
			}
		}
		return fmt.Sprintf("token %d is %q where it was %q", i, b[i].Text, a[i].Text)
	}
	return ""
}

func FuzzFormatSaysTheSameThing(f *testing.F) {
	for _, s := range []string{
		`select id, name from t where a = 'x' and b > 1;`,
		`select count(*) from t where id in (select id from u);`,
		"select a, -- note\n b from t;",
		`insert into t (a, b) values (1, 2), (3, 4);`,
		`select case when a then b else c end from t;`,
		"select $$ a ; b $$ from t;",
		`select /* one /* two */ */ 1;`,
		"select 'un\nterminated from t;",
		`select "Odd" from "Name";`,
		`update t set a = 1 where id = 2; delete from u;`,
		"((((((select 1))))))",
		";;;;",
		"select 1 -- ends here",
	} {
		f.Add(s)
	}

	langs := []string{"postgresql", "mysql", "sqlite", "sqlserver", "cql"}
	f.Fuzz(func(t *testing.T, in string) {
		if len(in) > 4000 {
			return // a formatter is for what somebody wrote, not for a corpus
		}
		for _, lang := range langs {
			d := sqllex.DialectFor(lang)
			out, err := Format(d, in, Default())

			// Whatever came back says what went in: the same tokens in the
			// same order, with only SQL's own words allowed to change case.
			//
			// Compared here rather than by calling Same, which is what the
			// formatter checks its own work with: a test that used it would
			// be asking the thing under test whether it was right.
			if why := differs(Lex(d, in), Lex(d, out)); why != "" {
				t.Fatalf("%s changed what it says: %s\n  in:  %q\n  out: %q\n  err: %v",
					lang, why, in, out, err)
			}
			if err != nil {
				// A refusal says how many statements were left as written.
				if !strings.Contains(err.Error(), "statement") {
					t.Fatalf("%s said %v", lang, err)
				}
				continue
			}
			// And laying out what is already laid out changes nothing.
			again, err := Format(d, out, Default())
			if err != nil {
				t.Fatalf("%s: laying out its own work: %v\n%q", lang, err, out)
			}
			if again != out {
				t.Fatalf("%s wanders\n  once:  %q\n  twice: %q", lang, out, again)
			}
		}
	})
}
