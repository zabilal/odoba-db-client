package shell

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	d := erd.FromSchema(db, opt)
	p := &diagramPanel{s: s, t: t, key: t.ref.String()}
	t.diagram = p

	// What was arranged goes on before the layout, not after, so that
	// everything else is arranged around it rather than laid out once and
	// then talked over.
	kept, ok := p.arrangement()
	if ok {
		app.ApplyLayout(d.Graph, kept)
	}
	canvas.Layout(d.Graph, canvas.DefaultLayout())

	p.w = diagram.New(d.Graph, s.colours())
	p.w.OnMoved = func() { p.keep() }
	p.w.OnSelect = func(id string) { p.say(d, id) }

	t.body.Objects = []fyne.CanvasObject{
		container.NewBorder(p.toolbar(), nil, nil, nil, p.w),
	}
	t.body.Refresh()
	if !ok || !app.RestoreView(p.w.View(), kept) {
		p.w.Fit()
	}
	p.say(d, "")
}

// toolbar is the few things a diagram can be told to do.
func (p *diagramPanel) toolbar() fyne.CanvasObject {
	return container.NewHBox(
		widget.NewButton("Fit", func() { p.w.Fit() }),
		widget.NewButton("−", func() { p.w.Zoom(false) }),
		widget.NewButton("+", func() { p.w.Zoom(true) }),
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
