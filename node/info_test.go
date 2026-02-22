package node

import (
	"net"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/packet"
)

func TestNodeInfo(t *testing.T) {
	// Setup Node
	conn := &mockPacketConn{}
	node := New(conn)

	// Create Listener
	_, err := node.Listen(8080)
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}

	// Test Info
	info := node.GetInfo()
	if info == nil {
		t.Error("expected info to be not nil")
	}

	// Test Listeners
	listeners := node.GetListeners()
	if len(listeners) != 1 || listeners[0].Port != 8080 {
		t.Errorf("expected 1 listener on port 8080, got %v", listeners)
	}
}

type mockPacketConn struct{}

func (m *mockPacketConn) ReadBuffer() (*packet.Buffer, error)  { return nil, nil }
func (m *mockPacketConn) WriteBuffer(buf *packet.Buffer) error { return nil }
func (m *mockPacketConn) Close() error                         { return nil }
func (m *mockPacketConn) SetReadTimeout(d time.Duration) error { return nil }
func (m *mockPacketConn) SetWriteTimeout(d time.Duration)      {}
func (m *mockPacketConn) GetRawConn() net.Conn                 { return nil }
