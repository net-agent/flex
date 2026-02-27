package stream

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWindowGuard_NewWindowGuard(t *testing.T) {
	t.Run("positive initial size", func(t *testing.T) {
		w := NewWindowGuard(1024, 16)
		assert.Equal(t, int32(1024), w.Available())
	})

	t.Run("zero initial size", func(t *testing.T) {
		w := NewWindowGuard(0, 16)
		assert.Equal(t, int32(0), w.Available())
	})
}

func TestWindowGuard_Consume(t *testing.T) {
	w := NewWindowGuard(1024, 16)

	w.Consume(100)
	assert.Equal(t, int32(924), w.Available())

	w.Consume(924)
	assert.Equal(t, int32(0), w.Available())
}

func TestWindowGuard_Release(t *testing.T) {
	w := NewWindowGuard(0, 16)

	w.Release(512)
	assert.Equal(t, int32(512), w.Available())

	// Event channel should have a signal
	select {
	case <-w.Event():
		// ok
	default:
		t.Fatal("Release should send event signal")
	}
}

func TestWindowGuard_Release_EventNonBlocking(t *testing.T) {
	w := NewWindowGuard(0, 1) // event channel cap = 1

	// Fill the event channel
	w.Release(100)
	// Second release should not block even if event channel is full
	w.Release(100)
	assert.Equal(t, int32(200), w.Available())
}

func TestWindowGuard_Event(t *testing.T) {
	w := NewWindowGuard(0, 16)

	ch := w.Event()
	assert.NotNil(t, ch)

	// Should be empty initially
	select {
	case <-ch:
		t.Fatal("Event channel should be empty initially")
	default:
		// ok
	}
}

func TestWindowGuard_SetSize(t *testing.T) {
	w := NewWindowGuard(1024, 16)

	w.SetSize(0)
	assert.Equal(t, int32(0), w.Available())

	w.SetSize(2048)
	assert.Equal(t, int32(2048), w.Available())
}

func TestWindowGuard_Signal(t *testing.T) {
	w := NewWindowGuard(0, 16)

	w.Signal()

	select {
	case <-w.Event():
		// ok
	default:
		t.Fatal("Signal should send event")
	}
}

func TestWindowGuard_ConcurrentConsumeRelease(t *testing.T) {
	const initial int32 = 10000
	w := NewWindowGuard(initial, 64)

	var wg sync.WaitGroup

	// 10 consumers, each consume 100 times with size 10
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				w.Consume(10)
			}
		}()
	}

	// 10 releasers, each release 100 times with size 10
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				w.Release(10)
			}
		}()
	}

	wg.Wait()
	assert.GreaterOrEqual(t, w.Available(), int32(0))
	assert.LessOrEqual(t, w.Available(), initial)
}
