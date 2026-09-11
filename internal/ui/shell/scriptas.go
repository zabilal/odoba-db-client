package shell

import (
	"context"
	"fmt"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
)

// scriptTimeout bounds describing an object to write a script for it.
const scriptTimeout = 30 * time.Second

// selectionIsTable reports whether the explorer's selection is a table: a
// view is written only as SELECT.
func (s *Shell) selectionIsTable() bool {
	_, n, ok := s.Explorer.SelectedNode()
	return ok && n.Browsable && n.Ref.Kind == model.KindTable
}

// scriptSelected writes a script for the explorer's selection.
func (s *Shell) scriptSelected(kind app.ScriptKind) {
	if conn, n, ok := s.Explorer.SelectedNode(); ok && n.Browsable {
		s.scriptAs(conn, n, kind)
	}
}

// scriptAs writes a statement for an object off the UI goroutine, then
// opens it in a new query tab as unsaved text: a starting point to edit and
// run, never run for the person (FR-2.4, ADR-0011 §15).
func (s *Shell) scriptAs(connID string, n model.Node, kind app.ScriptKind) {
	go func() {
		ctx, cancel := context.WithTimeout(s.ctx, scriptTimeout)
		defer cancel()
		live, err := s.d.WS.Connect(ctx, connID)
		var text string
		if err == nil {
			text, err = app.ScriptAs(ctx, live.Source, n.Ref, kind)
		}
		s.d.Run(func() {
			if s.ctx.Err() != nil {
				return
			}
			if err != nil {
				s.showError(fmt.Errorf("could not write the script for %s: %w", n.Label, err))
				s.crashed(connID, err)
				return
			}
			s.openScript(connID, text)
		})
	}()
}

// openScript opens text in a new query tab on a connection, unsaved, so
// that autosave keeps it like anything else typed there.
func (s *Shell) openScript(connID, text string) *tab {
	t := s.OpenQuery(connID)
	if t == nil {
		return nil
	}
	q := t.query
	q.editor.Document().SetText(text)
	q.dirty = true
	s.retitle(t)
	s.keep(t)
	q.editor.Refresh()
	return t
}
