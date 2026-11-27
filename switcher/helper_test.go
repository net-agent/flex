package switcher

import (
	"testing"
	"time"

	"github.com/net-agent/flex/v3/link"
	"github.com/net-agent/flex/v3/node"
	"github.com/net-agent/flex/v3/packet"
	"github.com/stretchr/testify/require"
)

func testDialLink(t *testing.T, pc packet.Conn, domain, pswd string) (*link.Link, uint16) {
	t.Helper()
	dialer := link.NewDialer(link.DialConfig{PSK: pswd, Domain: domain})
	lnk, ip, err := dialer.Dial(linkTransport(pc))
	if err != nil {
		t.Fatalf("dial %s: %v", domain, err)
	}
	return lnk, ip
}

func initTestEnv(t *testing.T, domain1, domain2 string) (*Server, *node.Node, *node.Node) {
	t.Helper()
	pswd := "testpswd"
	s := NewServer(pswd, nil, nil)
	pc1, pc2 := packet.Pipe()
	pc3, pc4 := packet.Pipe()

	go s.ServeConn(pc2)
	go s.ServeConn(pc4)

	lnk1, ip1 := testDialLink(t, pc1, domain1, pswd)
	node1 := node.NewFromLink(lnk1)
	node1.SetIP(ip1)
	node1.SetDomain(domain1)
	go node1.Serve()

	lnk2, ip2 := testDialLink(t, pc3, domain2, pswd)
	node2 := node.NewFromLink(lnk2)
	node2.SetIP(ip2)
	node2.SetDomain(domain2)
	go node2.Serve()

	t.Cleanup(func() {
		_ = node1.Close()
		_ = node2.Close()
		_ = s.Close()
	})

	// 等两个 context 都完成注册，替代固定 50ms sleep；失败时能直接定位。
	require.Eventually(t, func() bool {
		return len(s.GetClients()) == 2
	}, 2*time.Second, 5*time.Millisecond, "contexts not registered")

	return s, node1, node2
}
