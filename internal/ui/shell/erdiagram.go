package shell

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
	"github.com/ikigai-db/ikigai-db/internal/ui/canvas"
	"github.com/ikigai-db/ikigai-db/internal/ui/diagram"
	"github.com/ikigai-db/ikigai-db/internal/ui/erd"
	"github.com/ikigai-db/ikigai-db/internal/ui/filedlg"
)

// The way into a diagram (FR-8.1, FR-8.2).
//
// A diagram is read from the same snapshot a comparison is, so opening one
// costs what comparing costs and no more. It is laid out afresh and whatever
// was moved is put back, which is what makes a diagram somebody arranged
// last week still theirs today (ADR-0128).

// diagramTimeout bounds reading a schema to draw it. It is a snapshot, which
// on a large database is many catalogue queries.
const diagramTimeout = 2 * time.Minute

// diagramPanel is a diagram's state.
type diagramPanel struct {
	s   *Shell
	t   *tab
	w   *diagram.Widget
	key string

	// db is the schema as it was read, kept so that focusing on part of it
	// redraws without asking the server again: a diagram drawn from a
	// second reading could differ from the one somebody was looking at.
	db  *model.Database
	opt erd.Options

	// focus is the table a diagram is centred on and how far out it
	// reaches, or "" for all of it (FR-8.5).
	focus  string
	degree int

	chosen *widget.Button
	wider  *widget.Select
	all    *widget.Button
}

func diagramKey(connID string, ref model.ObjectRef) string {
	return "diagram:" + connID + ":" + ref.String()
}

// canDiagram reports whether the selection is something to draw.
func (s *Shell) canDiagram() bool {
	conn, n, ok := s.Explorer.SelectedNode()
	if !ok || !holdsAClass[n.Ref.Kind] {
		return false
	}
	live, open := s.d.WS.Get(conn)
	return open && app.CanCompare(live.Source)
}

func (s *Shell) diagramSelected() {
	if conn, n, ok := s.Explorer.SelectedNode(); ok && holdsAClass[n.Ref.Kind] {
		s.OpenDiagram(conn, n.Ref)
	}
}

// OpenDiagram draws a schema, or brings the tab already on it forward.
func (s *Shell) OpenDiagram(connID string, ref model.ObjectRef) *tab {
	key := diagramKey(connID, ref)
	if t := s.tabFor(key); t != nil {
		s.selectTab(t)
		return t
	}
	ctx, cancel := context.WithCancel(s.ctx)
	t := &tab{key: key, connID: connID, ref: ref, label: ref.Name(), structure: true, ctx: ctx, cancel: cancel,
		body: container.NewStack(quiet("Reading the schema…")), footer: widget.NewLabel("")}
	t.footer.Importance = widget.LowImportance
	t.item = container.NewTabItem("Diagram: "+ref.Name(), container.NewBorder(nil, t.footer, nil, nil, t.body))
	s.open = append(s.open, t)
	s.addTab(t)
	s.sync()

	go func() {
		ctx, cancel := context.WithTimeout(ctx, diagramTimeout)
		defer cancel()
		live, err := s.d.WS.Connect(ctx, connID)
		var db *model.Database
		if err == nil {
			db, err = app.Snapshot(ctx, live.Source, databaseOf(ref))
		}
		s.d.Run(func() {
			if t.ctx.Err() != nil {
				return
			}
			if err != nil {
				s.tabFailed(t, fmt.Errorf("could not read the schema: %w", err))
				return
			}
			s.showDiagram(t, db, opts(ref))
		})
	}()
	return t
}

// opts is what to draw, from the node somebody asked from: a schema draws
// itself, and a database draws everything in it.
func opts(ref model.ObjectRef) erd.Options {
	if ref.Kind == model.KindSchema {
		return erd.Options{Schemas: []string{ref.Name()}}
	}
	return erd.Options{}
}

// showDiagram draws the schema and puts back whatever was arranged.
func (s *Shell) showDiagram(t *tab, db *model.Database, opt erd.Options) {
	p := &diagramPanel{s: s, t: t, key: t.ref.String(), db: db, opt: opt, degree: 1}
	t.diagram = p
	p.draw()
}

// draw builds the picture from the schema already read, which is what makes
// focusing instant and what keeps it the same schema.
func (p *diagramPanel) draw() {
	s, t := p.s, p.t
	opt := p.opt
	if p.focus != "" {
		// A diagram of two hundred tables is a ball of string. One of the
		// tables within a few relationships of the one somebody is looking
		// at is a diagram (FR-8.5).
		opt.Tables = erd.Neighbourhood(p.db, []string{p.focus}, p.degree)
	}
	d := erd.FromSchema(p.db, opt)

	// What was arranged goes on before the layout, not after, so that
	// everything else is arranged around it rather than laid out once and
	// then talked over. One arrangement serves every focus of a diagram, so
	// a table keeps its place when the picture narrows around it.
	kept, ok := p.arrangement()
	if ok {
		app.ApplyLayout(d.Graph, kept)
	}
	canvas.Layout(d.Graph, canvas.DefaultLayout())

	p.w = diagram.New(d.Graph, s.colours())
	p.w.OnMoved = func() { p.keep() }
	p.w.OnSelect = func(id string) { p.chose(d, id) }

	t.body.Objects = []fyne.CanvasObject{
		container.NewBorder(p.toolbar(), nil, nil, nil, p.w),
	}
	t.body.Refresh()
	if p.focus != "" || !ok || !app.RestoreView(p.w.View(), kept) {
		// A narrowed diagram is fitted: the view kept was of a larger
		// picture, and showing a corner of it would look like a failure.
		p.w.Fit()
	}
	p.say(d, "")
}

// chose is a box being selected, which is what makes focusing on it
// possible: the starting point of a neighbourhood is the thing somebody is
// already looking at.
func (p *diagramPanel) chose(d erd.Diagram, id string) {
	p.say(d, id)
	p.refreshFocus(id)
}

// refreshFocus turns the focus controls on and off and names what they would
// do.
func (p *diagramPanel) refreshFocus(chosen string) {
	if p.chosen == nil {
		return
	}
	switch {
	case chosen != "":
		p.chosen.SetText("Focus on " + chosen)
		p.chosen.Enable()
	default:
		p.chosen.SetText("Focus on a Table")
		p.chosen.Disable()
	}
	p.wider.Selected = degreeNames[p.degree]
	p.wider.Refresh()
	if p.focus == "" {
		p.all.Disable()
	} else {
		p.all.Enable()
	}
}

// degreeNames are how far out a focused diagram reaches, in words: "one
// relationship away" says what it does and a bare number does not.
var degreeNames = map[int]string{
	0: "Just that table",
	1: "1 relationship away",
	2: "2 relationships away",
	3: "3 relationships away",
}

func degreeOf(name string) int {
	for n, s := range degreeNames {
		if s == name {
			return n
		}
	}
	return 1
}

// focusOn narrows the diagram to a table and what is near it.
func (p *diagramPanel) focusOn(id string) {
	if id == "" {
		return
	}
	p.focus = id
	p.draw()
}

// showEverything widens the diagram back to the whole schema.
func (p *diagramPanel) showEverything() {
	p.focus = ""
	p.draw()
}

// toolbar is the few things a diagram can be told to do.
func (p *diagramPanel) toolbar() fyne.CanvasObject {
	p.chosen = widget.NewButton("Focus on a Table", func() { p.focusOn(p.w.Selected()) })
	p.all = widget.NewButton("Show Everything", p.showEverything)
	names := make([]string, 0, len(degreeNames))
	for n := range degreeNames {
		names = append(names, degreeNames[n])
	}
	slices.Sort(names)
	p.wider = widget.NewSelect(names, func(name string) {
		p.degree = degreeOf(name)
		if p.focus != "" {
			p.draw()
		}
	})
	p.refreshFocus(p.w.Selected())

	return container.NewHBox(
		widget.NewButton("Fit", func() { p.w.Fit() }),
		widget.NewButton("−", func() { p.w.Zoom(false) }),
		widget.NewButton("+", func() { p.w.Zoom(true) }),
		p.chosen, p.wider, p.all,
		widget.NewButton("Lay Out Again", p.layOutAgain),
		widget.NewButton("Export…", p.export),
	)
}

// say tells the footer what is there, and what was chosen.
func (p *diagramPanel) say(d erd.Diagram, chosen string) {
	said := fmt.Sprintf("%s, %s.", nounCount(len(d.Graph.Nodes), "table"),
		nounCount(len(d.Graph.Edges), "relationship"))
	if d.Outside > 0 {
		// A table whose keys lead off the diagram would otherwise look
		// unrelated.
		said += fmt.Sprintf(" %s lead outside what is drawn.",
			nounCount(d.Outside, "relationship"))
	}
	if p.focus != "" {
		said = "Around " + p.focus + ", " + degreeNames[p.degree] + ". " + said
	}
	if chosen != "" {
		said = chosen + " — " + said
	}
	p.t.footer.SetText(said)
}

// keep writes the arrangement, which happens when a drag ends.
func (p *diagramPanel) keep() {
	if p.s.d.Layouts == nil {
		return
	}
	l := app.LayoutOf(p.w.Graph(), p.w.View())
	go func() {
		ctx, cancel := context.WithTimeout(p.s.ctx, storeTimeout)
		defer cancel()
		if err := p.s.d.Layouts.PutLayout(ctx, p.t.connID, p.key, l); err != nil {
			p.s.d.Log.Warn("keeping a diagram's arrangement", "err", err)
		}
	}()
}

// arrangement is what was kept, if anything.
func (p *diagramPanel) arrangement() (localdb.DiagramLayout, bool) {
	if p.s.d.Layouts == nil {
		return localdb.DiagramLayout{}, false
	}
	ctx, cancel := context.WithTimeout(p.s.ctx, storeTimeout)
	defer cancel()
	l, ok, err := p.s.d.Layouts.Layout(ctx, p.t.connID, p.key)
	if err != nil {
		p.s.d.Log.Warn("reading a diagram's arrangement", "err", err)
		return localdb.DiagramLayout{}, false
	}
	return l, ok
}

// layOutAgain throws away the arrangement and lays the diagram out afresh,
// which is the way back from having moved things into a mess.
func (p *diagramPanel) layOutAgain() {
	for i := range p.w.Graph().Nodes {
		p.w.Graph().Nodes[i].Pinned = false
	}
	canvas.Layout(p.w.Graph(), canvas.DefaultLayout())
	p.w.Fit()
	if p.s.d.Layouts == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(p.s.ctx, storeTimeout)
		defer cancel()
		if err := p.s.d.Layouts.ForgetLayout(ctx, p.t.connID, p.key); err != nil {
			p.s.d.Log.Warn("forgetting a diagram's arrangement", "err", err)
		}
	}()
}

// export writes the diagram as a picture (FR-8.4).
//
// The format follows the name: somebody who types .svg means SVG, and asking
// again in a second dialog would be asking a question they have answered.
func (p *diagramPanel) export() {
	p.s.d.Files.Save(p.s.win, filedlg.Options{
		Message:    "Export " + p.t.label,
		Name:       diagramFileName(p.t.label),
		Extensions: []string{"png", "svg"},
		Kind:       "picture",
		Accept:     "Export",
	}, func(path string, err error) {
		switch {
		case err != nil:
			p.s.showError(fmt.Errorf("could not export the diagram: %w", err))
		case path == "":
			// Cancelled, which is an answer.
		default:
			p.write(path)
		}
	})
}

// write puts the picture in a file, in the format its name asks for.
//
// Everything that can go wrong comes back one way, so there is one place
// that says so and one that says it worked.
func (p *diagramPanel) write(path string) {
	if err := p.encode(path); err != nil {
		p.s.showError(fmt.Errorf("could not export the diagram: %w", err))
		return
	}
	p.t.footer.SetText("Exported to " + filepath.Base(path) + ".")
}

// encode writes the file, in the format the name asks for.
func (p *diagramPanel) encode(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if strings.EqualFold(filepath.Ext(path), ".svg") {
		err = p.w.SVG(f, exportZoom)
	} else {
		err = p.w.PNG(f, exportZoom, p.s.d.Theme)
	}
	if err != nil {
		return err
	}
	return f.Sync()
}

// exportZoom is the scale an exported diagram is drawn at: its natural size,
// where a box is as large as it is on screen at zoom 1 and every label is
// legible. A picture too large for that is drawn smaller by the exporter.
const exportZoom = 1.0

// diagramFileName is what an exported diagram is called by default.
func diagramFileName(label string) string {
	name := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' {
			return '-'
		}
		return r
	}, label)
	return name + ".png"
}
