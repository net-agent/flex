package sched

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v3/packet"
)

const (
	DefaultControlQueueSize = 128
	DefaultQuantum          = 4
	ReadyQueueSize          = 1024
)

var ErrWriterClosed = errors.New("fair writer closed")

type FairWriter struct {
	writer  packet.Writer
	quantum int

	controlCh  chan *packet.Buffer
	streams    map[uint64]*StreamQueue
	mu         sync.Mutex // protects streams map only
	readyQueue chan uint64
	done       chan struct{}
	closeOne   sync.Once
	closed     atomic.Bool
}

// StreamQueue is a per-stream packet queue using slice+mutex instead of channel.
type StreamQueue struct {
	mu     sync.Mutex
	queue  []*packet.Buffer
	active atomic.Bool // whether this stream's SID is in readyQueue
}

func (sq *StreamQueue) Push(buf *packet.Buffer) {
	sq.mu.Lock()
	sq.queue = append(sq.queue, buf)
	sq.mu.Unlock()
}

// Drain removes and returns up to n packets from the queue.
func (sq *StreamQueue) Drain(n int) []*packet.Buffer {
	sq.mu.Lock()
	l := len(sq.queue)
	if l == 0 {
		sq.mu.Unlock()
		return nil
	}
	if n > l {
		n = l
	}
	out := make([]*packet.Buffer, n)
	copy(out, sq.queue[:n])
	copy(sq.queue, sq.queue[n:])
	sq.queue = sq.queue[:l-n]
	sq.mu.Unlock()
	return out
}

func (sq *StreamQueue) Len() int {
	sq.mu.Lock()
	n := len(sq.queue)
	sq.mu.Unlock()
	return n
}

func NewFairWriter(w packet.Writer, quantum ...int) *FairWriter {
	q := DefaultQuantum
	if len(quantum) > 0 && quantum[0] > 0 {
		q = quantum[0]
	}
	fw := &FairWriter{
		writer:     w,
		quantum:    q,
		controlCh:  make(chan *packet.Buffer, DefaultControlQueueSize),
		streams:    make(map[uint64]*StreamQueue),
		readyQueue: make(chan uint64, ReadyQueueSize),
		done:       make(chan struct{}),
	}
	go fw.loop()
	return fw
}

func (fw *FairWriter) WriteBuffer(buf *packet.Buffer) error {
	if fw.closed.Load() {
		return ErrWriterClosed
	}

	// Control packets: high priority
	if buf.Cmd() != packet.CmdPushStreamData {
		select {
		case fw.controlCh <- buf:
			return nil
		case <-fw.done:
			return ErrWriterClosed
		}
	}

	// Data packets: push to stream queue
	sid := buf.SID()
	fw.mu.Lock()
	sq, exists := fw.streams[sid]
	if !exists {
		sq = &StreamQueue{}
		fw.streams[sid] = sq
	}
	fw.mu.Unlock()

	sq.Push(buf)

	// Activate stream if not already in readyQueue (atomic CAS, no mutex needed)
	if sq.active.CompareAndSwap(false, true) {
		select {
		case fw.readyQueue <- sid:
		case <-fw.done:
			return ErrWriterClosed
		}
	}

	return nil
}

func (fw *FairWriter) SetWriteTimeout(dur time.Duration) {
	fw.writer.SetWriteTimeout(dur)
}

func (fw *FairWriter) Close() error {
	fw.closeOne.Do(func() {
		fw.closed.Store(true)
		close(fw.done)
	})
	return nil
}

func (fw *FairWriter) loop() {
	for {
		select {
		case <-fw.done:
			return
		case buf := <-fw.controlCh:
			fw.writer.WriteBuffer(buf)
			continue
		case sid := <-fw.readyQueue:
			// Preemption: drain control channel first
			fw.drainControl()
			fw.processStream(sid)
		}
	}
}

func (fw *FairWriter) drainControl() {
	for {
		select {
		case buf := <-fw.controlCh:
			fw.writer.WriteBuffer(buf)
		default:
			return
		}
	}
}

func (fw *FairWriter) processStream(sid uint64) {
	fw.mu.Lock()
	sq, exists := fw.streams[sid]
	fw.mu.Unlock()
	if !exists {
		return
	}

	bufs := sq.Drain(fw.quantum)
	if len(bufs) > 0 {
		if bw, ok := fw.writer.(packet.BatchWriter); ok {
			bw.WriteBufferBatch(bufs)
		} else {
			for _, buf := range bufs {
				fw.writer.WriteBuffer(buf)
			}
		}
	}

	// Re-queue if more data, otherwise deactivate
	if sq.Len() > 0 {
		select {
		case fw.readyQueue <- sid:
		default:
			go func() { fw.readyQueue <- sid }()
		}
	} else {
		sq.active.Store(false)
		// Double-check: producer may have pushed between Drain and Store
		if sq.Len() > 0 && sq.active.CompareAndSwap(false, true) {
			select {
			case fw.readyQueue <- sid:
			default:
				go func() { fw.readyQueue <- sid }()
			}
		}
	}
}
