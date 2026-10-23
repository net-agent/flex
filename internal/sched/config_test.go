package sched

import "testing"

func TestNormalizedDefaults(t *testing.T) {
	got := SchedulerConfig{}.Normalized()
	if got.Policy != PolicyCostBudget || got.QuantumBytes != DefaultQuantumBytes || got.MinChargeBytes != DefaultMinChargeBytes {
		t.Fatalf("unexpected defaults: %+v", got)
	}

	got = SchedulerConfig{Policy: PolicyPacketQuantum, QuantumPackets: 2}.Normalized()
	if got.Policy != PolicyPacketQuantum || got.QuantumPackets != 2 {
		t.Fatalf("unexpected packet policy: %+v", got)
	}

	got = SchedulerConfig{Policy: PolicyPacketQuantum, QuantumPackets: MaxPacketsPerVisit + 1}.Normalized()
	if got.QuantumPackets != MaxPacketsPerVisit {
		t.Fatalf("packet quantum should be capped: %+v", got)
	}
}
