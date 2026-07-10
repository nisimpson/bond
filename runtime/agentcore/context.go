package agentcore

import (
	"context"
	"net/http"
)

// Standard AgentCore runtime headers.
const (
	// HeaderSessionID is the platform-injected session identifier.
	HeaderSessionID = "X-Amzn-Bedrock-AgentCore-Runtime-Session-Id"
	// HeaderCustomPrefix is the prefix for custom runtime headers.
	HeaderCustomPrefix = "X-Amzn-Bedrock-AgentCore-Runtime-Custom-"
)

// Session holds AgentCore runtime metadata extracted from request headers.
type Session struct {
	// SessionID is the platform-managed session identifier.
	SessionID string
	// Headers contains all request headers (for custom header access).
	Headers http.Header
}

// CustomHeader returns the value of a custom AgentCore header by suffix.
// For example, CustomHeader("UserId") returns the value of
// "X-Amzn-Bedrock-AgentCore-Runtime-Custom-UserId".
func (s *Session) CustomHeader(name string) string {
	return s.Headers.Get(HeaderCustomPrefix + name)
}

// Header returns the value of any request header by full name.
func (s *Session) Header(name string) string {
	return s.Headers.Get(name)
}

// sessionContextKey is the context key for Session.
type sessionContextKey struct{}

// SessionFromContext retrieves the AgentCore Session from ctx.
// Returns nil if not running inside an AgentCore handler.
func SessionFromContext(ctx context.Context) *Session {
	s, _ := ctx.Value(sessionContextKey{}).(Session)
	return &s
}

// requestContextMiddleware extracts AgentCore headers and injects the
// Session into context for downstream access.
func requestContextMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session := Session{
			SessionID: r.Header.Get(HeaderSessionID),
			Headers:   r.Header,
		}
		ctx := context.WithValue(r.Context(), sessionContextKey{}, session)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
