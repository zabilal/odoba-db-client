package assistant

import (
	"fmt"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// What the model is told about the database (FR-14.1).
//
// A model that is not told the schema invents table names, and a person who
// gets a statement about tables they have not got cannot tell a wrong answer
// from a wrong question. So the schema goes with the question — and only the
// schema, unless somebody has said otherwise.
//
// It is bounded, and the bound is stated rather than silent: a database of two
// thousand tables does not fit in a prompt, and one that were quietly cut
// would produce a statement about the half the model happened to see. What is
// left out is counted and said, so the answer can be read knowing it.

// maxTables and maxColumns bound what one prompt describes. They are not about
// any provider's limit — providers differ and change — but about what a model
// can attend to: a schema of hundreds of tables in one prompt produces worse
// answers than the dozen that matter.
const (
	maxTables  = 40
	maxColumns = 60
)

// maxRows and maxCells bound a sample. A few rows is what shows a model the
// shape of a value; more is somebody's data leaving the machine for nothing.
const (
	maxRows  = 20
	maxCells = 400
)

// Grounding is what the model is told.
type Grounding struct {
	// Product and Dialect are the engine and the name of its language, so the
	// model writes that engine's SQL rather than something near it.
	Product string
	Dialect string

	// Tables are the tables and their columns, names only.
	Tables []Table

	// Left is how many tables were not described, which the prompt says: an
	// answer about part of a schema should be read as one.
	Left int

	// Rows are the sampled rows, where consent allows any (FR-14.3). Empty
	// means names only, which is the default and is what Request.SendsData
	// reads.
	Rows []Sample
}

// Table is one table, named as the query would name it.
type Table struct {
	Name    string
	Columns []Column

	// Left is how many columns were not described.
	Left int

	// Key is the primary key's columns, which is most of what a model needs to
	// write a join it has not been told about.
	Key []string
}

// Column is one column: its name and what it holds.
type Column struct {
	Name string
	Type string
}

// Sample is a few rows of one table, sent only where consent allows values to
// go at all.
type Sample struct {
	Table   string
	Columns []string
	Rows    [][]string
}

// FromSchema is the grounding a database gives, bounded.
//
// only, where it is given, names the tables to describe — which is how a
// question about two tables is asked without sending a hundred. Everything
// else in the schema is counted as left out, because a model told about two
// tables may answer as though there were only two.
func FromSchema(db *model.Database, product, dialect string, only []string) Grounding {
	g := Grounding{Product: product, Dialect: dialect}
	if db == nil {
		return g
	}
	wanted := func(schema, name string) bool {
		if len(only) == 0 {
			return true
		}
		for _, o := range only {
			if strings.EqualFold(o, name) || strings.EqualFold(o, schema+"."+name) {
				return true
			}
		}
		return false
	}
	for _, s := range db.Schemas {
		for _, t := range s.Tables {
			if !wanted(s.Name, t.Name) {
				g.Left++
				continue
			}
			if len(g.Tables) >= maxTables {
				g.Left++
				continue
			}
			g.Tables = append(g.Tables, tableOf(s.Name, t))
		}
	}
	return g
}

// tableOf describes one table, bounded by its columns.
func tableOf(schema string, t model.Table) Table {
	out := Table{Name: t.Name}
	if schema != "" {
		out.Name = schema + "." + t.Name
	}
	if t.PrimaryKey != nil {
		out.Key = t.PrimaryKey.Columns
	}
	for _, c := range t.Columns {
		if len(out.Columns) >= maxColumns {
			out.Left++
			continue
		}
		out.Columns = append(out.Columns, Column{Name: c.Name, Type: c.Type.Native})
	}
	return out
}

// SampleOf is a few rows of one table, as text, bounded by rows and by cells.
//
// As text because that is what a prompt carries, and because rendering a value
// for a model is not the same question as rendering it for a person: what
// matters is that it is recognisable, not that it round-trips.
func SampleOf(table string, cols []model.ColumnDef, rows []model.Row) Sample {
	s := Sample{Table: table}
	for _, c := range cols {
		s.Columns = append(s.Columns, c.Name)
	}
	cells := 0
	for _, r := range rows {
		if len(s.Rows) >= maxRows || cells >= maxCells {
			break
		}
		out := make([]string, 0, len(cols))
		for i := range cols {
			var v any
			if i < len(r) {
				v = r[i]
			}
			out = append(out, cellText(v))
			cells++
		}
		s.Rows = append(s.Rows, out)
	}
	return s
}

// cellText is one value as a prompt carries it. Nothing is shortened here
// beyond what a value is: a long text is cut, because a prompt full of one
// blob is a prompt about nothing else.
func cellText(v any) string {
	const longest = 120
	var text string
	switch x := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		// Bytes are not text and must not be shown as text: what a model needs
		// to know is that the column holds bytes and how many.
		return fmt.Sprintf("<%d bytes>", len(x))
	case string:
		text = x
	case model.JSON:
		text = string(x)
	case model.Decimal:
		text = string(x)
	default:
		text = fmt.Sprint(v)
	}
	if len(text) > longest {
		return text[:longest] + "…"
	}
	return text
}

// Describe is the grounding as the text a prompt carries.
//
// Written out here rather than as JSON, because a model reads a schema better
// as a schema: the shape a person would write it in is the shape it was trained
// on. What was left out is said at the end, so that an answer about part of a
// database can be read as one.
func (g Grounding) Describe() string {
	var b strings.Builder
	if g.Product != "" {
		b.WriteString("The database is " + g.Product)
		if g.Dialect != "" && !strings.EqualFold(g.Dialect, g.Product) {
			b.WriteString(", and its SQL is " + g.Dialect + "'s")
		}
		b.WriteString(".\n\n")
	}
	if len(g.Tables) > 0 {
		b.WriteString("Its tables:\n")
		for _, t := range g.Tables {
			b.WriteString(t.describe())
		}
	}
	if g.Left > 0 {
		b.WriteString(fmt.Sprintf("\n%s of this database is not described above; "+
			"say so if the answer depends on one of them.\n", plural(g.Left, "other table")))
	}
	for _, s := range g.Rows {
		b.WriteString(s.describe())
	}
	return b.String()
}

func (t Table) describe() string {
	var b strings.Builder
	b.WriteString("  " + t.Name + " (")
	for i, c := range t.Columns {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(c.Name)
		if c.Type != "" {
			b.WriteString(" " + c.Type)
		}
	}
	if t.Left > 0 {
		b.WriteString(fmt.Sprintf(", and %s not listed", plural(t.Left, "other column")))
	}
	b.WriteString(")")
	if len(t.Key) > 0 {
		b.WriteString(", keyed by " + strings.Join(t.Key, ", "))
	}
	b.WriteString("\n")
	return b.String()
}

func (s Sample) describe() string {
	if len(s.Rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nSome rows of " + s.Table + ", as they are:\n")
	b.WriteString("  " + strings.Join(s.Columns, " | ") + "\n")
	for _, r := range s.Rows {
		b.WriteString("  " + strings.Join(r, " | ") + "\n")
	}
	return b.String()
}

// plural says a count with its noun, in English: "1 other table", "2 other
// tables".
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
