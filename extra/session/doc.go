// Package session provides persistent conversation state management and
// conversation trimming strategies for bond agents.
//
// # Session Persistence
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
// # Conversation Trimming
//
// Large language models have finite context windows. As conversations grow,
// the accumulated messages may exceed the model's capacity, causing errors or
// degraded responses. The [ConversationManager] interface and concrete
// implementations trim message slices to fit within configured constraints
// while preserving conversation coherence.
//
// Choose a trimming strategy and pass it to the [TrimmingPlugin]:
//
//   - [SlidingWindowManager]: retains the last N user/assistant pairs.
//   - [TokenBudgetManager]: trims to fit within a token budget.
//
// Both strategies preserve system-level preamble messages and maintain
// the relative ordering of the original conversation.
//
// # Plugins
//
// The [SessionPlugin] registers hooks to automatically load and save conversation
// history, enabling stateful multi-turn conversations without modifying the agent loop:
//
//	store := session.NewInMemoryStore()
//	plugin := session.NewSessionPlugin(session.SessionPluginOptions{
//	    Store:    store,
//	    ResolveID: func(ctx context.Context) (string, error) { return "user-123", nil },
//	})
//
// The [TrimmingPlugin] integrates via bond's hook system, trimming messages before
// each model invocation. Optional auto-recovery detects [bond.ErrContextOverflow]
// from providers and retries with a trimmed history.
//
// # Deep Copy Semantics
//
// All store implementations must deep-copy messages on Save and Load to prevent
// aliasing between the caller's data and the stored data. The [InMemoryStore]
// implements this guarantee.
package session
