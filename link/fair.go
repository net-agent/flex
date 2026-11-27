package link

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/net-agent/flex/v3/internal/sched"
	"github.com/net-agent/flex/v3/wire"
)

// ErrWriterClosed is returned when sending on a closed link.
var ErrWriterClosed = errors.New("link: writer closed")

// streamQueue is a per-SID frame queue. Frames are owned by the queue until the
// writer drains and releases them.
type streamQueue struct {
	mu     sync.Mutex
	queue  []*wire.Frame
	active atomic.Bool
	closed bool // protected by fairWriter.mu only
}

func (q *streamQueue) Push(f *wire.Frame) {
	q.mu.Lock()
	q.queue = append(q.queue, f)
	q.mu.Unlock()
}

func (q *streamQueue) Len() int {
	q.mu.Lock()
	n := len(q.queue)
	q.mu.Unlock()
	return n
}

func (q *streamQueue) drainInto(dst []*wire.Frame, limit int) int {
	q.mu.Lock()
	defer q.mu.Unlock()

	n := limit
	if n > len(q.queue) {
		n = len(q.queue)
	}
	if n > len(dst) {
		n = len(dst)
	}
	if n <= 0 {
		return 0
	}
	copy(dst[:n], q.queue[:n])
	copy(q.queue, q.queue[n:])
	q.queue = q.queue[:len(q.queue)-n]
	return n
}

// drainCostInto removes the longest prefix of frames whose charged cost
// max(payload, minCharge) fits budget. The first frame is always taken, so a
// single frame larger than the budget is an allowed overshoot. This mirrors the
// cost-budget policy of internal/sched.
func (q *streamQueue) drainCostInto(dst []*wire.Frame, budget, minCharge int) int {
	q.mu.Lock()
	defer q.mu.Unlock()

	limit := len(dst)
	if limit > len(q.queue) {
		limit = len(q.queue)
	}

	n, spent := 0, 0
	for n < limit {
		charge := int(q.queue[n].PayloadSize())
		if charge < minCharge {
			charge = minCharge
		}
		if n > 0 && spent+charge > budget {
			break
		}
		spent += charge
		n++
	}
	if n == 0 {
		return 0
	}
	copy(dst[:n], q.queue[:n])
	copy(q.queue, q.queue[n:])
	q.queue = q.queue[:len(q.queue)-n]
	return n
}

func (q *streamQueue) releaseAll() {
	q.mu.Lock()
	for _, f := range q.queue {
		f.Release()
	}
	q.queue = q.queue[:0]
	q.mu.Unlock()
}

// fairWriter is a per-SID round-robin writer. Control frames bypass the stream
// queues; data/close frames keep per-stream order. It is the link's only writer
// goroutine, replacing the old FairConn plus per-context forward loop stack.
type fairWriter struct {
	send    func([]*wire.Frame) error
	onError func(error)
	cfg     sched.SchedulerConfig

	scratch    []*wire.Frame
	controlCh  chan *wire.Frame
	syncCh     chan syncReq
	readyQueue chan wire.SID

	mu      sync.Mutex
	streams map[wire.SID]*streamQueue
	closed  atomic.Bool

	done     chan struct{}
	stopOnce sync.Once
}

func newFairWriter(send func([]*wire.Frame) error, cfg sched.SchedulerConfig, onError func(error)) *fairWriter {
	cfg = cfg.Normalized()
	w := &fairWriter{
		send:       send,
		onError:    onError,
		cfg:        cfg,
		scratch:    make([]*wire.Frame, sched.MaxPacketsPerVisit),
		controlCh:  make(chan *wire.Frame, sched.DefaultControlQueueSize),
		syncCh:     make(chan syncReq, sched.DefaultControlQueueSize),
		readyQueue: make(chan wire.SID, sched.ReadyQueueSize),
		streams:    make(map[wire.SID]*streamQueue),
		done:       make(chan struct{}),
	}
	go w.loop()
	return w
}

func isStreamCommand(cmd wire.Cmd) bool {
	return cmd == wire.Cmd(wire.CmdPushStreamData) ||
		cmd == wire.Cmd(wire.CmdCloseStream) ||
		cmd == wire.Cmd(wire.AckCloseStream)
}

// Send takes ownership of f. It never touches the payload bytes.
func (w *fairWriter) Send(f *wire.Frame) error {
	if f == nil {
		return nil
	}
	if w.closed.Load() {
		f.Release()
		return ErrWriterClosed
	}

	cmd := f.Cmd()
	if !isStreamCommand(cmd) {
		select {
		case w.controlCh <- f:
			return nil
		case <-w.done:
			f.Release()
			return ErrWriterClosed
		}
	}

	sid := f.SID()
	w.mu.Lock()
	if w.closed.Load() {
		w.mu.Unlock()
		f.Release()
		return ErrWriterClosed
	}
	q := w.streams[sid]
	if q == nil {
		q = &streamQueue{}
		w.streams[sid] = q
	}
	q.Push(f)
	if cmd == wire.Cmd(wire.CmdCloseStream) || cmd == wire.Cmd(wire.AckCloseStream) {
		q.closed = true
	}
	wake := !q.active.Swap(true)
	w.mu.Unlock()

	if wake {
		select {
		case w.readyQueue <- sid:
		case <-w.done:
			return ErrWriterClosed
		}
	}
	return nil
}

type syncReq struct {
	f    *wire.Frame
	done chan error
}

// SendSync writes f and waits for the transport result. Control packets use
// this path so write errors are visible to the caller, matching the old
// synchronous packet.Writer behavior. Data packets stay asynchronous.
func (w *fairWriter) SendSync(f *wire.Frame) error {
	if f == nil {
		return nil
	}
	if w.closed.Load() {
		f.Release()
		return ErrWriterClosed
	}
	req := syncReq{f: f, done: make(chan error, 1)}
	select {
	case w.syncCh <- req:
	case <-w.done:
		f.Release()
		return ErrWriterClosed
	}
	select {
	case err := <-req.done:
		return err
	case <-w.done:
		return ErrWriterClosed
	}
}

func (w *fairWriter) writeSync(req syncReq) {
	err := w.send([]*wire.Frame{req.f})
	req.f.Release()
	req.done <- err
	if err != nil {
		w.stop(err)
	}
}

func (w *fairWriter) loop() {
	for {
		select {
		case <-w.done:
			return
		case req := <-w.syncCh:
			if w.closed.Load() {
				req.f.Release()
				req.done <- ErrWriterClosed
				continue
			}
			w.writeSync(req)
		case f := <-w.controlCh:
			if w.closed.Load() {
				f.Release()
				return
			}
			w.writeBatch([]*wire.Frame{f})
		case sid := <-w.readyQueue:
			if w.closed.Load() {
				return
			}
			w.drainControl()
			w.processStream(sid)
		}
	}
}

func (w *fairWriter) writeBatch(frames []*wire.Frame) {
	if len(frames) == 0 {
		return
	}
	if err := w.send(frames); err != nil {
		for _, f := range frames {
			f.Release()
		}
		w.stop(err)
		return
	}
	for _, f := range frames {
		f.Release()
	}
}

func (w *fairWriter) drainControl() {
	for {
		select {
		case <-w.done:
			return
		case f := <-w.controlCh:
			w.writeBatch([]*wire.Frame{f})
		default:
			return
		}
	}
}

func (w *fairWriter) processStream(sid wire.SID) {
	w.mu.Lock()
	q := w.streams[sid]
	w.mu.Unlock()
	if q == nil {
		return
	}

	n := w.takePackets(q)
	if n > 0 {
		frames := w.scratch[:n]
		if err := w.send(frames); err != nil {
			for _, f := range frames {
				f.Release()
			}
			w.stop(err)
			return
		}
		for _, f := range frames {
			f.Release()
		}
	}

	if q.Len() > 0 {
		w.requeue(sid)
		return
	}

	w.mu.Lock()
	// A close/ack-close frame flips q.closed. Do not delete the queue while
	// frames are still queued behind it: with simultaneous close, a stream can
	// enqueue its own Close and then the peer's AckClose (or vice versa) before
	// the first drain finishes. Deleting here would orphan the latter frame and
	// make the peer wait for its full close-ack timeout.
	if q.closed && q.Len() == 0 {
		delete(w.streams, sid)
		w.mu.Unlock()
		return
	}
	q.active.Store(false)
	w.mu.Unlock()

	// Double-check the producer/consumer race: a frame may have arrived after
	// the drain and before active was cleared.
	if q.Len() > 0 && q.active.CompareAndSwap(false, true) {
		w.requeue(sid)
	}
}

func (w *fairWriter) takePackets(q *streamQueue) int {
	if w.cfg.Policy == sched.PolicyPacketQuantum {
		return q.drainInto(w.scratch, w.cfg.QuantumPackets)
	}
	return q.drainCostInto(w.scratch, w.cfg.QuantumBytes, w.cfg.MinChargeBytes)
}

func (w *fairWriter) requeue(sid wire.SID) {
	select {
	case w.readyQueue <- sid:
	default:
		go func() {
			select {
			case w.readyQueue <- sid:
			case <-w.done:
			}
		}()
	}
}

func (w *fairWriter) stop(err error) {
	w.stopOnce.Do(func() {
		w.closed.Store(true)
		close(w.done)
		w.drainAll()
		if err != nil && w.onError != nil {
			w.onError(err)
		}
	})
}

func (w *fairWriter) drainAll() {
	for {
		select {
		case f := <-w.controlCh:
			f.Release()
		default:
			goto sync
		}
	}
sync:
	for {
		select {
		case req := <-w.syncCh:
			req.f.Release()
			req.done <- ErrWriterClosed
		default:
			goto streams
		}
	}
streams:
	w.mu.Lock()
	for sid, q := range w.streams {
		q.releaseAll()
		delete(w.streams, sid)
	}
	w.mu.Unlock()
}

// Close stops the writer and releases queued frames.
func (w *fairWriter) Close() error {
	w.stop(nil)
	return nil
}
