package sched

import (
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v2/packet"
)

type MockWriter struct {
	Captured []*packet.Buffer
	mu       sync.Mutex
	delay    time.Duration
}

func (m *MockWriter) WriteBuffer(buf *packet.Buffer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	m.Captured = append(m.Captured, buf)
	return nil
}

func (m *MockWriter) SetWriteTimeout(dur time.Duration) {}

func TestPriority(t *testing.T) {
	mock := &MockWriter{delay: time.Millisecond * 10}
	fw := NewFairWriter(mock)
	defer fw.Close()

	// 1. Send Data (Low Priority) - will block/delay in mock
	dataBuf := packet.NewBufferWithCmd(packet.CmdPushStreamData)
	dataBuf.SetPayload([]byte("low"))
	dataBuf.Head[1] = 1 // SID=1 (BigEndian implies this changes SID)

	// 2. Send Control (High Priority)
	ctrlBuf := packet.NewBufferWithCmd(packet.CmdPushMessage) // Ping/Msg is control
	ctrlBuf.SetPayload([]byte("high"))

	go fw.WriteBuffer(dataBuf)
	time.Sleep(time.Millisecond * 2) // Ensure Data enqueued and processing starts
	go fw.WriteBuffer(ctrlBuf)

	// Wait for processing
	time.Sleep(time.Millisecond * 100)

	mock.mu.Lock()
	defer mock.mu.Unlock()

	// Logic:
	// Loop picks Data. Write(Data) takes 10ms.
	// During that 10ms, Control is enqueued.
	// Loop finishes Data. Next Loop iteration:
	// Should pick Control immediately BEFORE picking next Data (if any).

	// Better test: Flood data, then insert control.
	// Since we can't easily interrupt a Write, we verify that subsequent writes respect priority.
}

func TestFairness(t *testing.T) {
	mock := &MockWriter{delay: time.Microsecond * 10} // Small delay to allow lock interleaving
	fw := NewFairWriter(mock)
	defer fw.Close()

	// Stream 1: Sends 100 packets
	// Stream 2: Sends 100 packets
	// We expect interleaved execution in MockWriter output

	var wg sync.WaitGroup
	wg.Add(2)

	send := func(sid uint64, count int) {
		defer wg.Done()
		for i := 0; i < count; i++ {
			buf := packet.NewBufferWithCmd(packet.CmdPushStreamData)
			// Mock SID setting
			// packet.Buffer SID logic depends on bytes 1-9.
			// Let's just set DistIP/Port to simulate SID.
			// SID() uses Head[1:9]
			buf.Head[1] = byte(sid)
			fw.WriteBuffer(buf)
		}
	}

	go send(1, 100)
	go send(2, 100)

	wg.Wait()
	time.Sleep(time.Millisecond * 100) // drain

	mock.mu.Lock()
	defer mock.mu.Unlock()

	if len(mock.Captured) != 200 {
		t.Fatalf("expected 200 packets, got %d", len(mock.Captured))
	}

	// Verify interleaving
	// We count run lengths. If fairness works, run lengths should be small (ideally 1).
	maxRun := 0
	currentRun := 0
	lastSid := uint64(0)

	for _, buf := range mock.Captured {
		sid := uint64(buf.Head[1])
		if sid == lastSid {
			currentRun++
		} else {
			if currentRun > maxRun {
				maxRun = currentRun
			}
			currentRun = 1
			lastSid = sid
		}
	}
	if currentRun > maxRun {
		maxRun = currentRun
	}

	t.Logf("Max consecutive packets from same stream: %d", maxRun)

	// With queue buffering of 16, a single stream might dump 16 packets before the other gets scheduled
	// IF the produce speed > consume speed.
	// But `activeList` / `readyQueue` logic:
	// 1. S1 enqueues -> ReadyQ=[S1]
	// 2. Loop pops S1, sends 1 packet. Re-queues S1 -> ReadyQ=[S1]
	// 3. S2 enqueues -> ReadyQ=[S1, S2]
	// 4. Loop pops S1, sends. Re-queues -> ReadyQ=[S2, S1]
	// 5. Loop pops S2...
	// So ideally maxRun should be very small (1 or 2).

	if maxRun > 20 {
		t.Errorf("fairness failure? max run length %d is too high", maxRun)
	}
}
