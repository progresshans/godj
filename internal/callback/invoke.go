// Package callback joins explicitly owned application callbacks. It does not
// schedule background work or abandon a callback when its context is canceled.
package callback

// Invoke waits through the callback's defers, including panic and Goexit.
// aborted is the owner's closed error for a callback that does not return;
// panic values are discarded. The callback owns resources until it returns a
// value that transfers their cleanup responsibility to its caller.
func Invoke[T any](call func() (T, error), aborted error) (T, error) {
	type result struct {
		value T
		err   error
	}
	completed := make(chan result, 1)
	go func() {
		outcome := result{err: aborted}
		defer func() { _ = recover(); completed <- outcome }()
		outcome.value, outcome.err = call()
	}()
	outcome := <-completed
	return outcome.value, outcome.err
}
