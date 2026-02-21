package pending

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

func TestTimeout(t *testing.T) {
	var r Requests[struct{}]

	ch, err := r.Register(1)
	assert.Nil(t, err)
	defer r.Remove(1)

	select {
	case <-ch:
		t.Fatal("should not receive")
	case <-time.After(50 * time.Millisecond):
		// expected timeout
	}
}

func TestRemoveIdempotent(t *testing.T) {
	var r Requests[int]

	_, err := r.Register(1)
	assert.Nil(t, err)

	r.Remove(1)
	r.Remove(1) // second call should not panic
	r.Remove(99) // non-existent key should not panic
}

func TestRemoveUnblocksWaiter(t *testing.T) {
	var r Requests[int]

	ch, err := r.Register(1)
	assert.Nil(t, err)

	go func() {
		time.Sleep(20 * time.Millisecond)
		r.Remove(1)
	}()

	_, ok := <-ch
	assert.False(t, ok, "channel should be closed by Remove")
}

func TestConcurrency(t *testing.T) {
	var r Requests[int]
	var wg sync.WaitGroup

	for i := uint16(0); i < 100; i++ {
		wg.Add(1)
		go func(key uint16) {
			defer wg.Done()

			ch, err := r.Register(key)
			if err != nil {
				return
			}
			defer r.Remove(key)

			go func() {
				r.Complete(key, int(key), nil)
			}()

			select {
			case res := <-ch:
				assert.Nil(t, res.Err)
				assert.Equal(t, int(key), res.Val)
			case <-time.After(time.Second):
				t.Errorf("timeout waiting for key %d", key)
			}
		}(i)
	}

	wg.Wait()
}
