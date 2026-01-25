package sched

import (
	"errors"
	"sync"
	"time"

	"github.com/net-agent/flex/v2/packet"
)

const (
	DefaultControlQueueSize = 128
	DefaultStreamQueueSize  = 16
	ReadyQueueSize          = 1024
)

var (
	ErrWriterClosed = errors.New("fair writer closed")
)

type FairWriter struct {
	writer packet.Writer

	// Control Priority Queue (Highest Priority)
	controlCh chan *packet.Buffer

	// Stream Queues
	// map[sid]*StreamQueue
	streams map[uint64]*StreamQueue
	mu      sync.Mutex

	// Ready Queue for Round Robin
	// Stores SIDs that have data to send
	readyQueue chan uint64

	// Signaling
	done     chan struct{}
	closeOne sync.Once
	closed   bool
}

type StreamQueue struct {
	ch       chan *packet.Buffer
	inReadyQ bool // protected by FairWriter.mu
}

func NewFairWriter(w packet.Writer) *FairWriter {
	fw := &FairWriter{
		writer:     w,
		controlCh:  make(chan *packet.Buffer, DefaultControlQueueSize),
		streams:    make(map[uint64]*StreamQueue),
		readyQueue: make(chan uint64, ReadyQueueSize),
		done:       make(chan struct{}),
	}
	go fw.loop()
	return fw
}

func (fw *FairWriter) WriteBuffer(buf *packet.Buffer) error {
	if fw.closed {
		return ErrWriterClosed
	}

	// 1. Classification
	if buf.Cmd() != packet.CmdPushStreamData {
		// High Priority (Control)
		select {
		case fw.controlCh <- buf:
			return nil
		case <-fw.done:
			return ErrWriterClosed
		}
	}

	// 2. Data Packet Handling
	// 2. Data Packet Handling
	fw.mu.Lock()
	sid := buf.SID()
	sq, exists := fw.streams[sid]
	if !exists {
		sq = &StreamQueue{
			ch: make(chan *packet.Buffer, DefaultStreamQueueSize),
		}
		fw.streams[sid] = sq
	}
	fw.mu.Unlock()

	// Push Data (Blocking)
	select {
	case sq.ch <- buf:
	case <-fw.done:
		return ErrWriterClosed
	}

	// Ensure Active
	// Check AFTER push to avoid race where consumer drains queue and marks inactive
	// while we were blocked.
	fw.mu.Lock()
	if !sq.inReadyQ {
		sq.inReadyQ = true
		select {
		case fw.readyQueue <- sid:
		default:
			// Queue full. Establish backpressure or force push.
			// Ideally readyQueue is large enough (1024) for active streams.
			// Blocking here is acceptable backpressure.
			fw.readyQueue <- sid
		}
	}
	fw.mu.Unlock()

	return nil
}

func (fw *FairWriter) SetWriteTimeout(dur time.Duration) {
	fw.writer.SetWriteTimeout(dur)
}

func (fw *FairWriter) Close() error {
	fw.closeOne.Do(func() {
		fw.closed = true
		close(fw.done)
	})
	return nil
}

func (fw *FairWriter) loop() {
	for {
		select {
		case <-fw.done:
			return

		// Priority 1: Control Packets
		case buf := <-fw.controlCh:
			fw.writer.WriteBuffer(buf)
			continue // Check control channel again immediately

		// Priority 2: Ready Streams
		case sid := <-fw.readyQueue:
			// Check if control channel has data first (Preemption check)
			select {
			case cBuf := <-fw.controlCh:
				fw.writer.WriteBuffer(cBuf)
				// Put SID back at head? No, back at tail is fine/fairer actually.
				// Or simplify: just process the sid. control will be caught next loop.
			default:
			}

			if !fw.processStream(sid) {
				// Stream finished or empty
			}
		}
	}
}

// processStream sends one packet from the stream and re-queues if more data exists
func (fw *FairWriter) processStream(sid uint64) bool {
	fw.mu.Lock()
	sq, exists := fw.streams[sid]
	if !exists {
		fw.mu.Unlock()
		return false
	}
	fw.mu.Unlock()

	select {
	case buf := <-sq.ch:
		fw.writer.WriteBuffer(buf)

		// Determine if we should re-queue
		// We can't peak easily.
		// Strategy: Always re-queue if we pulled a packet?
		// No, if empty, we want to stop spinning.

		fw.mu.Lock()
		// Double check if more data is available or likely available
		// len(sq.ch) > 0 is racy but acceptable hint.
		if len(sq.ch) > 0 {
			// Re-queue
			select {
			case fw.readyQueue <- sid:
			default:
				// Should not happen if we consume faster than produce
				// Force push or blocking
				go func() { fw.readyQueue <- sid }()
			}
		} else {
			// Mark as not in ready queue
			sq.inReadyQ = false
		}
		fw.mu.Unlock()
		return true

	default:
		// Queue was empty (spurious wake-up or race)
		fw.mu.Lock()
		sq.inReadyQ = false
		fw.mu.Unlock()
		return false
	}
}
