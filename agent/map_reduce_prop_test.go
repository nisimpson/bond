package agent_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync/atomic"
	"testing"
	"testing/quick"
	"time"

	"github.com/nisimpson/bond/agent"
)

// Feature: advanced-orchestration, Property 2: MapReduce results match input order
// **Validates: Requirements 3.5, 3.6**

// TestProperty_MapReduceResultsMatchInputOrder verifies that for any input slice
// and any worker function, results are returned in the same order as inputs
// regardless of goroutine execution order.
func TestProperty_MapReduceResultsMatchInputOrder(t *testing.T) {
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		ctx := context.Background()
		state := agent.NewMapState()

		// Generate a random number of inputs (1..50).
		numInputs := 1 + r.Intn(50)
		inputs := make([]int, numInputs)
		for i := range inputs {
			inputs[i] = r.Intn(1000)
		}

		// Pre-generate random delays per worker so we don't share rand
		// across goroutines.
		delays := make([]time.Duration, numInputs)
		for i := range delays {
			delays[i] = time.Duration(r.Intn(100)) * time.Microsecond
		}
		maxConcurrency := 1 + r.Intn(10)

		// Store inputs in state for the Map function to read.
		state.Set("inputs", inputs)

		// Worker doubles the input with pre-computed delay to create scheduling variance.
		action := agent.MapReduce(agent.MapReduceOptions[int, int]{
			Map: func(ctx context.Context, s agent.State) ([]int, error) {
				v, _ := s.Get("inputs")
				return v.([]int), nil
			},
			Worker: func(ctx context.Context, input int) (int, error) {
				// Use input value to index into pre-computed delays.
				// Since inputs[i] = i for indexing purposes, we use
				// a deterministic delay based on the input's original index.
				// The input is the value from inputs[], so we need the index.
				// We'll use a simpler approach: sleep based on input mod delays.
				time.Sleep(delays[input%len(delays)])
				return input * 2, nil
			},
			Reduce: func(ctx context.Context, s agent.State, results []agent.MapReduceResult[int]) error {
				// Verify results length matches inputs.
				if len(results) != numInputs {
					return fmt.Errorf("expected %d results, got %d", numInputs, len(results))
				}
				// Verify each result's Index matches its position and value is correct.
				for i, res := range results {
					if res.Index != i {
						return fmt.Errorf("result[%d].Index = %d, want %d", i, res.Index, i)
					}
					if res.Err != nil {
						return fmt.Errorf("result[%d] unexpected error: %v", i, res.Err)
					}
					if res.Value != inputs[i]*2 {
						return fmt.Errorf("result[%d].Value = %d, want %d", i, res.Value, inputs[i]*2)
					}
				}
				return nil
			},
			MaxConcurrency: maxConcurrency,
		})

		if err := action(ctx, state); err != nil {
			t.Logf("action failed: %v", err)
			return false
		}
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: MapReduce results match input order — failed: %v", err)
	}
}

// Feature: advanced-orchestration, Property 8: MapReduce concurrency bound
// **Validates: Requirements 3.9**

// TestProperty_MapReduceConcurrencyBound verifies that for any MaxConcurrency > 0
// and any input size, at most MaxConcurrency workers execute simultaneously.
func TestProperty_MapReduceConcurrencyBound(t *testing.T) {
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		ctx := context.Background()
		state := agent.NewMapState()

		// Generate random concurrency limit and input size.
		maxConcurrency := 1 + r.Intn(8)
		numInputs := maxConcurrency + 1 + r.Intn(20) // always more inputs than limit

		inputs := make([]int, numInputs)
		for i := range inputs {
			inputs[i] = i
		}
		state.Set("inputs", inputs)

		// Pre-generate random sleep durations per worker to avoid sharing rand
		// across goroutines.
		workerDelays := make([]time.Duration, numInputs)
		for i := range workerDelays {
			workerDelays[i] = time.Duration(1+r.Intn(5)) * time.Millisecond
		}

		// Track peak concurrent workers with atomic counter.
		var active atomic.Int64
		var peak atomic.Int64

		action := agent.MapReduce(agent.MapReduceOptions[int, int]{
			Map: func(ctx context.Context, s agent.State) ([]int, error) {
				v, _ := s.Get("inputs")
				return v.([]int), nil
			},
			Worker: func(ctx context.Context, input int) (int, error) {
				current := active.Add(1)
				// Update peak if current exceeds it.
				for {
					p := peak.Load()
					if current <= p {
						break
					}
					if peak.CompareAndSwap(p, current) {
						break
					}
				}
				// Sleep using pre-computed delay to create contention and overlap.
				time.Sleep(workerDelays[input])
				active.Add(-1)
				return input, nil
			},
			Reduce: func(ctx context.Context, s agent.State, results []agent.MapReduceResult[int]) error {
				return nil
			},
			MaxConcurrency: maxConcurrency,
		})

		if err := action(ctx, state); err != nil {
			t.Logf("action failed: %v", err)
			return false
		}

		// Verify peak never exceeded MaxConcurrency.
		observed := peak.Load()
		if observed > int64(maxConcurrency) {
			t.Logf("peak concurrency %d exceeded limit %d", observed, maxConcurrency)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: MapReduce concurrency bound — failed: %v", err)
	}
}

// Feature: advanced-orchestration, Error collection
// **Validates: Requirements 3.7**

// TestProperty_MapReduceErrorCollection verifies that when some workers fail,
// Reduce receives all results (both successes and failures) with non-nil Err
// fields for the failed workers.
func TestProperty_MapReduceErrorCollection(t *testing.T) {
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		ctx := context.Background()
		state := agent.NewMapState()

		// Generate inputs; randomly mark some as "should fail".
		numInputs := 2 + r.Intn(20)
		inputs := make([]int, numInputs)
		shouldFail := make([]bool, numInputs)
		failCount := 0
		for i := range inputs {
			inputs[i] = i
			shouldFail[i] = r.Float64() < 0.3 // ~30% failure rate
			if shouldFail[i] {
				failCount++
			}
		}
		state.Set("inputs", inputs)

		var reduceErr error

		action := agent.MapReduce(agent.MapReduceOptions[int, int]{
			Map: func(ctx context.Context, s agent.State) ([]int, error) {
				v, _ := s.Get("inputs")
				return v.([]int), nil
			},
			Worker: func(ctx context.Context, input int) (int, error) {
				if shouldFail[input] {
					return 0, fmt.Errorf("worker %d failed", input)
				}
				return input * 10, nil
			},
			Reduce: func(ctx context.Context, s agent.State, results []agent.MapReduceResult[int]) error {
				// Verify all results are present.
				if len(results) != numInputs {
					reduceErr = fmt.Errorf("expected %d results, got %d", numInputs, len(results))
					return reduceErr
				}
				// Verify error/success fields match expectations.
				for i, res := range results {
					if res.Index != i {
						reduceErr = fmt.Errorf("result[%d].Index = %d", i, res.Index)
						return reduceErr
					}
					if shouldFail[i] {
						if res.Err == nil {
							reduceErr = fmt.Errorf("result[%d] expected error, got nil", i)
							return reduceErr
						}
					} else {
						if res.Err != nil {
							reduceErr = fmt.Errorf("result[%d] unexpected error: %v", i, res.Err)
							return reduceErr
						}
						if res.Value != inputs[i]*10 {
							reduceErr = fmt.Errorf("result[%d].Value = %d, want %d", i, res.Value, inputs[i]*10)
							return reduceErr
						}
					}
				}
				return nil
			},
			MaxConcurrency: 1 + r.Intn(5),
		})

		if err := action(ctx, state); err != nil {
			t.Logf("action failed: %v", err)
			return false
		}
		if reduceErr != nil {
			t.Logf("reduce verification failed: %v", reduceErr)
			return false
		}
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: MapReduce error collection — failed: %v", err)
	}
}

// Feature: advanced-orchestration, Map error propagation
// **Validates: Requirements 3.5**

// TestProperty_MapReduceMapErrorPropagation verifies that when the Map function
// returns an error, the action returns that error without launching any workers.
func TestProperty_MapReduceMapErrorPropagation(t *testing.T) {
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		ctx := context.Background()
		state := agent.NewMapState()

		mapErr := fmt.Errorf("map error %d", r.Intn(1000))
		workerCalled := false
		reduceCalled := false

		action := agent.MapReduce(agent.MapReduceOptions[int, int]{
			Map: func(ctx context.Context, s agent.State) ([]int, error) {
				return nil, mapErr
			},
			Worker: func(ctx context.Context, input int) (int, error) {
				workerCalled = true
				return input, nil
			},
			Reduce: func(ctx context.Context, s agent.State, results []agent.MapReduceResult[int]) error {
				reduceCalled = true
				return nil
			},
			MaxConcurrency: 1 + r.Intn(10),
		})

		err := action(ctx, state)
		if err == nil {
			t.Logf("expected error from Map, got nil")
			return false
		}
		if !errors.Is(err, mapErr) {
			t.Logf("expected mapErr, got: %v", err)
			return false
		}
		if workerCalled {
			t.Logf("worker was called despite Map error")
			return false
		}
		if reduceCalled {
			t.Logf("reduce was called despite Map error")
			return false
		}
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property: MapReduce Map error propagation — failed: %v", err)
	}
}
