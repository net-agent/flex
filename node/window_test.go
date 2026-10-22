package node

import (
	"testing"
	"time"

	"github.com/net-agent/flex/v3/internal/idpool"
	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/stream"
	"github.com/stretchr/testify/assert"
)

func TestEffectiveWindowSize(t *testing.T) {
	n := New(nil)
	assert.Equal(t, stream.DefaultWindowSize, n.effectiveWindowSize(), "unset config uses the default")

	n.SetFlowConfig(FlowConfig{MaxWindowSize: 256 * stream.KB})
	assert.Equal(t, int32(256*stream.KB), n.effectiveWindowSize())

	// BDP 模式
	n.SetFlowConfig(FlowConfig{Bandwidth: 1 << 20, RTT: 100 * time.Millisecond})
	assert.Equal(t, int32(1<<20/10), n.effectiveWindowSize())

	// 超过协议硬上限必须钳制
	n.SetFlowConfig(FlowConfig{MaxWindowSize: 1 << 30})
	assert.Equal(t, stream.MaxWindowSize, n.effectiveWindowSize())
}

func TestNegotiateWindow(t *testing.T) {
	portm, _ := idpool.New(1, 10)
	n := New(nil)
	d := &Dialer{}
	d.init(n, portm)

	assert.Equal(t, stream.DefaultWindowSize, d.negotiateWindow(0), "zero falls back to local effective window")
	assert.Equal(t, stream.DefaultWindowSize, d.negotiateWindow(0xFFFFFFFF), "overflow value must be clamped")
	assert.Equal(t, stream.DefaultWindowSize, d.negotiateWindow(0x7FFFFFFF), "huge value must be clamped")
	assert.Equal(t, int32(64*stream.KB), d.negotiateWindow(64*stream.KB))

	// 本端配置更小的窗口：对端应答不得超过本端值
	n.SetFlowConfig(FlowConfig{MaxWindowSize: 128 * stream.KB})
	assert.Equal(t, int32(128*stream.KB), d.negotiateWindow(8*stream.MB))
	assert.Equal(t, int32(64*stream.KB), d.negotiateWindow(64*stream.KB))
}

// 协商窗口必须对称：双方最终使用的窗口一致，否则接收缓冲上限
// 可能小于对端发送窗口，触发误判溢出。
func TestWindowNegotiationSymmetric(t *testing.T) {
	cases := []struct {
		name        string
		dialerLimit int32
		acceptLimit int32
		want        int32
	}{
		{"dialer smaller", 256 * stream.KB, 0, 256 * stream.KB},
		{"acceptor smaller", 0, 512 * stream.KB, 512 * stream.KB},
		{"both default", 0, 0, stream.DefaultWindowSize},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c1, c2 := packet.Pipe()
			n1 := New(c1)
			n1.SetDomain("w1")
			n1.SetIP(1)
			if tc.dialerLimit > 0 {
				n1.SetFlowConfig(FlowConfig{MaxWindowSize: tc.dialerLimit})
			}
			go n1.Serve()
			defer n1.Close()

			n2 := New(c2)
			n2.SetDomain("w2")
			n2.SetIP(2)
			if tc.acceptLimit > 0 {
				n2.SetFlowConfig(FlowConfig{MaxWindowSize: tc.acceptLimit})
			}
			go n2.Serve()
			defer n2.Close()

			ln, err := n2.Listen(80)
			if err != nil {
				t.Fatal(err)
			}
			accepted := make(chan *stream.Stream, 1)
			go func() {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				accepted <- conn.(*stream.Stream)
			}()

			s, err := n1.DialIP(2, 80)
			if err != nil {
				t.Fatalf("dial failed: %v", err)
			}
			defer s.Close()

			var peer *stream.Stream
			select {
			case peer = <-accepted:
			case <-time.After(2 * time.Second):
				t.Fatal("accept timeout")
			}
			defer peer.Close()

			assert.Equal(t, tc.want, s.GetWindowAvailable(), "dialer window")
			assert.Equal(t, tc.want, peer.GetWindowAvailable(), "acceptor window")
		})
	}
}
