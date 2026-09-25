package redis

import (
	"fmt"
	"strconv"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// The dialect (source.Dialect) is what the rest of the application asks a
// source about its language: how a name is written, what a statement does,
// where one statement ends and the next begins, and what a browse would send.
//
// Redis's language is its commands (T2.44, ADR-0077), so all of this is
// redis-cli: GET, HSCAN, SCAN MATCH, and not SELECT.

var _ source.Dialect = (*redisSource)(nil)

// QuoteIdentifier writes a name as redis-cli reads one: a word with nothing
// surprising in it as it is, and anything else in quotes.
func (s *redisSource) QuoteIdentifier(name string) string { return quote(name) }

// QualifyRef writes an object as a command addresses it: a key by its name,
// and a database by the number it is.
func (s *redisSource) QualifyRef(ref model.ObjectRef) string {
	switch {
	case len(ref.Path) == 0:
		return ""
	case ref.Kind == model.KindKey && len(ref.Path) == 2:
		return quote(ref.Path[1])
	}
	return ref.Path[0]
}

// Placeholder is nothing: a command carries its arguments, and nothing is
// bound beside them.
func (s *redisSource) Placeholder(int) string { return "" }

// Classify says what a command does, so the guard can refuse it before it
// runs (NFR-S4). A command that cannot even be read is taken to change
// everything, which is the safe direction.
func (s *redisSource) Classify(statement string) source.Access {
	args, err := parseArgs(statement)
	if err != nil || len(args) == 0 {
		return source.AccessDDL
	}
	return commandAccess(args)
}

// SplitScript divides a console's script into its commands (FR-5.4, FR-5.10).
func (s *redisSource) SplitScript(script string) []source.ScriptStatement {
	return splitCommands(script)
}

// BuildBrowse renders what a browse sends, for the grid to show (FR-3.6, UX
// principle 6): the walk of a keyspace, or the read of one key's value.
func (s *redisSource) BuildBrowse(ref model.ObjectRef, opt source.BrowseOptions) (_ source.Statement, err error) {
	defer panics.Recover(&err, "writing a browse")
	limit := opt.Limit
	if limit <= 0 {
		limit = defaultPage
	}
	if ref.Kind == model.KindKey {
		return browseValueStatement(s.kindRead(ref), ref, opt, limit)
	}
	if name, err := channelOf(ref); err == nil {
		// What a person would type to see the same thing. Only a followed
		// browse sends it — a channel read without following sends nothing at
		// all, because there is nothing to ask for.
		return source.Statement{SQL: "SUBSCRIBE " + quote(name)}, nil
	}
	if _, err := databaseOf(ref); err != nil {
		return source.Statement{}, err
	}
	scan, err := scanOf(opt)
	if err != nil {
		return source.Statement{}, err
	}
	text := "SCAN 0"
	if scan.match != "" {
		text += " MATCH " + quote(scan.match)
	}
	text += " COUNT " + strconv.FormatInt(batchOf(limit), 10)
	if scan.kind != "" {
		text += " TYPE " + scan.kind
	}
	return source.Statement{SQL: text}, nil
}

// browseValueStatement is the command a key's own browse sends. What it is
// depends on what the key holds, which only the server knows, so the kind is
// asked for where a browse would ask.
func browseValueStatement(kind string, ref model.ObjectRef, opt source.BrowseOptions, limit int64) (source.Statement, error) {
	_, name, err := keyOf(ref)
	if err != nil {
		return source.Statement{}, err
	}
	if kind == "" {
		// Nothing has read this key yet, so what it holds is not known here
		// and no command can be written for it. The grid shows none rather
		// than one that is not what it sends.
		return source.Statement{}, fmt.Errorf("redis: what %s holds is not known yet", name)
	}
	cols, _, err := valueColumns(kind)
	if err != nil {
		return source.Statement{}, err
	}
	match, err := memberMatch(kind, cols, opt)
	if err != nil {
		return source.Statement{}, err
	}
	walk := func(cmd string) string {
		text := cmd + " " + quote(name) + " 0"
		if match != "" {
			text += " MATCH " + quote(match)
		}
		return text + " COUNT " + strconv.FormatInt(batchOf(limit), 10)
	}
	switch kind {
	case "string":
		return source.Statement{SQL: "GET " + quote(name)}, nil
	case "list":
		return source.Statement{SQL: fmt.Sprintf("LRANGE %s %d %d", quote(name), opt.Offset, opt.Offset+limit-1)}, nil
	case "set":
		return source.Statement{SQL: walk("SSCAN")}, nil
	case "zset":
		return source.Statement{SQL: walk("ZSCAN")}, nil
	case "stream":
		return source.Statement{SQL: fmt.Sprintf("XRANGE %s - + COUNT %d", quote(name), batchOf(limit))}, nil
	case "ReJSON-RL":
		return source.Statement{SQL: "JSON.GET " + quote(name) + " $"}, nil
	}
	return source.Statement{SQL: walk("HSCAN")}, nil
}
