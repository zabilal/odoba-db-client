package shell

import (
	"time"

	"fyne.io/fyne/v2"
)

// Native notifications (FR-15.5, ADR-0023). Long work that ends while Ikigai
// DB is in the background says so among the system's notifications. In
// front, the window says it already, and a notification would only repeat
// it. A notification carries no statement and no error message: it can be
// read over a shoulder, and stays in the system's history, so it says only
// that the work ended, and how.

// notifyAfter is how long a query must have run for its end to be told. A
// variable, so that a test can change it.
var notifyAfter = 10 * time.Second

// notify sends a notification, if the app is in the background.
func (s *Shell) notify(title, body string) {
	if s.away {
		s.app.SendNotification(fyne.NewNotification(title, body))
	}
}

// notifyTask tells how a task ended. A cancelled one is not told: whoever
// stopped it knows.
func (s *Shell) notifyTask(k *task) {
	switch k.state {
	case taskDone:
		s.notify(k.title, k.status)
	case taskFailed:
		s.notify(k.title, "Failed. The Tasks panel says why.")
	}
}
