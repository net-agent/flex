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

// BenchmarkSwitcherRelay 测量 node→switcher→node 全链路（两侧 FairConn）每包分配。
// allocs/op 包含 stream 层、双端 FairConn、switcher 读/转发/写出的全部分配。
// fair=false 的对照组用于分离 cloneBuffer 的占比。
func BenchmarkSwitcherRelay(b *testing.B) {
	for _, fair := range []bool{true, false} {
		for _, size := range []int{64, 1024, 32 * 1024} {
			b.Run(fmt.Sprintf("fair=%v/payload=%d", fair, size), func(b *testing.B) {
				pswd := "testpswd"
				quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
				s := NewServer(pswd, quiet, nil)
				defer s.Close()

				wrap := func(pc packet.Conn) packet.Conn {
					if fair {
						return sched.NewFairConn(pc)
					}
					return pc
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

				go func() { // drain
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
