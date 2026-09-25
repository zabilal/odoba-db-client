package model

import (
	"slices"
	"strconv"
)

// Object classes (FR-2.1, FR-2.2, ADR-0024) are the folders the explorer
// groups objects under, between a schema (or a database) and its objects:
// Tables, Views, Indexes and the rest. A class is named by the kind of
// object it holds, so its label and its place in the list are the same on
// every engine, and come from here rather than from each driver (REQ-DB-4).
// A driver lists the classes it has objects of. Each must hold a kind its
// capability descriptor declares, which the conformance suite checks.

// Class is one object class: the kind of object it holds, and its name.
type Class struct {
	Kind  ObjectKind
	Label string
}

// Classes are every object class, in the order the explorer lists them: the
// data first, then what serves it, then code, then types.
var Classes = []Class{
	{KindTable, "Tables"},
	{KindView, "Views"},
	{KindMaterializedView, "Materialized Views"},
	{KindIndex, "Indexes"},
	{KindTrigger, "Triggers"},
	{KindRoutine, "Routines"},
	{KindSequence, "Sequences"},
	{KindUserType, "Types"},
	{KindCollection, "Collections"},
	{KindKey, "Keys"},
	{KindChannel, "Channels"},
	{KindTopic, "Topics"},
	{KindConsumerGroup, "Consumer Groups"},
	{KindSubject, "Subjects"},
}

// ClassLabel is the name of the class holding kind k, or "" if none does.
func ClassLabel(k ObjectKind) string {
	for _, c := range Classes {
		if c.Kind == k {
			return c.Label
		}
	}
	return ""
}

// ClassRef addresses the class of kind k's objects under parent: parent's
// path, then the kind.
func ClassRef(parent ObjectRef, k ObjectKind) ObjectRef {
	return NewRef(KindFolder, append(slices.Clone(parent.Path), string(k))...)
}

// ClassOf is the kind of object a class holds. False for any other node.
func ClassOf(ref ObjectRef) (ObjectKind, bool) {
	if ref.Kind != KindFolder || len(ref.Path) == 0 {
		return "", false
	}
	k := ObjectKind(ref.Name())
	return k, ClassLabel(k) != ""
}

// ClassNode is the node for the class of kind k under parent, holding n
// objects: an exact count, and an expander only when there is something in
// it.
func ClassNode(parent ObjectRef, k ObjectKind, n int64) Node {
	return Node{Ref: ClassRef(parent, k), Label: ClassLabel(k), HasChildren: n > 0,
		Badge: &Badge{Text: strconv.FormatInt(n, 10), Exact: true}}
}

// OnTable names an index or a trigger with its table, as "orders_total on
// orders": its class lists every table's together, and two tables' may
// share a name.
func OnTable(name, table string) string { return name + " on " + table }

// ClassNodes are the classes under parent that hold something, in the
// classes' order, from a count of each kind's objects. An empty class is
// noise in the tree, and a kind no class holds is left out.
func ClassNodes(parent ObjectRef, counts map[ObjectKind]int64) []Node {
	var out []Node
	for _, c := range Classes {
		if n := counts[c.Kind]; n > 0 {
			out = append(out, ClassNode(parent, c.Kind, n))
		}
	}
	return out
}
