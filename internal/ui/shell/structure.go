package shell

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/app/textdiff"
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
	if conn, n, ok := s.Explorer.SelectedNode(); ok && (n.Browsable || n.Describable) {
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

// structureActions are what a structure view can do beyond showing what was
// described: work that costs a request, and is therefore offered rather than
// done. Each is zero where it does not apply.
type structureActions struct {
	// sample infers a collection's shape from its documents (FR-12.4).
	sample func()

	// indexes adds and drops them, where the source makes them on its own
	// (FR-6.3).
	indexes *indexActions

	// progress reads a consumer group's per-partition offsets and lag, which
	// describing the group deliberately does not (FR-13.10, T2.75).
	progress func()
}

// showStructure draws what was described, with the sampling offered where the
// source's structure is its data's (FR-12.4), the indexes where it makes them
// on their own (FR-6.3), and a group's progress where the source can read it
// (FR-13.10).
func (s *Shell) showStructure(t *tab, connID string, desc any, inferred bool) {
	var acts structureActions
	switch d := desc.(type) {
	case *model.Collection:
		if inferred {
			acts.sample = func() { s.sampleShape(t, connID, d) }
		}
		if live, ok := s.d.WS.Get(connID); ok && live.Source.Capabilities().Schema.Indexes {
			acts.indexes = &indexActions{
				add:  func() { s.addIndex(t) },
				drop: func(names []string) { s.dropIndex(t, names) },
			}
		}
	case *model.ConsumerGroup:
		// Offered only where the source says it can read a group, which is a
		// promise about reading and not about changing one (ADR-0107).
		if live, ok := s.d.WS.Get(connID); ok && live.Source.Capabilities().Stream.ConsumerGroups {
			acts.progress = func() { s.readGroupProgress(t, connID, d) }
		}
	}
	t.body.Objects = []fyne.CanvasObject{container.NewVScroll(structureView(desc, acts))}
	t.body.Refresh()
}

// readGroupProgress reads how far a group has got and redraws the structure
// with it. It is a separate act from describing the group because it costs
// the group's committed offsets and the ends of every log they are in, and
// nobody should pay that for opening a group (T2.75, FR-13.10).
func (s *Shell) readGroupProgress(t *tab, connID string, g *model.ConsumerGroup) {
	ctx, id := t.ctx, g.ID
	t.footer.SetText("Reading how far the group has got…")
	go func() {
		live, err := s.d.WS.Connect(ctx, connID)
		var offsets []model.GroupOffset
		if err == nil {
			offsets, err = app.GroupOffsets(ctx, live.Source, id)
		}
		s.d.Run(func() {
			if ctx.Err() != nil {
				return // the tab closed while it was reading
			}
			t.footer.SetText("")
			if err != nil {
				s.showError(fmt.Errorf("could not read the group's progress: %w", err))
				return
			}
			g.Offsets = offsets
			s.showStructure(t, connID, g, false)
		})
	}()
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
// acts are what this view can offer to do that costs a request: sampling the
// documents of a source whose structure is its data's, adding and dropping
// indexes, and reading how far a consumer group has got. Each is offered only
// where it applies, and none of it happens unless somebody asks.
func structureView(desc any, acts structureActions) fyne.CanvasObject {
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
		if acts.sample != nil {
			// A collection has no declared fields; reading some of its
			// documents is the only way to say what it holds, and it is
			// offered rather than done (FR-12.4).
			label := fmt.Sprintf("Sample %d documents", sampleSize)
			if v.Shape != nil {
				label = "Sample again"
			}
			b := widget.NewButton(label, acts.sample)
			b.Importance = widget.LowImportance
			add(container.NewHBox(b))
		}
		add(section("Indexes", documentIndexRows(v.Indexes)))
		if acts.indexes != nil {
			addIdx := widget.NewButton("Add Index…", func() { acts.indexes.add() })
			dropIdx := widget.NewButton("Drop Index…", func() { acts.indexes.drop(indexNames(v)) })
			addIdx.Importance, dropIdx.Importance = widget.LowImportance, widget.LowImportance
			if len(indexNames(v)) == 0 {
				dropIdx.Disable()
			}
			add(container.NewHBox(addIdx, dropIdx))
		}
	case *model.Schema:
		// A keyspace's structure is how it is replicated: how many copies of
		// a row there are, and where they are kept (FR-12.3). What it holds
		// is counted rather than listed; the tree is where it is read.
		add(section("Replication", attrRows(v.Attrs)))
		rows := [][]string{{"Holds", "How many"}, {"Tables", strconv.Itoa(len(v.Tables))}}
		if n := len(v.Views); n > 0 {
			rows = append(rows, []string{"Materialized views", strconv.Itoa(n)})
		}
		if n := len(v.UserTypes); n > 0 {
			rows = append(rows, []string{"Types", strconv.Itoa(n)})
		}
		add(section("What it holds", rows))
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
	case *model.Topic:
		// A topic's shape is how it is spread and how much it holds. What it
		// holds is bounded rather than counted, and the label has to say so
		// (FR-13.2); its partitions one by one are the topic's own view.
		add(quietLabel(topicHolds(v)))
		if n := underReplicated(v); n > 0 {
			// The copies exist; some are behind, and would not be there if
			// the leader failed now. It is what somebody opening this view is
			// usually looking for (FR-13.3).
			were := "partitions are"
			if n == 1 {
				were = "partition is"
			}
			add(plain(fmt.Sprintf("%d %s short of an in-sync copy.", n, were)))
		}
		if len(v.Partitions) > 0 {
			rows := [][]string{{"Partition", "Leader", "Replicas", "In sync", "Offsets"}}
			for _, p := range v.Partitions {
				rows = append(rows, []string{
					fmt.Sprintf("%d", p.ID), leaderText(p),
					brokerList(p.Replicas), brokerList(p.ISR), offsetRange(p),
				})
			}
			add(section("Partitions", rows))
		}
		if len(v.Attrs) > 0 {
			add(section("What it says about itself", attrRows(v.Attrs)))
		}
	case *model.SchemaSubject:
		// A subject is one thing that has been revised. What there is to say
		// is how often, under what rule the next revision will be judged,
		// what the newest one says, and what changed (FR-13.14, ADR-0105).
		add(quietLabel(subjectHolds(v)))
		if v.Compatibility != "" {
			add(plain(compatibilityLine(v.Compatibility)))
		}
		if len(v.Versions) > 0 {
			rows := [][]string{{"Version", "Schema id", "Language"}}
			for _, ver := range v.Versions {
				rows = append(rows, []string{
					strconv.Itoa(int(ver.Version)), strconv.Itoa(int(ver.ID)), ver.Format,
				})
			}
			add(section("Versions", rows))

			// Laid out rather than as the registry keeps it: a schema is
			// registered as one line however large it is (ADR-0105).
			newest := v.Versions[len(v.Versions)-1]
			if newest.Definition != "" {
				def := widget.NewLabelWithStyle(textdiff.Indent(newest.Definition),
					fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
				def.Selectable = true
				add(container.NewVBox(bold(fmt.Sprintf("Schema, version %d", newest.Version)), def))
			}
		}
		if len(v.Versions) > 1 {
			add(versionComparison(v.Versions))
		}
	case *model.ConsumerGroup:
		// A group is several consumers doing one job between them, so what
		// there is to say is what it is doing and who is in it (FR-13.10).
		// What each has fallen behind by is T2.76, and the model says why:
		// offsets are read on demand, never while a group is being listed.
		add(quietLabel(groupHolds(v)))
		// Nothing guards the empty group here: a table of nothing but its own
		// headings is what section already declines to draw, and a second
		// check in front of it would decide nothing.
		rows := [][]string{{"Member", "Client", "Host", "Assigned"}}
		for _, m := range v.Members {
			rows = append(rows, []string{m.ID, m.ClientID, m.Host, assignedText(m.Assignment)})
		}
		add(section("Members", rows))
		if len(v.Offsets) > 0 {
			progress := [][]string{{"Topic", "Partition", "Committed", "End of log", "Behind"}}
			for _, o := range v.Offsets {
				progress = append(progress, []string{o.Topic, strconv.Itoa(int(o.Partition)),
					offsetText(o.Current), offsetText(o.End), lagText(o)})
			}
			add(section("Progress", progress))
			add(quietLabel(behindText(v)))
		}
		if acts.progress != nil {
			// Offered, never done: it costs the group's commits and the ends
			// of every log they are in (T2.75, FR-13.10).
			label := "Read how far it has got"
			if len(v.Offsets) > 0 {
				label = "Read it again"
			}
			b := widget.NewButton(label, acts.progress)
			b.Importance = widget.LowImportance
			add(container.NewHBox(b))
			if len(v.Offsets) == 0 {
				add(quietLabel("How far each member has got is a pair of requests to the cluster, so it is read when you ask for it."))
			}
		}
	case *model.Cluster:
		// A cluster has no columns either. What there is to say is who its
		// brokers are, which of them answers for the whole, and what it calls
		// itself (FR-13.1).
		add(quietLabel(clusterHolds(v)))
		if len(v.Brokers) == 0 {
			add(plain("This cluster named no brokers."))
		} else {
			rows := [][]string{{"Broker", "Address", "Rack"}}
			for _, b := range v.Brokers {
				name := fmt.Sprintf("%d", b.ID)
				if b.ID == v.Controller {
					// Said on the row rather than in a legend elsewhere.
					name += " (controller)"
				}
				rows = append(rows, []string{name, fmt.Sprintf("%s:%d", b.Host, b.Port), b.Rack})
			}
			add(section("Brokers", rows))
		}
		if len(v.Attrs) > 0 {
			add(section("What it says about itself", attrRows(v.Attrs)))
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
// offsetText is a position in a log, or says that nobody could give one.
// Nothing here reads as -1: that is a fact about the protocol rather than
// about the data, and nobody should have to know how to read one.
func offsetText(at int64) string {
	if at < 0 {
		return "none"
	}
	return strconv.FormatInt(at, 10)
}

// lagText is how far behind one partition is.
//
// Two different unknowns arrive as -1 and must not read alike. A partition
// the group has never committed to, or one whose end nobody could read,
// cannot be measured at all and says so. Where both ends are known a
// negative is the measurement rather than the group — the commit and the end
// of the log are read a moment apart, so a busy partition can appear to be
// ahead of itself — and it is clamped, which is what the model asks of
// anybody reading Lag.
func lagText(o model.GroupOffset) string {
	if o.Current < 0 || o.End < 0 {
		return "unknown"
	}
	if o.Lag <= 0 {
		return "up to date"
	}
	return strconv.FormatInt(o.Lag, 10)
}

// behindText is what the group amounts to across every partition it reads,
// which is the number somebody opens this view for.
//
// It counts only what could be measured and says how much it left out: a
// total quietly omitting the partitions nobody could read would be the most
// misleading number on the page.
func behindText(g *model.ConsumerGroup) string {
	unknown := 0
	for _, o := range g.Offsets {
		if o.Current < 0 || o.End < 0 {
			unknown++
		}
	}
	var text string
	switch n := g.TotalLag(); {
	case n == 0:
		text = "Up to date on every partition that could be measured."
	case n == 1:
		text = "One record behind, across the partitions that could be measured."
	default:
		text = fmt.Sprintf("%d records behind, across the partitions that could be measured.", n)
	}
	switch {
	case unknown == 1:
		text += " One could not be, and is not counted."
	case unknown > 1:
		text += fmt.Sprintf(" %d could not be, and are not counted.", unknown)
	}
	return text
}

// groupHolds is what a group amounts to in one line: what it is doing, and
// how many are doing it.
func groupHolds(g *model.ConsumerGroup) string {
	state := g.State
	if state == "" {
		// A group whose state the cluster did not give: said as an absence
		// rather than left as a blank somebody has to interpret.
		state = "In a state the cluster did not name"
	}
	switch len(g.Members) {
	case 0:
		return state + ", with nobody in it."
	case 1:
		return state + ", with one member."
	}
	return fmt.Sprintf("%s, with %d members.", state, len(g.Members))
}

// assignedText is what one member was given to read, gathered by topic so
// that a member holding twenty partitions of one topic reads as that rather
// than as twenty entries.
//
// A member with nothing assigned says so: that is what somebody is looking
// for when a group has members and is not getting through its logs.
func assignedText(parts []model.TopicPartition) string {
	if len(parts) == 0 {
		return "nothing"
	}
	var order []string
	byTopic := map[string][]string{}
	for _, p := range parts {
		if _, seen := byTopic[p.Topic]; !seen {
			order = append(order, p.Topic)
		}
		byTopic[p.Topic] = append(byTopic[p.Topic], strconv.Itoa(int(p.Partition)))
	}
	out := make([]string, 0, len(order))
	for _, t := range order {
		out = append(out, t+" "+strings.Join(byTopic[t], ", "))
	}
	return strings.Join(out, "; ")
}

// subjectHolds is what a subject amounts to in one line.
func subjectHolds(s *model.SchemaSubject) string {
	switch len(s.Versions) {
	case 0:
		return "Nothing has been registered under this subject."
	case 1:
		return "One version, and nothing has replaced it."
	}
	return fmt.Sprintf("%d versions, the newest of them version %d.",
		len(s.Versions), s.Versions[len(s.Versions)-1].Version)
}

// compatibilityLine says what the registry will hold the next version to.
//
// The rule is the registry's own and the registry is what enforces it, so
// this reads its levels back rather than judging anything: a level this build
// has not heard of is named and left unexplained, which is better than
// explaining it wrongly.
func compatibilityLine(level string) string {
	gloss := map[string]string{
		"NONE":                "new versions are not checked against the ones before them",
		"BACKWARD":            "a new version must be able to read what the version before it wrote",
		"BACKWARD_TRANSITIVE": "a new version must be able to read what every earlier version wrote",
		"FORWARD":             "the version before it must be able to read what a new version writes",
		"FORWARD_TRANSITIVE":  "every earlier version must be able to read what a new version writes",
		"FULL":                "a new version and the one before it must each be able to read what the other wrote",
		"FULL_TRANSITIVE":     "a new version and every earlier one must each be able to read what the other wrote",
	}[level]
	if gloss == "" {
		return "Compatibility: " + level + "."
	}
	return "Compatibility: " + level + " — " + gloss + "."
}

// versionComparison is two versions picked, and the difference between them
// (FR-13.14, ADR-0105).
//
// It keeps its own state: which two versions somebody is looking at is
// nobody's business outside this view, so the pickers redraw the comparison
// beneath them rather than the tab around them.
func versionComparison(versions []model.SchemaVersion) fyne.CanvasObject {
	names := make([]string, len(versions))
	at := map[string]model.SchemaVersion{}
	for i, v := range versions {
		names[i] = fmt.Sprintf("Version %d", v.Version)
		at[names[i]] = v
	}
	body := container.NewVBox()
	before := widget.NewSelect(names, nil)
	after := widget.NewSelect(names, nil)
	redraw := func(string) {
		body.Objects = []fyne.CanvasObject{comparison(at[before.Selected], at[after.Selected])}
		body.Refresh()
	}
	before.OnChanged, after.OnChanged = redraw, redraw
	// The comparison somebody opens this view wanting: the newest against the
	// one before it. Put in place rather than selected, which would draw a
	// comparison against nothing on the way past.
	before.Selected, after.Selected = names[len(names)-2], names[len(names)-1]
	redraw("")
	return container.NewVBox(bold("What changed"),
		container.NewHBox(before, quietLabel("compared with"), after), body)
}

// comparison is one version against another, line by line. A line that
// arrived is marked +, and one that went -, so that what changed is never
// colour alone (ADR-0105).
func comparison(before, after model.SchemaVersion) fyne.CanvasObject {
	lines := textdiff.Lines(textdiff.Indent(before.Definition), textdiff.Indent(after.Definition))
	added, removed := textdiff.Summary(lines)
	box := container.NewVBox()
	// Which two versions these are, said in the comparison itself. The
	// pickers above it are a control rather than a caption, and somebody
	// reading or copying this out should not have to look elsewhere to know
	// what was compared with what.
	between := fmt.Sprintf("From version %d to version %d", before.Version, after.Version)
	if added == 0 && removed == 0 {
		box.Add(plain(between + ": the same text."))
		return box
	}
	box.Add(quietLabel(fmt.Sprintf("%s: %s arrived, %s went.", between, lineCount(added), lineCount(removed))))
	for _, l := range lines {
		sign, importance := "  ", widget.LowImportance
		switch l.Op {
		case textdiff.Added:
			sign, importance = "+ ", widget.SuccessImportance
		case textdiff.Removed:
			sign, importance = "- ", widget.DangerImportance
		}
		row := widget.NewLabelWithStyle(sign+l.Text, fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
		row.Importance = importance
		row.Selectable = true
		box.Add(row)
	}
	return box
}

// lineCount is a number of lines in words, so that one line is never "1
// lines".
func lineCount(n int) string {
	if n == 1 {
		return "1 line"
	}
	return fmt.Sprintf("%d lines", n)
}

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

// attrRows are an object's engine-specific properties, by name. They keep
// the engine's own words, as a Redis figure does: a person looking one up
// looks up what the server calls it.
func attrRows(attrs map[string]string) [][]string {
	rows := [][]string{{"Name", "Value"}}
	names := make([]string, 0, len(attrs))
	for name := range attrs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		rows = append(rows, []string{name, attrs[name]})
	}
	return rows
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

// underReplicated counts the partitions with fewer copies in sync than they
// have copies: the replicas exist, but some are behind.
func underReplicated(t *model.Topic) int {
	n := 0
	for _, p := range t.Partitions {
		if len(p.ISR) < len(p.Replicas) {
			n++
		}
	}
	return n
}

// leaderText names the broker a partition is read and written through, or
// says there is none: a partition without a leader cannot be used at all,
// which is worth more to read than a bare -1.
func leaderText(p model.Partition) string {
	if p.Leader < 0 {
		return "none"
	}
	return fmt.Sprintf("%d", p.Leader)
}

// brokerList is a list of broker ids, or a word where there are none.
func brokerList(ids []int32) string {
	if len(ids) == 0 {
		return "none"
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, fmt.Sprintf("%d", id))
	}
	return strings.Join(out, ", ")
}

// offsetRange is where a partition's log begins and ends.
//
// Offsets nobody could read say so, and a log whose ends meet says it is
// empty: not knowing and holding nothing are different things, and only one
// of them is a fact about the data (ADR-0092).
func offsetRange(p model.Partition) string {
	if p.LowWatermark < 0 || p.HighWatermark < 0 {
		return "unknown"
	}
	if p.LowWatermark == p.HighWatermark {
		return fmt.Sprintf("%d (empty)", p.LowWatermark)
	}
	return fmt.Sprintf("%d to %d", p.LowWatermark, p.HighWatermark)
}

// topicHolds is a topic in one line: how it is spread, and how much of it
// there is to read.
//
// The count is an upper bound on what is retained rather than a count of what
// was ever written: a log is aged out and compacted behind the reader, so the
// words say "up to" and mean it (FR-13.2).
func topicHolds(t *model.Topic) string {
	held := fmt.Sprintf("%d partitions", len(t.Partitions))
	if len(t.Partitions) == 1 {
		held = "one partition"
	}
	switch {
	case t.ReplicationFactor == 1:
		held += " on one broker"
	case t.ReplicationFactor > 1:
		held += fmt.Sprintf(" on %d brokers", t.ReplicationFactor)
	default:
		held += ", replicated differently by partition"
	}
	if n := t.MessageCount(); n > 0 {
		held += fmt.Sprintf(", holding up to %d records", n)
	}
	if t.Internal {
		return held + ". Kafka's own."
	}
	return held + "."
}

// clusterHolds is a cluster in one line: how many brokers it has, and what it
// calls itself.
func clusterHolds(c *model.Cluster) string {
	brokers := fmt.Sprintf("%d brokers", len(c.Brokers))
	if len(c.Brokers) == 1 {
		brokers = "one broker"
	}
	if c.ID == "" {
		return brokers + "."
	}
	return fmt.Sprintf("%s, calling itself %s.", brokers, c.ID)
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
