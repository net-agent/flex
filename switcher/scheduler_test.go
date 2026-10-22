package switcher

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/internal/sched"
)

func TestServerSchedulerConfigDefault(t *testing.T) {
	s := newLimitedServer(t, permissiveLimits())

	def := s.schedulerConfig()
	if def.Policy != sched.PolicyCostBudget || def.QuantumBytes != sched.DefaultQuantumBytes || def.MinChargeBytes != sched.DefaultMinChargeBytes {
		t.Fatalf("unexpected default config: %+v", def)
	}

	s.SetSchedulerConfig(SchedulerConfig{
		Policy:         SchedulerPolicyCostBudget,
		QuantumBytes:   4096,
		MinChargeBytes: 512,
	})
	got := s.schedulerConfig()
	if got.Policy != sched.PolicyCostBudget || got.QuantumBytes != 4096 || got.MinChargeBytes != 512 {
		t.Fatalf("unexpected cost config: %+v", got)
	}

	// Unknown policies fall back to the default cost policy.
	s.SetSchedulerConfig(SchedulerConfig{Policy: SchedulerPolicy(99)})
	got = s.schedulerConfig()
	if got.Policy != sched.PolicyCostBudget || got.QuantumBytes != sched.DefaultQuantumBytes {
		t.Fatalf("unknown policy should fall back to cost: %+v", got)
	}

	// The historical packet policy remains available explicitly.
	s.SetSchedulerConfig(SchedulerConfig{Policy: SchedulerPolicyPackets, QuantumPackets: 2})
	got = s.schedulerConfig()
	if got.Policy != sched.PolicyPacketQuantum || got.QuantumPackets != 2 {
		t.Fatalf("packet policy not honored: %+v", got)
	}
}

// 直接调用 ServeConn（无 Serve 包装）时，数据面也必须按配置启用 FairConn。
func TestServerCostSchedulerWrapsDirectServeConn(t *testing.T) {
	s := newLimitedServer(t, permissiveLimits())
	s.SetSchedulerConfig(SchedulerConfig{
		Policy:         SchedulerPolicyCostBudget,
		QuantumBytes:   8 * 1024,
		MinChargeBytes: 1024,
	})

	n1 := connectTestNode(t, s, "cost-a")
	n2 := connectTestNode(t, s, "cost-b")

	ln, err := n2.Listen(80)
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()

	client, err := n1.Dial("cost-b:80")
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer client.Close()

	peer := <-accepted
	defer peer.Close()

	payload := bytes.Repeat([]byte("x"), 4096)
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if err := peer.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(peer, got); err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("payload mismatch")
	}

	ctx, err := s.registry.lookupByDomain("cost-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ctx.getConn().(*sched.FairConn); !ok {
		t.Fatalf("context conn should be *sched.FairConn, got %T", ctx.getConn())
	}
}
