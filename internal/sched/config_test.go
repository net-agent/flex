package sched

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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

func TestPolicyString(t *testing.T) {
	tests := []struct {
		name   string
		policy Policy
		want   string
	}{
		{name: "cost budget", policy: PolicyCostBudget, want: "cost-budget"},
		{name: "packet quantum", policy: PolicyPacketQuantum, want: "packet-quantum"},
		{name: "unknown policy falls back to cost budget", policy: Policy(42), want: "cost-budget"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.policy.String())
		})
	}
}

func TestDefaultSchedulerConfig(t *testing.T) {
	want := SchedulerConfig{
		Policy:         PolicyCostBudget,
		QuantumBytes:   DefaultQuantumBytes,
		MinChargeBytes: DefaultMinChargeBytes,
	}
	got := DefaultSchedulerConfig()
	require.Equal(t, want, got)
	require.Equal(t, PolicyCostBudget, got.Policy)
	require.Zero(t, got.QuantumPackets, "cost-budget default must not set the packet quantum")

	// The defaults are already normalized, so normalizing must be a no-op.
	require.Equal(t, want, got.Normalized())
}

func TestNormalized(t *testing.T) {
	tests := []struct {
		name string
		in   SchedulerConfig
		want SchedulerConfig
	}{
		{
			name: "zero value fills cost-budget defaults",
			in:   SchedulerConfig{},
			want: SchedulerConfig{Policy: PolicyCostBudget, QuantumBytes: DefaultQuantumBytes, MinChargeBytes: DefaultMinChargeBytes},
		},
		{
			name: "explicit cost-budget values are kept",
			in:   SchedulerConfig{Policy: PolicyCostBudget, QuantumBytes: 4096, MinChargeBytes: 512},
			want: SchedulerConfig{Policy: PolicyCostBudget, QuantumBytes: 4096, MinChargeBytes: 512},
		},
		{
			name: "negative quantum bytes get the default",
			in:   SchedulerConfig{QuantumBytes: -1, MinChargeBytes: 128},
			want: SchedulerConfig{Policy: PolicyCostBudget, QuantumBytes: DefaultQuantumBytes, MinChargeBytes: 128},
		},
		{
			name: "zero quantum bytes get the default",
			in:   SchedulerConfig{QuantumBytes: 0, MinChargeBytes: 128},
			want: SchedulerConfig{Policy: PolicyCostBudget, QuantumBytes: DefaultQuantumBytes, MinChargeBytes: 128},
		},
		{
			name: "quantum bytes above the cap are clamped",
			in:   SchedulerConfig{QuantumBytes: MaxQuantumBytes + 1, MinChargeBytes: 1024},
			want: SchedulerConfig{Policy: PolicyCostBudget, QuantumBytes: MaxQuantumBytes, MinChargeBytes: 1024},
		},
		{
			name: "quantum bytes at the cap are unchanged",
			in:   SchedulerConfig{QuantumBytes: MaxQuantumBytes, MinChargeBytes: 1024},
			want: SchedulerConfig{Policy: PolicyCostBudget, QuantumBytes: MaxQuantumBytes, MinChargeBytes: 1024},
		},
		{
			name: "non-positive min charge gets the default",
			in:   SchedulerConfig{QuantumBytes: 8192, MinChargeBytes: -1},
			want: SchedulerConfig{Policy: PolicyCostBudget, QuantumBytes: 8192, MinChargeBytes: DefaultMinChargeBytes},
		},
		{
			name: "min charge above the quantum is clamped to the quantum",
			in:   SchedulerConfig{QuantumBytes: 512, MinChargeBytes: 1024},
			want: SchedulerConfig{Policy: PolicyCostBudget, QuantumBytes: 512, MinChargeBytes: 512},
		},
		{
			name: "defaulted min charge is clamped by a tiny quantum",
			in:   SchedulerConfig{QuantumBytes: 256},
			want: SchedulerConfig{Policy: PolicyCostBudget, QuantumBytes: 256, MinChargeBytes: 256},
		},
		{
			name: "min charge equal to the quantum is unchanged",
			in:   SchedulerConfig{QuantumBytes: 4096, MinChargeBytes: 4096},
			want: SchedulerConfig{Policy: PolicyCostBudget, QuantumBytes: 4096, MinChargeBytes: 4096},
		},
		{
			name: "unknown policy is normalized to cost budget",
			in:   SchedulerConfig{Policy: Policy(7), QuantumBytes: 4096, MinChargeBytes: 1024},
			want: SchedulerConfig{Policy: PolicyCostBudget, QuantumBytes: 4096, MinChargeBytes: 1024},
		},
		{
			name: "packet quantum zero gets the historical default",
			in:   SchedulerConfig{Policy: PolicyPacketQuantum, QuantumPackets: 0},
			want: SchedulerConfig{Policy: PolicyPacketQuantum, QuantumPackets: DefaultQuantum},
		},
		{
			name: "packet quantum negative gets the historical default",
			in:   SchedulerConfig{Policy: PolicyPacketQuantum, QuantumPackets: -1},
			want: SchedulerConfig{Policy: PolicyPacketQuantum, QuantumPackets: DefaultQuantum},
		},
		{
			name: "packet quantum above the cap is clamped",
			in:   SchedulerConfig{Policy: PolicyPacketQuantum, QuantumPackets: MaxPacketsPerVisit + 1},
			want: SchedulerConfig{Policy: PolicyPacketQuantum, QuantumPackets: MaxPacketsPerVisit},
		},
		{
			name: "packet quantum at the cap is unchanged",
			in:   SchedulerConfig{Policy: PolicyPacketQuantum, QuantumPackets: MaxPacketsPerVisit},
			want: SchedulerConfig{Policy: PolicyPacketQuantum, QuantumPackets: MaxPacketsPerVisit},
		},
		{
			name: "packet policy leaves cost fields untouched",
			in:   SchedulerConfig{Policy: PolicyPacketQuantum, QuantumPackets: 3, QuantumBytes: -1, MinChargeBytes: -2},
			want: SchedulerConfig{Policy: PolicyPacketQuantum, QuantumPackets: 3, QuantumBytes: -1, MinChargeBytes: -2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.in.Normalized()
			require.Equal(t, tt.want, got)
			require.Equal(t, got, got.Normalized(), "Normalized must be idempotent")
		})
	}
}

// TestVisitLimit pins down the per-visit packet bound. visitLimit currently has
// no production caller (疑似死代码); the test exists so that its arithmetic is
// still specified if the fair writer ever starts using it.
func TestVisitLimit(t *testing.T) {
	tests := []struct {
		name string
		in   SchedulerConfig
		want int
	}{
		{
			name: "packet quantum returns the configured packet count",
			in:   SchedulerConfig{Policy: PolicyPacketQuantum, QuantumPackets: 7},
			want: 7,
		},
		{
			name: "cost budget divides bytes by min charge",
			in:   SchedulerConfig{Policy: PolicyCostBudget, QuantumBytes: 32 * 1024, MinChargeBytes: 1024},
			want: 32,
		},
		{
			name: "cost budget rounds up to at least one packet",
			in:   SchedulerConfig{Policy: PolicyCostBudget, QuantumBytes: 100, MinChargeBytes: 200},
			want: 1,
		},
		{
			name: "cost budget exactly at the packet cap is unchanged",
			in:   SchedulerConfig{Policy: PolicyCostBudget, QuantumBytes: MaxPacketsPerVisit * 8, MinChargeBytes: 8},
			want: MaxPacketsPerVisit,
		},
		{
			name: "cost budget above the packet cap is clamped",
			in:   SchedulerConfig{Policy: PolicyCostBudget, QuantumBytes: MaxQuantumBytes, MinChargeBytes: 1},
			want: MaxPacketsPerVisit,
		},
		{
			name: "default config allows 32 packets per visit",
			in:   DefaultSchedulerConfig(),
			want: DefaultQuantumBytes / DefaultMinChargeBytes,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.in.visitLimit())
		})
	}
}
