package switcher

import "github.com/net-agent/flex/v3/internal/sched"

// SchedulerPolicy selects how a FairConn bounds each per-stream visit.
type SchedulerPolicy int

const (
	// SchedulerPolicyCostBudget is the default. It charges
	// max(payload, MinChargeBytes) per packet against QuantumBytes. It bounds
	// both the wire cost and the packet count of a visit, which keeps 1-byte
	// packet streams from monopolizing the link.
	SchedulerPolicyCostBudget SchedulerPolicy = iota

	// SchedulerPolicyPackets is the historical packet-quantum policy:
	// a visit sends at most QuantumPackets packets.
	SchedulerPolicyPackets
)

// SchedulerConfig configures FairConn scheduling for newly accepted
// connections. The zero policy is SchedulerPolicyCostBudget, the default.
//
// The fields mirror node.SchedulerConfig; both map to the internal scheduler
// policy. SetSchedulerConfig must be called before Serve (existing connections
// keep their policy).
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

// SetSchedulerConfig selects the FairConn scheduling policy for connections
// accepted after this call. It is safe to call before Serve; doing so after
// Serve only affects future connections.
func (s *Server) SetSchedulerConfig(cfg SchedulerConfig) {
	internal := cfg.toInternal()
	s.fairMu.Lock()
	s.fairCfg = internal
	s.fairMu.Unlock()
}

func (s *Server) schedulerConfig() sched.SchedulerConfig {
	s.fairMu.RLock()
	defer s.fairMu.RUnlock()
	return s.fairCfg
}
