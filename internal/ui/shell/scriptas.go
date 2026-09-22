package shell

import (
	"context"
	"fmt"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
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

// Writing an object, or a whole schema, as the DDL that would build it
// (FR-6.7).
//
// It opens in a query tab as unsaved text, like every other Script As: a
// starting point to read, keep or edit, never something run for somebody.
// That is also why it does not go through the DDL preview — there is
// nothing to preview, because nothing is going to run.

// createKinds are the objects this can write a CREATE for.
var createKinds = map[model.ObjectKind]bool{
	model.KindTable:            true,
	model.KindView:             true,
	model.KindMaterializedView: true,
	model.KindRoutine:          true,
	model.KindTrigger:          true,
	model.KindSequence:         true,
}

// canScriptCreate reports whether the selection is an object this connection
// could write DDL for.
func (s *Shell) canScriptCreate() bool {
	conn, n, ok := s.structureTarget()
	if !ok || !createKinds[n.Ref.Kind] {
		return false
	}
	live, open := s.d.WS.Get(conn)
	return open && app.CanRenderDDL(live.Source)
}

func (s *Shell) scriptSelectedCreate() {
	if conn, n, ok := s.structureTarget(); ok && createKinds[n.Ref.Kind] {
		s.writeDDL(conn, n.Label, func(ctx context.Context, src source.Source) ([]source.Statement, error) {
			return app.ScriptCreate(ctx, src, n.Ref)
		}, "")
	}
}

// holdsAClass are the nodes a whole-schema script can be asked for: the node
// the objects are listed under.
//
// A database is one of them because not every engine has a schema level.
// PostgreSQL puts the tables under a schema; MySQL and SQLite put them
// straight under the database, and the objects are the same objects.
var holdsAClass = map[model.ObjectKind]bool{
	model.KindSchema:   true,
	model.KindDatabase: true,
}

// canScriptSchema reports whether a whole schema is selected.
func (s *Shell) canScriptSchema() bool {
	conn, n, ok := s.Explorer.SelectedNode()
	if !ok || !holdsAClass[n.Ref.Kind] {
		return false
	}
	live, open := s.d.WS.Get(conn)
	return open && app.CanRenderDDL(live.Source)
}

func (s *Shell) scriptSelectedSchema() {
	conn, n, ok := s.Explorer.SelectedNode()
	if !ok || !holdsAClass[n.Ref.Kind] {
		return
	}
	s.writeDDL(conn, n.Label, func(ctx context.Context, src source.Source) ([]source.Statement, error) {
		return app.ScriptSchema(ctx, src, n.Ref)
	}, schemaHeader(n.Label))
}

// schemaHeader says why the script is in the order it is in, because that
// order is the only part of it somebody cannot see for themselves.
func schemaHeader(name string) string {
	return "-- " + name + ", in an order this can be run in.\n" +
		"-- Foreign keys are added after the tables, because two tables can refer\n" +
		"-- to each other and no order of CREATE TABLE would satisfy that.\n" +
		"-- Nothing here has run. This is a script to read, keep or edit.\n"
}

// writeDDL renders off the UI goroutine and opens the result as a script.
func (s *Shell) writeDDL(connID, label string, render func(context.Context, source.Source) ([]source.Statement, error), header string) {
	s.status.SetText("Writing the DDL for " + label + "…")
	go func() {
		ctx, cancel := context.WithTimeout(s.ctx, scriptTimeout)
		defer cancel()
		live, err := s.d.WS.Connect(ctx, connID)
		var stmts []source.Statement
		if err == nil {
			stmts, err = render(ctx, live.Source)
		}
		s.d.Run(func() {
			if s.ctx.Err() != nil {
				return
			}
			if err != nil {
				s.status.SetText("")
				s.showError(fmt.Errorf("could not write the DDL for %s: %w", label, err))
				s.crashed(connID, err)
				return
			}
			if len(stmts) == 0 {
				s.status.SetText("There is nothing in " + label + " to write.")
				return
			}
			// Nothing is said in the status line afterwards: opening the
			// tab redraws it as the connection's own, so anything put
			// there would be written and wiped in the same instant. What
			// the script is, and that it has not run, is in the script.
			s.openScript(connID, header+scriptOf(stmts))
		})
	}()
}
