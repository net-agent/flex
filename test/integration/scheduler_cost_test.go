package integration

import (
	"fmt"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/node"
	"github.com/net-agent/flex/v3/switcher"
	"github.com/stretchr/testify/require"
)

const (
	costDomain1Port = 1235
	costDomain2Port = 5679
)

// Exercise the cost-budget policy end to end: the switcher applies it to both
// data planes, and both sessions rebuild their nodes with the same policy.
func TestIntegration_CostBudgetScheduler(t *testing.T) {
	password := "test-password"
	logCfg := &switcher.LogConfig{
		Server:   slog.LevelWarn,
		Registry: slog.LevelWarn,
		Router:   slog.LevelWarn,
		Context:  slog.LevelWarn,
	}

	srv := switcher.NewServer(password, nil, logCfg)
	srv.SetSchedulerConfig(switcher.SchedulerConfig{
		Policy:         switcher.SchedulerPolicyCostBudget,
		QuantumBytes:   32 * 1024,
		MinChargeBytes: 1024,
	})
	defer srv.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	go srv.Serve(ln)

	costCfg := node.SchedulerConfig{
		Policy:         node.SchedulerPolicyCostBudget,
		QuantumBytes:   32 * 1024,
		MinChargeBytes: 1024,
	}

	sess1 := node.NewSession(tcpConnector(addr), node.SessionConfig{Domain: "cost1", Password: password})
	sess1.SetSchedulerConfig(costCfg)
	defer sess1.Close()
	go sess1.Serve()

	sess2 := node.NewSession(tcpConnector(addr), node.SessionConfig{Domain: "cost2", Password: password})
	sess2.SetSchedulerConfig(costCfg)
	defer sess2.Close()
	go sess2.Serve()

	ln1, err := sess1.Listen(costDomain1Port)
	require.NoError(t, err)
	go serveProtoService(ln1)

	ln2, err := sess2.Listen(costDomain2Port)
	require.NoError(t, err)
	go serveProtoService(ln2)

	require.NoError(t, sess1.WaitReady(5*time.Second))
	require.NoError(t, sess2.WaitReady(5*time.Second))

	p := testProfile{
		name:          "cost-budget",
		connPerDomain: 2,
		dataSize:      64 * 1024,
		minChunk:      1,
		maxChunk:      8 * 1024,
		connTimeout:   10 * time.Second,
	}

	var wg sync.WaitGroup
	for i := 0; i < p.connPerDomain; i++ {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			verifyProtocol(t, sess1, fmt.Sprintf("cost2:%d", costDomain2Port), idx, "d1->d2", p)
		}(i)
		go func(idx int) {
			defer wg.Done()
			verifyProtocol(t, sess2, fmt.Sprintf("cost1:%d", costDomain1Port), idx, "d2->d1", p)
		}(i)
	}
	wg.Wait()
}
