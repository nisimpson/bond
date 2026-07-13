// Package session provides persistent conversation state management for bond agents.
//
// The session package enables stateful conversations by persisting message history
// across requests. It defines the [SessionStore] interface for storage backends and
// provides an [InMemoryStore] for development and testing.
//
// # Storage Backends
//
// The [SessionStore] interface abstracts the persistence layer. Implementations include:
//
//   - [InMemoryStore]: Thread-safe, in-process store for development and testing.
//   - dynamostore.Store: DynamoDB-backed store for production (in sub-package).
//
// # Usage with SessionPlugin
//
// The [SessionPlugin] registers hooks to automatically load and save conversation
// history, enabling stateful multi-turn conversations without modifying the agent loop:
//
//	store := session.NewInMemoryStore()
//	plugin := session.NewSessionPlugin(session.SessionPluginOptions{
//	    Store:    store,
//	    Resolver: func(ctx context.Context) string { return "user-123" },
//	})
//
//	bond.Stream(ctx, agent, messages, bond.AgentOptions{
//	    Plugins: []bond.Plugin{plugin},
//	})
//
// # Deep Copy Semantics
//
// All store implementations must deep-copy messages on Save and Load to prevent
// aliasing between the caller's data and the stored data. The [InMemoryStore]
// implements this guarantee.
package session
