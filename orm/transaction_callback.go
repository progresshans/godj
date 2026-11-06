package orm

import "sync"

// transactionCallbackGuard enforces single synchronous entry for ORM transaction
// orchestration. It publishes completion under the same lock used by seal.
type transactionCallbackGuard struct {
	mu        sync.Mutex
	sealed    bool
	entries   int
	completed int
	result    error
}

type transactionCallbackSnapshot struct {
	entries   int
	completed int
	result    error
}

func (guard *transactionCallbackGuard) invoke(callback func() error) error {
	guard.mu.Lock()
	if guard.sealed {
		guard.mu.Unlock()
		return relationBackendInvalidPlan("transaction callback was invoked after its outer call returned")
	}
	guard.entries++
	if guard.entries != 1 {
		guard.mu.Unlock()
		return relationBackendInvalidPlan("transaction callback was invoked more than once")
	}
	guard.mu.Unlock()

	result := callback()
	guard.mu.Lock()
	guard.completed++
	guard.result = result
	guard.mu.Unlock()
	return result
}

func (guard *transactionCallbackGuard) seal() transactionCallbackSnapshot {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	guard.sealed = true
	return transactionCallbackSnapshot{
		entries:   guard.entries,
		completed: guard.completed,
		result:    guard.result,
	}
}
