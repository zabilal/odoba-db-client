package assistant

import (
	"strings"
	"testing"
)

// What the model is told, and how its answer is read (FR-14.1, FR-14.2,
// FR-14.6).

// Every kind of question tells the model not to write anything that changes the
// database. That is not what makes this safe — nothing here runs anything — but
// it is what makes the common case clean, and a kind that forgot it would be
// the one that produced a DELETE next to a Run button.
func TestEveryKindIsToldNotToWriteAChange(t *testing.T) {
	for _, k := range []Kind{KindQuery, KindChat} {
		system, _ := Prompt(Request{Kind: k, Question: "delete everything"})
		for _, want := range []string{"DELETE", "DROP", "say so"} {
			if !strings.Contains(system, want) {
				t.Errorf("%s: it does not mention %q", k, want)
			}
		}
	}
	// Explaining is not writing, so the two explain kinds are told to explain
	// and not to rewrite — being told not to write a change would be telling
	// them not to do something they were not asked to do.
	for _, k := range []Kind{KindExplain, KindExplainPlan} {
		system, _ := Prompt(Request{Kind: k, Question: "SELECT 1"})
		if !strings.Contains(strings.ToLower(system), "explain") {
			t.Errorf("%s: it does not say to explain: %q", k, system)
		}
	}
}

// A query is asked for alone, because a statement wrapped in prose has to be
// unwrapped and unwrapping prose is guessing.
func TestAQueryIsAskedForAlone(t *testing.T) {
	system, _ := Prompt(Request{Kind: KindQuery, Question: "how many"})
	for _, want := range []string{"statement alone", "no code fence", "only the tables and columns you are given"} {
		if !strings.Contains(system, want) {
			t.Errorf("it does not say %q:\n%s", want, system)
		}
	}
}

// The question carries the schema under it, and says what it is being asked.
func TestWhatTheQuestionCarries(t *testing.T) {
	g := Grounding{Product: "PostgreSQL", Dialect: "postgresql",
		Tables: []Table{{Name: "people", Columns: []Column{{Name: "id", Type: "integer"}}}}}
	for name, c := range map[string]struct {
		kind Kind
		says string
	}{
		"a query":   {KindQuery, "The question:"},
		"a chat":    {KindChat, "The question:"},
		"a explain": {KindExplain, "Explain this statement:"},
		"a plan":    {KindExplainPlan, "Explain this query plan:"},
	} {
		t.Run(name, func(t *testing.T) {
			_, user := Prompt(Request{Kind: c.kind, Question: "  SELECT 1  ", Grounding: g})
			if !strings.Contains(user, c.says) {
				t.Errorf("it reads\n%s\nwhich lacks %q", user, c.says)
			}
			if !strings.Contains(user, "people (id integer)") {
				t.Errorf("it does not carry the schema:\n%s", user)
			}
			// Trimmed, because what somebody typed usually has a newline on the
			// end and a model reads that as part of the question.
			if strings.Contains(user, "  SELECT 1  ") {
				t.Errorf("the question went in untrimmed:\n%s", user)
			}
		})
	}
	// No schema is no section about one, rather than an empty heading.
	_, bare := Prompt(Request{Kind: KindQuery, Question: "how many"})
	if strings.Contains(bare, "Its tables") {
		t.Errorf("it reads\n%s", bare)
	}
}

// The statement in an answer, out of whatever the model wrapped it in: a person
// about to review one should not have to delete prose from their editor first.
func TestTheStatementInAnAnswer(t *testing.T) {
	for name, c := range map[string]struct {
		answer string
		want   string
	}{
		"the statement alone":     {"SELECT 1", "SELECT 1"},
		"with space around it":    {"\n  SELECT 1  \n", "SELECT 1"},
		"fenced":                  {"```sql\nSELECT 1\n```", "SELECT 1"},
		"fenced with no language": {"```\nSELECT 1\n```", "SELECT 1"},
		"a sentence, then the statement": {
			"Here is the query you asked for:\n\nSELECT count(*) FROM people", "SELECT count(*) FROM people"},
		"a sentence, then a fence": {
			"Here you are:\n```sql\nSELECT 1\n```", "SELECT 1"},
		"several lines": {"SELECT id\nFROM people\nWHERE id > 1", "SELECT id\nFROM people\nWHERE id > 1"},
		"a CTE":         {"WITH n AS (SELECT 1) SELECT * FROM n", "WITH n AS (SELECT 1) SELECT * FROM n"},
		"bracketed":     {"(SELECT 1) UNION (SELECT 2)", "(SELECT 1) UNION (SELECT 2)"},
		"lower case":    {"select 1", "select 1"},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := Statement(c.answer)
			if !ok {
				t.Fatalf("it found no statement in %q", c.answer)
			}
			if got != c.want {
				t.Errorf("it found %q, want %q", got, c.want)
			}
		})
	}
}

// An answer with no statement in it is an answer, and is shown as the sentence
// it is: a model that cannot answer from the schema says so, and guessing a
// statement out of that would put something nobody asked for in the editor.
func TestAnAnswerWithNoStatementIsAnAnswer(t *testing.T) {
	for name, answer := range map[string]string{
		"a refusal":       "There is no table of orders in this schema, so I cannot count them.",
		"a question back": "Which of the two date columns did you mean?",
		"nothing at all":  "",
		"only spaces":     "   \n  ",
		// A change is not a statement this returns: a model told not to write
		// one and doing so anyway is an answer, and is shown as the prose it is.
		"a delete":        "DELETE FROM people",
		"an update":       "UPDATE people SET name = 'x'",
		"a create":        "CREATE TABLE t (a int)",
		"a fenced delete": "```sql\nDROP TABLE people\n```",
	} {
		t.Run(name, func(t *testing.T) {
			if got, ok := Statement(answer); ok {
				t.Errorf("it found the statement %q", got)
			}
		})
	}
}

// A statement is labelled before a connection is even involved, so an answer can
// be shown with what it would do beside it.
func TestWhatAStatementWouldDo(t *testing.T) {
	for statement, changes := range map[string]bool{
		"SELECT 1":                             false,
		"with n as (select 1) select * from n": false,
		"SHOW TABLES":                          false,
		"EXPLAIN SELECT 1":                     false,
		"DELETE FROM people":                   true,
		"UPDATE people SET a = 1":              true,
		"DROP TABLE people":                    true,
		"":                                     true,
		"anything else at all":                 true,
	} {
		if got := Changes(statement); got != changes {
			t.Errorf("%q changes %v, want %v", statement, got, changes)
		}
	}
}

// A nil answer has no summary, rather than one made of empty fields.
func TestANilAnswerHasNoSummary(t *testing.T) {
	var a *Answer
	if got := a.Summary(); got != "" {
		t.Errorf("it says %q", got)
	}
}
