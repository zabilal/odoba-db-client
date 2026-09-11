package sqlscript

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

func TestPredicateTakesOneCondition(t *testing.T) {
	for _, c := range []struct {
		d  *sqllex.Dialect
		in string
	}{
		{sqllex.PostgreSQL, "total > 100 AND status = 'paid'"},
		{sqllex.PostgreSQL, "name::text = 'x'"},
		{sqllex.PostgreSQL, "note = 'it''s; fine' -- a note"},
		{sqllex.PostgreSQL, `(a = 1 OR b = 2) AND "odd;name" = 3`},
		{sqllex.PostgreSQL, "body = $$semi;colon$$"},
		{sqllex.PostgreSQL, "a = 1\n  AND b = 2"},
		{sqllex.MySQL, "`group` = 'a' # a note"},
		{sqllex.SQLite, "born > '2000-01-01' /* closed */"},
	} {
		got, err := Predicate(c.d, c.in)
		if err != nil || got != "(\n"+c.in+"\n)" {
			t.Errorf("%s %q: %q, %v", c.d.Name, c.in, got, err)
		}
	}
}

func TestPredicateRefusesWhatIsNotOneCondition(t *testing.T) {
	for _, c := range []struct {
		d        *sqllex.Dialect
		in, want string
	}{
		{sqllex.PostgreSQL, "  ", "empty"},
		{sqllex.PostgreSQL, "1 = 1; DROP TABLE t", "one condition"},
		{sqllex.PostgreSQL, "1 = 1)", "never opened"},
		{sqllex.PostgreSQL, "(1 = 1", "bracket open"},
		{sqllex.PostgreSQL, "name = 'open", "string or comment open"},
		{sqllex.PostgreSQL, "1 = 1 /* open", "string or comment open"},
		{sqllex.PostgreSQL, `"open = 1`, "quoted name open"},
		{sqllex.PostgreSQL, "id = $1", "parameters"},
		{sqllex.MySQL, "id = ?", "parameters"},
	} {
		_, err := Predicate(c.d, c.in)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %q: %v, want an error saying %q", c.d.Name, c.in, err, c.want)
		}
	}
}
