package sqlcomplete

import "strings"

// A Snippet is a statement worth typing once: its trigger, what it writes,
// and how it reads in the popup.
//
// Body holds the places a person fills in, as ${1:name}, in the order they
// are stepped through, and $0 where the caret ends. The editor reads them
// when it writes the snippet in (ADR-0061); nothing here parses them, and
// nothing else in a completion's Insert is ever read that way.
type Snippet struct {
	Trigger string
	Detail  string
	Body    string
}

// snippets are the statements every SQL dialect writes the same way. They are
// deliberately few: a list long enough to need searching is slower than
// typing. Each is a shape a person types often and gets wrong once — the
// order of INSERT's columns and values, a CASE's END, a CTE's brackets.
var snippets = []Snippet{
	{"sel", "SELECT … FROM …", "SELECT ${1:*}\nFROM ${2:table}"},
	{"selw", "SELECT … WHERE …", "SELECT ${1:*}\nFROM ${2:table}\nWHERE ${3:condition}"},
	{"selc", "SELECT count(*) …", "SELECT count(*)\nFROM ${1:table}"},
	{"ins", "INSERT INTO …", "INSERT INTO ${1:table} (${2:columns})\nVALUES (${3:values})"},
	{"upd", "UPDATE … SET …", "UPDATE ${1:table}\nSET ${2:column} = ${3:value}\nWHERE ${4:condition}"},
	{"del", "DELETE FROM …", "DELETE FROM ${1:table}\nWHERE ${2:condition}"},
	{"join", "JOIN … ON …", "JOIN ${1:table} ${2:alias} ON ${3:condition}"},
	{"cte", "WITH … AS (…)", "WITH ${1:name} AS (\n\t${2:SELECT 1}\n)\nSELECT ${3:*}\nFROM ${1:name}"},
	{"case", "CASE WHEN … END", "CASE WHEN ${1:condition} THEN ${2:value}\n\tELSE ${3:other}\nEND"},
	{"ct", "CREATE TABLE …", "CREATE TABLE ${1:table} (\n\t${2:id} ${3:integer} PRIMARY KEY\n)"},
	{"ci", "CREATE INDEX …", "CREATE INDEX ${1:name} ON ${2:table} (${3:columns})"},
	{"grp", "GROUP BY … HAVING …", "GROUP BY ${1:columns}\nHAVING ${2:condition}"},
}

// snippetCandidates are the snippets as candidates, written in the case being
// typed, as a keyword is.
func snippetCandidates(prefix string) []completion {
	upper := keywordUpper(prefix)
	out := make([]completion, 0, len(snippets))
	for _, s := range snippets {
		body := s.Body
		detail := s.Detail
		if !upper {
			body, detail = strings.ToLower(body), strings.ToLower(detail)
		}
		out = append(out, completion{label: s.Trigger, insert: body, detail: detail})
	}
	return out
}

// completion is a candidate before it is scored, kept apart from
// source.Completion so that the snippet set says nothing about ranking.
type completion struct{ label, insert, detail string }
