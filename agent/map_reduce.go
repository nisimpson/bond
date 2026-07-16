package agent

import (
	"context"
	"sync"
)

// MapReduceResult holds one worker's output alongside any error.
// The Index field corresponds to the position in the original input slice
// returned by the Map function.
type MapReduceResult[T any] struct {
	Index int   // position in the original input slice
	Value T     // worker output (zero value if Err != nil)
	Err   error // nil on success
}

// MapReduceOptions configures a [MapReduce] action.
type MapReduceOptions[In, Out any] struct {
	// Map produces work items from the current state. Called once before
	// workers are launched. If Map returns an error, the action returns
	// that error immediately without launching any workers.
	Map func(ctx context.Context, state State) ([]In, error)
	// Worker processes a single input. Called concurrently for each
	// item produced by Map. Implementations should respect context
	// cancellation.
	Worker func(ctx context.Context, input In) (Out, error)
	// Reduce combines all results into state. Called once after all
	// workers finish. Results are in input order (indexed by position).
	// Check each result's Err field for individual worker failures.
	Reduce func(ctx context.Context, state State, results []MapReduceResult[Out]) error
	// MaxConcurrency limits parallel workers. Zero means unlimited.
	MaxConcurrency int
}

// MapReduce creates an [ActionFunc] that runs a map-reduce pipeline.
// Suitable for use as a [GraphNode].Action.
//
// Execution model:
//  1. Map reads state and produces a slice of inputs.
//  2. Workers run concurrently (bounded by MaxConcurrency via a
//     buffered channel semaphore when MaxConcurrency > 0).
//  3. Results are collected in input order.
//  4. Reduce receives all results (including errors) and writes to state.
func MapReduce[In, Out any](opts MapReduceOptions[In, Out]) ActionFunc {
	return func(ctx context.Context, state State) error {
		// Step 1: Map — produce inputs from state.
		inputs, err := opts.Map(ctx, state)
		if err != nil {
			return err
		}

		// Pre-allocate results slice indexed by input position.
		results := make([]MapReduceResult[Out], len(inputs))

		// Step 2: Launch workers concurrently.
		var wg sync.WaitGroup

		// Use a buffered channel as semaphore when MaxConcurrency > 0.
		var sem chan struct{}
		if opts.MaxConcurrency > 0 {
			sem = make(chan struct{}, opts.MaxConcurrency)
		}

		for i, input := range inputs {
			wg.Add(1)
			go func(idx int, in In) {
				defer wg.Done()

				// Acquire semaphore slot if concurrency is bounded.
				if sem != nil {
					select {
					case sem <- struct{}{}:
						defer func() { <-sem }()
					case <-ctx.Done():
						results[idx] = MapReduceResult[Out]{
							Index: idx,
							Err:   ctx.Err(),
						}
						return
					}
				}

				// Respect context cancellation before doing work.
				if ctx.Err() != nil {
					results[idx] = MapReduceResult[Out]{
						Index: idx,
						Err:   ctx.Err(),
					}
					return
				}

				out, workerErr := opts.Worker(ctx, in)
				results[idx] = MapReduceResult[Out]{
					Index: idx,
					Value: out,
					Err:   workerErr,
				}
			}(i, input)
		}

		wg.Wait()

		// Step 3: Reduce — combine all results into state.
		return opts.Reduce(ctx, state, results)
	}
}
