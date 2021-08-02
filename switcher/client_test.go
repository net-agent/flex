package switcher

import (
	"net"
	"testing"

	"github.com/net-agent/flex/node"
)

var testPassword = "abc"

func TestClient(t *testing.T) {
	client1, client2, err := makeTwoNodes("localhost:12345")
	if err != nil {
		t.Error(err)
		return
	}
	node.HelpTest2Node(t, client1, client2, 0)
}

func TestDialDomain(t *testing.T) {
	client1, client2, err := makeTwoNodes("localhost:12346")
	if err != nil {
		t.Error(err)
		return
	}
	go client1.Run()
	go client2.Run()
	defer client1.Close()
	defer client2.Close()

	_, err = client1.DialDomain("test2", 80)
	if err == nil {
		t.Error("unexpected nil err")
		return
	}

	_, err = client2.Listen(80)
	if err != nil {
		t.Error(err)
		return
	}

	c, err := client1.DialDomain("test2", 80)
	if err != nil {
		t.Error(err)
		return
	}
	c.Close()
}

func makeTwoNodes(addr string) (*node.Node, *node.Node, error) {
	_, err := startTestServer(addr)
	if err != nil {
		return nil, nil, err
	}

	client1, err := ConnectServer(addr, "test1", "mac1", testPassword)
	if err != nil {
		return nil, nil, err
	}

	client2, err := ConnectServer(addr, "test2", "mac2", testPassword)
	if err != nil {
		return nil, nil, err
	}

	return client1, client2, nil
}

func startTestServer(addr string) (*Server, error) {
	app := NewServer(testPassword)

	l, err := net.Listen("tcp4", addr)
	if err != nil {
		return nil, err
	}

	go app.Serve(l)

	return app, nil
}
