package sched

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/packet"
)

func makeSizedBuf(sid uint16, size int) *packet.Buffer {
	buf := packet.NewBufferWithCmd(packet.CmdPushStreamData)
	buf.SetDistIP(sid)
	if size > 0 {
		_ = buf.SetPayload(make([]byte, size))
	}
	return buf
}

func TestSchedulerConfigNormalized(t *testing.T) {
	cases := []struct {
		name    string
		in      SchedulerConfig
		policy  Policy
		quantum int
		bytes   int
		min     int
		limit   int
	}{
		{"default", SchedulerConfig{}, PolicyCostBudget, 0, DefaultQuantumBytes, DefaultMinChargeBytes, 32},
		{"packet zero", SchedulerConfig{Policy: PolicyPacketQuantum}, PolicyPacketQuantum, DefaultQuantum, 0, 0, DefaultQuantum},
		{"packet clamp high", SchedulerConfig{Policy: PolicyPacketQuantum, QuantumPackets: 99999}, PolicyPacketQuantum, MaxPacketsPerVisit, 0, 0, MaxPacketsPerVisit},
		{"unknown policy", SchedulerConfig{Policy: Policy(99)}, PolicyCostBudget, 0, DefaultQuantumBytes, DefaultMinChargeBytes, 32},
		{"cost defaults", SchedulerConfig{Policy: PolicyCostBudget}, PolicyCostBudget, 0, DefaultQuantumBytes, DefaultMinChargeBytes, 32},
		{"cost small", SchedulerConfig{Policy: PolicyCostBudget, QuantumBytes: 4096}, PolicyCostBudget, 0, 4096, DefaultMinChargeBytes, 4},
		{"cost min gt budget", SchedulerConfig{Policy: PolicyCostBudget, QuantumBytes: 1024, MinChargeBytes: 4096}, PolicyCostBudget, 0, 1024, 1024, 1},
		{"cost huge", SchedulerConfig{Policy: PolicyCostBudget, QuantumBytes: 1 << 30, MinChargeBytes: 1}, PolicyCostBudget, 0, MaxQuantumBytes, 1, MaxPacketsPerVisit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.in.normalized()
			if got.Policy != tc.policy {
				t.Fatalf("policy=%v want %v", got.Policy, tc.policy)
			}
			if got.QuantumPackets != tc.quantum {
				t.Fatalf("quantum=%d want %d", got.QuantumPackets, tc.quantum)
			}
			if got.QuantumBytes != tc.bytes {
				t.Fatalf("bytes=%d want %d", got.QuantumBytes, tc.bytes)
			}
			if got.MinChargeBytes != tc.min {
				t.Fatalf("min=%d want %d", got.MinChargeBytes, tc.min)
			}
			if l := got.visitLimit(); l != tc.limit {
				t.Fatalf("visitLimit=%d want %d", l, tc.limit)
			}
		})
	}
}

func TestDrainCostBudget(t *testing.T) {
	const budget = 32 * 1024
	const minCharge = 1024
	cases := []struct {
		size    int
		count   int
		wantN   int
		wantSum int
	}{
		{16 * 1024, 10, 2, 32 * 1024},
		{1024, 100, 32, 32 * 1024},
		{64, 100, 32, 2 * 1024},
		{1, 100, 32, 32},
		{65535, 4, 1, 65535}, // 单包超过预算：允许一次 overshoot
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("size=%d", tc.size), func(t *testing.T) {
			sq := &StreamQueue{}
			for i := 0; i < tc.count; i++ {
				sq.Push(makeSizedBuf(1, tc.size))
			}
			dst := make([]*packet.Buffer, 64)
			n := sq.drainCostInto(dst, budget, minCharge)
			if n != tc.wantN {
				t.Fatalf("drained=%d want %d", n, tc.wantN)
			}
			sum := 0
			for _, b := range dst[:n] {
				sum += len(b.Payload)
			}
			if sum != tc.wantSum {
				t.Fatalf("payload=%d want %d", sum, tc.wantSum)
			}
			// 队列中剩余数量必须正确
			if got := sq.Len(); got != tc.count-tc.wantN {
				t.Fatalf("remaining=%d want %d", got, tc.count-tc.wantN)
			}
		})
	}
}

func TestDrainCostOrderAndNoLoss(t *testing.T) {
	sq := &StreamQueue{}
	const total = 500
	for i := 0; i < total; i++ {
		b := packet.NewBufferWithCmd(packet.CmdPushStreamData)
		b.SetDistIP(1)
		_ = b.SetPayload([]byte{byte(i)})
		sq.Push(b)
	}

	dst := make([]*packet.Buffer, 64)
	got := make([]byte, 0, total)
	for {
		n := sq.drainCostInto(dst, 8*1024, 1024)
		if n == 0 {
			break
		}
		for _, b := range dst[:n] {
			got = append(got, b.Payload[0])
		}
	}
	if len(got) != total {
		t.Fatalf("got %d packets, want %d", len(got), total)
	}
	for i, b := range got {
		if int(b) != i%256 {
			t.Fatalf("packet[%d]=%d want %d", i, b, i%256)
		}
	}
}

// 成本公平性：在轮转机会均等的前提下，四个包长差异巨大的流应当拿到
// 相同的“计费成本”（而非相同的 payload 字节）。
func TestDrainCostRoundRobinFairness(t *testing.T) {
	sizes := []int{16 * 1024, 1024, 64, 1}
	const budget = 32 * 1024
	const minCharge = 1024
	const visitsPerStream = 100

	queues := make([]*StreamQueue, len(sizes))
	refill := func(i int) {
		for k := 0; k < 256; k++ {
			queues[i].Push(makeSizedBuf(uint16(i+1), sizes[i]))
		}
	}
	for i := range queues {
		queues[i] = &StreamQueue{}
		refill(i)
	}

	dst := make([]*packet.Buffer, 64)
	cost := make([]int64, len(sizes))
	payload := make([]int64, len(sizes))
	visits := make([]int, len(sizes))

	for v := 0; v < len(sizes)*visitsPerStream; v++ {
		i := v % len(sizes)
		n := queues[i].drainCostInto(dst, budget, minCharge)
		if n == 0 {
			refill(i)
			n = queues[i].drainCostInto(dst, budget, minCharge)
		}
		if n == 0 {
			t.Fatalf("stream %d has no packets", i)
		}
		visits[i]++
		for _, b := range dst[:n] {
			c := len(b.Payload)
			if c < minCharge {
				c = minCharge
			}
			cost[i] += int64(c)
			payload[i] += int64(len(b.Payload))
		}
	}

	var totalCost, totalPayload int64
	for i := range sizes {
		if visits[i] != visitsPerStream {
			t.Fatalf("stream %d visits=%d want %d", i, visits[i], visitsPerStream)
		}
		totalCost += cost[i]
		totalPayload += payload[i]
	}
	for i := range sizes {
		share := float64(cost[i]) / float64(totalCost)
		if share < 0.249 || share > 0.251 {
			t.Fatalf("stream %d cost share=%.4f, want ~0.25", i, share)
		}
	}

	// 成本公平不等于 payload 公平：1B 流拿到同样的成本份额，但 payload 极少。
	oneByteShare := float64(payload[3]) / float64(totalPayload)
	if oneByteShare > 0.001 {
		t.Fatalf("1B stream payload share=%.5f, want < 0.1%%", oneByteShare)
	}
	if float64(payload[0])/float64(totalPayload) < 0.4 {
		t.Fatalf("16KB stream payload share too low: %.4f", float64(payload[0])/float64(totalPayload))
	}
}

// batchRecorder 记录每次 WriteBufferBatch（= 一次 visit）的流、包数与 payload。
type batchRecorder struct {
	mu       sync.Mutex
	batches  []batchRecord
	packets  int
	closeHit bool
}

type batchRecord struct {
	sid     uint64
	count   int
	payload int
	cmd     byte
}

func (r *batchRecorder) WriteBuffer(buf *packet.Buffer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batches = append(r.batches, batchRecord{sid: buf.SID(), count: 1, payload: len(buf.Payload), cmd: buf.Cmd()})
	r.packets++
	if buf.Cmd() == packet.CmdCloseStream {
		r.closeHit = true
	}
	return nil
}

func (r *batchRecorder) WriteBufferBatch(bufs []*packet.Buffer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(bufs) == 0 {
		return nil
	}
	rec := batchRecord{sid: bufs[0].SID(), count: len(bufs), cmd: bufs[0].Cmd()}
	for _, b := range bufs {
		rec.payload += len(b.Payload)
		if b.Cmd() == packet.CmdCloseStream {
			r.closeHit = true
		}
	}
	r.batches = append(r.batches, rec)
	r.packets += len(bufs)
	return nil
}

func (r *batchRecorder) SetWriteTimeout(time.Duration) {}

func (r *batchRecorder) waitPackets(t *testing.T, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		got := r.packets
		r.mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	r.mu.Lock()
	got := r.packets
	r.mu.Unlock()
	t.Fatalf("timeout waiting for %d packets, got %d", n, got)
}

func TestFairWriterCostBatch(t *testing.T) {
	rec := &batchRecorder{}
	cfg := SchedulerConfig{Policy: PolicyCostBudget, QuantumBytes: 8 * 1024, MinChargeBytes: 1024}
	fw := newFairWriter(rec, cfg, false)

	// Enqueue a deterministic backlog before starting the loop.
	for i := 0; i < 8; i++ {
		if err := fw.WriteBuffer(makeSizedBuf(1, 16*1024)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 64; i++ {
		if err := fw.WriteBuffer(makeSizedBuf(2, 1024)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 64; i++ {
		if err := fw.WriteBuffer(makeSizedBuf(3, 64)); err != nil {
			t.Fatal(err)
		}
	}
	go fw.loop()
	defer fw.Close()

	rec.waitPackets(t, 8+64+64, 2*time.Second)

	rec.mu.Lock()
	defer rec.mu.Unlock()

	// SID 由 DistIP:0 组成；按批次的第一包分组
	bySID := map[uint64][]batchRecord{}
	for _, b := range rec.batches {
		bySID[b.sid] = append(bySID[b.sid], b)
	}
	sid1 := uint64(1) << 48
	sid2 := uint64(2) << 48
	sid3 := uint64(3) << 48

	for _, b := range bySID[sid1] {
		if b.count != 1 {
			t.Fatalf("16KB stream batch=%d, want 1 (8KB budget, overshoot)", b.count)
		}
	}
	for _, b := range bySID[sid2] {
		if b.count > 8 {
			t.Fatalf("1KB stream batch=%d, want <=8", b.count)
		}
	}
	for _, b := range bySID[sid3] {
		if b.count > 8 {
			t.Fatalf("64B stream batch=%d, want <=8", b.count)
		}
		if b.payload > 8*64 {
			t.Fatalf("64B stream payload=%d exceeds budget-derived batch", b.payload)
		}
	}
	if len(bySID[sid1]) != 8 || len(bySID[sid2]) != 8 || len(bySID[sid3]) != 8 {
		t.Fatalf("batch counts per stream: %d/%d/%d, want 8/8/8",
			len(bySID[sid1]), len(bySID[sid2]), len(bySID[sid3]))
	}
}

func TestFairWriterCostClose(t *testing.T) {
	rec := &batchRecorder{}
	cfg := SchedulerConfig{Policy: PolicyCostBudget, QuantumBytes: 8 * 1024, MinChargeBytes: 1024}
	fw := newFairWriter(rec, cfg, false)

	for i := 0; i < 32; i++ {
		if err := fw.WriteBuffer(makeSizedBuf(7, 1024)); err != nil {
			t.Fatal(err)
		}
	}
	closeBuf := packet.NewBufferWithCmd(packet.CmdCloseStream)
	closeBuf.SetDistIP(7)
	if err := fw.WriteBuffer(closeBuf); err != nil {
		t.Fatal(err)
	}
	go fw.loop()
	defer fw.Close()

	rec.waitPackets(t, 33, 2*time.Second)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		fw.mu.Lock()
		n := len(fw.streams)
		fw.mu.Unlock()
		if n == 0 {
			rec.mu.Lock()
			hit := rec.closeHit
			rec.mu.Unlock()
			if !hit {
				t.Fatal("close frame was not written")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("stream queue was not reaped after close")
}

// BenchmarkStreamQueueDrain compares the per-visit dequeue cost for the same
// 32KB of 1KB packets: packet quantum (16 visits), cost budget (1 visit), and
// the legacy allocating Drain.
func BenchmarkStreamQueueDrain(b *testing.B) {
	const packets = 32
	const payload = 1024
	dst := make([]*packet.Buffer, 64)
	bufs := make([]*packet.Buffer, packets)
	for i := range bufs {
		bufs[i] = makeSizedBuf(1, payload)
	}
	b.SetBytes(packets * payload)

	run := func(b *testing.B, drain func(sq *StreamQueue)) {
		sq := &StreamQueue{}
		for _, p := range bufs {
			sq.Push(p)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			drain(sq)
			// refill; buf order is irrelevant after removal
			for _, p := range bufs {
				sq.Push(p)
			}
		}
		b.StopTimer()
	}

	b.Run("C-packet-2", func(b *testing.B) {
		run(b, func(sq *StreamQueue) {
			for i := 0; i < packets/2; i++ {
				sq.drainInto(dst, 2)
			}
		})
	})
	b.Run("C-Drain-alloc", func(b *testing.B) {
		run(b, func(sq *StreamQueue) {
			for i := 0; i < packets/2; i++ {
				sq.Drain(2)
			}
		})
	})
	b.Run("B-cost-32KB", func(b *testing.B) {
		run(b, func(sq *StreamQueue) {
			sq.drainCostInto(dst, 32*1024, 1024)
		})
	})
}
