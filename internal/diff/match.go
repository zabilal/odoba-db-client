package diff

import (
	"maps"
	"slices"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Matching two lists by name, and settling what a node's status is.
//
// This is where the package's first rule lives. Everything is matched by
// name and nothing by position: two servers list their objects in whatever
// order they please, and a comparison that depended on that order would
// report a schema as rewritten because somebody rebuilt a table.

// match pairs two lists by name and compares each pair.
//
// What is in both is compared; what is in one is added or removed whole. The
// result is sorted by name, so the same two schemas always compare to the
// same tree whatever order the two servers listed them in.
func match[T any](from, to []T, kind func(T) model.ObjectKind, name func(T) string,
	compare func(a, b T) Node, skip ...func(string) bool) []Node {
	here := index(from, name)
	there := index(to, name)

	var out []Node
	for _, n := range slices.Sorted(maps.Keys(union(here, there))) {
		if leftOut(n, skip) {
			continue
		}
		a, inFrom := here[n]
		b, inTo := there[n]
		switch {
		case inFrom && inTo:
			out = append(out, compare(a, b))
		case inTo:
			out = append(out, Node{Kind: kind(b), Name: n, Status: Added})
		default:
			out = append(out, Node{Kind: kind(a), Name: n, Status: Removed})
		}
	}
	return out
}

// fixed is the kind of a thing that is only ever one kind, which is
// everything but a view.
//
// A view's kind is a property of the view: a materialized view listed on one
// side only must still be called one, or the tree calls it a view and
// everything reading the tree believes it.
func fixed[T any](k model.ObjectKind) func(T) model.ObjectKind {
	return func(T) model.ObjectKind { return k }
}

// leftOut reports a name a rule says not to look at. There is at most one
// rule, and none at all for the parts inside an object: a name pattern names
// objects, not the columns they are made of, or ignoring a table called
// audit would ignore a column of that name in every table there is.
func leftOut(name string, skip []func(string) bool) bool {
	return len(skip) > 0 && skip[0] != nil && skip[0](name)
}

// matchOne is match for something a table has at most one of, like its
// primary key. It is still matched by name, because a key renamed is a key
// dropped and a key made.
func matchOne[T any](from, to *T, kind model.ObjectKind, name func(*T) string, compare func(a, b *T) Node) []Node {
	var fromList, toList []*T
	if from != nil {
		fromList = []*T{from}
	}
	if to != nil {
		toList = []*T{to}
	}
	return match(fromList, toList, fixed[*T](kind), name, compare)
}

// index is a list by name. A name appearing twice keeps the first, because a
// duplicate is a server's business and not something to report as a
// difference in the other database.
func index[T any](list []T, name func(T) string) map[string]T {
	out := make(map[string]T, len(list))
	for _, v := range list {
		if _, seen := out[name(v)]; !seen {
			out[name(v)] = v
		}
	}
	return out
}

func union[T any](a, b map[string]T) map[string]struct{} {
	out := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		out[k] = struct{}{}
	}
	for k := range b {
		out[k] = struct{}{}
	}
	return out
}

// settle gives a node the status its own detail and its children imply.
//
// A node is changed if anything about it differs or anything under it does.
// A node with no detail and no children that differ is the same, which is
// how a tree can be filtered down to what matters and still hold what does
// not (FR-7.2).
func settle(n Node) Node {
	n.Status = Same
	if len(n.Detail) > 0 {
		n.Status = Changed
		return n
	}
	for _, c := range n.Children {
		if c.Status != Same {
			n.Status = Changed
			return n
		}
	}
	return n
}

// field is one property compared. It answers nothing when the two agree, so
// that Detail holds only what differs and a node's status can be read off
// its length.
func field(name, from, to string) *Field {
	if from == to {
		return nil
	}
	return &Field{Name: name, From: from, To: to}
}

// fields drops the properties that agreed.
func fields(fs ...*Field) []Field {
	var out []Field
	for _, f := range fs {
		if f != nil {
			out = append(out, *f)
		}
	}
	return out
}

// attrFields compares the engine-specific properties a model carries.
//
// A key in one and not the other is a difference like any other, and is
// shown with the side that has nothing written as nothing — which is what it
// is. Comparing two different engines' attributes will be noisy, and FR-7.5's
// ignore rules are where that is answered rather than by pretending the
// attributes are not there.
func attrFields(from, to map[string]string) []Field {
	var out []Field
	for _, k := range slices.Sorted(maps.Keys(union(from, to))) {
		if f := field(k, from[k], to[k]); f != nil {
			out = append(out, *f)
		}
	}
	return out
}

// list renders an ordered set of names, because the order is part of what
// they mean: a key on (a, b) is not a key on (b, a).
func list(names []string) string { return strings.Join(names, ", ") }

func yes(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// pick is the first of two names that is not empty.
func pick(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
