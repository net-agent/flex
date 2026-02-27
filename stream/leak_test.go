package stream

import (
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/packet"
	"github.com/stretchr/testify/assert"
)

func TestCloseLeakOnTimeout(t *testing.T) {
	s := New(&mockWriter{}, 0)
	s.closeAckTimeout = testMedTimeout

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 1024)
		for {
			_, err := s.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	startTime := time.Now()
	err := s.Close()
	dur := time.Since(startTime)

	assert.NotNil(t, err, "expected timeout error")
	assert.GreaterOrEqual(t, dur, testMedTimeout, "close should wait for timeout")

	select {
	case <-done:
		// consumer goroutine exited normally
	case <-time.After(testLongTimeout):
		t.Error("consumer goroutine leaked: Read() is still blocked")
	}
}

func TestConcurrentClose(t *testing.T) {
	s := New(&mockWriter{}, 0)
	s.closeAckTimeout = testShortTimeout

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.Close()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.Close()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.HandleCmdCloseStream(packet.NewBuffer())
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)
		s.HandleAckCloseStream(packet.NewBuffer())
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// all goroutines exited — no panic, no deadlock
	case <-time.After(2 * time.Second):
		t.Fatal("TestConcurrentClose deadlocked")
	}
}
