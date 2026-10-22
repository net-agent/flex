package sched

// Policy selects the unit used to bound a per-stream visit.
type Policy int

const (
	// PolicyCostBudget is the default. It charges each packet
	// max(payload, MinChargeBytes) against QuantumBytes; a visit stops when the
	// next packet would exceed the budget. Fairness unit = approximate wire cost.
	PolicyCostBudget Policy = iota

	// PolicyPacketQuantum is the historical packet-quantum policy: a visit sends
	// at most QuantumPackets packets. Fairness unit = packet.
	PolicyPacketQuantum
)

const (
	// DefaultQuantumBytes / DefaultMinChargeBytes are the defaults for
	// PolicyCostBudget. With these values a visit carries at most 32 packets
	// (32KB / 1KB), so tiny packets cannot hold the wire for thousands of
	// packets while large packets still get ~32KB per visit.
	DefaultQuantumBytes   = 32 * 1024
	DefaultMinChargeBytes = 1024

	// MaxQuantumBytes is a sanity cap for the cost budget.
	MaxQuantumBytes = 16 * 1024 * 1024

	// MaxPacketsPerVisit bounds the scratch slice and the per-visit loop even
	// when MinChargeBytes is configured very small.
	MaxPacketsPerVisit = 1024
)

func (p Policy) String() string {
	switch p {
	case PolicyPacketQuantum:
		return "packet-quantum"
	default:
		return "cost-budget"
	}
}

// SchedulerConfig configures the FairWriter policy.
//
// PolicyCostBudget (zero value) is the default: charge =
// max(payload, MinChargeBytes), budget = QuantumBytes, and at most
// QuantumBytes/MinChargeBytes packets per visit. PolicyPacketQuantum retains
// the historical behavior for callers that want packet-based fairness.
type SchedulerConfig struct {
	Policy         Policy
	QuantumPackets int // PolicyPacketQuantum
	QuantumBytes   int // PolicyCostBudget
	MinChargeBytes int // PolicyCostBudget
}

// DefaultSchedulerConfig returns the cost-budget policy (B).
func DefaultSchedulerConfig() SchedulerConfig {
	return SchedulerConfig{
		Policy:         PolicyCostBudget,
		QuantumBytes:   DefaultQuantumBytes,
		MinChargeBytes: DefaultMinChargeBytes,
	}
}

// Normalized returns a copy with defaults filled and values clamped. It is
// useful for callers that keep the effective configuration for observability.
func (c SchedulerConfig) Normalized() SchedulerConfig { return c.normalized() }

// normalized clamps the config into a safe range and fills defaults.
func (c SchedulerConfig) normalized() SchedulerConfig {
	if c.Policy == PolicyPacketQuantum {
		if c.QuantumPackets <= 0 {
			c.QuantumPackets = DefaultQuantum
		}
		if c.QuantumPackets > MaxPacketsPerVisit {
			c.QuantumPackets = MaxPacketsPerVisit
		}
		return c
	}
	c.Policy = PolicyCostBudget

	if c.QuantumBytes <= 0 {
		c.QuantumBytes = DefaultQuantumBytes
	}
	if c.QuantumBytes > MaxQuantumBytes {
		c.QuantumBytes = MaxQuantumBytes
	}
	if c.MinChargeBytes <= 0 {
		c.MinChargeBytes = DefaultMinChargeBytes
	}
	if c.MinChargeBytes > c.QuantumBytes {
		c.MinChargeBytes = c.QuantumBytes
	}
	return c
}

// visitLimit is the maximum number of packets that can be drained in one visit.
func (c SchedulerConfig) visitLimit() int {
	if c.Policy == PolicyPacketQuantum {
		return c.QuantumPackets
	}
	n := c.QuantumBytes / c.MinChargeBytes
	if n < 1 {
		n = 1
	}
	if n > MaxPacketsPerVisit {
		n = MaxPacketsPerVisit
	}
	return n
}
