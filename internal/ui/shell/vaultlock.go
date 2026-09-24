package shell

import (
	"errors"
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
)

// The app-level lock over the credential vault, in the window (NFR-S7).
//
// What it costs is said wherever it is offered, because it is the kind of
// thing somebody turns on once and finds out about later: a passphrase
// nobody remembers is secrets nobody can read, and the only way on is to
// type them again.

// canLockVault reports whether a lock could be put on or changed.
func (s *Shell) canLockVault() bool { return s.d.Conns.VaultOpen() }

// canCloseVault reports whether there is an open lock to shut.
func (s *Shell) canCloseVault() bool { return s.d.Conns.VaultLocked() && s.d.Conns.VaultOpen() }

// canRemoveVaultLock reports whether there is a lock to take off. A
// portable install's cannot come off: its passwords live in a file beside
// the application, which is only ever written sealed (ADR-0154).
func (s *Shell) canRemoveVaultLock() bool {
	return s.d.Conns.VaultLocked() && !s.d.Conns.Vault().Portable()
}

// lockVault asks for a passphrase, twice, and seals the vault with it.
func (s *Shell) lockVault() {
	if !s.canLockVault() {
		return
	}
	title, act := "Lock the Vault", "Lock"
	said := "Every saved password is stored sealed with this passphrase, and the application asks for it once each start. " +
		"Nothing keeps it for you: a passphrase nobody remembers is passwords nobody can read, and the way on is to type them again."
	if s.d.Conns.VaultLocked() {
		title, act = "Change the Vault Passphrase", "Change"
		said = "Every saved password is sealed again with the new passphrase. The old one stops working."
	} else if s.d.Conns.Vault().Portable() {
		said = "This copy keeps everything beside itself, and a password beside the application is only worth keeping sealed. " +
			"Until there is a passphrase, each password is asked for once a session and kept no longer. " +
			"Nothing keeps the passphrase for you: one nobody remembers is passwords nobody can read."
	}
	pass, again := newSecretEntry(), newSecretEntry()
	pass.Validator = func(text string) error {
		if len(text) < 8 {
			return errors.New("a passphrase of at least eight characters")
		}
		return nil
	}
	again.Validator = func(text string) error {
		if text != pass.Text {
			return errors.New("the two do not match")
		}
		return nil
	}
	pass.OnChanged = func(string) { again.Validate() }
	note := quiet(said)
	d := dialog.NewForm(title, act, "Cancel", []*widget.FormItem{
		{Text: "Passphrase", Widget: pass},
		{Text: "Again", Widget: again},
		{Widget: note},
	}, func(ok bool) {
		if ok {
			s.setVaultPassphrase(strings.TrimSpace(pass.Text))
		}
	}, s.win)
	d.Resize(fyne.NewSize(520, d.MinSize().Height))
	d.Show()
}

// newSecretEntry is a field for something nobody should read over a
// shoulder.
func newSecretEntry() *widget.Entry {
	e := widget.NewPasswordEntry()
	return e
}

// setVaultPassphrase seals the vault, off the UI goroutine: deriving a key
// is meant to take a moment, and every secret is written again.
func (s *Shell) setVaultPassphrase(pass string) {
	conns := s.d.Conns
	k := s.startTask(nil, "Sealing the saved passwords", nil)
	go func() {
		err := conns.LockVault(pass)
		s.d.Run(func() {
			if err != nil {
				s.endTask(k, taskFailed, err.Error())
				s.showError(fmt.Errorf("the vault could not be locked: %w", err))
				return
			}
			s.endTask(k, taskDone, "Sealed")
			s.status.SetText("The vault is locked. It asks for the passphrase at each start.")
			s.sync()
		})
	}()
}

// closeVault shuts the lock now, without waiting for a restart.
func (s *Shell) closeVault() {
	if !s.canCloseVault() {
		return
	}
	s.d.Conns.CloseVault()
	s.status.SetText("The vault is shut. Saved passwords are held back until the passphrase is typed.")
	s.sync()
}

// removeVaultLock asks for the passphrase and takes the lock off.
func (s *Shell) removeVaultLock() {
	if !s.canRemoveVaultLock() {
		return
	}
	pass := newSecretEntry()
	d := dialog.NewForm("Remove the Vault Lock", "Remove", "Cancel", []*widget.FormItem{
		{Text: "Passphrase", Widget: pass},
		{Widget: quiet("Every saved password goes back to being kept by the operating system's keychain alone, " +
			"which anything running as you can read.")},
	}, func(ok bool) {
		if !ok {
			return
		}
		s.takeVaultLockOff(pass.Text)
	}, s.win)
	d.Resize(fyne.NewSize(520, d.MinSize().Height))
	d.Show()
}

func (s *Shell) takeVaultLockOff(pass string) {
	conns := s.d.Conns
	k := s.startTask(nil, "Taking the lock off the saved passwords", nil)
	go func() {
		err := conns.RemoveVaultLock(pass)
		s.d.Run(func() {
			switch {
			case errors.Is(err, app.ErrPassphrase):
				s.endTask(k, taskFailed, "That is not the passphrase")
				s.showError(formError("That is not the passphrase."))
			case err != nil:
				s.endTask(k, taskFailed, err.Error())
				s.showError(fmt.Errorf("the lock could not be taken off: %w", err))
			default:
				s.endTask(k, taskDone, "The lock is off")
				s.status.SetText("The vault's lock is off.")
			}
			s.sync()
		})
	}()
}

// askToUnlock puts the unlock sheet up where the vault is shut, and calls
// then once it is open or once somebody has said to go on without it.
//
// Nothing else happens until it is answered: the tabs of the last session
// reopen onto connections whose passwords are in the vault, and reopening
// them first would be a window full of connections that failed for a
// reason nobody had been asked about yet.
func (s *Shell) askToUnlock(then func()) {
	if !s.d.Conns.VaultLocked() || s.d.Conns.VaultOpen() {
		then()
		return
	}
	pass := newSecretEntry()
	said := widget.NewLabel("")
	said.Wrapping = fyne.TextWrapWord
	said.Importance = widget.LowImportance
	var d *dialog.CustomDialog
	unlock := widget.NewButton("Unlock", nil)
	unlock.Importance = widget.HighImportance
	unlock.OnTapped = func() {
		if err := s.d.Conns.UnlockVault(pass.Text); err != nil {
			said.SetText("That is not the passphrase.")
			pass.SetText("")
			s.win.Canvas().Focus(pass)
			return
		}
		d.Hide()
		s.sync()
		then()
	}
	pass.OnSubmitted = func(string) { unlock.OnTapped() }
	on := widget.NewButton("Go on Without It", func() {
		d.Hide()
		s.status.SetText("The vault is shut: saved passwords are held back until it is unlocked.")
		s.sync()
		then()
	})
	body := container.NewVBox(
		quiet("The saved passwords are sealed. Type the passphrase to unlock them, or go on and type each password as it is needed."),
		pass, said)
	d = dialog.NewCustomWithoutButtons("Unlock the Vault", body, s.win)
	d.SetButtons([]fyne.CanvasObject{on, unlock})
	d.Resize(fyne.NewSize(520, d.MinSize().Height))
	d.Show()
	s.win.Canvas().Focus(pass)
}
