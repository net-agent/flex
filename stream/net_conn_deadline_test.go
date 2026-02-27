package stream

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeadline(t *testing.T) {
	t.Run("past deadline immediately expires", func(t *testing.T) {
		s := New(&mockWriter{}, 0)
		past := time.Now().Add(-time.Second)

		// Past deadline should succeed and immediately expire Done()
		s.SetReadDeadline(past)

		// Read should return timeout immediately
		_, err := s.Read(make([]byte, 10))
		assert.Equal(t, ErrTimeout, err)
	})

	t.Run("cancel deadline prevents timeout", func(t *testing.T) {
		s := New(&mockWriter{}, 0)
		s.SetDeadline(time.Now().Add(150 * time.Millisecond))
		time.Sleep(50 * time.Millisecond)
		s.SetDeadline(time.Time{})
		time.Sleep(150 * time.Millisecond)

		assert.False(t, isReadClosed(s), "reader should not be closed after cancel")
		assert.False(t, isWriteClosed(s), "writer should not be closed after cancel")
	})

	t.Run("deadline expires and times out Read", func(t *testing.T) {
		s := New(&mockWriter{}, 0)
		s.SetReadDeadline(time.Now().Add(testMedTimeout))

		done := make(chan error, 1)
		go func() {
			_, err := s.Read(make([]byte, 10))
			done <- err
		}()

		select {
		case err := <-done:
			assert.Equal(t, ErrTimeout, err)
		case <-time.After(time.Second):
			t.Fatal("Read did not unblock after deadline")
		}
	})

	t.Run("deadline is reversible - can use stream after new deadline", func(t *testing.T) {
		s := New(&mockWriter{}, 0)

		// Set a short deadline that expires
		s.SetReadDeadline(time.Now().Add(testShortTimeout))
		time.Sleep(testMedTimeout) // wait for it to expire

		// Read should timeout
		_, err := s.Read(make([]byte, 10))
		assert.Equal(t, ErrTimeout, err)

		// Set a new deadline (future) — stream should be usable again
		s.SetReadDeadline(time.Now().Add(time.Second))

		// Inject data, should be readable
		s.recvQueue <- []byte("hello")
		buf := make([]byte, 10)
		n, err := s.Read(buf)
		assert.Nil(t, err)
		assert.Equal(t, 5, n)
		assert.Equal(t, "hello", string(buf[:n]))
	})

	t.Run("timeout error satisfies net.Error", func(t *testing.T) {
		s := New(&mockWriter{}, 0)
		s.SetReadDeadline(time.Now().Add(testShortTimeout))

		_, err := s.Read(make([]byte, 10))
		require.NotNil(t, err)

		var netErr net.Error
		assert.ErrorAs(t, err, &netErr)
		assert.True(t, netErr.Timeout(), "should report Timeout()=true")
	})
}

// --- Concurrent DeadlineGuard testing ---

func TestDeadlineGuardConcurrentSet(t *testing.T) {
	guard := &DeadlineGuard{}
	var wg sync.WaitGroup
	const goroutines = 20

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				guard.Set(time.Now().Add(time.Second))
			} else {
				guard.Set(time.Time{})
			}
		}(i)
	}
	wg.Wait()
}

func TestConcurrentSetDeadlineWithReadWrite(t *testing.T) {
	s1, s2 := Pipe()

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			s1.Write([]byte("data"))
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 64)
		for i := 0; i < 50; i++ {
			s2.Read(buf)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			s1.SetWriteDeadline(time.Now().Add(time.Second))
			s2.SetReadDeadline(time.Now().Add(time.Second))
			time.Sleep(2 * time.Millisecond)
		}
	}()

	wg.Wait()
}

func TestReadDeadlineTriggersReadError(t *testing.T) {
	s := New(&mockWriter{}, 0)

	s.SetReadDeadline(time.Now().Add(testMedTimeout))

	done := make(chan error, 1)
	go func() {
		_, err := s.Read(make([]byte, 10))
		done <- err
	}()

	select {
	case err := <-done:
		assert.Equal(t, ErrTimeout, err)
	case <-time.After(time.Second):
		t.Fatal("Read did not unblock after read deadline")
	}
}

func TestWriteDeadlineTriggersWriteError(t *testing.T) {
	s := New(&mockWriter{}, 1024)
	s.window.SetSize(0)

	s.SetWriteDeadline(time.Now().Add(testMedTimeout))

	done := make(chan error, 1)
	go func() {
		_, err := s.Write([]byte("blocked"))
		done <- err
	}()

	select {
	case err := <-done:
		assert.Equal(t, ErrTimeout, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Write did not unblock after write deadline")
	}
}

func TestDeadlineGuardRapidSetOnlyLastFires(t *testing.T) {
	guard := &DeadlineGuard{}

	// Rapid sets with short deadlines
	for i := 0; i < 100; i++ {
		guard.Set(time.Now().Add(200 * time.Millisecond))
	}

	// After all timers expire, Done() should be closed
	time.Sleep(500 * time.Millisecond)

	select {
	case <-guard.Done():
		// expected: the last deadline expired
	default:
		t.Fatal("Done() should be closed after deadline expired")
	}
}

func TestDeadlineZeroValueCancels(t *testing.T) {
	guard := &DeadlineGuard{}

	// Set a deadline
	guard.Set(time.Now().Add(testMedTimeout))

	// Cancel it
	guard.Set(time.Time{})

	// Wait past the original deadline
	time.Sleep(testLongTimeout)

	// Done() should never close (returns a new never-closing channel)
	select {
	case <-guard.Done():
		t.Fatal("Done() should not close after cancel")
	default:
		// expected
	}
}
