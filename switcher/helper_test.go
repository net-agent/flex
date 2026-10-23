package switcher

import (
	"testing"
	"time"

	"github.com/net-agent/flex/v3/link"
	"github.com/net-agent/flex/v3/node"
	"github.com/net-agent/flex/v3/packet"
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

	<-time.After(time.Millisecond * 50)

	return s, node1, node2
}
