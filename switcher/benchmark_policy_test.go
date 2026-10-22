package switcher

import (
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/net-agent/flex/v3/internal/admit"
	"github.com/net-agent/flex/v3/internal/sched"
	"github.com/net-agent/flex/v3/node"
	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/stream"
)

// BenchmarkSwitcherSchedulerPolicies compares the packet-quantum policy (C)
// with the cost-budget policy (B) on the full node→switcher→node path.
func BenchmarkSwitcherSchedulerPolicies(b *testing.B) {
	variants := []struct {
		name string
		cfg  sched.SchedulerConfig
	}{
		{"C1", sched.SchedulerConfig{Policy: sched.PolicyPacketQuantum, QuantumPackets: 1}},
		{"C2", sched.SchedulerConfig{Policy: sched.PolicyPacketQuantum, QuantumPackets: 2}},
		{"C4", sched.SchedulerConfig{Policy: sched.PolicyPacketQuantum, QuantumPackets: 4}},
		{"B-32K-1K", sched.SchedulerConfig{Policy: sched.PolicyCostBudget, QuantumBytes: 32 * 1024, MinChargeBytes: 1024}},
	}

	for _, v := range variants {
		for _, size := range []int{1024, 32 * 1024} {
			b.Run(fmt.Sprintf("%s/payload=%d", v.name, size), func(b *testing.B) {
				pswd := "testpswd"
				quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
				s := NewServer(pswd, quiet, nil)
				defer s.Close()

				wrap := func(pc packet.Conn) packet.Conn {
					return sched.NewFairConnWithConfig(pc, v.cfg)
				}
				connect := func(domain string) *node.Node {
					c1, c2 := packet.Pipe()
					go s.ServeConn(wrap(c2))
					ip, err := admit.Handshake(c1, domain, "", pswd)
					if err != nil {
						b.Fatal(err)
					}
					n := node.New(wrap(c1))
					n.SetIP(ip)
					n.SetDomain(domain)
					n.SetLogger(quiet)
					go n.Serve()
					return n
				}

				n1 := connect("bench-a")
				defer n1.Close()
				n2 := connect("bench-b")
				defer n2.Close()

				ln, err := n2.Listen(80)
				if err != nil {
					b.Fatal(err)
				}
				st, err := n1.Dial("bench-b:80")
				if err != nil {
					b.Fatal(err)
				}
				conn, err := ln.Accept()
				if err != nil {
					b.Fatal(err)
				}
				st2 := conn.(*stream.Stream)
				go func() {
					buf := make([]byte, 64*1024)
					for {
						if _, err := st2.Read(buf); err != nil {
							return
						}
					}
				}()

				payload := make([]byte, size)
				b.SetBytes(int64(size))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := st.Write(payload); err != nil {
						b.Fatal(err)
					}
				}
				b.StopTimer()
			})
		}
	}
}
