package acp

import (
	"context"
	"sync"

	"github.com/nisimpson/bond"
)

// Session holds the conversation state for a single ACP session.
type Session struct {
	ID      string
	CWD     string
	History []bond.Message

	// Runtime state for active prompt turn
	promptCancel context.CancelFunc
	promptActive bool
	mu           sync.Mutex
}

// newSession creates a session with the given id and working directory.
func newSession(id, cwd string) *Session {
	return &Session{
		ID:      id,
		CWD:     cwd,
		History: make([]bond.Message, 0),
	}
}

// close cancels any active prompt on this session.
func (s *Session) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.promptCancel != nil {
		s.promptCancel()
		s.promptCancel = nil
	}
	s.promptActive = false
}
