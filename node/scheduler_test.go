package node

import (
	"testing"

	"github.com/net-agent/flex/v3/internal/sched"
)

func TestSessionSchedulerConfig(t *testing.T) {
	s := NewSession(nil, testSessionConfig())

	def := s.schedulerConfig()
	if def.Policy != sched.PolicyCostBudget || def.QuantumBytes != sched.DefaultQuantumBytes || def.MinChargeBytes != sched.DefaultMinChargeBytes {
		t.Fatalf("unexpected default config: %+v", def)
	}

	s.SetSchedulerConfig(SchedulerConfig{
		Policy:         SchedulerPolicyCostBudget,
		QuantumBytes:   16 * 1024,
		MinChargeBytes: 2048,
	})
	got := s.schedulerConfig()
	if got.Policy != sched.PolicyCostBudget || got.QuantumBytes != 16*1024 || got.MinChargeBytes != 2048 {
		t.Fatalf("unexpected cost config: %+v", got)
	}

	// Unknown policy falls back to the default cost policy.
	s.SetSchedulerConfig(SchedulerConfig{Policy: SchedulerPolicy(42)})
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
