package switcher

import (
	"bytes"
	"log/slog"
	"testing"

	domainpkg "github.com/net-agent/flex/v3/internal/domain"
	"github.com/net-agent/flex/v3/internal/idpool"
	"github.com/net-agent/flex/v3/internal/sched"
	"github.com/stretchr/testify/require"
)

func TestNewContextRegistryNilLogger(t *testing.T) {
	ipm, err := idpool.New(1, 100)
	require.NoError(t, err)

	reg := newContextRegistry(ipm, nil, nil)

	require.NotNil(t, reg.logger)
	require.Same(t, ipm, reg.ipm)
	require.Nil(t, reg.presence)
	require.NotNil(t, reg.domainIndex)
	require.NotNil(t, reg.ipIndex)
	require.Positive(t, reg.domainPingTimeout)
}

func TestRegistryAttachRejectsInvalidDomain(t *testing.T) {
	s := NewServer("", nil, nil)
	t.Cleanup(func() { _ = s.Close() })

	ctx := NewContext(1, nil, "bad domain!", "", nil)
	err := s.registry.attach(ctx)

	require.ErrorIs(t, err, domainpkg.ErrInvalidDomain)
	require.False(t, ctx.isAttached())
	require.Zero(t, ctx.IP)
	require.Zero(t, s.registry.ipm.InUse())
	require.Empty(t, s.registry.activeContexts())
}

func TestSchedulerDefaultConfig(t *testing.T) {
	cfg := DefaultSchedulerConfig()

	require.Equal(t, SchedulerPolicyCostBudget, cfg.Policy)
	require.Equal(t, sched.DefaultQuantumBytes, cfg.QuantumBytes)
	require.Equal(t, sched.DefaultMinChargeBytes, cfg.MinChargeBytes)
	require.Zero(t, cfg.QuantumPackets)
}

func TestLoggerWithGroup(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	logger := newModuleLogger(base, slog.LevelInfo, "testmod")

	logger.WithGroup("grp").Info("hello", "key", "value")

	out := buf.String()
	require.Contains(t, out, "grp.key=value", "WithGroup must preserve the group in output")
	require.Contains(t, out, "module=testmod")
}
