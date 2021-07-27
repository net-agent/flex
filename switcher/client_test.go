package switcher

import (
	"net"
	"testing"

	"github.com/net-agent/flex/node"
)

func startTestServer(addr string) (*Server, error) {
	app := NewServer()

	l, err := net.Listen("tcp4", addr)
	if err != nil {
		return nil, err
	}

	go app.Run()
	go app.Serve(l)

	return app, nil
}

func TestClient(t *testing.T) {
	addr := "localhost:12345"
	_, err := startTestServer(addr)
	if err != nil {
		t.Error(err)
		return
	}

	client1, err := ConnectServer(addr, "test1")
	if err != nil {
		t.Error(err)
		return
	}

	client2, err := ConnectServer(addr, "test2")
	if err != nil {
		t.Error(err)
		return
	}

	node.HelpTest2Node(t, client1, client2, 1)
}
