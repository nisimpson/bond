// Package conversation provides strategies for trimming conversation history
// to stay within model context limits.
//
// # Problem
//
// Large language models have finite context windows. As conversations grow,
// the accumulated messages may exceed the model's capacity, causing errors or
// degraded responses. This package defines the [ConversationManager] interface
// and concrete implementations that trim message slices to fit within
// configured constraints while preserving conversation coherence.
//
// # Usage
//
// Choose a trimming strategy and pass it to the [TrimmingPlugin]:
//
//   - [SlidingWindowManager]: retains the last N user/assistant pairs.
//   - [TokenBudgetManager]: trims to fit within a token budget.
//
// Both strategies preserve system-level preamble messages and maintain
// the relative ordering of the original conversation.
//
// The plugin integrates via bond's hook system, trimming messages before
// each model invocation. Optional auto-recovery detects
// [bond.ErrContextOverflow] from providers and retries with a trimmed
// history.
package conversation
