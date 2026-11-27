package wire

import (
	"fmt"
	"sync"
)

// Lane selects the ordering/dispatch channel for a command at the endpoint.
// Lane is deliberately separate from Class: a control payload may still need
// stream ordering (for example AckOpenStream), and a no-payload command may be
// safe on the unordered lane.
type Lane uint8

const (
	// LaneControl is the unordered control lane.
	LaneControl Lane = iota
	// LaneOrdered is the per-stream ordered lane.
	LaneOrdered
)

func (l Lane) String() string {
	switch l {
	case LaneControl:
		return "control"
	case LaneOrdered:
		return "ordered"
	default:
		return fmt.Sprintf("lane(%d)", uint8(l))
	}
}

// Handler processes one frame. Control handlers must not retain the frame after
// returning; the dispatcher releases it. Data handlers may take ownership of
// the frame (the stream receive queue does) and must release it themselves.
type Handler func(*Frame) error

// Spec is a registered command handler.
type Spec struct {
	Class   Class
	Lane    Lane
	Handler Handler
}

// Registry maps command bytes to handlers for one endpoint (node or switcher).
// It is safe for concurrent lookups; registration is expected at startup.
type Registry struct {
	mu    sync.RWMutex
	specs map[Cmd]Spec
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{specs: make(map[Cmd]Spec)}
}

// RegisterControl registers a control command. The command's table class must
// be ClassControl; a mismatch (for example a DataCmd converted to ControlCmd)
// is rejected at startup.
func (r *Registry) RegisterControl(cmd ControlCmd, lane Lane, h Handler) error {
	return r.register(Cmd(cmd), ClassControl, lane, h)
}

// RegisterData registers an opaque-data command.
func (r *Registry) RegisterData(cmd DataCmd, lane Lane, h Handler) error {
	return r.register(Cmd(cmd), ClassData, lane, h)
}

// RegisterEmpty registers a no-payload command. Its class comes from the
// command table (control for Close, data for AckPushStreamData).
func (r *Registry) RegisterEmpty(cmd EmptyCmd, lane Lane, h Handler) error {
	c := Cmd(cmd)
	class, ok := ClassOf(c)
	if !ok {
		return fmt.Errorf("%w: 0x%02x", ErrUnknownCommand, byte(c))
	}
	if class == ClassHandshake {
		return fmt.Errorf("%w: handshake command %s is not routable", ErrClassMismatch, c)
	}
	return r.register(c, class, lane, h)
}

// MustRegisterControl is RegisterControl for static, startup-time registration.
func (r *Registry) MustRegisterControl(cmd ControlCmd, lane Lane, h Handler) {
	if err := r.RegisterControl(cmd, lane, h); err != nil {
		panic(err)
	}
}

// MustRegisterData is RegisterData for static, startup-time registration.
func (r *Registry) MustRegisterData(cmd DataCmd, lane Lane, h Handler) {
	if err := r.RegisterData(cmd, lane, h); err != nil {
		panic(err)
	}
}

// MustRegisterEmpty is RegisterEmpty for static, startup-time registration.
func (r *Registry) MustRegisterEmpty(cmd EmptyCmd, lane Lane, h Handler) {
	if err := r.RegisterEmpty(cmd, lane, h); err != nil {
		panic(err)
	}
}

func (r *Registry) register(cmd Cmd, want Class, lane Lane, h Handler) error {
	if h == nil {
		return fmt.Errorf("wire: nil handler for %s", cmd)
	}
	if lane != LaneControl && lane != LaneOrdered {
		return fmt.Errorf("wire: invalid lane %d for %s", lane, cmd)
	}
	class, ok := ClassOf(cmd)
	if !ok {
		return fmt.Errorf("%w: 0x%02x", ErrUnknownCommand, byte(cmd))
	}
	if class != want {
		return fmt.Errorf("%w: cmd=%s class=%s want=%s", ErrClassMismatch, cmd, class, want)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.specs[cmd]; exists {
		return fmt.Errorf("wire: duplicate handler for %s", cmd)
	}
	r.specs[cmd] = Spec{Class: class, Lane: lane, Handler: h}
	return nil
}

// Lookup returns the handler for a command.
func (r *Registry) Lookup(cmd Cmd) (Spec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	spec, ok := r.specs[cmd]
	return spec, ok
}

// Len returns the number of registered handlers.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.specs)
}

// Commands returns the registered command bytes (unordered).
func (r *Registry) Commands() []Cmd {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Cmd, 0, len(r.specs))
	for cmd := range r.specs {
		out = append(out, cmd)
	}
	return out
}
