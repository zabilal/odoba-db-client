package mongo

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// The dialect (source.Dialect) is what the rest of the application asks a
// source about its language: how a name is written, what a statement does,
// where one statement ends and the next begins, and what a browse would send.
//
// MongoDB's language is the console's (T2.37, ADR-0070), so all of this is
// mongosh: db.people.find({…}), not SELECT.

var _ source.Dialect = (*mongoSource)(nil)

// QuoteIdentifier writes a name as the shell reads one: in quotes, since a
// collection may be called anything at all.
func (s *mongoSource) QuoteIdentifier(name string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(name) + `"`
}

// QualifyRef writes an object as the shell addresses it.
func (s *mongoSource) QualifyRef(ref model.ObjectRef) string {
	switch len(ref.Path) {
	case 0:
		return ""
	case 1:
		return ref.Path[0]
	}
	return "db." + ref.Path[1]
}

// Placeholder is nothing: a command carries its values in the document it is
// written with, and nothing is bound beside it.
func (s *mongoSource) Placeholder(int) string { return "" }

// Classify says what a command does, so the guard can refuse it before it
// runs (NFR-S4). A command that cannot be read is taken to change
// everything, which is the safe direction.
func (s *mongoSource) Classify(statement string) source.Access {
	cmd, err := parseCommand(statement)
	if err != nil {
		return source.AccessDDL
	}
	access := cmd.access()
	if access == source.AccessRead && cmd.writesInPipeline() {
		return source.AccessWrite
	}
	return access
}

// SplitScript divides a console's script into its commands (FR-5.4, FR-5.10).
func (s *mongoSource) SplitScript(script string) []source.ScriptStatement {
	return splitCommands(script)
}

// BuildBrowse renders what a browse sends, for the grid to show (FR-3.6, UX
// principle 6): the find it would run, with its filter, its order and its
// page.
func (s *mongoSource) BuildBrowse(ref model.ObjectRef, opt source.BrowseOptions) (_ source.Statement, err error) {
	defer panics.Recover(&err, "writing a browse")
	if ref.Kind != model.KindCollection || len(ref.Path) < 2 {
		return source.Statement{}, fmt.Errorf("mongodb: %s holds no documents", ref)
	}
	filter, err := browseFilter(opt)
	if err != nil {
		return source.Statement{}, err
	}
	call := fmt.Sprintf("db.%s.find(%s", ref.Path[1], extJSON(filter))
	if len(opt.Columns) > 0 {
		call += ", " + extJSON(projectionOf(opt.Columns))
	}
	call += ")"
	if sort := sortOf(opt.Sorts); len(sort) > 0 {
		call += ".sort(" + extJSON(sort) + ")"
	}
	if opt.Offset > 0 {
		call += fmt.Sprintf(".skip(%d)", opt.Offset)
	}
	if opt.Limit > 0 {
		call += fmt.Sprintf(".limit(%d)", opt.Limit)
	}
	return source.Statement{SQL: call}, nil
}

// oneDocument reports whether the text is one document and nothing else.
// Extended JSON reads the first document it finds and ignores what follows,
// so {"a": 1} {"b": 2} would pass as a condition and the second one would be
// dropped without a word — the document store's version of a second
// statement smuggled in behind a semicolon.
func oneDocument(text string) error {
	dec := json.NewDecoder(strings.NewReader(text))
	var first json.RawMessage
	if err := dec.Decode(&first); err != nil {
		return fmt.Errorf("mongodb: a condition here is a filter document, such as {\"score\": {\"$gt\": 10}}: %w", err)
	}
	if dec.More() {
		return errors.New("mongodb: a condition is one filter document, and this is more than one")
	}
	if len(first) == 0 || first[0] != '{' {
		return errors.New("mongodb: a condition is a filter document, not a list or a value: {\"score\": {\"$gt\": 10}}")
	}
	return nil
}

// browseFilter is a browse's filters and its typed condition as one query
// document: the condition is a filter document of the person's own, which is
// what a condition is in this language (FR-3.6).
func browseFilter(opt source.BrowseOptions) (bson.D, error) {
	filter, err := filterOf(opt.Filters)
	if err != nil {
		return nil, err
	}
	where := strings.TrimSpace(opt.Where)
	if where == "" {
		return filter, nil
	}
	if err := oneDocument(where); err != nil {
		return nil, err
	}
	var typed bson.D
	if err := bson.UnmarshalExtJSON([]byte(where), false, &typed); err != nil {
		return nil, fmt.Errorf("mongodb: a condition here is a filter document, such as {\"score\": {\"$gt\": 10}}: %w", err)
	}
	if len(filter) == 0 {
		return typed, nil
	}
	return bson.D{{Key: "$and", Value: bson.A{filter, typed}}}, nil
}
