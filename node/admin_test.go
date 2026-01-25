package node

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/net-agent/flex/v2/packet"
)

func TestAdminServer(t *testing.T) {
	// Setup Node
	conn := &mockPacketConn{}
	node := New(conn)

	// Create Listener
	_, err := node.Listen(8080)
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}

	admin := NewAdminServer(node, ":0")

	// Test Info
	req := httptest.NewRequest("GET", "/api/v1/info", nil)
	w := httptest.NewRecorder()
	admin.handleInfo(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var info NodeInfo
	if err := json.NewDecoder(w.Body).Decode(&info); err != nil {
		t.Errorf("decode info failed: %v", err)
	}

	// Test Listeners
	req = httptest.NewRequest("GET", "/api/v1/listeners", nil)
	w = httptest.NewRecorder()
	admin.handleListeners(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var listeners []ListenerInfo
	if err := json.NewDecoder(w.Body).Decode(&listeners); err != nil {
		t.Errorf("decode listeners failed: %v", err)
	}
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
