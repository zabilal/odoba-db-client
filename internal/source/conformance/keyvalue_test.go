package conformance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
)

// The key-value checks, against a keyspace in memory with a fault put in it.
// Redis passes them, so without these a check that stopped checking would
// pass as well.

func TestTheKeyValueChecksPassAKeyspaceThatKeepsTheContract(t *testing.T) {
	for _, shape := range []string{"", "a key's time to live cannot be emptied",
		"a key that has gone is refused when planned", "counts only roughly", "no part is ever changed"} {
		f := newKVFake(shape)
		if found := run(f, f, f.under); found != "" {
			t.Errorf("%q: the checks found %s", shape, found)
		}
		// And they ran to the end: the last key and the one consented to are
		// deleted, and the first is all that is left.
		if len(f.keys) != 1 || f.keys["a"] == nil {
			t.Errorf("%q: the keyspace holds %v", shape, f.keys)
		}
	}

	// With no connection to open under a guard, the guard goes unchecked,
	// and the key it would have deleted is left.
	f := newKVFake("")
	if found := run(f, f, nil); found != "" || len(f.keys) != 2 {
		t.Errorf("without a guard: the checks found %q, and the keyspace holds %v", found, f.keys)
	}

	// Keys that open onto nothing have only themselves to be checked.
	f = newKVFake("")
	plain := struct {
		source.Source
		source.Countable
	}{f, f}
	if found := run(plain, f, f.under); found != "" || len(f.keys) != 1 {
		t.Errorf("keys that open onto nothing: the checks found %q, and the keyspace holds %v", found, f.keys)
	}
}

func TestTheKeyValueChecksFindEachFaultPutInAKeyspace(t *testing.T) {
	for fault, want := range map[string]string{
		// The keyspace, read.
		"the keyspace cannot be read":               "reading database:db0",
		"the keys are not told apart":               "the keys of database:db0 cannot be told apart",
		"a keyspace never written to is miscounted": "database:db0 counts 4 rows, and a browse of it reads 3",
		"a deleted key is still counted":            "database:db0 counts 3 rows, and a browse of it reads 2",
		"the keyspace takes a row":                  "a key planned as a new row of its keyspace",

		// What a key holds, read.
		"a key opens onto nothing":                       "names nothing to open",
		"a key opens onto its keyspace":                  "opens onto database:",
		"a key opens onto a kind not declared":           "opens onto table:",
		"parts are not told apart":                       "holds rows of columns",
		"parts are told apart by what they have not got": "which they have not got",
		"a row is short":                                 "values for 2 columns",
		"a key is miscounted":                            "key:db0.a counts 3 rows, and a browse of it reads 2",
		"a key cannot be counted":                        ": cannot count",

		// What a key holds, written.
		"a part not there is written":                          "a change to a part that is not there, in key:db0.a",
		"a failure names no change":                            "a change to a part that is not there, in key:db0.a",
		"a position past 32 bits is another":                   "a change to a part that is not there, in key:db0.b",
		"a part not there is refused, and written":             "a change to a part that is not there wrote to",
		"a change writes another value":                        "where conformance was written",
		"a change writes the rest":                             "changed the rest",
		"the outcome of update part fails, counted as applied": "changing value of",
		"the outcome of update part counts none":               "changing value of",
		"a plan cannot be applied":                             "Apply: cannot apply",
		"a part added is lost":                                 "after adding a part to",
		"a part added writes another part too":                 "after adding a part to",
		"a part added is written wrong":                        "the part added to",
		"a part added writes the rest":                         "adding a part to key:db0.a changed the rest",
		"the outcome of insert part fails, counted as applied": "adding a part to key:db0.a: &{",
		"the outcome of insert part counts none":               "adding a part to key:db0.a: &{",
		"a part is added over another":                         "a part added under a key that is there",
		"taking a part away takes more":                        "after taking away the part",
		"the outcome of delete part fails, counted as applied": "of key:db0.a: &{",
		"the outcome of delete part counts none":               "of key:db0.a: &{",
		"a refused change writes":                              "taking a part away was refused and still wrote",
		"a part's change is refused, and written":              "changing value was refused and still wrote",
		"a part added is refused, and written":                 "adding a part was refused and still wrote",
		"a part added reads as another twice":                  "after adding a part to",
		"a part not there is written, said to fail":            "a change to a part that is not there, in key:db0.a",
		"a key's deletion is refused":                          "deleting the key [c]: change 1: refused",
		"every change to a part is refused":                    "the target needs one",

		// A key itself, written.
		"a key's change is refused":                           "setting ttl of [a] to 1h: change 1: refused",
		"the outcome of update key fails, counted as applied": "setting ttl of [a] to 1h: <nil> &{",
		"the outcome of update key counts none":               "setting ttl of [a] to 1h: <nil> &{",
		"a time to live is written longer":                    "where 1h was written",
		"a time to live is not set":                           "where 1h was written",
		"a time to live reads as nothing left":                "where 1h was written",
		"a time to live is not taken away":                    "where <nil> was written",
		"a key's change writes other keys":                    "changed other keys",
		"the outcome of delete key fails, counted as applied": "deleting the key [c]: <nil> &{",
		"the outcome of delete key counts none":               "deleting the key [c]: <nil> &{",
		"a deleted key is still listed":                       "after deleting [c]",
		"deleting a key changes another":                      "after deleting [c]",
		"a deleted key still holds what it held":              "still holds",

		// A plan.
		"a key that has gone is written":        "is to a key that has gone",
		"a failure is not said":                 "is to a key that has gone",
		"a plan fails at another change":        "is to a key that has gone",
		"a failure counts every change applied": "is to a key that has gone",
		"a plan says it was undone":             "is to a key that has gone",
		"a plan promises a transaction":         "a plan that says atomic true",
		"a plan leaves a change out":            "a plan of 1 statements and 2 descriptions for 2 changes",
		"a plan leaves a line out":              "a plan of 2 statements and 1 descriptions for 2 changes",

		// The guard.
		"a read-only plan is refused":              "Plan: change 1: read-only",
		"a read-only connection writes":            "a read-only connection writes nothing",
		"a production plan is refused":             "Plan: change 1: production",
		"a production plan is not guarded":         "a production plan is guarded",
		"production writes without consent":        "production without consent: <nil>",
		"a key is deleted without consent":         "was deleted before anybody consented",
		"a consented plan is refused when planned": "Plan: change 1: consented",
		"a consented plan is refused":              "production with consent: refused",
		"a consented plan fails":                   "production with consent: <nil> &{",
		"a consented plan writes nothing":          "the key [b] consented to is still there",
	} {
		f := newKVFake(fault)
		if found := run(f, f, f.under); !strings.Contains(found, want) {
			t.Errorf("%s: the checks found %q, and nothing about %q", fault, found, want)
		}
	}
}

func TestTheKeyValueChecksNeedThreeKeys(t *testing.T) {
	f := newKVFake("")
	delete(f.keys, "c")
	if found := run(f, f, f.under); !strings.Contains(found, "holds 2 keys, and the checks need three") {
		t.Errorf("two keys: %q", found)
	}
}

func TestAValueReadBackIsTheValueWritten(t *testing.T) {
	for _, c := range []struct {
		got, written any
		reads        bool
	}{
		{nil, nil, true},
		{"x", nil, false},
		{nil, "x", false},
		{"x", "x", true},
		{"x", "y", false},
		{2.5, 2.5, true},
		{59 * time.Minute, "1h", true},
		{time.Hour, "1h", true},
		{time.Hour + time.Second, "1h", false},
		{time.Duration(0), "1h", false},
		{model.JSON(`{"a": 1, "b": [true]}`), model.JSON(`{"b":[true],"a":1}`), true},
		{model.JSON(`{"a": 1}`), model.JSON(`{"a": 2}`), false},
		{model.JSON(`not json`), model.JSON(`not json`), true},
		{model.JSON(`not json`), model.JSON(`nor this`), false},
	} {
		if got := reads(c.got, c.written); got != c.reads {
			t.Errorf("reads(%v, %v) = %v", c.got, c.written, got)
		}
	}
}

// recorder is a reporter that keeps what the checks find. A fatal finding
// stops the checks, as it stops a test.
type recorder struct{ found []string }

type stopped struct{}

func (r *recorder) Helper()             {}
func (r *recorder) Logf(string, ...any) {}
func (r *recorder) Error(args ...any)   { r.found = append(r.found, fmt.Sprint(args...)) }
func (r *recorder) Errorf(format string, args ...any) {
	r.found = append(r.found, fmt.Sprintf(format, args...))
}
func (r *recorder) Fatal(args ...any) { r.Error(args...); panic(stopped{}) }
func (r *recorder) Fatalf(format string, args ...any) {
	r.Errorf(format, args...)
	panic(stopped{})
}

// run runs the key-value checks against a keyspace, and says what they found.
func run(src source.Source, w source.Writer, guarded func(source.Guard) source.Source) (found string) {
	r := &recorder{}
	defer func() {
		if p := recover(); p != nil {
			if _, ok := p.(stopped); !ok {
				panic(p)
			}
		}
		found = strings.Join(r.found, "; ")
	}()
	keyValueChecks(r, src, w, model.NewRef(model.KindDatabase, "db0"), guarded)
	return ""
}

// kvFake is a keyspace in memory, and fault names what it gets wrong.
//
// Key a holds fields, as a hash does. Key b holds elements, as a list does,
// which are added at the end, known by a position that is not theirs to
// change, and not taken away by it; it has a time left to live, which counts
// down as it is read. Key c holds fields with a flag beside each, of a class
// nothing is typed into. A key's kind is read beside it, and is not written.
// What a key holds is read in no order: backwards, every other time.
type kvFake struct {
	keys  map[string]*fakeKey
	gone  map[string]*fakeKey // keys deleted, which a fault goes on reading
	guard source.Guard
	fault string
	reads int
}

type fakeKey struct {
	ttl    time.Duration
	fields map[string]string
	list   []string // a list's elements, where it is one
	twice  bool     // its first row read twice over
}

func newKVFake(fault string) *kvFake {
	return &kvFake{fault: fault, gone: map[string]*fakeKey{}, keys: map[string]*fakeKey{
		"a": {fields: map[string]string{"x": "1", "y": "2"}},
		"b": {ttl: time.Hour, list: []string{"p", "q"}},
		"c": {fields: map[string]string{"z": "4"}},
	}}
}

// under is a connection to the same keyspace under a guard.
func (f *kvFake) under(g source.Guard) source.Source {
	c := *f
	c.guard = g
	return &c
}

func (f *kvFake) Capabilities() capability.Capabilities {
	return capability.Capabilities{Paradigm: model.ParadigmKeyValue,
		Data: capability.Data{Insert: true, Update: true, Delete: true, RowObjects: true,
			ExactCount: f.fault != "counts only roughly"},
		Objects: map[model.ObjectKind]bool{model.KindDatabase: true, model.KindKey: true}}
}

func (f *kvFake) Info(context.Context) (source.ServerInfo, error) {
	return source.ServerInfo{Product: "fake"}, nil
}
func (f *kvFake) Ping(context.Context) error                                      { return nil }
func (f *kvFake) Close() error                                                    { return nil }
func (f *kvFake) Root(context.Context) ([]model.Node, error)                      { return nil, nil }
func (f *kvFake) Children(context.Context, model.ObjectRef) ([]model.Node, error) { return nil, nil }
func (f *kvFake) Describe(context.Context, model.ObjectRef) (any, error)          { return nil, nil }
func (f *kvFake) Badge(context.Context, model.ObjectRef) (model.Badge, bool, error) {
	return model.Badge{}, false, nil
}

func (f *kvFake) Browse(_ context.Context, ref model.ObjectRef, _ source.BrowseOptions) (model.RowStream, error) {
	text := model.DataType{Class: model.TypeString}
	if ref.Kind == model.KindDatabase {
		if f.fault == "the keyspace cannot be read" {
			return nil, errors.New("no keyspace")
		}
		out := &fakeRows{id: model.RowIdentity{Kind: model.IdentityKeyName, Columns: []string{"key"}, Target: ref},
			cols: []model.ColumnDef{{Name: "key", Type: text}, {Name: "kind", Type: text, ReadOnly: true}, {Name: "ttl",
				Type: model.DataType{Class: model.TypeInterval, Nullable: f.fault != "a key's time to live cannot be emptied"}}}}
		if f.fault == "the keys are not told apart" {
			out.id.Kind = model.IdentityNone
		}
		for _, name := range sortedKeys(f.keys) {
			k := f.keys[name]
			if k.ttl > time.Second {
				k.ttl -= time.Second
			}
			var left any
			switch {
			case k.ttl > 0 && f.fault == "a time to live reads as nothing left":
				left = time.Duration(0)
			case k.ttl > 0:
				left = k.ttl
			}
			kind := "hash"
			if k.list != nil {
				kind = "list"
			}
			out.rows = append(out.rows, model.Row{name, kind, left})
		}
		return out, nil
	}

	name := ref.Path[1]
	k := f.keys[name]
	if k == nil && f.fault == "a deleted key still holds what it held" {
		k = f.gone[name]
	}
	if k == nil {
		return nil, fmt.Errorf("no key called %s", name)
	}
	out := &fakeRows{id: model.RowIdentity{Kind: model.IdentityKeyName, Columns: []string{"field"}, Target: ref}}
	switch {
	case k.list != nil:
		out.id.Columns = []string{"index"}
		out.cols = []model.ColumnDef{{Name: "index", Type: model.DataType{Class: model.TypeInteger}, ReadOnly: true},
			{Name: "value", Type: text}}
		for i, v := range k.list {
			out.rows = append(out.rows, model.Row{int64(i), v})
		}
	case name == "c":
		out.cols = []model.ColumnDef{{Name: "field", Type: text}, {Name: "flag", Type: model.DataType{Class: model.TypeBool}},
			{Name: "value", Type: text}}
		for _, field := range sortedKeys(k.fields) {
			out.rows = append(out.rows, model.Row{field, false, k.fields[field]})
		}
	default:
		out.cols = []model.ColumnDef{{Name: "field", Type: text}, {Name: "value", Type: text}}
		for _, field := range sortedKeys(k.fields) {
			out.rows = append(out.rows, model.Row{field, k.fields[field]})
		}
	}
	if k.twice && len(out.rows) > 0 {
		out.rows = append(out.rows, out.rows[0])
	}
	switch f.fault {
	case "parts are not told apart":
		out.id.Kind = model.IdentityNone
	case "parts are told apart by what they have not got":
		out.id.Columns = []string{"member"}
	case "a row is short":
		for i := range out.rows {
			out.rows[i] = out.rows[i][:1]
		}
	}
	if f.reads++; f.reads%2 == 0 {
		slices.Reverse(out.rows)
	}
	return out, nil
}

func (f *kvFake) Count(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (int64, error) {
	rs, err := f.Browse(ctx, ref, opt)
	if err != nil {
		return 0, err
	}
	n := int64(len(rs.(*fakeRows).rows))
	keyspace := ref.Kind == model.KindDatabase
	switch {
	case f.fault == "counts only roughly",
		f.fault == "a key is miscounted" && !keyspace,
		f.fault == "a keyspace never written to is miscounted" && keyspace && len(f.gone) == 0:
		n++
	case f.fault == "a deleted key is still counted" && keyspace:
		n += int64(len(f.gone))
	case f.fault == "a key cannot be counted" && !keyspace:
		return n, errors.New("cannot count")
	}
	return n, nil
}

func (f *kvFake) ObjectOf(ref model.ObjectRef, _ []model.ColumnDef, row model.Row) (model.ObjectRef, bool) {
	switch f.fault {
	case "a key opens onto nothing":
		return model.ObjectRef{}, false
	case "a key opens onto its keyspace":
		return ref, true
	case "a key opens onto a kind not declared":
		return model.NewRef(model.KindTable, ref.Path[0], fmt.Sprint(row[0])), true
	}
	return model.NewRef(model.KindKey, ref.Path[0], fmt.Sprint(row[0])), true
}

func (f *kvFake) Plan(_ context.Context, cs source.Changeset) (*source.WritePlan, error) {
	production := f.guard.Environment == source.EnvProduction
	switch {
	case f.fault == "a read-only plan is refused" && f.guard.ReadOnly:
		return nil, errors.New("change 1: read-only")
	case f.fault == "a production plan is refused" && production && !cs.Confirmed:
		return nil, errors.New("change 1: production")
	case f.fault == "a consented plan is refused when planned" && production && cs.Confirmed:
		return nil, errors.New("change 1: consented")
	}
	plan := &source.WritePlan{Target: cs.Target, Atomic: f.fault == "a plan promises a transaction",
		Guarded: f.fault != "a production plan is not guarded" && f.guard.RequiresConfirmation(source.AccessWrite)}
	for i, c := range cs.Changes {
		var run func() (int64, error)
		var err error
		what := changeWord(c) + " part"
		switch k := f.keys[cs.Target.Path[len(cs.Target.Path)-1]]; {
		case cs.Target.Kind == model.KindDatabase:
			run, err, what = f.keyChange(c), nil, changeWord(c)+" key"
			if run == nil {
				err = f.keyRefusal(c)
			}
		case k == nil:
			err = errors.New("no such key")
		default:
			run, err = f.partChange(k, c)
		}
		if err != nil {
			return nil, fmt.Errorf("change %d: %w", i+1, err)
		}
		if !(f.fault == "a plan leaves a change out" && i > 0) {
			plan.Statements = append(plan.Statements, source.Statement{SQL: what, Op: run, Confirmed: cs.Confirmed})
		}
		if !(f.fault == "a plan leaves a line out" && i > 0) {
			plan.Descriptions = append(plan.Descriptions, what)
		}
	}
	return plan, nil
}

func changeWord(c source.RowChange) string {
	switch c.Kind {
	case source.ChangeInsert:
		return "insert"
	case source.ChangeDelete:
		return "delete"
	}
	return "update"
}

func (f *kvFake) Apply(_ context.Context, plan *source.WritePlan) (*source.WriteOutcome, error) {
	guard := f.guard
	confirmed := len(plan.Statements) > 0 && plan.Statements[0].Confirmed
	switch {
	case f.fault == "a read-only connection writes":
		guard.ReadOnly = false
	case f.fault == "production writes without consent":
		guard = source.Guard{ReadOnly: guard.ReadOnly}
	case f.fault == "a key is deleted without consent" && plan.Guarded && !confirmed:
		delete(f.keys, "b")
	case f.fault == "a plan cannot be applied" && plan.Target.Kind == model.KindKey:
		return nil, errors.New("cannot apply")
	}
	if err := sqlscript.AllowWrites(guard, plan); err != nil {
		return nil, err
	}
	if plan.Guarded && confirmed {
		switch f.fault {
		case "a consented plan is refused":
			return nil, errors.New("refused")
		case "a consented plan fails":
			return &source.WriteOutcome{Err: errors.New("failed")}, nil
		case "a consented plan writes nothing":
			return &source.WriteOutcome{FailedAt: -1, Applied: len(plan.Statements)}, nil
		}
	}
	if len(plan.Statements) > 0 && f.fault == "the outcome of "+plan.Statements[0].SQL+" fails, counted as applied" {
		return &source.WriteOutcome{Applied: len(plan.Statements), Err: errors.New("failed")}, nil
	}
	out := sqlscript.ApplyWith(plan, func(st source.Statement) (int64, error) {
		return st.Op.(func() (int64, error))()
	}, func() error { return nil }, func() error {
		if f.fault == "a plan says it was undone" {
			return nil
		}
		return errors.New("nothing here is undone")
	})
	switch {
	case out.Err == nil && len(plan.Statements) > 0 && f.fault == "the outcome of "+plan.Statements[0].SQL+" counts none":
		out.Applied = 0
	case out.Err == nil && f.fault == "a part not there is written, said to fail":
		out.FailedAt = 0
	case out.Err != nil && f.fault == "a failure names no change":
		out.FailedAt = -1
	case out.Err != nil && f.fault == "a failure is not said":
		out.Err = nil
	case out.Err != nil && len(plan.Statements) > 1 && f.fault == "a plan fails at another change":
		out.FailedAt = 0
	case out.Err != nil && f.fault == "a failure counts every change applied":
		out.Applied = len(plan.Statements)
	}
	return out, nil
}

// keyRefusal is why a change to a key itself is refused when it is planned.
func (f *kvFake) keyRefusal(c source.RowChange) error {
	switch {
	case c.Kind == source.ChangeInsert:
		return errors.New("a key is written into being")
	case f.fault == "a key's change is refused", f.fault == "a key's deletion is refused":
		return errors.New("refused")
	case f.fault == "a key's time to live cannot be emptied":
		return errors.New("a time to live cannot be emptied")
	}
	return fmt.Errorf("no key called %v", c.Key[0])
}

// keyChange is a change to a key itself — its time to live, or whether it is
// there — or nil where it is refused when planned.
func (f *kvFake) keyChange(c source.RowChange) func() (int64, error) {
	if c.Kind == source.ChangeInsert {
		if f.fault != "the keyspace takes a row" {
			return nil
		}
		return func() (int64, error) { return 1, nil }
	}
	name := fmt.Sprint(c.Key[0])
	if f.keys[name] == nil && f.fault == "a key that has gone is refused when planned" {
		return nil
	}
	if c.Kind == source.ChangeDelete {
		if f.fault == "a key's deletion is refused" {
			return nil
		}
		return func() (int64, error) {
			k := f.keys[name]
			if k == nil {
				return f.gonePart(), nil
			}
			if f.fault != "a deleted key is still listed" {
				delete(f.keys, name)
			}
			if f.fault == "deleting a key changes another" {
				for _, other := range f.keys {
					other.ttl = 0
				}
			}
			f.gone[name] = k
			return 1, nil
		}
	}
	if f.fault == "a key's change is refused" {
		return nil
	}
	var ttl time.Duration
	if v := c.Values["ttl"]; v != nil {
		ttl, _ = time.ParseDuration(fmt.Sprint(v))
	} else if f.fault == "a key's time to live cannot be emptied" {
		return nil
	}
	if f.fault == "a time to live is written longer" {
		ttl *= 2
	}
	return func() (int64, error) {
		switch k := f.keys[name]; {
		case k == nil:
			return f.gonePart(), nil
		case ttl == 0 && f.fault == "a time to live is not taken away",
			ttl > 0 && f.fault == "a time to live is not set":
			return 1, nil
		}
		for other, k := range f.keys {
			if other == name || f.fault == "a key's change writes other keys" {
				k.ttl = ttl
			}
		}
		return 1, nil
	}
}

// gonePart is how many rows a change to something that is not there wrote:
// none, or one where that is the fault.
func (f *kvFake) gonePart() int64 {
	if f.fault == "a key that has gone is written" {
		return 1
	}
	return 0
}

// partChange is a change to what a key holds.
func (f *kvFake) partChange(k *fakeKey, c source.RowChange) (func() (int64, error), error) {
	if f.fault == "every change to a part is refused" || c.Kind == source.ChangeUpdate && f.fault == "no part is ever changed" {
		return nil, errors.New("refused")
	}
	if k.list != nil {
		return f.elementChange(k, c)
	}
	switch c.Kind {
	case source.ChangeInsert:
		field, value := fmt.Sprint(c.Values["field"]), fmt.Sprint(c.Values["value"])
		if f.fault == "a part added is refused, and written" {
			k.fields[field] = value
			return nil, errors.New("refused")
		}
		return func() (int64, error) {
			if _, there := k.fields[field]; there && f.fault != "a part is added over another" {
				return 0, fmt.Errorf("%s is there already", field)
			}
			switch f.fault {
			case "a part added is lost":
			case "a part added reads as another twice":
				k.twice = true
			case "a part added is written wrong":
				k.fields[field] = ""
			case "a part added writes another part too":
				k.fields[field], k.fields[field+" too"] = value, value
			case "a part added writes the rest":
				for other := range k.fields {
					k.fields[other] = value
				}
				k.fields[field] = value
			default:
				k.fields[field] = value
			}
			return 1, nil
		}, nil
	case source.ChangeUpdate:
		field, value := fmt.Sprint(c.Key[0]), fmt.Sprint(c.Values["value"])
		switch _, there := k.fields[field]; {
		case !there && f.fault == "a part not there is refused, and written",
			there && f.fault == "a part's change is refused, and written":
			k.fields[field] = value
			return nil, errors.New("refused")
		}
		return func() (int64, error) {
			_, there := k.fields[field]
			switch {
			case !there && f.fault != "a part not there is written" && f.fault != "a part not there is written, said to fail":
				return 0, nil
			case f.fault == "a change writes another value":
				value += "!"
			case f.fault == "a change writes the rest":
				for other := range k.fields {
					k.fields[other] = value
				}
			}
			k.fields[field] = value
			return 1, nil
		}, nil
	}
	field := fmt.Sprint(c.Key[0])
	return func() (int64, error) {
		if _, there := k.fields[field]; !there {
			return 0, nil
		}
		delete(k.fields, field)
		if f.fault == "taking a part away takes more" {
			for other := range k.fields {
				delete(k.fields, other)
				break
			}
		}
		return 1, nil
	}, nil
}

// elementChange is a change to a list's elements.
func (f *kvFake) elementChange(k *fakeKey, c source.RowChange) (func() (int64, error), error) {
	switch c.Kind {
	case source.ChangeInsert:
		value := fmt.Sprint(c.Values["value"])
		return func() (int64, error) {
			k.list = append(k.list, value)
			return 1, nil
		}, nil
	case source.ChangeUpdate:
		at, ok := c.Key[0].(int64)
		if !ok {
			return nil, fmt.Errorf("%v is not a position", c.Key[0])
		}
		value := fmt.Sprint(c.Values["value"])
		return func() (int64, error) {
			if f.fault == "a position past 32 bits is another" {
				at %= 1 << 32
			}
			if at < 0 || at >= int64(len(k.list)) {
				return 0, nil
			}
			k.list[at] = value
			return 1, nil
		}, nil
	}
	if f.fault == "a refused change writes" {
		k.list = k.list[:len(k.list)-1]
	}
	return nil, errors.New("an element is not taken away by its position")
}

// fakeRows streams rows read, and says how they are told apart.
type fakeRows struct {
	cols []model.ColumnDef
	rows []model.Row
	id   model.RowIdentity
	at   int
}

func (r *fakeRows) Columns() []model.ColumnDef  { return r.cols }
func (r *fakeRows) Identity() model.RowIdentity { return r.id }
func (r *fakeRows) Close() error                { return nil }
func (r *fakeRows) Next(context.Context) (model.Row, error) {
	if r.at == len(r.rows) {
		return nil, io.EOF
	}
	r.at++
	return r.rows[r.at-1], nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
