package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
	"github.com/ikigai-db/ikigai-db/internal/value"
)

// checkKeyValueWriter writes to a store whose rows are keys (FR-12.2).
//
// A keyspace is rows twice over: its keys, and what each key holds, which
// opens as rows of its own (source.RowObject). The suite cannot make a key —
// one comes into being when something is written to it, and a row of a
// keyspace is where a value is rather than the value — so the target fills
// the Writable keyspace, and the checks change and delete what is there.
//
// A kind of value takes only some changes: a log is added to and never
// rewritten, and a string is one value with no part to add. So each change
// here is either refused when it is planned, with nothing sent, or does just
// what it says when it is applied. Beyond that the checks assume only the
// contract: rows told apart by the identity their stream reports, a change
// that writes what it names and nothing else, and a change to what is not
// there that writes nothing.
func checkKeyValueWriter(t *testing.T, target Target, src source.Source, w source.Writer) {
	var guarded func(source.Guard) source.Source
	if target.OpenGuarded != nil {
		guarded = func(g source.Guard) source.Source { return target.OpenGuarded(context.Background(), t, g) }
	}
	keyValueChecks(t, src, w, target.Writable, guarded)
}

// reporter is what the key-value checks say what they find through: a test's
// own *testing.T, or a recorder when the checks are themselves checked
// against a keyspace built to break them.
type reporter interface {
	Helper()
	Error(args ...any)
	Errorf(format string, args ...any)
	Fatal(args ...any)
	Fatalf(format string, args ...any)
	Logf(format string, args ...any)
}

// keyValueChecks are the checks themselves, over the keyspace ref. guarded
// opens a connection under a guard, and nil leaves the guard unchecked.
func keyValueChecks(t reporter, src source.Source, w source.Writer, ref model.ObjectRef, guarded func(source.Guard) source.Source) {
	ctx := context.Background()
	k := &kvChecks{ctx: ctx, src: src, w: w, caps: src.Capabilities()}

	keys := k.read(t, ref)
	if !keys.id.Editable() {
		t.Fatalf("the keys of %s cannot be told apart: %+v", ref, keys.id)
	}
	if len(keys.rows) < 3 {
		t.Fatalf("%s holds %d keys, and the checks need three the target wrote", ref, len(keys.rows))
	}
	k.count(t, ref, keys)

	// A key is not added as a row of its keyspace: the row would hold
	// nothing, and a key comes into being when something is written to it.
	added := source.RowChange{Kind: source.ChangeInsert, Values: map[string]any{keys.id.Columns[0]: "conformance:added"}}
	if _, err := k.plan(t, w, ref, keys.id, false, added); err == nil {
		t.Error("a key planned as a new row of its keyspace, where it would hold nothing")
	}

	ro, objects := src.(source.RowObject)
	if objects {
		k.held(t, ro, ref, keys)
	}

	// A change to a key itself writes what it names, and leaves the other
	// keys alone: given a value, the column reads as one; emptied, it reads
	// as empty.
	keys = k.read(t, ref)
	first := keys.key(keys.rows[0])
	col := keys.changeable()
	nullable := col >= 0 && keys.cols[col].Type.Nullable
	if nullable {
		c := keys.cols[col]
		for _, to := range []any{sample(c), nil} {
			out, err := k.apply(t, ref, keys.id, source.RowChange{Kind: source.ChangeUpdate, Key: first,
				Values: map[string]any{c.Name: to}})
			if err != nil || out.Err != nil || out.Applied != 1 {
				t.Errorf("setting %s of %v to %v: %v %+v", c.Name, first, to, err, out)
				continue
			}
			now := k.read(t, ref)
			if row := now.find(first); row == nil || !reads(row[col], to) {
				t.Errorf("%s of %v reads %v, where %v was written", c.Name, first, row, to)
			}
			if now.without(first) != keys.without(first) {
				t.Errorf("changing %v changed other keys: %s, where there were %s", first, now.without(first), keys.without(first))
			}
		}
	}

	// A key deleted is gone, and so is what it held, and no other key with
	// it.
	keys = k.read(t, ref)
	lastRow := keys.rows[len(keys.rows)-1]
	last := keys.key(lastRow)
	out, err := k.apply(t, ref, keys.id, source.RowChange{Kind: source.ChangeDelete, Key: last})
	if err != nil || out.Err != nil || out.Applied != 1 {
		t.Fatalf("deleting the key %v: %v %+v", last, err, out)
	}
	now := k.read(t, ref)
	if now.String() != keys.without(last) {
		t.Errorf("after deleting %v the keys are %s, where they were %s", last, now, keys)
	}
	k.count(t, ref, now)
	if objects {
		if obj, ok := ro.ObjectOf(ref, keys.cols, lastRow); ok {
			if held, _ := k.tryRead(obj); len(held.rows) > 0 {
				t.Errorf("the key %v was deleted, and %s still holds %s", last, obj, held)
			}
		}
	}

	// A change to a key that has gone fails and says which change it was;
	// the changes before it are undone only where the source claims
	// transactions (FR-4.5).
	changes := []source.RowChange{{Kind: source.ChangeDelete, Key: last}}
	if nullable {
		changes = append([]source.RowChange{{Kind: source.ChangeUpdate, Key: first,
			Values: map[string]any{keys.cols[col].Name: nil}}}, changes...)
	}
	at := len(changes) - 1
	if out, err := k.apply(t, ref, keys.id, changes...); err == nil &&
		(out.Err == nil || out.FailedAt != at || out.Applied != at || out.RolledBack != k.caps.Data.TransactionalWrite) {
		t.Errorf("a plan whose change %d is to a key that has gone: %+v", at, out)
	}

	// The guard is asked before anything is written.
	if guarded == nil {
		return
	}
	keys = now
	victim := keys.key(keys.rows[1])
	del := source.RowChange{Kind: source.ChangeDelete, Key: victim}
	there := func() bool { return k.read(t, ref).find(victim) != nil }

	readOnly := guarded(source.Guard{ReadOnly: true})
	defer readOnly.Close()
	rw := readOnly.(source.Writer)
	plan, err := k.plan(t, rw, ref, keys.id, true, del)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if _, err := rw.Apply(ctx, plan); !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("a read-only connection writes nothing: %v", err)
	}
	prod := guarded(source.Guard{Environment: source.EnvProduction})
	defer prod.Close()
	pw := prod.(source.Writer)
	if plan, err = k.plan(t, pw, ref, keys.id, false, del); err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !plan.Guarded {
		t.Error("a production plan is guarded")
	}
	if _, err := pw.Apply(ctx, plan); !errors.Is(err, source.ErrConfirmationRequired) {
		t.Errorf("production without consent: %v", err)
	}
	if !there() {
		t.Fatalf("the key %v was deleted before anybody consented", victim)
	}
	if plan, err = k.plan(t, pw, ref, keys.id, true, del); err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if out, err := pw.Apply(ctx, plan); err != nil || out.Err != nil {
		t.Errorf("production with consent: %v %+v", err, out)
	}
	if there() {
		t.Errorf("the key %v consented to is still there", victim)
	}
}

// kvChecks is what the key-value checks share.
type kvChecks struct {
	ctx  context.Context
	src  source.Source
	w    source.Writer
	caps capability.Capabilities
}

// held checks what each key holds, opened as rows of its own (FR-12.2).
func (k *kvChecks) held(t reporter, ro source.RowObject, ref model.ObjectRef, keys rowsRead) {
	t.Helper()
	wrote := false
	for _, row := range keys.rows {
		obj, ok := ro.ObjectOf(ref, keys.cols, row)
		if !ok {
			t.Errorf("the key %v names nothing to open", keys.key(row))
			continue
		}
		if !k.caps.Supports(obj.Kind) || obj.Equal(ref) {
			t.Errorf("the key %v opens onto %v", keys.key(row), obj)
			continue
		}
		wrote = k.value(t, obj) || wrote
	}
	if !wrote {
		t.Errorf("no key of %s holds a value a change was written to; the target needs one", ref)
	}
}

// value checks what one key holds: its rows told apart and counted as they
// read, a change to a part that is not there writing nothing, a change
// writing what it names, and a part added and taken away again. It says
// whether a change was written, which a kind may refuse every one of.
func (k *kvChecks) value(t reporter, obj model.ObjectRef) bool {
	t.Helper()
	before := k.read(t, obj)
	if len(before.cols) == 0 || !before.id.Editable() {
		t.Errorf("%s holds rows of columns %+v, told apart by %+v", obj, before.cols, before.id)
		return false
	}
	for _, c := range before.id.Columns {
		if before.column(c) < 0 {
			t.Errorf("the rows of %s are told apart by %s, which they have not got", obj, c)
			return false
		}
	}
	for _, row := range before.rows {
		if len(row) != len(before.cols) {
			t.Errorf("a row of %s has %d values for %d columns", obj, len(row), len(before.cols))
			return false
		}
	}
	k.count(t, obj, before)
	col := before.changeable()

	// A change to a part that is not there writes nothing.
	gone := source.RowChange{Kind: source.ChangeDelete, Key: before.absent()}
	if col >= 0 {
		gone = source.RowChange{Kind: source.ChangeUpdate, Key: before.absent(),
			Values: map[string]any{before.cols[col].Name: sample(before.cols[col])}}
	}
	k.writesNothing(t, obj, before, "a change to a part that is not there", before.id, gone)

	// A change writes what it names, and leaves the rest alone.
	wrote := false
	if col >= 0 && len(before.rows) > 0 {
		c, v := before.cols[col], sample(before.cols[col])
		key := before.key(before.rows[0])
		out, err := k.apply(t, obj, before.id, source.RowChange{Kind: source.ChangeUpdate, Key: key,
			Values: map[string]any{c.Name: v}})
		switch {
		case err != nil:
			k.refused(t, obj, before, "changing "+c.Name, err)
		case out.Err != nil || out.Applied != 1:
			t.Errorf("changing %s of %v in %s: %+v", c.Name, key, obj, out)
		default:
			wrote = true
			now := k.read(t, obj)
			if row := now.find(key); row == nil || !reads(row[col], v) {
				t.Errorf("%s of %v in %s reads %v, where %v was written", c.Name, key, obj, row, v)
			}
			if now.without(key) != before.without(key) {
				t.Errorf("changing %v in %s changed the rest: %s, where it held %s", key, obj, now.without(key), before.without(key))
			}
		}
	}

	// A part is added with what it is given, and needs no key to be. A
	// column nothing is typed into is left out, as a cell nobody typed in is.
	before = k.read(t, obj)
	values := map[string]any{}
	for _, c := range before.cols {
		if v := sample(c); v != nil && !c.ReadOnly {
			values[c.Name] = v
		}
	}
	out, err := k.apply(t, obj, model.RowIdentity{}, source.RowChange{Kind: source.ChangeInsert, Values: values})
	if err != nil {
		k.refused(t, obj, before, "adding a part", err)
		return wrote
	}
	if out.Err != nil || out.Applied != 1 {
		t.Errorf("adding a part to %s: %+v", obj, out)
		return wrote
	}
	after := k.read(t, obj)
	var part model.Row
	for _, row := range after.rows {
		if before.find(after.key(row)) == nil {
			part = row
		}
	}
	if part == nil || len(after.rows) != len(before.rows)+1 {
		t.Errorf("after adding a part to %s it holds %s, where it held %s", obj, after, before)
		return wrote
	}
	for name, v := range values {
		if !reads(part[after.column(name)], v) {
			t.Errorf("the part added to %s has %s %v, where %v was given", obj, name, part[after.column(name)], v)
		}
	}
	added := after.key(part)
	if after.without(added) != before.String() {
		t.Errorf("adding a part to %s changed the rest: %s, where it held %s", obj, after.without(added), before)
	}

	// One added under the key of a part that is there is refused, rather
	// than written over it.
	if len(before.rows) > 0 && !slices.ContainsFunc(before.id.Columns, func(c string) bool { _, ok := values[c]; return !ok }) {
		twice := maps.Clone(values)
		for i, c := range before.id.Columns {
			twice[c] = before.key(before.rows[0])[i]
		}
		k.writesNothing(t, obj, after, "a part added under a key that is there", model.RowIdentity{},
			source.RowChange{Kind: source.ChangeInsert, Values: twice})
	}

	// Taken away again, it takes nothing else with it.
	out, err = k.apply(t, obj, after.id, source.RowChange{Kind: source.ChangeDelete, Key: added})
	switch {
	case err != nil:
		k.refused(t, obj, after, "taking a part away", err)
	case out.Err != nil || out.Applied != 1:
		t.Errorf("taking away the part %v of %s: %+v", added, obj, out)
	default:
		if now := k.read(t, obj); now.String() != before.String() {
			t.Errorf("after taking away the part %v, %s holds %s, where it held %s", added, obj, now, before)
		}
	}
	return true
}

// writesNothing plans and applies a change that must write nothing: it is
// refused when it is planned, or fails when it is applied and says it was
// the first change, and either way the object holds what it held.
func (k *kvChecks) writesNothing(t reporter, obj model.ObjectRef, held rowsRead, what string, id model.RowIdentity, c source.RowChange) {
	t.Helper()
	out, err := k.apply(t, obj, id, c)
	switch {
	case err != nil:
		t.Logf("%s, in %s, refused when planned: %v", what, obj, err)
	case out.Err == nil || out.FailedAt != 0:
		t.Errorf("%s, in %s: %+v", what, obj, out)
	}
	if now := k.read(t, obj); now.String() != held.String() {
		t.Errorf("%s wrote to %s: it holds %s, where it held %s", what, obj, now, held)
	}
}

// refused is a change a kind of value does not take, refused when it was
// planned. That is the kind's to decide, so it is logged rather than failed —
// a run says what each kind refused and why — but a refusal must have written
// nothing.
func (k *kvChecks) refused(t reporter, obj model.ObjectRef, held rowsRead, what string, err error) {
	t.Helper()
	t.Logf("%s, in %s, refused when planned: %v", what, obj, err)
	if now := k.read(t, obj); now.String() != held.String() {
		t.Errorf("%s was refused and still wrote to %s: it holds %s, where it held %s", what, obj, now, held)
	}
}

// plan plans changes to an object, and checks what any plan says: a
// statement and a line for each change, and the atomicity the source claims
// (FR-4.4, FR-4.5). A plan refused comes back as its error.
func (k *kvChecks) plan(t reporter, w source.Writer, ref model.ObjectRef, id model.RowIdentity, confirmed bool, changes ...source.RowChange) (*source.WritePlan, error) {
	t.Helper()
	plan, err := w.Plan(k.ctx, source.Changeset{Target: ref, Identity: id, Changes: changes, Confirmed: confirmed})
	if err != nil {
		return nil, err
	}
	if len(plan.Statements) != len(changes) || len(plan.Descriptions) != len(changes) {
		t.Fatalf("a plan of %d statements and %d descriptions for %d changes",
			len(plan.Statements), len(plan.Descriptions), len(changes))
	}
	if plan.Atomic != k.caps.Data.TransactionalWrite {
		t.Fatalf("a plan that says atomic %v, where the source claims TransactionalWrite %v",
			plan.Atomic, k.caps.Data.TransactionalWrite)
	}
	return plan, nil
}

// apply plans and applies changes to an object. A plan refused comes back as
// its error, with nothing applied.
func (k *kvChecks) apply(t reporter, ref model.ObjectRef, id model.RowIdentity, changes ...source.RowChange) (*source.WriteOutcome, error) {
	t.Helper()
	plan, err := k.plan(t, k.w, ref, id, false, changes...)
	if err != nil {
		return nil, err
	}
	out, err := k.w.Apply(k.ctx, plan)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return out, nil
}

// count checks that an object counts as the rows a browse of it reads, where
// the source claims to count exactly: the grid's pager is drawn from it.
func (k *kvChecks) count(t reporter, ref model.ObjectRef, read rowsRead) {
	t.Helper()
	c, ok := k.src.(source.Countable)
	if !ok || !k.caps.Data.ExactCount {
		return
	}
	if n, err := c.Count(k.ctx, ref, source.BrowseOptions{}); err != nil || n != int64(len(read.rows)) {
		t.Errorf("%s counts %d rows, and a browse of it reads %d: %v", ref, n, len(read.rows), err)
	}
}

// read is every row of an object, up to a thousand.
func (k *kvChecks) read(t reporter, ref model.ObjectRef) rowsRead {
	t.Helper()
	got, err := k.tryRead(ref)
	if err != nil {
		t.Fatalf("reading %s: %v", ref, err)
	}
	return got
}

func (k *kvChecks) tryRead(ref model.ObjectRef) (rowsRead, error) {
	rs, err := k.src.Browse(k.ctx, ref, source.BrowseOptions{Limit: 1000})
	if err != nil {
		return rowsRead{}, err
	}
	defer rs.Close()
	out := rowsRead{cols: rs.Columns(), id: model.IdentityOf(rs)}
	for {
		row, err := rs.Next(k.ctx)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		out.rows = append(out.rows, row)
	}
}

// rowsRead is an object's rows as a browse reads them, and how the browse
// says they are told apart.
type rowsRead struct {
	cols []model.ColumnDef
	id   model.RowIdentity
	rows []model.Row
}

func (r rowsRead) column(name string) int {
	return slices.IndexFunc(r.cols, func(c model.ColumnDef) bool { return c.Name == name })
}

// key is the values a row is told apart by.
func (r rowsRead) key(row model.Row) []any {
	out := make([]any, 0, len(r.id.Columns))
	for _, c := range r.id.Columns {
		if i := r.column(c); i >= 0 && i < len(row) {
			out = append(out, row[i])
		} else {
			out = append(out, nil)
		}
	}
	return out
}

// find is the row a key addresses, or nil.
func (r rowsRead) find(key []any) model.Row {
	for _, row := range r.rows {
		if fmt.Sprint(r.key(row)) == fmt.Sprint(key) {
			return row
		}
	}
	return nil
}

// without is the rows as text in a settled order, leaving out the one a key
// addresses: what a change to that row must leave as it was. A store walked
// in no order reads its rows in any.
func (r rowsRead) without(key []any) string {
	var out []string
	for _, row := range r.rows {
		if key != nil && fmt.Sprint(r.key(row)) == fmt.Sprint(key) {
			continue
		}
		parts := make([]string, len(row))
		for i, v := range row {
			parts[i] = valueText(v)
			if i < len(r.cols) && r.cols[i].Type.Class == model.TypeInterval {
				// A length of time left counts down between two reads of
				// it, so it reads as whether there is one.
				parts[i] = fmt.Sprint(v != nil)
			}
		}
		out = append(out, "["+strings.Join(parts, " ")+"]")
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

func (r rowsRead) String() string { return r.without(nil) }

// changeable is the first column a change is written to: not read-only, not
// what the rows are told apart by, and of a class the checks type into.
func (r rowsRead) changeable() int {
	for i, c := range r.cols {
		if c.ReadOnly || slices.Contains(r.id.Columns, c.Name) {
			continue
		}
		if _, ok := samples[c.Type.Class]; ok {
			return i
		}
	}
	return -1
}

// absent is a key no row has: a number where the rows are told apart by a
// number, and text otherwise. The number is past what 32 bits hold, which a
// server may read as a smaller one it has.
func (r rowsRead) absent() []any {
	out := make([]any, 0, len(r.id.Columns))
	for _, c := range r.id.Columns {
		if i := r.column(c); i >= 0 && r.cols[i].Type.Class == model.TypeInteger {
			out = append(out, int64(1)<<40)
		} else {
			out = append(out, "conformance: no such part")
		}
	}
	return out
}

// samples are the text the checks type into a column of each class they
// write.
var samples = map[model.TypeClass]string{
	model.TypeString: "conformance", model.TypeInteger: "7", model.TypeFloat: "2.5",
	model.TypeJSON: `{"conformance":"yes"}`, model.TypeInterval: "1h",
}

// sample is a column's sample text read as the grid reads what is typed into
// a cell (value.Parse), so that what a writer is handed is what a person's
// typing would hand it. Nil where the checks type nothing into a column of
// its class.
func sample(col model.ColumnDef) any {
	text, ok := samples[col.Type.Class]
	if !ok {
		return nil
	}
	v, _ := value.Parse(text, col, time.UTC)
	return v
}

// reads reports whether a value read back is the value written. A length of
// time is read back as more than nothing and at most what was written, since
// what is left of it counts down; JSON is compared by what it says rather
// than how it is spaced.
func reads(got, written any) bool {
	if written == nil || got == nil {
		return got == nil && written == nil
	}
	if d, ok := got.(time.Duration); ok {
		want, _ := time.ParseDuration(fmt.Sprint(written))
		return d > 0 && d <= want
	}
	return valueText(got) == valueText(written)
}

// valueText is a value as the checks compare it: JSON by what it says, and
// everything else as it prints.
func valueText(v any) string {
	if j, ok := v.(model.JSON); ok {
		var x any
		if json.Unmarshal(j, &x) == nil {
			if b, err := json.Marshal(x); err == nil {
				return string(b)
			}
		}
		return string(j)
	}
	return fmt.Sprint(v)
}
