package pending

import (
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterAndComplete(t *testing.T) {
	var r Requests[string]

	ch, err := r.Register(1)
	assert.Nil(t, err)

	go func() {
		err := r.Complete(1, "hello", nil)
		assert.Nil(t, err)
	}()

	res := <-ch
	assert.Nil(t, res.Err)
	assert.Equal(t, "hello", res.Val)
}

func TestRegisterAndCompleteWithError(t *testing.T) {
	var r Requests[string]

	ch, err := r.Register(1)
	assert.Nil(t, err)

	testErr := assert.AnError
	go func() {
		r.Complete(1, "", testErr)
	}()

	res := <-ch
	assert.Equal(t, testErr, res.Err)
}

func TestDuplicateRegister(t *testing.T) {
	var r Requests[int]

	_, err := r.Register(1)
	assert.Nil(t, err)

	_, err = r.Register(1)
	assert.Equal(t, ErrAlreadyRegistered, err)
}

func TestCompleteUnknownKey(t *testing.T) {
	var r Requests[int]

	err := r.Complete(99, 0, nil)
	assert.Equal(t, ErrNotFound, err)
}

// TestTimeout keeps the original "no result before the timeout" assertion, but
// runs it on the synctest fake clock so the 50ms never elapses in real time.
func TestTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var r Requests[struct{}]

		ch, err := r.Register(1)
		require.NoError(t, err)
		defer r.Remove(1)

		select {
		case <-ch:
			t.Fatal("should not receive")
		case <-time.After(50 * time.Millisecond):
			// Expected: the fake clock jumps straight to the deadline.
		}

		require.Equal(t, 1, r.Len(), "the request is still registered before removal")
	})
}

func TestRemoveIdempotent(t *testing.T) {
	var r Requests[int]

	_, err := r.Register(1)
	assert.Nil(t, err)

	r.Remove(1)
	r.Remove(1)  // second call should not panic
	r.Remove(99) // non-existent key should not panic

	assert.Equal(t, 0, r.Len())
}

func TestRemoveUnblocksWaiter(t *testing.T) {
	var r Requests[int]

	ch, err := r.Register(1)
	require.NoError(t, err)

	start := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-start
		r.Remove(1)
	}()
	close(start)

	_, ok := <-ch
	assert.False(t, ok, "channel should be closed by Remove")
	<-done

	require.Equal(t, 0, r.Len())
}

func TestConcurrency(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var r Requests[int]
		var wg sync.WaitGroup
		errCh := make(chan error, 512)

		for i := uint16(0); i < 100; i++ {
			wg.Add(1)
			go func(key uint16) {
				defer wg.Done()

				ch, err := r.Register(key)
				if err != nil {
					errCh <- fmt.Errorf("register %d: %w", key, err)
					return
				}
				defer r.Remove(key)

				wg.Add(1)
				go func() {
					defer wg.Done()
					if err := r.Complete(key, int(key), nil); err != nil {
						errCh <- fmt.Errorf("complete %d: %w", key, err)
					}
				}()

				select {
				case res := <-ch:
					if res.Err != nil {
						errCh <- fmt.Errorf("wait %d: %w", key, res.Err)
					} else if res.Val != int(key) {
						errCh <- fmt.Errorf("wait %d: got value %d", key, res.Val)
					}
				case <-time.After(time.Second):
					errCh <- fmt.Errorf("timeout waiting for key %d", key)
				}
			}(i)
		}

		wg.Wait()
		close(errCh)
		for err := range errCh {
			require.NoError(t, err)
		}
		require.Equal(t, 0, r.Len(), "completed and removed requests must not stay registered")
	})
}

func TestLen(t *testing.T) {
	var r Requests[int]
	require.Equal(t, 0, r.Len(), "zero-value Requests has no waiters")

	_, err := r.Register(1)
	require.NoError(t, err)
	require.Equal(t, 1, r.Len())

	_, err = r.Register(2)
	require.NoError(t, err)
	require.Equal(t, 2, r.Len())

	require.NoError(t, r.Complete(1, 10, nil))
	require.Equal(t, 1, r.Len(), "Complete removes the waiter")

	r.Remove(2)
	require.Equal(t, 0, r.Len(), "Remove removes the waiter")

	r.Remove(2)
	require.Equal(t, 0, r.Len(), "removing an unknown key does not change the count")
}

func TestCompleteThenRemoveKeepsResult(t *testing.T) {
	var r Requests[string]

	ch, err := r.Register(7)
	require.NoError(t, err)

	require.NoError(t, r.Complete(7, "done", nil))
	require.Equal(t, 0, r.Len())

	r.Remove(7) // no-op: Complete already unregistered the waiter

	res, ok := <-ch
	require.True(t, ok, "Remove after Complete must not close the delivered channel")
	require.NoError(t, res.Err)
	require.Equal(t, "done", res.Val)

	// The key can be reused after Complete.
	_, err = r.Register(7)
	require.NoError(t, err)
	require.Equal(t, 1, r.Len())
}

func TestCompleteAndRemoveConcurrentSameKey(t *testing.T) {
	const rounds = 100

	for i := 0; i < rounds; i++ {
		var r Requests[int]

		ch, err := r.Register(1)
		require.NoError(t, err)

		start := make(chan struct{})
		completeErr := make(chan error, 1)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			completeErr <- r.Complete(1, 7, nil)
		}()
		go func() {
			defer wg.Done()
			<-start
			r.Remove(1)
		}()
		close(start)
		wg.Wait()

		err = <-completeErr
		res, ok := <-ch
		if err == nil {
			require.True(t, ok, "round %d: Complete won, so it must deliver a result", i)
			require.Equal(t, 7, res.Val)
		} else {
			require.ErrorIs(t, err, ErrNotFound)
			require.False(t, ok, "round %d: Remove won, so the channel must be closed", i)
		}
		require.Equal(t, 0, r.Len(), "round %d: the key must be gone after the race", i)
	}
}

func TestRegisterConcurrentSameKey(t *testing.T) {
	var r Requests[int]

	const goroutines = 32
	var wg sync.WaitGroup
	var mu sync.Mutex
	var registered, duplicate int
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := r.Register(1)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				registered++
			case err == ErrAlreadyRegistered:
				duplicate++
			}
		}()
	}
	wg.Wait()

	require.Equal(t, 1, registered, "exactly one Register must win")
	require.Equal(t, goroutines-1, duplicate, "all other Registers must see ErrAlreadyRegistered")
	require.Equal(t, 1, r.Len())
}
