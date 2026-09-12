package mongo

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// The command console (FR-12.1, T2.37).
//
// mongosh is JavaScript, and this is not a JavaScript engine. It reads the
// commands a person actually types at a database prompt — db.people.find({…}),
// show collections, use shop — and says plainly what it does not understand,
// rather than guessing at a language it only half speaks.

// command is one console line, read.
type command struct {
	// kind is what it does: find, insertOne, show, use.
	kind string

	// collection is the collection it is on, empty for a command on the
	// database itself.
	collection string

	// args are its arguments, each read as a document or a value.
	args []bson.RawValue

	// word is the bare word after show or use.
	word string
}

// access is what a command does, for the guard (NFR-S4). A command this does
// not know is taken to change everything, as an unclassifiable statement is
// everywhere else.
func (c command) access() source.Access {
	switch c.kind {
	case "find", "findOne", "count", "countDocuments", "estimatedDocumentCount",
		"distinct", "aggregate", "getIndexes", "stats", "getCollectionNames",
		"show", "use", "explain":
		return source.AccessRead
	case "insertOne", "insertMany", "updateOne", "updateMany", "replaceOne",
		"deleteOne", "deleteMany", "findOneAndUpdate", "findOneAndDelete":
		return source.AccessWrite
	case "drop", "createIndex", "dropIndex", "createCollection", "renameCollection":
		return source.AccessDDL
	}
	return source.AccessDDL
}

// writesInPipeline reports an aggregate whose stages write ($out, $merge),
// which no classification of the command itself would catch.
func (c command) writesInPipeline() bool {
	if c.kind != "aggregate" || len(c.args) == 0 {
		return false
	}
	stages, err := stagesOf(c.args[0])
	if err != nil {
		return false
	}
	return writes(stages)
}

// parseCommand reads one console line.
func parseCommand(text string) (command, error) {
	line := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), ";"))
	if line == "" {
		return command{}, errors.New("mongodb: there is nothing to run")
	}
	if word, rest, ok := bareWord(line, "show"); ok {
		if word == "" {
			return command{}, errors.New("mongodb: show what? collections, dbs")
		}
		_ = rest
		return command{kind: "show", word: strings.ToLower(word)}, nil
	}
	if word, _, ok := bareWord(line, "use"); ok {
		if word == "" {
			return command{}, errors.New("mongodb: use which database?")
		}
		return command{kind: "use", word: word}, nil
	}
	if !strings.HasPrefix(line, "db.") && !strings.HasPrefix(line, "db[") {
		return command{}, fmt.Errorf("mongodb: %s is not a command this console knows; try db.<collection>.find(), show collections, or use <database>", firstWord(line))
	}
	rest := line[len("db"):]
	coll, rest, err := member(rest)
	if err != nil {
		return command{}, err
	}
	// db.<method>(…) is a command on the database itself; db.<coll>.<method>(…)
	// is one on a collection.
	name := coll
	if strings.HasPrefix(rest, ".") || strings.HasPrefix(rest, "[") {
		method, after, err := member(rest)
		if err != nil {
			return command{}, err
		}
		args, err := arguments(after, name+"."+method)
		if err != nil {
			return command{}, err
		}
		return command{kind: method, collection: name, args: args}, nil
	}
	args, err := arguments(rest, name)
	if err != nil {
		return command{}, err
	}
	return command{kind: name, args: args}, nil
}

// bareWord reads "show collections" and "use shop": a word, then a word.
func bareWord(line, keyword string) (word, rest string, ok bool) {
	if !strings.HasPrefix(strings.ToLower(line), keyword) {
		return "", "", false
	}
	after := line[len(keyword):]
	if after != "" && !isSpace(after[0]) {
		return "", "", false // "shows" or "used", not "show" or "use"
	}
	fields := strings.Fields(after)
	if len(fields) == 0 {
		return "", "", true
	}
	return fields[0], strings.TrimSpace(after[strings.Index(after, fields[0])+len(fields[0]):]), true
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

// member reads ".name" or ["name"] from the front of a line.
func member(line string) (name, rest string, err error) {
	switch {
	case strings.HasPrefix(line, "."):
		i := 1
		for i < len(line) && (isNameByte(line[i])) {
			i++
		}
		if i == 1 {
			return "", "", errors.New("mongodb: a name is expected after the dot")
		}
		return line[1:i], line[i:], nil
	case strings.HasPrefix(line, "["):
		end := strings.Index(line, "]")
		if end < 0 {
			return "", "", errors.New("mongodb: the [ has no ]")
		}
		inner := strings.TrimSpace(line[1:end])
		unquoted, err := strconv.Unquote(inner)
		if err != nil {
			// Single quotes are JavaScript's too.
			if len(inner) >= 2 && inner[0] == '\'' && inner[len(inner)-1] == '\'' {
				unquoted = inner[1 : len(inner)-1]
			} else {
				return "", "", fmt.Errorf("mongodb: %s is not a name in brackets", inner)
			}
		}
		return unquoted, line[end+1:], nil
	}
	return "", "", errors.New("mongodb: a command reads db.<collection>.<method>(…)")
}

func isNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '$'
}

// arguments reads the (…) of a call: each argument as extended JSON.
func arguments(line, called string) ([]bson.RawValue, error) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "(") {
		return nil, fmt.Errorf("mongodb: %s is not called: write %s(…)", called, called)
	}
	end := matching(line)
	if end < 0 {
		return nil, fmt.Errorf("mongodb: the ( of %s has no )", called)
	}
	if tail := strings.TrimSpace(line[end+1:]); tail != "" {
		// .find({}).limit(5) and friends: one call a line, said plainly.
		return nil, fmt.Errorf("mongodb: one call at a time; %s is more than the console reads", strings.TrimSpace(tail))
	}
	inner := strings.TrimSpace(line[1:end])
	if inner == "" {
		return nil, nil
	}
	var out []bson.RawValue
	for _, arg := range splitArgs(inner) {
		v, err := valueOf(arg)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// matching is the index of the ) closing the ( at the front, or -1. Brackets
// inside strings are text.
func matching(line string) int {
	depth, quote := 0, byte(0)
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
			if depth == 0 && c == ')' {
				return i
			}
		}
	}
	return -1
}

// splitArgs divides a call's arguments on the commas between them, leaving
// the commas inside documents, lists and strings where they are.
func splitArgs(inner string) []string {
	var out []string
	depth, quote, start := 0, byte(0), 0
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case c == ',' && depth == 0:
			out = append(out, strings.TrimSpace(inner[start:i]))
			start = i + 1
		}
	}
	return append(out, strings.TrimSpace(inner[start:]))
}

// valueOf reads one argument: a document, a list, or a value of its own.
func valueOf(text string) (bson.RawValue, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return bson.RawValue{}, errors.New("mongodb: an argument is empty")
	}
	// Everything is read as the field of one document, because extended JSON
	// reads a document and an argument may be a number or a string.
	var doc bson.D
	wrapped := `{"v":` + jsQuoted(text) + `}`
	if err := bson.UnmarshalExtJSON([]byte(wrapped), false, &doc); err != nil {
		return bson.RawValue{}, fmt.Errorf("mongodb: %s is not a value this console reads: %w", text, err)
	}
	raw, err := bson.Marshal(doc)
	if err != nil {
		return bson.RawValue{}, err
	}
	return bson.Raw(raw).Lookup("v"), nil
}

// jsQuoted turns JavaScript's single-quoted strings into JSON's double ones,
// so that db.people.find({'name': 'Ada'}) reads as it looks. Quotes inside a
// double-quoted string are left alone.
func jsQuoted(text string) string {
	var b strings.Builder
	quote := byte(0)
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case quote == '"':
			b.WriteByte(c)
			if c == '\\' && i+1 < len(text) {
				i++
				b.WriteByte(text[i])
			} else if c == '"' {
				quote = 0
			}
			continue
		case quote == '\'':
			switch {
			case c == '\\' && i+1 < len(text):
				b.WriteByte(c)
				i++
				b.WriteByte(text[i])
			case c == '\'':
				b.WriteByte('"')
				quote = 0
			case c == '"':
				b.WriteString(`\"`) // it was text inside single quotes
			default:
				b.WriteByte(c)
			}
			continue
		case c == '"':
			quote = '"'
		case c == '\'':
			quote = '\''
			b.WriteByte('"')
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// stagesOf reads an aggregate's first argument as a pipeline.
func stagesOf(v bson.RawValue) ([]bson.D, error) {
	arr, ok := v.ArrayOK()
	if !ok {
		return nil, errors.New("mongodb: an aggregate takes an array of stages")
	}
	vals, err := arr.Values()
	if err != nil {
		return nil, err
	}
	out := make([]bson.D, 0, len(vals))
	for i, item := range vals {
		doc, ok := item.DocumentOK()
		if !ok {
			return nil, fmt.Errorf("mongodb: stage %d is not a document", i+1)
		}
		var stage bson.D
		if err := bson.Unmarshal(doc, &stage); err != nil {
			return nil, err
		}
		out = append(out, stage)
	}
	return out, nil
}

// firstWord is what a line begins with, for saying what was not understood
// without repeating the whole line back.
func firstWord(line string) string {
	if i := strings.IndexAny(line, " \t(.["); i > 0 {
		return line[:i]
	}
	return line
}
