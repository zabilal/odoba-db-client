package postgres

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Rendering a table's structure as PostgreSQL statements (FR-6.4, FR-6.7).
//
// Nothing here runs anything. Every method answers text, because the preview
// is the requirement: UX principle 6 and FR-6.4 say somebody reads the exact
// DDL before it is sent, and a generator that could also execute would make
// that a convention rather than a rule.
//
// The order statements come back in is the point of the whole file. DDL is
// not a set; it is a sequence, and the wrong sequence fails halfway and
// leaves a table nobody meant to build:
//
//  1. Renames first, so that everything after names what things are now
//     called rather than what they used to be.
//  2. Then what is dropped, constraints and indexes before the columns they
//     are made of, because a column cannot go while something holds it.
//  3. Then the columns themselves: added, retyped, made null or not null,
//     given defaults or relieved of them.
//  4. Then what is added, constraints and indexes after the columns they
//     are made of, because they cannot be made before there is anything to
//     make them on.
//  5. Comments last. They depend on everything and nothing depends on them,
//     so a comment that fails takes nothing with it.

// CreateObject renders the DDL that would recreate an object.
func (d dialect) CreateObject(ref model.ObjectRef, obj any) ([]source.Statement, error) {
	t, ok := obj.(*model.Table)
	if !ok {
		return nil, fmt.Errorf("postgres: %T cannot be rendered as DDL yet", obj)
	}
	name := d.QualifyRef(ref)
	if name == "" {
		name = d.QuoteIdentifier(t.Name)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "CREATE TABLE %s (\n", name)
	parts := make([]string, 0, len(t.Columns)+4)
	for _, c := range t.Columns {
		parts = append(parts, "    "+d.columnSpec(c))
	}
	if t.PrimaryKey != nil {
		parts = append(parts, "    "+d.primaryKeySpec(*t.PrimaryKey))
	}
	for _, u := range t.Uniques {
		parts = append(parts, "    "+d.uniqueSpec(u))
	}
	for _, f := range t.ForeignKeys {
		parts = append(parts, "    "+d.foreignKeySpec(f))
	}
	for _, c := range t.Checks {
		parts = append(parts, "    "+d.checkSpec(c))
	}
	b.WriteString(strings.Join(parts, ",\n"))
	b.WriteString("\n)")

	out := []source.Statement{{SQL: b.String()}}
	for _, idx := range t.Indexes {
		out = append(out, source.Statement{SQL: d.createIndex(name, idx)})
	}
	out = append(out, d.comments(name, nil, t)...)
	return out, nil
}

// DropObject renders the DDL removing an object.
func (d dialect) DropObject(ref model.ObjectRef, cascade bool) ([]source.Statement, error) {
	name := d.QualifyRef(ref)
	if name == "" {
		return nil, fmt.Errorf("postgres: %s cannot be named", ref)
	}
	what := "TABLE"
	switch ref.Kind {
	case model.KindView:
		what = "VIEW"
	case model.KindMaterializedView:
		what = "MATERIALIZED VIEW"
	case model.KindIndex:
		what = "INDEX"
	case model.KindSequence:
		what = "SEQUENCE"
	}
	sql := fmt.Sprintf("DROP %s %s", what, name)
	if cascade {
		sql += " CASCADE"
	}
	return []source.Statement{{SQL: sql}}, nil
}

// AlterObject renders the DDL transforming from into to. An empty result
// means the two say the same thing.
func (d dialect) AlterObject(ref model.ObjectRef, from, to any) ([]source.Statement, error) {
	was, ok := from.(*model.Table)
	if !ok {
		return nil, fmt.Errorf("postgres: %T cannot be altered yet", from)
	}
	now, ok := to.(*model.Table)
	if !ok {
		return nil, fmt.Errorf("postgres: %T cannot be altered into", to)
	}
	// The ref names the table, because a model.Table carries a bare name
	// and a table in a schema that is not on the search path would
	// otherwise be another table of the same name, or none.
	name := d.QualifyRef(ref)
	if name == "" {
		name = d.QuoteIdentifier(was.Name)
	}
	renamed := now.Name != "" && now.Name != was.Name

	var renames, colDrops, columns, adds []source.Statement
	if renamed {
		renames = append(renames, source.Statement{SQL: fmt.Sprintf("ALTER TABLE %s RENAME TO %s",
			name, d.QuoteIdentifier(now.Name))})
		// Everything after the rename names the table by what it is then
		// called, in the schema it is still in.
		name = d.qualifyIn(ref, now.Name)
	}

	// Columns are matched by name, never by position. Drop a column in the
	// middle and every column after it moves up one; matched by position,
	// each would read as the column before it renamed, and a rename that
	// carries a column's data would be rendered as a drop and an add that
	// does not. Whoever knows about a rename does it first, with
	// RenameColumn, and hands over a table already using the new names.
	for _, w := range was.Columns {
		n, ok := columnNamed(now.Columns, w.Name)
		if !ok {
			colDrops = append(colDrops, source.Statement{SQL: fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s",
				name, d.QuoteIdentifier(w.Name))})
			continue
		}
		columns = append(columns, d.alterColumn(name, w, n)...)
	}
	for _, n := range now.Columns {
		if _, ok := columnNamed(was.Columns, n.Name); !ok {
			columns = append(columns, source.Statement{SQL: fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s",
				name, d.columnSpec(n))})
		}
	}

	// Constraints and indexes: what went, then what arrived. A constraint
	// changed is both, because PostgreSQL has no ALTER CONSTRAINT for what
	// it is made of.
	dropped, added := d.constraintChanges(ref, name, was, now)
	adds = append(adds, added...)

	// What holds a column goes before the column does. A constraint or an
	// index still standing on a column refuses the drop, and PostgreSQL
	// says so by naming the constraint rather than the column, which sends
	// somebody looking in the wrong place.
	out := slices.Concat(renames, dropped, colDrops, columns, adds, d.comments(name, was, now))
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// alterColumn renders what changed about one column, other than its name.
func (d dialect) alterColumn(table string, was, now model.Column) []source.Statement {
	var out []source.Statement
	col := d.QuoteIdentifier(now.Name)
	alter := func(what string) {
		out = append(out, source.Statement{SQL: fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s %s", table, col, what)})
	}
	if renderType(now.Type) != renderType(was.Type) {
		// USING is not offered. PostgreSQL will cast what it can, and where
		// it cannot the error names the column and the two types, which is
		// more use than a cast this guessed at.
		alter("TYPE " + renderType(now.Type))
	}
	if now.Type.Nullable != was.Type.Nullable {
		if now.Type.Nullable {
			alter("DROP NOT NULL")
		} else {
			alter("SET NOT NULL")
		}
	}
	switch {
	case now.HasDefault && now.Default != was.Default:
		alter("SET DEFAULT " + now.Default)
	case !now.HasDefault && was.HasDefault:
		alter("DROP DEFAULT")
	}
	return out
}

// constraintChanges renders what to drop and what to add. They are kept
// apart so that every drop runs before every add: a constraint replaced
// under the same name cannot be added while the old one holds it.
func (d dialect) constraintChanges(ref model.ObjectRef, table string, was, now *model.Table) (drops, adds []source.Statement) {
	drop := func(name string) {
		drops = append(drops, source.Statement{SQL: fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s",
			table, d.QuoteIdentifier(name))})
	}
	add := func(spec string) {
		adds = append(adds, source.Statement{SQL: fmt.Sprintf("ALTER TABLE %s ADD %s", table, spec)})
	}

	if !samePrimaryKey(was.PrimaryKey, now.PrimaryKey) {
		if was.PrimaryKey != nil {
			drop(keyName(was.PrimaryKey.Name, was.Name+"_pkey"))
		}
		if now.PrimaryKey != nil {
			add(d.primaryKeySpec(*now.PrimaryKey))
		}
	}

	for _, w := range was.Uniques {
		if !slices.ContainsFunc(now.Uniques, func(n model.UniqueConstraint) bool { return sameUnique(w, n) }) {
			drop(w.Name)
		}
	}
	for _, n := range now.Uniques {
		if !slices.ContainsFunc(was.Uniques, func(w model.UniqueConstraint) bool { return sameUnique(w, n) }) {
			add(d.uniqueSpec(n))
		}
	}
	for _, w := range was.ForeignKeys {
		if !slices.ContainsFunc(now.ForeignKeys, func(n model.ForeignKey) bool { return sameFK(w, n) }) {
			drop(w.Name)
		}
	}
	for _, n := range now.ForeignKeys {
		if !slices.ContainsFunc(was.ForeignKeys, func(w model.ForeignKey) bool { return sameFK(w, n) }) {
			add(d.foreignKeySpec(n))
		}
	}
	for _, w := range was.Checks {
		if !slices.ContainsFunc(now.Checks, func(n model.CheckConstraint) bool { return sameCheck(w, n) }) {
			drop(w.Name)
		}
	}
	for _, n := range now.Checks {
		if !slices.ContainsFunc(was.Checks, func(w model.CheckConstraint) bool { return sameCheck(w, n) }) {
			add(d.checkSpec(n))
		}
	}

	// An index is not a constraint and is not altered as one — except the
	// ones that are. PostgreSQL builds an index to enforce a primary key or
	// a unique constraint, reports it among the table's indexes under the
	// constraint's own name, and will not let it be dropped on its own: it
	// goes when the constraint goes. Rendering it as an index as well would
	// send a DROP INDEX for something that no longer exists.
	for _, w := range was.Indexes {
		if backsAConstraint(was, w.Name) {
			continue
		}
		if !slices.ContainsFunc(now.Indexes, func(n model.Index) bool { return sameIdx(w, n) }) {
			drops = append(drops, source.Statement{SQL: "DROP INDEX " + d.qualifyIn(ref, w.Name)})
		}
	}
	for _, n := range now.Indexes {
		if backsAConstraint(now, n.Name) {
			continue
		}
		if !slices.ContainsFunc(was.Indexes, func(w model.Index) bool { return sameIdx(w, n) }) {
			adds = append(adds, source.Statement{SQL: d.createIndex(table, n)})
		}
	}
	return drops, adds
}

// comments are rendered last: they depend on everything and nothing depends
// on them, so one that fails takes nothing with it.
func (d dialect) comments(table string, was, now *model.Table) []source.Statement {
	var out []source.Statement
	wasComment := ""
	if was != nil {
		wasComment = was.Comment
	}
	if now.Comment != wasComment {
		out = append(out, source.Statement{SQL: fmt.Sprintf("COMMENT ON TABLE %s IS %s",
			table, literal(now.Comment))})
	}
	for _, c := range now.Columns {
		before := ""
		if was != nil {
			if w, ok := columnNamed(was.Columns, c.Name); ok {
				before = w.Comment
			}
		}
		if c.Comment == before {
			continue
		}
		out = append(out, source.Statement{SQL: fmt.Sprintf("COMMENT ON COLUMN %s.%s IS %s",
			table, d.QuoteIdentifier(c.Name), literal(c.Comment))})
	}
	return out
}

// columnSpec is a column as it is written inside CREATE TABLE or after ADD
// COLUMN.
func (d dialect) columnSpec(c model.Column) string {
	spec := d.QuoteIdentifier(c.Name) + " " + renderType(c.Type)
	if c.Generated != "" {
		return spec + " GENERATED ALWAYS AS (" + c.Generated + ") STORED"
	}
	if c.Identity {
		spec += " GENERATED BY DEFAULT AS IDENTITY"
	}
	if !c.Type.Nullable {
		spec += " NOT NULL"
	}
	if c.HasDefault && c.Default != "" && !c.Identity {
		spec += " DEFAULT " + c.Default
	}
	return spec
}

func (d dialect) primaryKeySpec(pk model.PrimaryKey) string {
	return constraintClause(d, pk.Name) + "PRIMARY KEY (" + d.columnList(pk.Columns) + ")"
}

func (d dialect) uniqueSpec(u model.UniqueConstraint) string {
	return constraintClause(d, u.Name) + "UNIQUE (" + d.columnList(u.Columns) + ")"
}

func (d dialect) checkSpec(c model.CheckConstraint) string {
	return constraintClause(d, c.Name) + "CHECK (" + c.Expression + ")"
}

func (d dialect) foreignKeySpec(f model.ForeignKey) string {
	ref := d.QuoteIdentifier(f.RefTable)
	if f.RefSchema != "" {
		ref = d.QuoteIdentifier(f.RefSchema) + "." + ref
	}
	spec := constraintClause(d, f.Name) + "FOREIGN KEY (" + d.columnList(f.Columns) + ") REFERENCES " +
		ref + " (" + d.columnList(f.RefColumns) + ")"
	if f.OnDelete != "" {
		spec += " ON DELETE " + string(f.OnDelete)
	}
	if f.OnUpdate != "" {
		spec += " ON UPDATE " + string(f.OnUpdate)
	}
	return spec
}

func (d dialect) createIndex(table string, idx model.Index) string {
	var b strings.Builder
	b.WriteString("CREATE ")
	if idx.Unique {
		b.WriteString("UNIQUE ")
	}
	b.WriteString("INDEX ")
	if idx.Name != "" {
		b.WriteString(d.QuoteIdentifier(idx.Name) + " ")
	}
	b.WriteString("ON " + table)
	if idx.Method != "" {
		b.WriteString(" USING " + idx.Method)
	}
	parts := make([]string, 0, len(idx.Columns))
	for _, c := range idx.Columns {
		part := c.Expression
		if part == "" {
			part = d.QuoteIdentifier(c.Name)
		}
		if c.Descending {
			part += " DESC"
		}
		parts = append(parts, part)
	}
	b.WriteString(" (" + strings.Join(parts, ", ") + ")")
	if len(idx.Include) > 0 {
		b.WriteString(" INCLUDE (" + d.columnList(idx.Include) + ")")
	}
	if idx.Predicate != "" {
		b.WriteString(" WHERE " + idx.Predicate)
	}
	return b.String()
}

func (d dialect) columnList(cols []string) string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = d.QuoteIdentifier(c)
	}
	return strings.Join(out, ", ")
}

// constraintClause writes the CONSTRAINT clause, or nothing where the engine
// is to choose the name itself.
func constraintClause(d dialect, name string) string {
	if name == "" {
		return ""
	}
	return "CONSTRAINT " + d.QuoteIdentifier(name) + " "
}

// keyName is a constraint's name, or what PostgreSQL would have called it.
func keyName(name, fallback string) string {
	if name != "" {
		return name
	}
	return fallback
}

// renderType is the engine's own word for a type, which is what the designer
// holds and what a person typed (ADR-0114).
func renderType(t model.DataType) string {
	if n := strings.TrimSpace(t.Native); n != "" {
		return n
	}
	return "text"
}

// literal writes a string as PostgreSQL reads one, or NULL for none. A
// comment is the one place a person's words reach a statement, so the
// doubling matters.
func literal(s string) string {
	if s == "" {
		return "NULL"
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func samePrimaryKey(a, b *model.PrimaryKey) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	}
	return a.Name == b.Name && slices.Equal(a.Columns, b.Columns)
}

func sameUnique(a, b model.UniqueConstraint) bool {
	return a.Name == b.Name && slices.Equal(a.Columns, b.Columns)
}

func sameCheck(a, b model.CheckConstraint) bool {
	return a.Name == b.Name && a.Expression == b.Expression
}

func sameFK(a, b model.ForeignKey) bool {
	return a.Name == b.Name && slices.Equal(a.Columns, b.Columns) &&
		a.RefSchema == b.RefSchema && a.RefTable == b.RefTable &&
		slices.Equal(a.RefColumns, b.RefColumns) &&
		a.OnDelete == b.OnDelete && a.OnUpdate == b.OnUpdate
}

func sameIdx(a, b model.Index) bool {
	return a.Name == b.Name && a.Unique == b.Unique && a.Method == b.Method &&
		a.Predicate == b.Predicate && slices.Equal(a.Include, b.Include) &&
		slices.EqualFunc(a.Columns, b.Columns, func(x, y model.IndexColumn) bool {
			return x.Name == y.Name && x.Expression == y.Expression && x.Descending == y.Descending
		})
}

// RenameColumn renders renaming one of a table's columns. It is its own
// statement, and its own method, because only a caller that kept track of
// which column became which can know a rename happened at all (ADR-0114).
func (d dialect) RenameColumn(ref model.ObjectRef, from, to string) ([]source.Statement, error) {
	name := d.QualifyRef(ref)
	if name == "" {
		return nil, fmt.Errorf("postgres: %s cannot be named", ref)
	}
	if from == "" || to == "" {
		return nil, fmt.Errorf("postgres: a rename needs both names, and has %q and %q", from, to)
	}
	if from == to {
		return nil, nil
	}
	return []source.Statement{{SQL: fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s",
		name, d.QuoteIdentifier(from), d.QuoteIdentifier(to))}}, nil
}

// columnNamed finds a column by name, which is how two tables are compared
// when neither knows what was renamed.
func columnNamed(cols []model.Column, name string) (model.Column, bool) {
	for _, c := range cols {
		if c.Name == name {
			return c, true
		}
	}
	return model.Column{}, false
}

// qualifyIn names a table by a new name in the schema its ref is in.
func (d dialect) qualifyIn(ref model.ObjectRef, name string) string {
	if p := ref.Path; len(p) >= 2 {
		return d.QuoteIdentifier(p[len(p)-2]) + "." + d.QuoteIdentifier(name)
	}
	return d.QuoteIdentifier(name)
}

// backsAConstraint reports an index PostgreSQL built to enforce a key or a
// unique constraint. It carries the constraint's name, and it lives and dies
// with it.
func backsAConstraint(t *model.Table, name string) bool {
	if name == "" {
		return false
	}
	if t.PrimaryKey != nil && t.PrimaryKey.Name == name {
		return true
	}
	return slices.ContainsFunc(t.Uniques, func(u model.UniqueConstraint) bool { return u.Name == name })
}
