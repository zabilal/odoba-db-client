package shell

import (
	"context"
	"fmt"
	"os"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/ui/filedlg"
)

// Backing up everything the application keeps, and putting it back
// (FR-17.5).
//
// What the archive holds and what it does not is said in the window as
// well as in the archive, because "back up my data" and "back up my
// passwords" are the same sentence to most people and these are not the
// same thing.

// backupTimeout bounds writing the archive, which is a copy of a local
// database and a small file.
const backupTimeout = 2 * time.Minute

// canBackUp reports whether there is anywhere to back up from.
func (s *Shell) canBackUp() bool { return s.d.Backup != nil }

// backUp asks where the archive goes and writes it.
func (s *Shell) backUp() {
	if !s.canBackUp() {
		return
	}
	s.d.Files.Save(s.win, filedlg.Options{
		Message:    "Back up everything Ikigai DB keeps",
		Name:       "ikigai-backup-" + time.Now().Format("2006-01-02") + ".zip",
		Extensions: []string{"zip"},
		Kind:       "backup archive",
		Accept:     "Back Up",
	}, func(path string, err error) {
		switch {
		case err != nil:
			s.showError(fmt.Errorf("could not choose where the backup goes: %w", err))
		case path == "":
			// Cancelled, which is an answer and not a failure.
		default:
			s.backUpTo(path)
		}
	})
}

// backUpTo writes the archive as a task: it copies a database, which is as
// long as the database is.
func (s *Shell) backUpTo(path string) {
	b := *s.d.Backup
	ctx, cancel := context.WithTimeout(s.ctx, backupTimeout)
	k := s.startTask(nil, "Backing up", cancel)
	go func() {
		defer cancel()
		err := writeBackup(ctx, b, path)
		s.d.Run(func() {
			if err != nil {
				_ = os.Remove(path) // a half-written archive looks like a whole one
				s.endTask(k, taskFailed, err.Error())
				s.showError(fmt.Errorf("the backup could not be written: %w", err))
				return
			}
			s.endTask(k, taskDone, "Backed up")
			s.status.SetText("Backed up. No passwords are in it: they are in the keychain.")
		})
	}()
}

func writeBackup(ctx context.Context, b app.Backup, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := b.Write(ctx, f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// canRestore reports whether there is anywhere to restore into.
func (s *Shell) canRestore() bool { return s.d.Backup != nil }

// restoreBackup asks which archive, says what putting it back means, and
// does it.
func (s *Shell) restoreBackup() {
	if !s.canRestore() {
		return
	}
	s.d.Files.Open(s.win, filedlg.Options{
		Message:    "Restore everything from a backup",
		Extensions: []string{"zip"},
		Kind:       "backup archive",
		Accept:     "Restore",
	}, func(path string, err error) {
		switch {
		case err != nil:
			s.showError(fmt.Errorf("could not choose the backup: %w", err))
		case path == "":
		default:
			s.confirmRestore(path)
		}
	})
}

// confirmRestore says what is about to be replaced, and by what.
func (s *Shell) confirmRestore(path string) {
	man, err := aboutBackup(path)
	if err != nil {
		s.showError(fmt.Errorf("that backup could not be read: %w", err))
		return
	}
	said := fmt.Sprintf("This backup was written on %s. Restoring it replaces every connection, "+
		"saved query and piece of history on this machine with the ones in it, and the application closes so that "+
		"it starts again from what was restored.\n\nWhat is here now is kept beside it, so this can be undone.\n\n%s",
		man.Written.Local().Format("2 January 2006 at 15:04"), man.Secrets)
	d := dialog.NewConfirm("Restore from this backup?", said, func(yes bool) {
		if yes {
			s.restoreFrom(path)
		}
	}, s.win)
	d.SetConfirmText("Restore")
	d.SetDismissText("Cancel")
	d.SetConfirmImportance(widget.DangerImportance)
	d.Resize(fyne.NewSize(520, d.MinSize().Height))
	d.Show()
}

// aboutBackup reads what an archive says about itself.
func aboutBackup(path string) (app.Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return app.Manifest{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return app.Manifest{}, err
	}
	return app.About(f, st.Size())
}

// restoreFrom puts the archive back and ends the session.
func (s *Shell) restoreFrom(path string) {
	b := *s.d.Backup
	f, err := os.Open(path)
	if err != nil {
		s.showError(fmt.Errorf("that backup could not be read: %w", err))
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		s.showError(fmt.Errorf("that backup could not be read: %w", err))
		return
	}
	// Nothing more of this session is written from here: the database it
	// has been writing into is about to be another database.
	s.stopWriting()
	if _, err := b.Restore(f, st.Size()); err != nil {
		s.showError(fmt.Errorf("the backup could not be put back: %w", err))
		return
	}
	s.status.SetText("Restored. The application is closing so that it starts again from what was restored.")
	s.win.Close()
}

// stopWriting ends the background writer and forgets it, so that nothing
// this session was keeping is written into a database that has been
// replaced. Everything that writes asks whether there is a writer first.
func (s *Shell) stopWriting() {
	if s.writer == nil {
		return
	}
	s.writer.close(shutdownWait)
	s.writer = nil
}
