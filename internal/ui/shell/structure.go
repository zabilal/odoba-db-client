package shell

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

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

// openSelectedStructure opens the structure of whatever a person is looking
// at: the explorer's selection, or the object the tab in front is on.
func (s *Shell) openSelectedStructure() {
	if conn, n, ok := s.structureTarget(); ok {
		s.OpenStructure(conn, n)
	}
}

// structureTarget is the object whose structure to show. The explorer's
// selection comes first, being the more deliberate choice; where there is
// none, it is the object the tab in front is on — which is the only way to
// reach one that is not in the tree at all, a Redis key among them.
func (s *Shell) structureTarget() (string, model.Node, bool) {
	if conn, n, ok := s.Explorer.SelectedNode(); ok && n.Browsable {
		return conn, n, true
	}
	t := s.activeTab()
	if t == nil || t.structure || t.query != nil || t.ref.Kind == "" {
		return "", model.Node{}, false
	}
	return t.connID, model.Node{Ref: t.ref, Label: t.ref.Name(), Browsable: true}, true
}

// canOpenStructure reports whether there is anything to show the structure of.
func (s *Shell) canOpenStructure() bool {
	_, _, ok := s.structureTarget()
	return ok
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
			s.showStructure(t, connID, desc, live.Source.Capabilities().Structure.InferredShape)
		})
	}()
	return t
}

// reopenStructure reads an object's structure again, after something
// changed it. The tab stays where it is; only what it shows is read again.
func (s *Shell) reopenStructure(t *tab) {
	ctx, ref, connID := t.ctx, t.ref, t.connID
	go func() {
		live, err := s.d.WS.Connect(ctx, connID)
		var desc any
		var inferred bool
		if err == nil {
			desc, err = app.Describe(ctx, live.Source, ref)
			inferred = live.Source.Capabilities().Structure.InferredShape
		}
		s.d.Run(func() {
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				s.showError(fmt.Errorf("could not read the structure: %w", err))
				return
			}
			s.showStructure(t, connID, desc, inferred)
		})
	}()
}

// sampleSize is how many documents Sample reads. Enough for a rare field to
// show up, few enough to come back while a person is looking at the panel.
const sampleSize = 200

// indexActions are what a structure tab offers for an object's indexes,
// where the source makes and unmakes them on their own (FR-6.3).
type indexActions struct {
	add  func()
	drop func(names []string)
}

// showStructure draws what was described, with the sampling offered where the
// source's structure is its data's (FR-12.4), and the indexes where it makes
// them on their own (FR-6.3).
func (s *Shell) showStructure(t *tab, connID string, desc any, inferred bool) {
	var sample func()
	var idx *indexActions
	if coll, ok := desc.(*model.Collection); ok {
		if inferred {
			sample = func() { s.sampleShape(t, connID, coll) }
		}
		if live, ok := s.d.WS.Get(connID); ok && live.Source.Capabilities().Schema.Indexes {
			idx = &indexActions{
				add:  func() { s.addIndex(t) },
				drop: func(names []string) { s.dropIndex(t, names) },
			}
		}
	}
	t.body.Objects = []fyne.CanvasObject{container.NewVScroll(structureView(desc, sample, idx))}
	t.body.Refresh()
}

// sampleShape reads documents and redraws the structure with what they hold.
// It never runs unasked: inference reads data, and a person chooses when.
func (s *Shell) sampleShape(t *tab, connID string, coll *model.Collection) {
	ctx, ref := t.ctx, t.ref
	t.footer.SetText(fmt.Sprintf("Reading %d documents…", sampleSize))
	go func() {
		live, err := s.d.WS.Connect(ctx, connID)
		var shape *model.DocumentShape
		if err == nil {
			shape, err = app.InferShape(ctx, live.Source, ref, sampleSize)
		}
		s.d.Run(func() {
			if ctx.Err() != nil {
				return // the tab closed while it was reading
			}
			if err != nil {
				t.footer.SetText("")
				s.showError(fmt.Errorf("could not sample the documents: %w", err))
				return
			}
			coll.Shape = shape
			t.footer.SetText("")
			s.showStructure(t, connID, coll, true)
		})
	}()
}

// structureView lays out what Describe returned. Sections with nothing in
// them are left out (UX principle 2).
//
// sample, where there is one, reads documents to say what an object holds:
// only a source whose structure is its data's has one.
func structureView(desc any, sample func(), idx *indexActions) fyne.CanvasObject {
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
		if sample != nil {
			// A collection has no declared fields; reading some of its
			// documents is the only way to say what it holds, and it is
			// offered rather than done (FR-12.4).
			label := fmt.Sprintf("Sample %d documents", sampleSize)
			if v.Shape != nil {
				label = "Sample again"
			}
			b := widget.NewButton(label, sample)
			b.Importance = widget.LowImportance
			add(container.NewHBox(b))
		}
		add(section("Indexes", documentIndexRows(v.Indexes)))
		if idx != nil {
			addIdx := widget.NewButton("Add Index…", func() { idx.add() })
			dropIdx := widget.NewButton("Drop Index…", func() { idx.drop(indexNames(v)) })
			addIdx.Importance, dropIdx.Importance = widget.LowImportance, widget.LowImportance
			if len(indexNames(v)) == 0 {
				dropIdx.Disable()
			}
			add(container.NewHBox(addIdx, dropIdx))
		}
	case *model.Keyspace:
		// A keyspace has no columns: what there is to say about it is how
		// much it holds and what the server is spending on it (FR-12.2).
		if v.Keys >= 0 {
			held := fmt.Sprintf("%d keys.", v.Keys)
			if v.Expiring >= 0 {
				held = fmt.Sprintf("%d keys, %d of them set to expire.", v.Keys, v.Expiring)
			}
			add(quietLabel(held))
		}
		if len(v.Figures) == 0 {
			add(plain("This server says nothing about itself."))
		}
		for _, g := range v.Figures {
			add(section(g.Title, figureRows(g.Values)))
		}
	case *model.StoredKey:
		add(quietLabel(keyHolds(v)))
		rows := [][]string{{"Name", "Value"}, {"Kind", v.Kind}}
		if v.Encoding != "" {
			rows = append(rows, []string{"Held as", v.Encoding})
		}
		if v.Length >= 0 {
			rows = append(rows, []string{"Length", strconv.FormatInt(v.Length, 10)})
		}
		if v.Bytes >= 0 {
			rows = append(rows, []string{"Bytes", strconv.FormatInt(v.Bytes, 10)})
		}
		expires := "never"
		if v.TTL > 0 {
			expires = "in " + v.TTL.Round(time.Second).String()
		}
		rows = append(rows, []string{"Expires", expires})
		add(section("Key", rows))
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

// figureRows renders a server's own numbers, by its own names for them.
func figureRows(values []model.Figure) [][]string {
	rows := [][]string{{"Name", "Value"}}
	for _, f := range values {
		rows = append(rows, []string{f.Name, f.Value})
	}
	return rows
}

// keyHolds says in a line what a key is.
func keyHolds(k *model.StoredKey) string {
	what := map[string]string{
		"string": "A string: one value.",
		"hash":   "A hash: fields and their values.",
		"list":   "A list: elements in the order they were put there.",
		"set":    "A set: members, each of them once.",
		"zset":   "A sorted set: members, ordered by their scores.",
		"stream": "A stream: entries, written once and read in order.",
	}
	if said, ok := what[k.Kind]; ok {
		return said
	}
	if k.Kind == "ReJSON-RL" {
		return "A JSON document: one value, with a structure of its own."
	}
	return "A " + k.Kind + "."
}
