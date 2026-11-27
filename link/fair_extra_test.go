package link

import (
	"errors"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v4/internal/sched"
	"github.com/net-agent/flex/v4/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newManualWriter builds a fairWriter without starting its loop, so tests can
// drive individual branches deterministically.
func newManualWriter(send func([]*wire.Frame) error) *fairWriter {
	cfg := sched.DefaultSchedulerConfig().Normalized()
	return &fairWriter{
		send:       send,
		cfg:        cfg,
		scratch:    make([]*wire.Frame, sched.MaxPacketsPerVisit),
		controlCh:  make(chan *wire.Frame, 4),
		syncCh:     make(chan syncReq, 4),
		readyQueue: make(chan wire.SID, 4),
		streams:    make(map[wire.SID]*streamQueue),
		done:       make(chan struct{}),
	}
}

func testSID() wire.SID {
	return wire.SID{Src: wire.Addr{IP: 1, Port: 1000}, Dst: wire.Addr{IP: 2, Port: 80}}
}

func mustData(t *testing.T, sid wire.SID, payload string) *wire.Frame {
	t.Helper()
	f, err := wire.NewData(sid.Src, sid.Dst, []byte(payload))
	require.NoError(t, err)
	return f
}

func mustControl(t *testing.T, cmd wire.ControlCmd) *wire.Frame {
	t.Helper()
	f, err := wire.NewControl(cmd, wire.Addr{IP: 1}, wire.Addr{IP: 2}, nil)
	require.NoError(t, err)
	return f
}

func TestStreamQueueDrainLimits(t *testing.T) {
	t.Run("drainInto", func(t *testing.T) {
		q := &streamQueue{}
		dst := make([]*wire.Frame, 8)
		for i := 0; i < 3; i++ {
			q.Push(mustData(t, testSID(), "x"))
		}
		n := q.drainInto(dst, 2)
		assert.Equal(t, 2, n)
		assert.Equal(t, 1, q.Len())

		// limit exceeds both the queue and dst capacity.
		n = q.drainInto(dst, 100)
		assert.Equal(t, 1, n)
		assert.Zero(t, q.Len())

		for _, f := range dst[:3] {
			f.Release()
		}

		q.Push(mustData(t, testSID(), "y"))
		q.Push(mustData(t, testSID(), "z"))
		n = q.drainInto(dst[:1], 5)
		assert.Equal(t, 1, n)
		assert.Equal(t, 1, q.Len())
		dst[0].Release()

		n = q.drainInto(dst, 0)
		assert.Zero(t, n)
		q.releaseAll()
	})

	t.Run("drainCostInto empty", func(t *testing.T) {
		q := &streamQueue{}
		n := q.drainCostInto(make([]*wire.Frame, 2), 1024, 1)
		assert.Zero(t, n)
	})

	t.Run("releaseAll", func(t *testing.T) {
		q := &streamQueue{}
		q.Push(mustData(t, testSID(), "a"))
		q.Push(mustData(t, testSID(), "b"))
		q.releaseAll()
		assert.Zero(t, q.Len())
		assert.Empty(t, q.queue)
	})
}

func TestFairWriterSendClosedEdges(t *testing.T) {
	t.Run("nil frames", func(t *testing.T) {
		w := newManualWriter(func([]*wire.Frame) error { return nil })
		assert.NoError(t, w.Send(nil))
		assert.NoError(t, w.SendSync(nil))
		w.stop(nil)
	})

	t.Run("control send sees done", func(t *testing.T) {
		w := newManualWriter(func([]*wire.Frame) error { return nil })
		w.controlCh = make(chan *wire.Frame) // unbuffered: only done can win
		f := mustControl(t, wire.CmdPingDomain)
		res := make(chan error, 1)
		go func() { res <- w.Send(f) }()
		close(w.done)
		assert.ErrorIs(t, <-res, ErrWriterClosed)
	})

	t.Run("second closed check", func(t *testing.T) {
		oldProcs := runtime.GOMAXPROCS(1)
		defer runtime.GOMAXPROCS(oldProcs)
		// Send checks closed once before and once after taking w.mu. Force the
		// second check by holding the lock while the sender runs its first
		// check, then closing the writer and letting it continue.
		for i := 0; i < 64; i++ {
			w := newManualWriter(func([]*wire.Frame) error { return nil })
			w.mu.Lock()
			res := make(chan error, 1)
			go func() { res <- w.Send(mustData(t, testSID(), "x")) }()
			for j := 0; j < 4; j++ {
				runtime.Gosched()
			}
			w.closed.Store(true)
			w.mu.Unlock()
			assert.ErrorIs(t, <-res, ErrWriterClosed)
			w.stop(nil)
		}
	})

	t.Run("wake sees done", func(t *testing.T) {
		w := newManualWriter(func([]*wire.Frame) error { return nil })
		w.readyQueue = nil // no receiver, so only done can win
		close(w.done)
		assert.ErrorIs(t, w.Send(mustData(t, testSID(), "x")), ErrWriterClosed)
		for _, q := range w.streams {
			q.releaseAll()
		}
	})

	t.Run("sync channel full and done", func(t *testing.T) {
		w := newManualWriter(func([]*wire.Frame) error { return nil })
		w.syncCh = nil
		close(w.done)
		assert.ErrorIs(t, w.SendSync(mustControl(t, wire.CmdPingDomain)), ErrWriterClosed)
	})

	t.Run("sync wait interrupted by done", func(t *testing.T) {
		w := newManualWriter(func([]*wire.Frame) error { return nil })
		res := make(chan error, 1)
		f := mustControl(t, wire.CmdPingDomain)
		go func() { res <- w.SendSync(f) }()

		req := <-w.syncCh
		require.Same(t, f, req.f)
		close(w.done)
		assert.ErrorIs(t, <-res, ErrWriterClosed)
		req.f.Release()
	})
}

func TestFairWriterLoopClosedBranches(t *testing.T) {
	t.Run("sync request", func(t *testing.T) {
		w := newManualWriter(func([]*wire.Frame) error { return nil })
		w.closed.Store(true)
		req := syncReq{f: mustControl(t, wire.CmdPingDomain), done: make(chan error, 1)}
		w.syncCh <- req
		loopDone := make(chan struct{})
		go func() {
			w.loop()
			close(loopDone)
		}()
		assert.ErrorIs(t, <-req.done, ErrWriterClosed)
		w.stop(nil)
		<-loopDone
	})

	t.Run("control frame", func(t *testing.T) {
		w := newManualWriter(func([]*wire.Frame) error { return nil })
		w.closed.Store(true)
		w.controlCh <- mustControl(t, wire.CmdPingDomain)
		loopDone := make(chan struct{})
		go func() {
			w.loop()
			close(loopDone)
		}()
		select {
		case <-loopDone:
		case <-time.After(time.Second):
			t.Fatal("loop did not exit")
		}
		w.stop(nil)
	})

	t.Run("ready stream", func(t *testing.T) {
		w := newManualWriter(func([]*wire.Frame) error { return nil })
		w.closed.Store(true)
		w.readyQueue <- testSID()
		loopDone := make(chan struct{})
		go func() {
			w.loop()
			close(loopDone)
		}()
		select {
		case <-loopDone:
		case <-time.After(time.Second):
			t.Fatal("loop did not exit")
		}
		w.stop(nil)
	})
}

func TestFairWriterWriteBatchAndDrainControl(t *testing.T) {
	t.Run("writeBatch empty", func(t *testing.T) {
		w := newManualWriter(func([]*wire.Frame) error { return nil })
		w.writeBatch(nil)
		w.stop(nil)
	})

	t.Run("writeBatch send error", func(t *testing.T) {
		want := errBoom()
		w := newManualWriter(func([]*wire.Frame) error { return want })
		errCh := make(chan error, 1)
		w.onError = func(err error) { errCh <- err }
		w.writeBatch([]*wire.Frame{mustData(t, testSID(), "x")})
		assert.ErrorIs(t, <-errCh, want)
		assert.True(t, w.closed.Load())
	})

	t.Run("drainControl done", func(t *testing.T) {
		w := newManualWriter(func([]*wire.Frame) error { return nil })
		close(w.done)
		w.drainControl()
	})

	t.Run("drainControl drains", func(t *testing.T) {
		var got int
		w := newManualWriter(func(frames []*wire.Frame) error {
			got += len(frames)
			return nil
		})
		f := mustControl(t, wire.CmdPingDomain)
		w.controlCh <- f
		w.drainControl()
		assert.Equal(t, 1, got)
		assert.Zero(t, len(w.controlCh))
		w.stop(nil)
	})
}

func TestFairWriterProcessStreamEdges(t *testing.T) {
	t.Run("unknown stream", func(t *testing.T) {
		w := newManualWriter(func([]*wire.Frame) error { return nil })
		w.processStream(testSID()) // no queue: must be a no-op
		w.stop(nil)
	})

	t.Run("send error stops writer", func(t *testing.T) {
		want := errBoom()
		errCh := make(chan error, 1)
		w := newFairWriter(func([]*wire.Frame) error { return want }, sched.DefaultSchedulerConfig(), func(err error) {
			errCh <- err
		})
		require.NoError(t, w.Send(mustData(t, testSID(), "a")))
		select {
		case err := <-errCh:
			assert.ErrorIs(t, err, want)
		case <-time.After(time.Second):
			t.Fatal("writer did not report send error")
		}
		// Once the send fails the writer stops; subsequent sends are rejected
		// instead of being silently queued.
		assert.ErrorIs(t, w.Send(mustData(t, testSID(), "b")), ErrWriterClosed)
		assert.True(t, w.closed.Load())
	})
}

func TestFairWriterRequeueFullReadyQueue(t *testing.T) {
	w := newManualWriter(func([]*wire.Frame) error { return nil })
	w.readyQueue = make(chan wire.SID, 1)
	sid := testSID()
	w.readyQueue <- wire.SID{Src: wire.Addr{IP: 99}}

	// The queue is full, so requeue must take its deferred-goroutine path.
	w.requeue(sid)
	<-w.readyQueue // free the slot; the deferred goroutine can now deliver
	select {
	case got := <-w.readyQueue:
		assert.Equal(t, sid, got)
	case <-time.After(time.Second):
		t.Fatal("requeued SID was not delivered")
	}
	w.stop(nil)
}

func TestFairWriterDrainAllReleasesEverything(t *testing.T) {
	w := newManualWriter(func([]*wire.Frame) error { return nil })
	sid := testSID()

	ctrl := mustControl(t, wire.CmdPingDomain)
	w.controlCh <- ctrl

	req := syncReq{f: mustData(t, sid, "sync"), done: make(chan error, 1)}
	w.syncCh <- req

	q := &streamQueue{}
	q.Push(mustData(t, sid, "stream-a"))
	q.Push(mustData(t, sid, "stream-b"))
	w.streams[sid] = q

	w.stop(nil)

	assert.Zero(t, len(w.controlCh))
	assert.Zero(t, len(w.syncCh))
	assert.Empty(t, w.streams)
	assert.ErrorIs(t, <-req.done, ErrWriterClosed)
	assert.Zero(t, q.Len())
}

// gatedTransport blocks the first write until the test releases it, which lets
// the test pile Close/AckClose frames into the closed stream queue behind the
// in-flight write.
type gatedTransport struct {
	once         sync.Once
	firstStarted chan struct{}
	releaseFirst chan struct{}
	writes       chan struct{}

	mu        sync.Mutex
	frames    []FrameBytes
	closed    chan struct{}
	closeOnce sync.Once
}

func newGatedTransport() *gatedTransport {
	return &gatedTransport{
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
		writes:       make(chan struct{}, 16),
		closed:       make(chan struct{}),
	}
}

func (t *gatedTransport) ReadHeader([]byte) error {
	<-t.closed
	return errors.New("closed")
}

func (t *gatedTransport) ReadPayload(int) ([]byte, error) { return nil, errors.New("closed") }

func (t *gatedTransport) WriteFrames(frames []FrameBytes) error {
	t.mu.Lock()
	for _, f := range frames {
		t.frames = append(t.frames, FrameBytes{Header: f.Header, Payload: append([]byte(nil), f.Payload...)})
	}
	t.mu.Unlock()
	select {
	case t.writes <- struct{}{}:
	default:
	}

	t.once.Do(func() {
		close(t.firstStarted)
		<-t.releaseFirst
	})
	return nil
}

func (t *gatedTransport) snapshot() []FrameBytes {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]FrameBytes(nil), t.frames...)
}

func (t *gatedTransport) SetReadDeadline(time.Time) error  { return nil }
func (t *gatedTransport) SetWriteDeadline(time.Time) error { return nil }
func (t *gatedTransport) LocalAddr() net.Addr              { return nil }
func (t *gatedTransport) RemoteAddr() net.Addr             { return nil }

func (t *gatedTransport) Close() error {
	t.closeOnce.Do(func() { close(t.closed) })
	return nil
}

// TestFairWriterDoesNotDropFramesQueuedBehindClosedStream is the regression
// test for the closed-queue deletion bug: after the first Close frame is
// drained, a concurrently queued Close/AckClose pair must still be written. The
// old code deleted the queue as soon as it was marked closed and orphaned the
// frames still sitting in it.
func TestFairWriterDoesNotDropFramesQueuedBehindClosedStream(t *testing.T) {
	tr := newGatedTransport()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(tr.releaseFirst) }) }
	defer release()

	lnk := NewDirectWithConfig(tr, Config{Scheduler: sched.SchedulerConfig{
		Policy:         sched.PolicyPacketQuantum,
		QuantumPackets: 1,
	}})
	defer lnk.Close()

	src := wire.Addr{IP: 1, Port: 1000}
	dst := wire.Addr{IP: 2, Port: 80}

	// First Close occupies the writer and blocks inside the transport.
	require.NoError(t, lnk.SendEmpty(wire.CmdCloseStream, src, dst))
	select {
	case <-tr.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first close was not written")
	}

	// Concurrently queue the remaining close frames behind the blocked write.
	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	for _, cmd := range []wire.EmptyCmd{wire.CmdCloseStream, wire.AckCloseStream} {
		wg.Add(1)
		go func(cmd wire.EmptyCmd) {
			defer wg.Done()
			errCh <- lnk.SendEmpty(cmd, src, dst)
		}(cmd)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}

	release()

	deadline := time.After(time.Second)
	for {
		frames := tr.snapshot()
		if len(frames) >= 3 {
			counts := map[wire.Cmd]int{}
			for _, f := range frames {
				h, err := (wire.Codec{}).DecodeHeader(f.Header[:])
				require.NoError(t, err)
				counts[h.Cmd]++
			}
			assert.Equal(t, 2, counts[wire.Cmd(wire.CmdCloseStream)])
			assert.Equal(t, 1, counts[wire.Cmd(wire.AckCloseStream)])
			return
		}
		select {
		case <-tr.writes:
		case <-deadline:
			t.Fatalf("lost queued close frames: wrote %d", len(frames))
		}
	}
}
