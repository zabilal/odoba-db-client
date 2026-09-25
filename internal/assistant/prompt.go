package assistant

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// What the model is told to do, and how its answer is read (FR-14.1, FR-14.2,
// FR-14.6).
//
// Two rules run through every instruction here, and they are the reason this is
// a file rather than a format string. The model is told to write one statement
// and nothing else, because a statement wrapped in prose has to be unwrapped
// and unwrapping prose is guessing. And it is told not to write anything that
// changes the database, because what comes back lands in an editor beside a Run
// button — a model that answered a question about counting with a DELETE would
// be one keystroke from a very bad afternoon.
//
// Neither rule is trusted. The answer is read back through Statement, which
// takes the statement out of whatever the model wrapped it in; and nothing here
// runs anything, ever: a generated statement goes into the editor and a person
// runs it (FR-14.6). The instructions are there so the common case is clean,
// not so the uncommon case is safe.

// Prompt is what a request becomes: what the model is told it is, and what it
// is being asked.
func Prompt(r Request) (system, user string) {
	return systemFor(r.Kind), userFor(r)
}

// systemFor is what the model is told it is doing.
func systemFor(kind Kind) string {
	const care = "Never write a statement that changes the database or its structure — " +
		"no INSERT, UPDATE, DELETE, MERGE, CREATE, ALTER, DROP, TRUNCATE or GRANT. " +
		"If what was asked for needs one, say so in a sentence instead of writing it."
	switch kind {
	case KindQuery:
		return "You write one SQL query for the database described, and nothing else. " +
			"Use only the tables and columns you are given; if what was asked for needs " +
			"something that is not there, say so in a sentence instead of inventing a name. " +
			"Answer with the statement alone — no explanation, no code fence, no prose. " + care
	case KindExplain:
		return "You explain what a SQL statement does, in plain words, for somebody who " +
			"knows SQL but not this database. Say what it reads, what it joins, what it " +
			"filters and what comes out. Be brief. Do not rewrite it."
	case KindExplainPlan:
		return "You explain what a query plan means, in plain words. Say which steps cost " +
			"the most, what they are scanning, and what would make them cheaper. Be brief, " +
			"and say plainly where you are guessing."
	}
	return "You answer questions about the database described, in plain words. " +
		"Answer only from what you are given; say so where the answer is not in it. " + care
}

// userFor is the question, with the schema under it.
func userFor(r Request) string {
	var b strings.Builder
	if g := r.Grounding.Describe(); g != "" {
		b.WriteString(g)
		b.WriteString("\n")
	}
	switch r.Kind {
	case KindExplain:
		b.WriteString("Explain this statement:\n\n")
	case KindExplainPlan:
		b.WriteString("Explain this query plan:\n\n")
	default:
		b.WriteString("The question:\n\n")
	}
	b.WriteString(strings.TrimSpace(r.Question))
	b.WriteString("\n")
	return b.String()
}

// fence matches a fenced block, with or without a language on it, which is what
// a model wraps a statement in however firmly it is told not to.
var fence = regexp.MustCompile("(?s)```[a-zA-Z0-9_+-]*\\s*\\n(.*?)```")

// Statement is the statement in an answer, and whether there was one.
//
// A model told to answer with a statement alone usually does, and sometimes
// wraps it in a fence, and sometimes says a sentence first. This takes the
// statement out of all three, because a person about to review one should not
// have to delete prose from their editor first.
//
// What it will not do is guess. An answer with no statement in it — which is
// what a model says when the question cannot be answered from the schema — comes
// back as no statement, and the answer is shown as the sentence it is.
func Statement(answer string) (string, bool) {
	if m := fence.FindStringSubmatch(answer); m != nil {
		if s := strings.TrimSpace(m[1]); looksLikeStatement(s) {
			return s, true
		}
	}
	text := strings.TrimSpace(answer)
	if looksLikeStatement(text) {
		return text, true
	}
	// A sentence, then the statement: the statement is from the first line that
	// begins like one to the end, which is where a model puts it.
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if !startsAStatement(strings.TrimSpace(line)) {
			continue
		}
		rest := strings.TrimSpace(strings.Join(lines[i:], "\n"))
		if looksLikeStatement(rest) {
			return rest, true
		}
	}
	return "", false
}

// looksLikeStatement reports whether text is a statement rather than prose. It
// is a reading of the first word and nothing cleverer: what makes this safe is
// that nothing runs what comes back, so a wrong reading costs a person a glance
// rather than a table.
func looksLikeStatement(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	return startsAStatement(text)
}

// starters are the words a statement this assistant may return begins with.
// Only readers: a model told not to write a change and doing so anyway is an
// answer, not a statement, and is shown as the prose it is (FR-14.6).
var starters = []string{"select", "with", "show", "explain", "describe", "values", "table"}

func startsAStatement(text string) bool {
	word, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(text)), " ")
	word = strings.TrimLeft(word, "(")
	for _, s := range starters {
		if word == s {
			return true
		}
	}
	return false
}

// Changes reports whether a statement would change the database, which is asked
// of what a model returned before it is put anywhere near an editor.
//
// It is the driver's own classifier that decides in the end — this is one word
// read from the front, and is here so that a model's answer can be labelled
// before a connection is even involved. A caller with a dialect asks that
// instead.
func Changes(statement string) bool { return !startsAStatement(statement) }

// Summary is a one-line note of what answered, for the window to show beside
// the answer (FR-14.5).
func (a *Answer) Summary() string {
	if a == nil {
		return ""
	}
	return fmt.Sprintf("%s · %s · %s", a.Provider, a.Model, a.Took.Round(time.Millisecond))
}
