package bulk

import (
	"context"
	"sync"
)

type Operation func(ctx context.Context, item interface{}) error

type Result struct {
	Index int
	Item  interface{}
	Error error
}

func ProcessBatch(ctx context.Context, items []interface{}, batchSize int, op Operation) []Result {
	results := make([]Result, len(items))
	var wg sync.WaitGroup

	for i := 0; i < len(items); i += batchSize {
		end := i + batchSize
		if end > len(items) {
			end = len(items)
		}

		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			for j := start; j < end; j++ {
				err := op(ctx, items[j])
				results[j] = Result{
					Index: j,
					Item:  items[j],
					Error: err,
				}
			}
		}(i, end)
	}

	wg.Wait()
	return results
}

func ProcessConcurrent(ctx context.Context, items []interface{}, workers int, op Operation) []Result {
	results := make([]Result, len(items))
	jobs := make(chan int, len(items))
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				err := op(ctx, items[idx])
				results[idx] = Result{
					Index: idx,
					Item:  items[idx],
					Error: err,
				}
			}
		}()
	}

	for i := range items {
		jobs <- i
	}
	close(jobs)

	wg.Wait()
	return results
}
