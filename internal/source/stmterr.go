package source

import "github.com/ikigai-db/ikigai-db/internal/redact"

// StatementError is a failure the server reported for one statement. It
// carries the Message — the engine's code and the character position — that
// the editor needs to underline the offending token (FR-5.10).
type StatementError struct {
	Message Message
	Detail  string
	Hint    string
	Err     error
}

func (e *StatementError) Error() string {
	s := e.Message.Text
	if e.Message.Code != "" {
		s = e.Message.Code + ": " + s
	}
	return redact.String(s)
}

func (e *StatementError) Unwrap() error { return e.Err }
