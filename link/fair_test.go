package link

import (
	"testing"
	"time"

	"github.com/net-agent/flex/v3/internal/sched"
	"github.com/net-agent/flex/v3/wire"
)

func runBatchLimit(t *testing.T, cfg sched.SchedulerConfig, count int) (total, max int) {
	t.Helper()
	batches := make(chan int, 128)
	w := newFairWriter(func(frames []*wire.Frame) error {
		batches <- len(frames)
		return nil
	}, cfg, nil)
	defer w.Close()

	src := wire.Addr{IP: 1, Port: 1000}
	dst := wire.Addr{IP: 2, Port: 80}
	for i := 0; i < count; i++ {
		f, err := wire.NewData(src, dst, []byte{1})
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Send(f); err != nil {
			t.Fatal(err)
		}
	}

	deadline := time.After(2 * time.Second)
	for total < count {
		select {
		case n := <-batches:
			total += n
			if n > max {
				max = n
			}
		case <-deadline:
			t.Fatalf("timeout: total=%d max=%d", total, max)
		}
	}
	return total, max
}

func TestFairWriterCostBudgetBatchLimit(t *testing.T) {
	// 1B packets cost MinChargeBytes (1KB) each; the 32KB budget caps a visit
	// at 32 packets.
	total, max := runBatchLimit(t, sched.DefaultSchedulerConfig(), 64)
	if total != 64 {
		t.Fatalf("total=%d", total)
	}
	if max > 32 {
		t.Fatalf("cost-budget batch=%d, want <=32", max)
	}
}

func TestFairWriterPacketQuantumBatchLimit(t *testing.T) {
	cfg := sched.SchedulerConfig{Policy: sched.PolicyPacketQuantum, QuantumPackets: 2}
	total, max := runBatchLimit(t, cfg, 6)
	if total != 6 {
		t.Fatalf("total=%d", total)
	}
	if max > 2 {
		t.Fatalf("packet-quantum batch=%d, want <=2", max)
	}
}
