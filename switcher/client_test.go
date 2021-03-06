package switcher

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/net-agent/flex/v2/node"
)

var testPassword = "abc"

func TestClient(t *testing.T) {
	client1, client2, err := makeTwoNodes("localhost:12345")
	if err != nil {
		t.Error(err)
		return
	}
	node.ExampleOf2NodeTest(t, client1, client2, 0)
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

	// dial error test
	_, err = client1.DialDomain("notexist", 80)
	if err == nil {
		t.Error("unexpected nil err")
		return
	}
	if !strings.HasPrefix(err.Error(), "resolve failed") {
		t.Error("unexpected err", err)
		return
	}
}

func makeTwoNodes(addr string) (*node.Node, *node.Node, error) {
	s, err := startTestServer(addr)
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

	info := s.PrintCtxRecords()
	buf, err := json.Marshal(info)
	if err == nil {
		fmt.Printf("server context: %v\n", string(buf))
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
