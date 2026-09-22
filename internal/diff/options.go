package diff

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

// What a comparison is told to leave out (FR-7.5).
//
// The package compares text as text and every attribute it is given, and
// says so (ADR-0119): normalising quietly would be this package deciding two
// different things are the same, somewhere nobody can see or turn off. This
// is where that is decided instead — out loud, by whoever is comparing.
//
// A rule is applied while the comparison is made and not as a filter over
// the tree afterwards. A difference ignored is not a difference: it does not
// make its table read as changed, it is not counted in the summary, and it
// cannot be chosen for a sync script. Hiding it after the fact would leave
// all three wrong.
//
// Because a rule can hide a dropped column, whatever shows a comparison has
// to say that rules are in force. Options.Describe is written for that line.

// Options are the differences a comparison is told not to report.
type Options struct {
	// Schemas are left out entirely, by name. An audit schema nobody deploys
	// is the case this is for.
	Schemas []string

	// Names are patterns, matched against an object's own name: * for any
	// run of characters and ? for one, as a shell matches a file name. An
	// object whose name matches one is left out wherever it is.
	Names []string

	// Whitespace compares text — a view's body, a routine's, a check's
	// expression — with runs of whitespace read as one space, so that two
	// servers printing the same definition differently agree.
	Whitespace bool

	// Collation leaves out charsets and collations, which differ between two
	// servers far more often than anybody means them to.
	Collation bool

	// Comments leaves out every comment, which is the difference most often
	// deliberate on one side and absent on the other.
	Comments bool
}

// Any reports whether anything is being left out.
func (o Options) Any() bool {
	return len(o.Schemas) > 0 || len(o.Names) > 0 || o.Whitespace || o.Collation || o.Comments
}

// Check refuses a pattern that is not one, before a comparison is made with
// it: a rule nobody can see the effect of is worse than none.
func (o Options) Check() error {
	for _, p := range o.Names {
		if _, err := path.Match(p, ""); err != nil {
			return fmt.Errorf("diff: %q is not a name pattern: %w", p, err)
		}
	}
	return nil
}

// Describe says what is being left out, in a line whatever draws a
// comparison can put where it will be read.
func (o Options) Describe() string {
	var parts []string
	if len(o.Schemas) > 0 {
		parts = append(parts, "the "+strings.Join(o.Schemas, ", ")+" schemas")
	}
	if len(o.Names) > 0 {
		parts = append(parts, "names matching "+strings.Join(o.Names, ", "))
	}
	for _, c := range []struct {
		on   bool
		what string
	}{{o.Whitespace, "whitespace"}, {o.Collation, "collation"}, {o.Comments, "comments"}} {
		if c.on {
			parts = append(parts, c.what)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "Leaving out " + strings.Join(parts, ", ") + "."
}

// skipSchema reports a schema this comparison does not look at.
func (o Options) skipSchema(name string) bool {
	return slices.Contains(o.Schemas, name)
}

// skipName reports an object this comparison does not look at.
//
// A pattern that will not parse leaves the object in: Check is where a bad
// pattern is refused, and a comparison that silently dropped objects because
// of one would be the failure this is meant to prevent.
func (o Options) skipName(name string) bool {
	for _, p := range o.Names {
		if ok, err := path.Match(p, name); err == nil && ok {
			return true
		}
	}
	return false
}

// text is how a definition is compared: as it is, or with its whitespace
// read as one space.
func (o Options) text(s string) string {
	if !o.Whitespace {
		return s
	}
	return strings.Join(strings.Fields(s), " ")
}

// comment is a comment, or nothing at all where comments are left out.
func (o Options) comment(s string) string {
	if o.Comments {
		return ""
	}
	return s
}

// collationAttrs are the engine-specific properties a collation rule covers.
// They are matched by name because no two engines call them the same thing.
func collationAttr(key string) bool {
	k := strings.ToLower(key)
	return strings.Contains(k, "collat") || strings.Contains(k, "charset") ||
		strings.Contains(k, "encoding") || strings.Contains(k, "ctype")
}

// attrs is a set of engine-specific properties with what is left out taken
// away.
func (o Options) attrs(in map[string]string) map[string]string {
	if !o.Collation || len(in) == 0 {
		return in
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if !collationAttr(k) {
			out[k] = v
		}
	}
	return out
}
