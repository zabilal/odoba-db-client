package shell

import (
	"context"
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// The structure tab shows what an object is made of, read-only (FR-2.4,
// ADR-0011 §14): a table's columns, keys, indexes, constraints and
// triggers, or a view's definition and columns, as the driver's Describe
// reports them.

func structureKey(connID string, ref model.ObjectRef) string {
	return "structure:" + view.NodeID(connID, ref)
}

// openSelectedStructure opens the structure of the explorer's selection.
func (s *Shell) openSelectedStructure() {
	if conn, n, ok := s.Explorer.SelectedNode(); ok && n.Browsable {
		s.OpenStructure(conn, n)
	}
}

// OpenStructure shows an object's structure in a tab, or brings its tab
// forward. Like a data tab it never blocks: the connection and the
// description arrive in the background.
func (s *Shell) OpenStructure(connID string, n model.Node) *tab {
	key := structureKey(connID, n.Ref)
	if t := s.tabFor(key); t != nil {
		s.selectTab(t)
		return t
	}
	ctx, cancel := context.WithCancel(s.ctx)
	t := &tab{key: key, connID: connID, ref: n.Ref, label: n.Label, structure: true, ctx: ctx, cancel: cancel,
		body: container.NewStack(quiet("Reading the structure…")), footer: widget.NewLabel("")}
	t.footer.Importance = widget.LowImportance
	t.item = container.NewTabItem("Structure: "+n.Label, container.NewBorder(nil, t.footer, nil, nil, t.body))
	s.open = append(s.open, t)
	s.addTab(t)
	s.sync()

	go func() {
		live, err := s.d.WS.Connect(ctx, connID)
		var desc any
		if err == nil {
			desc, err = app.Describe(ctx, live.Source, n.Ref)
		}
		s.d.Run(func() {
			if ctx.Err() != nil {
				return // the tab closed while it was reading
			}
			if err != nil {
				s.tabFailed(t, fmt.Errorf("could not read the structure: %w", err))
				s.crashed(connID, err)
				return
			}
			t.body.Objects = []fyne.CanvasObject{container.NewVScroll(structureView(desc))}
			t.body.Refresh()
		})
	}()
	return t
}

// structureView lays out what Describe returned. Sections with nothing in
// them are left out (UX principle 2).
func structureView(desc any) fyne.CanvasObject {
	box := container.NewVBox()
	add := func(o fyne.CanvasObject) { box.Add(o) }
	switch v := desc.(type) {
	case *model.Table:
		if v.Comment != "" {
			add(plain(v.Comment))
		}
		if v.RowsEstimate >= 0 {
			add(quietLabel(fmt.Sprintf("About %d rows, as the server estimates it.", v.RowsEstimate)))
		}
		add(section("Columns", columnRows(v.Columns)))
		if pk := v.PrimaryKey; pk != nil && len(pk.Columns) > 0 {
			add(section("Primary key", [][]string{{"Name", "Columns"}, {pk.Name, strings.Join(pk.Columns, ", ")}}))
		}
		add(section("Indexes", indexRows(v.Indexes)))
		add(section("Foreign keys", foreignKeyRows(v.ForeignKeys)))
		rows := [][]string{{"Name", "Columns"}}
		for _, u := range v.Uniques {
			rows = append(rows, []string{u.Name, strings.Join(u.Columns, ", ")})
		}
		add(section("Unique constraints", rows))
		rows = [][]string{{"Name", "Expression"}}
		for _, c := range v.Checks {
			rows = append(rows, []string{c.Name, c.Expression})
		}
		add(section("Check constraints", rows))
		rows = [][]string{{"Name", "When", "Definition"}}
		for _, tr := range v.Triggers {
			when := strings.TrimSpace(tr.Timing + " " + strings.Join(tr.Events, " OR "))
			if tr.ForEachRow {
				when += ", for each row"
			}
			rows = append(rows, []string{tr.Name, when, tr.Definition})
		}
		add(section("Triggers", rows))
	case *model.View:
		if v.Comment != "" {
			add(plain(v.Comment))
		}
		if v.Definition != "" {
			heading := "Definition"
			if v.Materialized {
				heading = "Definition (materialized)"
			}
			def := widget.NewLabelWithStyle(v.Definition, fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
			def.Selectable = true
			add(container.NewVBox(bold(heading), def))
		}
		add(section("Columns", columnRows(v.Columns)))
		add(section("Indexes", indexRows(v.Indexes)))
	case *model.Collection:
		if v.Attrs["type"] == "view" {
			add(plain("A view: its documents are another collection's, through a pipeline."))
		}
		if v.Attrs["readOnly"] == "true" {
			add(quietLabel("Read-only."))
		}
		if v.DocumentsEstimate >= 0 {
			add(quietLabel(fmt.Sprintf("About %d documents, as the server estimates it.", v.DocumentsEstimate)))
		}
		if v.Shape != nil {
			add(quietLabel(fmt.Sprintf("Fields seen in %d documents sampled.", v.Shape.Sampled)))
			add(section("Fields", fieldRows(v.Shape.Fields)))
		}
		add(section("Indexes", documentIndexRows(v.Indexes)))
	default:
		add(plain("The structure of this kind of object cannot be shown yet."))
	}
	return container.NewPadded(box)
}

func columnRows(cols []model.Column) [][]string {
	rows := [][]string{{"Name", "Type", "Null", "Default", "Details", "Comment"}}
	for _, c := range cols {
		null := "no"
		if c.Type.Nullable {
			null = "yes"
		}
		var details []string
		if c.Identity {
			details = append(details, "identity")
		}
		if c.AutoIncrement {
			details = append(details, "auto-increment")
		}
		if c.Generated != "" {
			details = append(details, "generated as "+c.Generated)
		}
		rows = append(rows, []string{c.Name, c.Type.Native, null, c.Default, strings.Join(details, ", "), c.Comment})
	}
	return rows
}

// documentIndexRows renders a collection's indexes. They are not a table's:
// a key can be a kind of index rather than a direction, and a document store
// has sparse and expiring indexes where a relational one has partial ones.
func documentIndexRows(idx []model.DocumentIndex) [][]string {
	rows := [][]string{{"Name", "Keys", "Kind", "Expires after"}}
	for _, ix := range idx {
		var keys []string
		for _, k := range ix.Keys {
			switch {
			case k.Expression != "":
				keys = append(keys, k.Name+" "+k.Expression)
			case k.Descending:
				keys = append(keys, k.Name+" DESC")
			default:
				keys = append(keys, k.Name)
			}
		}
		var kind []string
		if ix.Unique {
			kind = append(kind, "unique")
		}
		if ix.Sparse {
			kind = append(kind, "sparse")
		}
		ttl := ""
		if ix.TTL > 0 {
			ttl = fmt.Sprintf("%d seconds", ix.TTL)
		}
		rows = append(rows, []string{ix.Name, strings.Join(keys, ", "), strings.Join(kind, ", "), ttl})
	}
	return rows
}

// fieldRows renders an inferred shape: what was seen, and how often, never as
// though the server had declared it (FR-12.4).
func fieldRows(fields []model.InferredField) [][]string {
	rows := [][]string{{"Field", "Types", "In"}}
	for _, f := range fields {
		var types []string
		for _, t := range f.Types {
			types = append(types, t.Type.Native)
		}
		rows = append(rows, []string{f.Name, strings.Join(types, ", "),
			fmt.Sprintf("%.0f%% of those sampled", f.Presence*100)})
	}
	return rows
}

func indexRows(idx []model.Index) [][]string {
	rows := [][]string{{"Name", "Columns", "Kind", "Where", "Includes"}}
	for _, ix := range idx {
		var cols []string
		for _, c := range ix.Columns {
			name := c.Name
			if c.Expression != "" {
				name = c.Expression
			}
			if c.Descending {
				name += " DESC"
			}
			cols = append(cols, name)
		}
		kind := ix.Method
		if ix.Unique {
			kind = strings.TrimSpace("unique " + kind)
		}
		rows = append(rows, []string{ix.Name, strings.Join(cols, ", "), kind, ix.Predicate, strings.Join(ix.Include, ", ")})
	}
	return rows
}

func foreignKeyRows(fks []model.ForeignKey) [][]string {
	rows := [][]string{{"Name", "Columns", "References", "On delete", "On update"}}
	for _, fk := range fks {
		target := fk.RefTable
		if fk.RefSchema != "" {
			target = fk.RefSchema + "." + target
		}
		rows = append(rows, []string{fk.Name, strings.Join(fk.Columns, ", "),
			fmt.Sprintf("%s (%s)", target, strings.Join(fk.RefColumns, ", ")), string(fk.OnDelete), string(fk.OnUpdate)})
	}
	return rows
}

// section is a heading over a grid whose first row names its columns. A
// section with no rows below that is nothing, and left out.
func section(title string, rows [][]string) fyne.CanvasObject {
	if len(rows) < 2 {
		return container.NewVBox()
	}
	grid := container.NewGridWithColumns(len(rows[0]))
	for i, r := range rows {
		for _, cell := range r {
			l := widget.NewLabel(cell)
			l.Truncation = fyne.TextTruncateEllipsis
			l.Selectable = true
			if i == 0 {
				l.TextStyle = fyne.TextStyle{Bold: true}
			}
			grid.Add(l)
		}
	}
	return container.NewVBox(bold(title), grid, widget.NewSeparator())
}

func bold(text string) *widget.Label {
	return widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
}

func plain(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.Wrapping = fyne.TextWrapWord
	return l
}

func quietLabel(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.Importance = widget.LowImportance
	return l
}
