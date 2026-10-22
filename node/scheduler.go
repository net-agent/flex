package node

import "github.com/net-agent/flex/v3/internal/sched"

// SchedulerPolicy selects how a Session's FairConn bounds each per-stream visit.
type SchedulerPolicy int

const (
	// SchedulerPolicyPackets is the historical packet-quantum policy.
	SchedulerPolicyPackets SchedulerPolicy = iota

	// SchedulerPolicyCostBudget charges max(payload, MinChargeBytes) per packet
	// against QuantumBytes. It bounds both wire cost and packets per visit.
	SchedulerPolicyCostBudget
)

// SchedulerConfig configures the FairConn policy used by a Session after each
// (re)connect. The zero policy is SchedulerPolicyPackets, preserving current
// behavior. The fields mirror switcher.SchedulerConfig.
//
// SetSchedulerConfig must be called before Serve; the config is applied to the
// Node rebuilt on every reconnect.
type SchedulerConfig struct {
	Policy         SchedulerPolicy
	QuantumPackets int // SchedulerPolicyPackets; default 2
	QuantumBytes   int // SchedulerPolicyCostBudget; default 32KB
	MinChargeBytes int // SchedulerPolicyCostBudget; default 1KB
}

// DefaultSchedulerConfig returns the cost-budget policy (B).
func DefaultSchedulerConfig() SchedulerConfig {
	return SchedulerConfig{
		Policy:         SchedulerPolicyCostBudget,
		QuantumBytes:   sched.DefaultQuantumBytes,
		MinChargeBytes: sched.DefaultMinChargeBytes,
	}
}

func (c SchedulerConfig) toInternal() sched.SchedulerConfig {
	out := sched.DefaultSchedulerConfig()
	out.QuantumPackets = c.QuantumPackets
	out.QuantumBytes = c.QuantumBytes
	out.MinChargeBytes = c.MinChargeBytes
	if c.Policy == SchedulerPolicyPackets {
		out.Policy = sched.PolicyPacketQuantum
	} else {
		out.Policy = sched.PolicyCostBudget
	}
	return out.Normalized()
}

// SetSchedulerConfig selects the FairConn policy used by the Node rebuilt after
// each connect/reconnect. Must be called before Serve.
func (s *Session) SetSchedulerConfig(cfg SchedulerConfig) {
	internal := cfg.toInternal()
	s.schedMu.Lock()
	s.schedCfg = internal
	s.schedMu.Unlock()
}

func (s *Session) schedulerConfig() sched.SchedulerConfig {
	s.schedMu.RLock()
	defer s.schedMu.RUnlock()
	return s.schedCfg
}
