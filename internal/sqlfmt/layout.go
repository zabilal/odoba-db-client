package sqlfmt

import (
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Where a statement's lines break.
//
// One rule decides everything: a thing is written along a line if it fits,
// and broken down the page if it does not. A clause of three short columns
// reads better on one line than on three, and a clause of twenty does not
// fit on any line at all.
//
// Nothing here knows any grammar. It reads the clause words as they go past,
// which is enough to lay out what somebody wrote and cannot mislay what it
// does not recognise: a word this has never heard of is written where it
// stood.

// clauses begin a line of their own.
var clauses = words(`select from where group having order limit offset fetch
	window union intersect except values set returning insert update delete
	with join`)

// followers are the rest of a clause's own name: GROUP BY, INSERT INTO,
// UNION ALL, LEFT OUTER JOIN. They belong to the word before them.
var followers = words(`by into from all distinct outer`)

// leadIns are the words a join begins with, which belong to the JOIN after
// them rather than to the clause before.
var leadIns = words(`inner left right full cross natural lateral`)

// conjunctions break a condition down the page when it will not fit along
// one, which is what makes a WHERE of four conditions four lines.
var conjunctions = words(`and or`)

// unary are the words after which a clause word is not a clause of its own:
// NOT EXISTS, and anything else that reads as one expression.
var unary = words(`not exists any all some as`)

func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(s) {
		out[w] = true
	}
	return out
}

func lower(t Tok) string { return strings.ToLower(t.Text) }

// isClause reports a token that begins a clause of its own.
func isClause(toks []Tok, i int) bool {
	t := toks[i]
	if t.Kind != sqllex.TokKeyword {
		return false
	}
	w := lower(t)
	if leadIns[w] {
		// LEFT is a clause only when a JOIN follows it, near enough.
		return joinFollows(toks, i)
	}
	if !clauses[w] {
		return false
	}
	if i > 0 && unaryBefore(toks[i-1]) {
		return false
	}
	if i > 0 && followers[w] && toks[i-1].Kind == sqllex.TokKeyword {
		// The BY of GROUP BY, the INTO of INSERT INTO.
		return false
	}
	return true
}

// joinFollows reports a JOIN within the next few words, which is what makes
// LEFT the beginning of a clause rather than a word in one.
func joinFollows(toks []Tok, i int) bool {
	for j := i + 1; j < len(toks) && j <= i+3; j++ {
		if toks[j].Kind != sqllex.TokKeyword {
			return false
		}
		if lower(toks[j]) == "join" {
			return true
		}
		if !followers[lower(toks[j])] && !leadIns[lower(toks[j])] {
			return false
		}
	}
	return false
}

func unaryBefore(prev Tok) bool {
	return prev.Kind == sqllex.TokKeyword && unary[lower(prev)]
}

// clause is a clause's own words and what follows them.
type clause struct {
	head []Tok // SELECT, GROUP BY, LEFT OUTER JOIN
	body []Tok
}

// tables are the words a table's name follows, after which a bracket is a
// list of its columns rather than a call.
var tables = words(`into table references`)

// markColumnLists finds the brackets that hold a table's columns.
//
// A name followed by a bracket is a call everywhere else, and the two read
// differently: count(*) is one thing and writes (name, n) is two.
func markColumnLists(in []Tok) {
	for i := 2; i < len(in); i++ {
		if in[i].Text != "(" || !callable(in[i-1]) {
			continue
		}
		if in[i-2].Kind == sqllex.TokKeyword && tables[lower(in[i-2])] {
			in[i].Spaced = true
		}
	}
}

// writeOne lays one statement out.
func writeOne(stmt []Tok, opt Options) string {
	var b strings.Builder
	for _, c := range clausesOf(stmt) {
		writeClause(&b, c, 0, opt)
	}
	return b.String()
}

// statements splits a script at the semicolons that end one, keeping each
// semicolon with the statement it ends.
func statements(in []Tok) [][]Tok {
	var out [][]Tok
	start, depth := 0, 0
	for i, t := range in {
		switch t.Text {
		case "(":
			depth++
		case ")":
			depth--
		case ";":
			if depth == 0 {
				out = append(out, in[start:i+1])
				start = i + 1
			}
		}
	}
	if start < len(in) {
		out = append(out, in[start:])
	}
	return out
}

// clausesOf splits a statement into its clauses. Anything before the first
// clause word is a clause with no name, which is how a statement this has
// never seen is written out at all.
//
// Only at the top level of brackets: the SELECT of a subquery belongs to the
// subquery, and pulling it out here would lay a statement out as though the
// brackets were not there.
func clausesOf(stmt []Tok) []clause {
	tops := topLevel(stmt)
	var out []clause
	i := 0
	for i < len(stmt) {
		if !tops[i] {
			// Words before any clause: an unrecognised statement, or a
			// comment standing on its own.
			j := i
			for j < len(stmt) && !tops[j] {
				j++
			}
			out = append(out, clause{body: stmt[i:j]})
			i = j
			continue
		}
		start := i
		i++
		for i < len(stmt) && extendsHead(stmt, i) {
			i++
		}
		head := stmt[start:i]
		j := i
		for j < len(stmt) && !tops[j] {
			j++
		}
		out = append(out, clause{head: head, body: stmt[i:j]})
		i = j
	}
	return out
}

// extendsHead reports the rest of a clause's own name: the BY of GROUP BY,
// the OUTER JOIN of LEFT OUTER JOIN.
//
// A word that only begins a join where a join follows it does not belong to
// the clause before it otherwise: the LEFT of LEFT(name, 3) is a function,
// and taking it into a SELECT's name would push its own bracket away.
func extendsHead(stmt []Tok, i int) bool {
	if stmt[i].Kind != sqllex.TokKeyword {
		return false
	}
	w := lower(stmt[i])
	if followers[w] || w == "join" {
		return true
	}
	return leadIns[w] && joinFollows(stmt, i)
}

// topLevel marks the tokens that begin a clause outside any bracket.
func topLevel(stmt []Tok) []bool {
	out := make([]bool, len(stmt))
	depth := 0
	for i, t := range stmt {
		switch t.Text {
		case "(":
			depth++
			continue
		case ")":
			depth--
			continue
		}
		out[i] = depth == 0 && isClause(stmt, i)
	}
	return out
}

// writeClause writes one clause at a depth.
func writeClause(b *strings.Builder, c clause, depth int, opt Options) {
	pad := strings.Repeat(opt.Indent, depth)
	head := inline(c.head, opt)
	body := inline(c.body, opt)

	if len(c.body) == 0 {
		line(b, pad+head)
		return
	}
	whole := strings.TrimSpace(head + " " + body)
	if fits(pad, whole, opt) && !hasLineComment(c.body) && !hasBreak(c.body) {
		line(b, pad+whole)
		return
	}
	if head != "" {
		line(b, pad+head)
		depth++
	}
	writeItems(b, c.body, depth, opt)
}

// hasBreak reports a body that must go down the page whatever its length: a
// subquery is read as a statement of its own, not as a long expression.
func hasBreak(body []Tok) bool {
	depth := 0
	for i, t := range body {
		switch t.Text {
		case "(":
			depth++
			continue
		case ")":
			depth--
			continue
		}
		if depth > 0 && isClause(body, i) {
			return true
		}
	}
	return false
}

// writeItems writes a clause's body down the page, one item per line.
func writeItems(b *strings.Builder, body []Tok, depth int, opt Options) {
	for _, item := range items(body) {
		writeItem(b, item, depth, opt)
	}
}

// writeItem writes one item: along a line where it fits, and opened out
// where it does not.
func writeItem(b *strings.Builder, item []Tok, depth int, opt Options) {
	pad := strings.Repeat(opt.Indent, depth)
	if fits(pad, inline(item, opt), opt) && !hasLineComment(item) && !hasBreak(item) {
		line(b, pad+inline(item, opt))
		return
	}
	writeGroup(b, item, depth, opt)
}

// writeGroup opens out one item that will not fit along a line.
//
// In the order a reader looks for the next thing: a condition breaks at its
// AND and OR, a CASE breaks at its WHEN and ELSE, and anything else opens at
// the first bracket worth opening. Failing all three it is written as it is,
// long: a line nobody can break is better whole than broken somewhere that
// means nothing.
func writeGroup(b *strings.Builder, item []Tok, depth int, opt Options) {
	pad := strings.Repeat(opt.Indent, depth)
	if parts := split(item, conjunctionAt); len(parts) > 1 {
		for _, part := range parts {
			writeItem(b, part, depth, opt)
		}
		return
	}
	if open := caseAt(item); open >= 0 {
		writeCase(b, item, open, depth, opt)
		return
	}
	open := openWorthBreaking(item)
	if open < 0 {
		writeLeaf(b, item, pad, opt)
		return
	}
	close := matching(item, open)
	if close < 0 {
		writeLeaf(b, item, pad, opt)
		return
	}
	line(b, pad+strings.TrimSpace(inline(item[:open+1], opt)))
	inner := item[open+1 : close]
	if cs := clausesOf(inner); len(cs) > 1 || (len(cs) == 1 && cs[0].head != nil) {
		for _, c := range cs {
			writeClause(b, c, depth+1, opt)
		}
	} else {
		writeItems(b, inner, depth+1, opt)
	}
	line(b, pad+inline(item[close:], opt))
}

// writeCase opens a CASE out, one WHEN to a line.
func writeCase(b *strings.Builder, item []Tok, open, depth int, opt Options) {
	pad := strings.Repeat(opt.Indent, depth)
	close := endAt(item, open)
	if close < 0 {
		writeLeaf(b, item, pad, opt)
		return
	}
	if open > 0 {
		line(b, pad+inline(item[:open], opt))
		depth++
		pad = strings.Repeat(opt.Indent, depth)
	}
	line(b, pad+inline(item[open:open+1], opt))
	for _, arm := range split(item[open+1:close], armAt) {
		writeItem(b, arm, depth+1, opt)
	}
	line(b, pad+inline(item[close:], opt))
}

// armAt reports a word that begins an arm of a CASE.
func armAt(toks []Tok, i int) bool {
	if toks[i].Kind != sqllex.TokKeyword {
		return false
	}
	w := lower(toks[i])
	return w == "when" || w == "else"
}

// caseAt is where a CASE begins, outside any bracket, or -1.
func caseAt(toks []Tok) int {
	depth := 0
	for i, t := range toks {
		switch t.Text {
		case "(":
			depth++
		case ")":
			depth--
		}
		if depth == 0 && t.Kind == sqllex.TokKeyword && lower(t) == "case" {
			return i
		}
	}
	return -1
}

// endAt is where the CASE opened at i ends, or -1.
func endAt(toks []Tok, i int) int {
	depth := 0
	for j := i; j < len(toks); j++ {
		if toks[j].Kind != sqllex.TokKeyword {
			continue
		}
		switch lower(toks[j]) {
		case "case":
			depth++
		case "end":
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

// openWorthBreaking is the first bracket with anything in it, or -1. An
// empty one — NOW(), COUNT() — opens out into two lines saying nothing.
func openWorthBreaking(toks []Tok) int {
	for i, t := range toks {
		if t.Text != "(" {
			continue
		}
		if j := matching(toks, i); j > i+1 {
			return i
		}
	}
	return -1
}

// writeLeaf writes tokens that are not being broken any further, ending the
// line after a comment that would otherwise swallow what follows it.
func writeLeaf(b *strings.Builder, toks []Tok, pad string, opt Options) {
	start := 0
	for i, t := range toks {
		if t.Kind == sqllex.TokComment && !strings.HasPrefix(t.Text, "/*") {
			line(b, pad+inline(toks[start:i+1], opt))
			start = i + 1
		}
	}
	if start < len(toks) {
		line(b, pad+inline(toks[start:], opt))
	}
}

// conjunctionAt reports a token that begins a new line of a condition.
func conjunctionAt(toks []Tok, i int) bool {
	return toks[i].Kind == sqllex.TokKeyword && conjunctions[lower(toks[i])]
}

// items splits a body at the commas that separate its items, keeping each
// comma with the item it ends.
func items(body []Tok) [][]Tok {
	var out [][]Tok
	start, depth := 0, 0
	for i, t := range body {
		switch t.Text {
		case "(":
			depth++
		case ")":
			depth--
		case ",":
			if depth == 0 {
				out = append(out, body[start:i+1])
				start = i + 1
			}
		}
	}
	if start < len(body) {
		out = append(out, body[start:])
	}
	return out
}

// split breaks a run of tokens before every token the test picks out, at the
// top level of brackets.
func split(toks []Tok, at func([]Tok, int) bool) [][]Tok {
	var out [][]Tok
	start, depth := 0, 0
	for i, t := range toks {
		switch t.Text {
		case "(":
			depth++
		case ")":
			depth--
		}
		if i > start && depth == 0 && at(toks, i) {
			out = append(out, toks[start:i])
			start = i
		}
	}
	if start < len(toks) {
		out = append(out, toks[start:])
	}
	return out
}

// firstOpen is where the first bracket of an item opens, or -1.
func firstOpen(toks []Tok) int {
	for i, t := range toks {
		if t.Text == "(" {
			return i
		}
	}
	return -1
}

// matching is where the bracket opened at i closes, or -1.
func matching(toks []Tok, i int) int {
	depth := 0
	for j := i; j < len(toks); j++ {
		switch toks[j].Text {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

// fits reports whether a line of this width is one somebody can read.
func fits(pad, text string, opt Options) bool {
	return len(pad)+len(text) <= opt.Width
}

// line writes one.
//
// Nothing is trimmed off the end of it. Every line is an indent and then a
// run of tokens written by inline, which puts one space between two tokens
// and none after the last, so a trailing space is not something that can
// arrive here.
func line(b *strings.Builder, s string) {
	b.WriteString(s)
	b.WriteByte('\n')
}
