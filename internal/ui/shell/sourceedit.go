package shell

import (
	"context"
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
	"github.com/ikigai-db/ikigai-db/internal/ui/editor"
	"github.com/ikigai-db/ikigai-db/internal/ui/editor/view"
)

// Editing the objects whose structure is their source (FR-6.5): views,
// procedures, functions and triggers.
//
// The designer's shape does not fit them. A view is its text, not a list of
// columns, and a function's body is a language this program does not parse —
// PL/pgSQL, or SQL, or Python. So what is edited is what the engine keeps
// and gives back, in the same editor a query is typed in, and what runs is
// rendered from it and read first like every other structural change
// (ADR-0115).
//
// A sequence is here too, and is the exception: it has no source at all, so
// what is shown is the statement that would set its numbers. Editing that
// statement is editing the sequence.

// sourceKinds are the objects this edits.
var sourceKinds = map[model.ObjectKind]bool{
	model.KindView:             true,
	model.KindMaterializedView: true,
	model.KindRoutine:          true,
	model.KindTrigger:          true,
	model.KindSequence:         true,
}

// sourcePanel is a source editor's state.
type sourcePanel struct {
	s   *Shell
	t   *tab
	ed  *view.Editor
	was string // the source as it was read, for saying whether it changed
	obj any    // what was described, which is what is rendered back
}

func sourceKey(connID string, ref model.ObjectRef) string {
	return "source:" + connID + ":" + ref.String()
}

// canEditSource reports whether the selection is an object whose source this
// can edit.
func (s *Shell) canEditSource() bool {
	_, n, ok := s.structureTarget()
	return ok && sourceKinds[n.Ref.Kind]
}

func (s *Shell) editSelectedSource() {
	if conn, n, ok := s.structureTarget(); ok && sourceKinds[n.Ref.Kind] {
		s.OpenSource(conn, n)
	}
}

// OpenSource opens an object's source for editing, or brings the tab already
// on it forward.
func (s *Shell) OpenSource(connID string, n model.Node) *tab {
	key := sourceKey(connID, n.Ref)
	if t := s.tabFor(key); t != nil {
		s.selectTab(t)
		return t
	}
	ctx, cancel := context.WithCancel(s.ctx)
	t := &tab{key: key, connID: connID, ref: n.Ref, label: n.Label, structure: true, ctx: ctx, cancel: cancel,
		body: container.NewStack(quiet("Reading the source…")), footer: widget.NewLabel("")}
	t.footer.Importance = widget.LowImportance
	t.item = container.NewTabItem("Source: "+n.Label, container.NewBorder(nil, t.footer, nil, nil, t.body))
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
				return
			}
			if err != nil {
				s.tabFailed(t, fmt.Errorf("could not read the source: %w", err))
				s.crashed(connID, err)
				return
			}
			s.showSource(t, desc)
		})
	}()
	return t
}

// showSource draws the editor over what was described.
func (s *Shell) showSource(t *tab, desc any) {
	text, err := sourceOf(s, t, desc)
	if err != nil {
		s.tabFailed(t, err)
		return
	}
	p := &sourcePanel{s: s, t: t, obj: desc, was: text}
	t.source = p
	p.ed = view.New(editor.NewDocument(text, sqllex.DialectFor(dialectName(s, t.connID))), s.colours())
	p.ed.OnChanged = func() { p.say() }

	preview := widget.NewButton("Preview…", p.preview)
	revert := widget.NewButton("Revert", func() {
		p.ed.Document().SetText(p.was)
		p.say()
	})
	t.body.Objects = []fyne.CanvasObject{
		container.NewBorder(container.NewHBox(preview, revert), nil, nil, nil, p.ed),
	}
	p.say()
	t.body.Refresh()
}

// sourceOf is the text to edit, which for everything but a sequence is what
// the engine gave back.
func sourceOf(s *Shell, t *tab, desc any) (string, error) {
	switch v := desc.(type) {
	case *model.View:
		return v.Definition, nil
	case *model.Routine:
		return v.Definition, nil
	case *model.Trigger:
		return v.Definition, nil
	case *model.Sequence:
		// A sequence has no source, so what is shown is what would set it.
		live, ok := s.d.WS.Get(t.connID)
		if !ok {
			return "", fmt.Errorf("this connection is not open")
		}
		stmts, err := app.PlanSource(live.Source, t.ref, v)
		if err != nil {
			return "", err
		}
		return scriptOf(stmts), nil
	}
	return "", fmt.Errorf("%T has no source to edit", desc)
}

// preview renders what the edited source would run.
func (p *sourcePanel) preview() {
	live, ok := p.s.d.WS.Get(p.t.connID)
	if !ok {
		p.t.footer.SetText("This connection is not open.")
		return
	}
	var stmts []source.Statement
	var err error
	if _, isSequence := p.obj.(*model.Sequence); isSequence {
		// A sequence's editor holds the statements themselves, because a
		// sequence has no other source. What was edited is what runs.
		stmts = app.SplitStatements(live.Source, p.ed.Document().Text())
	} else {
		stmts, err = app.PlanSource(live.Source, p.t.ref, p.edited())
	}
	if err != nil {
		p.s.showError(err)
		return
	}
	if len(stmts) == 0 {
		p.t.footer.SetText("There is nothing here to run.")
		return
	}
	p.s.previewDDL(p.t.ddl(), stmts, func() { p.s.reopenSource(p.t) })
}

// edited is what was described, with the source as it now reads.
func (p *sourcePanel) edited() any {
	text := p.ed.Document().Text()
	switch v := p.obj.(type) {
	case *model.View:
		out := *v
		out.Definition = text
		return &out
	case *model.Routine:
		out := *v
		out.Definition = text
		return &out
	case *model.Trigger:
		out := *v
		out.Definition = text
		return &out
	}
	return p.obj
}

// say tells the footer whether anything has changed.
func (p *sourcePanel) say() {
	if p.ed.Document().Text() == p.was {
		p.t.footer.SetText("No changes yet.")
		return
	}
	p.t.footer.SetText("Changed. Preview to see what would run.")
}

// reopenSource reads the object again, after something changed it.
func (s *Shell) reopenSource(t *tab) {
	t.body.Objects = []fyne.CanvasObject{quiet("Reading the source…")}
	t.body.Refresh()
	go func() {
		live, err := s.d.WS.Connect(t.ctx, t.connID)
		var desc any
		if err == nil {
			desc, err = app.Describe(t.ctx, live.Source, t.ref)
		}
		s.d.Run(func() {
			if t.ctx.Err() != nil {
				return
			}
			if err != nil {
				s.tabFailed(t, fmt.Errorf("could not read the source again: %w", err))
				return
			}
			s.showSource(t, desc)
		})
	}()
}

// dialectName is what to highlight the source as, which is the connection's
// own language.
func dialectName(s *Shell, connID string) string {
	c, ok := s.d.Conns.Get(connID)
	if !ok {
		return ""
	}
	return c.Driver
}
