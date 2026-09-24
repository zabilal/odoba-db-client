package shell

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/filedlg"
)

// Saving a model: the schema as it stands, written where version control
// can hold it (FR-7.6, ADR-0121).
//
// A model is what a comparison is against, and until now the only way to
// have one was to write it from somewhere else. This is the way in.

// canSaveModelSelected reports whether the selection is something to save.
// It is the same question a comparison asks, because a model is read back
// by one: what can be compared can be saved, and nothing else can be.
func (s *Shell) canSaveModelSelected() bool { return s.canCompareSelected() }

// saveModelSelected asks where the model should go and writes it there.
//
// A model is a tree of files rather than one, so what is chosen is where
// its database.json is to be, and the rest is written beside it. That is
// the same bargain a comparison strikes when it reads one: a file dialog
// chooses files, and a directory is the file's own.
func (s *Shell) saveModelSelected() {
	conn, n, ok := s.Explorer.SelectedNode()
	if !ok || !holdsAClass[n.Ref.Kind] {
		return
	}
	ref := n.Ref
	s.d.Files.Save(s.win, filedlg.Options{
		Message:    "Save " + ref.Name() + " as a model",
		Name:       modelFileName,
		Extensions: []string{"json"},
		Kind:       "saved model",
		Accept:     "Save",
	}, func(path string, err error) {
		switch {
		case err != nil:
			s.showError(fmt.Errorf("could not choose where to save the model: %w", err))
		case path == "":
			// Cancelled, which is an answer and not a failure.
		default:
			s.saveModelTo(conn, ref, filepath.Dir(path))
		}
	})
}

// modelFileName is the file a model is known by, and the one a comparison
// picks out to name the directory the model is in.
const modelFileName = "database.json"

// saveModelTo writes the model, off the UI goroutine.
//
// Reading a whole schema is as long as the schema is, so it is a task:
// somebody can watch it, and stop it, and what it wrote is said when it
// ends rather than guessed at.
func (s *Shell) saveModelTo(connID string, ref model.ObjectRef, dir string) {
	ctx, cancel := context.WithCancel(s.ctx)
	k := s.startTask(nil, "Saving "+ref.Name()+" as a model", cancel)
	go func() {
		defer cancel()
		live, err := s.d.WS.Connect(ctx, connID)
		if err == nil {
			err = app.SaveModel(ctx, live.Source, databaseOf(ref), dir)
		}
		s.d.Run(func() {
			switch {
			case errors.Is(err, context.Canceled):
				s.endTask(k, taskCancelled, "Cancelled; what it had written is still there")
			case err != nil:
				s.endTask(k, taskFailed, err.Error())
				s.showError(fmt.Errorf("could not save the model: %w", err))
			default:
				s.endTask(k, taskDone, "Saved to "+dir)
			}
		})
	}()
}
