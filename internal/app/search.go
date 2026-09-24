package app

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Searching a database's structure (FR-2.7): a column somewhere among two
// hundred tables, a table named in a procedure's body, a default nobody
// can account for.
//
// It walks the tree and describes each object, which is what the explorer
// and the schema comparison already do, through the same calls. So it
// works on every engine that can be browsed, and it costs a round trip per
// object: the scope is what somebody picked in the tree, the results
// arrive as they are found, and stopping is cancelling the context.

// A Query is what to look for.
type Query struct {
	Text string
	// Case matches upper and lower case exactly. Folded, which is the
	// default, is what somebody typing a column's name expects.
	Case bool
	// Whole matches only where the text stands as a word of its own, so
	// that "id" does not answer with every "paid" and "width".
	Whole bool
}

// A Hit is one place the text was found.
type Hit struct {
	// Node is the object it was found in, as the tree gives it, so that
	// opening a hit is opening what the tree would have opened.
	Node model.Node
	// In names the part of the object: "the name", "column total", "the
	// body", "check ck_positive".
	In string
	// Line is the line the text is on, with the space around it trimmed,
	// and At is where in that line the match begins, in bytes.
	Line string
	At   int
}

// Empty reports whether there is nothing to look for.
func (q Query) Empty() bool { return strings.TrimSpace(q.Text) == "" }

// find returns where the query's text occurs in s, or -1.
func (q Query) find(s string) int {
	text, hay := q.Text, s
	if !q.Case {
		text, hay = strings.ToLower(text), strings.ToLower(hay)
	}
	for at := 0; ; {
		i := strings.Index(hay[at:], text)
		if i < 0 {
			return -1
		}
		i += at
		if !q.Whole || whole(hay, i, len(text)) {
			return i
		}
		at = i + 1
	}
}

// whole reports whether the run of n bytes at i stands as a word: what is
// on either side of it is not something a name is made of.
func whole(s string, i, n int) bool {
	before := i == 0 || !namely(rune(s[i-1]))
	after := i+n == len(s) || !namely(rune(s[i+n]))
	return before && after
}

// namely reports whether a byte is one an identifier is made of.
func namely(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// Search walks what root holds and reports every place the query's text
// occurs in the structure. A zero root is the source's own.
//
// Each hit goes to emit as it is found, so that a search over a large
// database fills a list rather than waiting to. emit returns false to stop
// looking, which is how a limit is kept; cancelling ctx stops it too.
func Search(ctx context.Context, src source.Source, root model.ObjectRef, q Query, emit func(Hit) bool) (err error) {
	defer panics.Recover(&err, "searching the structure") // the driver is called here
	if q.Empty() {
		return nil
	}
	_, err = searchUnder(ctx, src, root, q, emit)
	return err
}

// searchUnder searches one node's children, and reports whether to go on.
func searchUnder(ctx context.Context, src source.Source, ref model.ObjectRef, q Query, emit func(Hit) bool) (bool, error) {
	var (
		kids []model.Node
		err  error
	)
	if ref.IsZero() {
		kids, err = src.Root(ctx)
	} else {
		kids, err = src.Children(ctx, ref)
	}
	if err != nil {
		return false, fmt.Errorf("reading what %s holds: %w", naming(ref.Name()), err)
	}
	for _, n := range kids {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if HoldsObjects(n.Ref) {
			on, err := searchUnder(ctx, src, n.Ref, q, emit)
			if err != nil || !on {
				return on, err
			}
			continue
		}
		on, err := searchObject(ctx, src, n, q, emit)
		if err != nil || !on {
			return on, err
		}
	}
	return true, nil
}

// HoldsObjects reports whether a node is somewhere objects live rather
// than an object: a database, a schema, or a class folder. A search
// descends into one and not into an object, because what an object holds
// arrives with it.
//
// It is exported because whatever offers a search has to ask the same
// question about what somebody has selected, and asking it twice in two
// places is how the two come to disagree.
func HoldsObjects(ref model.ObjectRef) bool {
	if _, ok := model.ClassOf(ref); ok {
		return true
	}
	return ref.Kind == model.KindDatabase || ref.Kind == model.KindSchema
}

// searchObject describes one object and matches what it says.
//
// An object that cannot be described is passed over rather than failing
// the search: one kind nobody wrote a Describe for would otherwise be a
// database nobody can search.
func searchObject(ctx context.Context, src source.Source, n model.Node, q Query, emit func(Hit) bool) (bool, error) {
	hit := func(in, text string) bool {
		at := q.find(text)
		if at < 0 {
			return true
		}
		line, at := lineAt(text, at)
		return emit(Hit{Node: n, In: in, Line: line, At: at})
	}
	if !hit("the name", n.Ref.Name()) {
		return false, nil
	}
	desc, err := src.Describe(ctx, n.Ref)
	if err != nil {
		return true, nil
	}
	switch v := desc.(type) {
	case *model.Table:
		return searchTable(v, hit), nil
	case *model.View:
		return searchView(v, hit), nil
	case *model.Routine:
		return searchRoutine(v, hit), nil
	}
	return true, nil
}

// looker looks in one place and reports whether to go on looking.
type looker func(in, text string) bool

func searchTable(t *model.Table, hit looker) bool {
	if !hit("the comment", t.Comment) {
		return false
	}
	for _, c := range t.Columns {
		if !searchColumn(c, hit) {
			return false
		}
	}
	for _, i := range t.Indexes {
		if !hit("index "+i.Name, i.Name+" "+indexed(i)+" "+i.Predicate) {
			return false
		}
	}
	for _, c := range t.Checks {
		if !hit("check "+c.Name, c.Name+" "+c.Expression) {
			return false
		}
	}
	for _, f := range t.ForeignKeys {
		if !hit("foreign key "+f.Name, f.Name+" "+strings.Join(f.Columns, ", ")+" "+f.RefTable) {
			return false
		}
	}
	for _, g := range t.Triggers {
		if !hit("trigger "+g.Name, g.Name+"\n"+g.Condition+"\n"+g.Definition) {
			return false
		}
	}
	return true
}

// indexed is what an index is on: the columns it names, and the
// expressions where it is on an expression rather than a column.
func indexed(i model.Index) string {
	var parts []string
	for _, c := range i.Columns {
		if c.Expression != "" {
			parts = append(parts, c.Expression)
			continue
		}
		parts = append(parts, c.Name)
	}
	return strings.Join(append(parts, i.Include...), ", ")
}

func searchColumn(c model.Column, hit looker) bool {
	where := "column " + c.Name
	return hit(where, c.Name+" "+c.Type.Native) &&
		hit(where+"'s default", c.Default+" "+c.Generated) &&
		hit(where+"'s comment", c.Comment)
}

func searchView(v *model.View, hit looker) bool {
	for _, c := range v.Columns {
		if !searchColumn(c, hit) {
			return false
		}
	}
	return hit("the comment", v.Comment) && hit("the definition", v.Definition)
}

func searchRoutine(r *model.Routine, hit looker) bool {
	for _, p := range r.Parameters {
		if !hit("parameter "+p.Name, p.Name+" "+p.Type.Native) {
			return false
		}
	}
	return hit("the comment", r.Comment) && hit("the body", r.Definition)
}

// lineAt is the line a match is on, trimmed of the space around it, and
// where in that line the match begins. A definition is many lines long and
// a list shows one: this is the one it shows.
func lineAt(text string, at int) (string, int) {
	from := strings.LastIndexByte(text[:at], '\n') + 1
	to := strings.IndexByte(text[at:], '\n')
	if to < 0 {
		to = len(text)
	} else {
		to += at
	}
	line := text[from:to]
	cut := len(line) - len(strings.TrimLeft(line, " \t"))
	return strings.TrimSpace(line), at - from - cut
}
