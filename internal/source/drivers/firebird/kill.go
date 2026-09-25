package firebird

import (
	"context"
	"fmt"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Cancelling a running statement (FR-5.5).
//
// The library speaks Firebird's op_cancel when the statement's context is
// done, which is a real cancellation on the server rather than a client
// looking away: the engine stops the request. So the handle names the session
// this driver holds and cancelling it cancels the context the statement is
// running under, and there is no second connection to open.
//
// Firebird has another way — deleting a row of MON$STATEMENTS, which asks the
// engine to cancel somebody's statement — and it is deliberately not used
// here. It needs the privileges to see other people's attachments, it cancels
// by a server-side id this side would have to go and look up, and for a
// statement of one's own it does the same thing the protocol already did.

var _ source.Killer = (*firebirdSource)(nil)

func (s *firebirdSource) KillQuery(_ context.Context, handle string) error {
	s.mu.Lock()
	ss, ok := s.sessions[handle]
	s.mu.Unlock()
	if !ok {
		// The session has already gone, which means the statement has too.
		// Saying so rather than reporting success: a caller that asked to
		// stop something is entitled to know it was not there to stop.
		return fmt.Errorf("firebird: there is no session %q to cancel", handle)
	}
	ss.kill()
	return nil
}
