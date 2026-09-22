package diff

import (
	"maps"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// The properties a comparison has to hold whatever it is given (RISK-8).
//
// The tests written by hand say what this package does about the cases
// somebody thought of. These say what it must never do about any case at
// all, and they are put to thousands of schemas built from a stream of
// bytes — which is how a comparison of a table with no columns, a schema
// with two objects of one name, and a routine with no return type all get
// compared without anybody sitting down to write them out.

// seeds are the streams kept as examples: they run in an ordinary test run,
// so a failure found by fuzzing stays found.
var seeds = [][]byte{
	{},
	{0},
	{1, 2, 3},
	{255, 255, 255, 255},
	{7, 3, 9, 1, 4, 1, 5, 9, 2, 6, 5, 3, 5, 8, 9, 7, 9, 3},
	{1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0},
	{9, 9, 9, 1, 1, 1, 2, 2, 2, 3, 3, 3, 4, 4, 4, 5, 5, 5, 6, 6, 6, 7, 7, 7},
}

// A model compared with itself differs in nothing. This is the property that
// catches a comparison reading a pointer's address rather than what it
// points at, or one whose answer depends on the order a map was walked.
func FuzzAModelComparedWithItselfNeverDiffers(f *testing.F) {
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, seed []byte) {
		db := (&gen{b: seed}).database()
		if got := Compare(db, db); got.Differs() {
			t.Fatalf("a model compared with itself as %s:\n%s", got.Status, changedIn(got))
		}
		// And with a copy of itself, which is the same claim without the
		// same pointers to lean on.
		other := (&gen{b: seed}).database()
		if got := Compare(db, other); got.Differs() {
			t.Fatalf("two readings of one model compared as %s:\n%s", got.Status, changedIn(got))
		}
	})
}

// Turning the comparison round turns every difference round with it and
// changes nothing else. A comparison that was not symmetric would write a
// sync script that does the opposite of what was asked in one direction.
//
// The two trees are walked together rather than matched by name, because the
// root is named after the target and the target is the other model this way
// round. Everything below it is in the same order both ways, since a
// comparison sorts what it matches.
func FuzzTheComparisonIsSymmetric(f *testing.F) {
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, seed []byte) {
		a, b := twoOf(seed)
		mirror := map[Status]Status{Added: Removed, Removed: Added, Changed: Changed, Same: Same}
		there, back := Compare(a, b), Compare(b, a)

		var walk func(x, y Node, at string, root bool)
		walk = func(x, y Node, at string, root bool) {
			where := at + "/" + string(x.Kind) + ":" + x.Name
			if !root && (x.Name != y.Name || !sameKind(x.Kind, y.Kind)) {
				t.Fatalf("%s one way and %s %q the other", describeNode(where, x), y.Kind, y.Name)
			}
			if mirror[x.Status] != y.Status {
				t.Fatalf("%s one way and %s the other", describeNode(where, x), y.Status)
			}
			if len(x.Detail) != len(y.Detail) {
				t.Fatalf("%s holds %d differences one way and %d the other",
					describeNode(where, x), len(x.Detail), len(y.Detail))
			}
			for i, d := range x.Detail {
				if e := y.Detail[i]; d.Name != e.Name || d.From != e.To || d.To != e.From {
					t.Fatalf("%s says %q %q -> %q one way and %q %q -> %q the other",
						describeNode(where, x), d.Name, d.From, d.To, e.Name, e.From, e.To)
				}
			}
			if len(x.Children) != len(y.Children) {
				t.Fatalf("%s holds %d one way and %d the other",
					describeNode(where, x), len(x.Children), len(y.Children))
			}
			for i := range x.Children {
				walk(x.Children[i], y.Children[i], where, false)
			}
		}
		walk(there, back, "", true)
	})
}

// Every difference reported is about something in one of the two models.
// A comparison that invented a node would put an object in a sync script
// that nobody has.
func FuzzEveryNodeIsInOneOfTheModels(f *testing.F) {
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, seed []byte) {
		a, b := twoOf(seed)
		in, out := namesIn(a), namesIn(b)
		Compare(a, b).Walk(func(id string, n Node) {
			if n.Kind == model.KindDatabase {
				return
			}
			switch n.Status {
			case Added:
				if !out[key(n)] {
					t.Fatalf("%s is reported as missing here but is in neither model", describeNode(id, n))
				}
			case Removed:
				if !in[key(n)] {
					t.Fatalf("%s is reported as only here but is in neither model", describeNode(id, n))
				}
			default:
				if !in[key(n)] || !out[key(n)] {
					t.Fatalf("%s is reported as in both and is not", describeNode(id, n))
				}
			}
		})
	})
}

// key names an object for the purpose of "is it in this model".
//
// A view is keyed without its kind, because a node's kind is the target's: a
// plain view on one side and a materialized one on the other is one object
// that differs, not two.
func key(n Node) string { return string(plainly(n.Kind)) + ":" + n.Name }

func plainly(k model.ObjectKind) model.ObjectKind {
	if k == model.KindMaterializedView {
		return model.KindView
	}
	return k
}

// namesIn is every object a model holds, by kind and name.
func namesIn(db *model.Database) map[string]bool {
	out := map[string]bool{}
	add := func(k model.ObjectKind, name string) { out[string(plainly(k))+":"+name] = true }
	for _, s := range db.Schemas {
		add(model.KindSchema, s.Name)
		for _, t := range s.Tables {
			add(model.KindTable, t.Name)
			for _, c := range t.Columns {
				add(model.KindColumn, c.Name)
			}
			if t.PrimaryKey != nil {
				add(model.KindPrimaryKey, t.PrimaryKey.Name)
			}
			for _, u := range t.Uniques {
				add(model.KindUnique, u.Name)
			}
			for _, c := range t.Checks {
				add(model.KindCheck, c.Name)
			}
			for _, k := range t.ForeignKeys {
				add(model.KindForeignKey, k.Name)
			}
			for _, i := range t.Indexes {
				add(model.KindIndex, i.Name)
			}
			for _, g := range t.Triggers {
				add(model.KindTrigger, g.Name)
			}
		}
		for _, v := range s.Views {
			add(viewKind(v), v.Name)
			for _, c := range v.Columns {
				add(model.KindColumn, c.Name)
			}
			for _, i := range v.Indexes {
				add(model.KindIndex, i.Name)
			}
		}
		for _, r := range s.Routines {
			add(model.KindRoutine, routineName(r))
		}
		for _, q := range s.Sequences {
			add(model.KindSequence, q.Name)
		}
		for _, u := range s.UserTypes {
			add(model.KindUserType, u.Name)
		}
	}
	return out
}

// The order two servers list their objects in is not a difference, so
// shuffling either model changes nothing at all.
func FuzzOrderIsNotADifference(f *testing.F) {
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, seed []byte) {
		a, b := twoOf(seed)
		want := Compare(a, b)
		shuffle(rand.New(rand.NewPCG(1, uint64(len(seed)))), b)
		got := Compare(a, b)
		if changedIn(want) != changedIn(got) {
			t.Fatalf("shuffled, it found\n%s\ninstead of\n%s", changedIn(got), changedIn(want))
		}
		if !slices.Equal(idsOf(want), idsOf(got)) {
			t.Fatalf("shuffled, the tree holds %v instead of %v", idsOf(got), idsOf(want))
		}
	})
}

func idsOf(n Node) []string {
	var out []string
	n.Walk(func(id string, _ Node) { out = append(out, id) })
	return out
}

// shuffle puts a model's lists in a different order, which no comparison may
// notice.
func shuffle(r *rand.Rand, db *model.Database) {
	r.Shuffle(len(db.Schemas), func(i, j int) { db.Schemas[i], db.Schemas[j] = db.Schemas[j], db.Schemas[i] })
	for si := range db.Schemas {
		s := &db.Schemas[si]
		r.Shuffle(len(s.Tables), func(i, j int) { s.Tables[i], s.Tables[j] = s.Tables[j], s.Tables[i] })
		r.Shuffle(len(s.Views), func(i, j int) { s.Views[i], s.Views[j] = s.Views[j], s.Views[i] })
		r.Shuffle(len(s.Routines), func(i, j int) { s.Routines[i], s.Routines[j] = s.Routines[j], s.Routines[i] })
		r.Shuffle(len(s.Sequences), func(i, j int) { s.Sequences[i], s.Sequences[j] = s.Sequences[j], s.Sequences[i] })
		r.Shuffle(len(s.UserTypes), func(i, j int) { s.UserTypes[i], s.UserTypes[j] = s.UserTypes[j], s.UserTypes[i] })
		for ti := range s.Tables {
			tb := &s.Tables[ti]
			r.Shuffle(len(tb.Columns), func(i, j int) { tb.Columns[i], tb.Columns[j] = tb.Columns[j], tb.Columns[i] })
			r.Shuffle(len(tb.Indexes), func(i, j int) { tb.Indexes[i], tb.Indexes[j] = tb.Indexes[j], tb.Indexes[i] })
			r.Shuffle(len(tb.Triggers), func(i, j int) { tb.Triggers[i], tb.Triggers[j] = tb.Triggers[j], tb.Triggers[i] })
		}
	}
}

// A rule leaves things out. It can never put one in: whatever a comparison
// with rules reports, a comparison without them reports too.
func FuzzARuleNeverAddsADifference(f *testing.F) {
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, seed []byte) {
		a, b := twoOf(seed)
		opt := (&gen{b: seed}).options()
		if opt.Check() != nil {
			t.Skip("not a pattern, which Check refuses before a comparison is made")
		}
		plain, ruled := Compare(a, b), CompareWith(a, b, opt)

		all := map[string]Node{}
		plain.Walk(func(id string, n Node) { all[id] = n })
		ruled.Walk(func(id string, n Node) {
			was, held := all[id]
			if !held {
				t.Fatalf("a rule put %s into the comparison", describeNode(id, n))
			}
			if n.Status == Same || was.Status == n.Status {
				return
			}
			// The only status a rule may change is one that differed
			// becoming the same, or a parent no longer changed.
			t.Fatalf("a rule turned %s into %s", describeNode(id, was), n.Status)
		})
		if len(idsOf(ruled)) > len(idsOf(plain)) {
			t.Fatalf("a rule grew the tree from %d to %d", len(idsOf(plain)), len(idsOf(ruled)))
		}
	})
}

// Comparing twice answers the same thing, and every node can be found again
// by the name it was walked under.
func FuzzATreeCanBeWalkedAndFoundAgain(f *testing.F) {
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, seed []byte) {
		a, b := twoOf(seed)
		first := Compare(a, b)
		if second := Compare(a, b); changedIn(first) != changedIn(second) {
			t.Fatalf("comparing twice found\n%s\nthen\n%s", changedIn(first), changedIn(second))
		}

		counted := Counts{}
		first.Walk(func(id string, n Node) {
			counted[n.Status]++
			found, ok := first.Find(id)
			if !ok {
				t.Fatalf("%s cannot be found again", describeNode(id, n))
			}
			if found.Name != n.Name || found.Kind != n.Kind || found.Status != n.Status {
				t.Fatalf("%s was found as %s", describeNode(id, n), describeNode(id, found))
			}
		})
		if !maps.Equal(counted, first.Count()) {
			t.Fatalf("it counted %v and a walk found %v", first.Count(), counted)
		}
	})
}

// sameKind allows the one kind that is meant to depend on which way round
// the comparison was made: a view's, which is what the target says it is.
func sameKind(a, b model.ObjectKind) bool {
	if a == b {
		return true
	}
	viewish := func(k model.ObjectKind) bool {
		return k == model.KindView || k == model.KindMaterializedView
	}
	return viewish(a) && viewish(b)
}
