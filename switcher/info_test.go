package switcher

import (
	"testing"
)

func TestServerInfo(t *testing.T) {
	// Setup Server
	server := NewServer("test-pwd")
	// Add some dummy context if possible, but empty is fine for stats check

	// Test Stats
	stats := server.GetStats()
	if stats == nil {
		t.Error("expected stats to be not nil")
	}

	// Test Clients
	clients := server.GetClients()
	if len(clients) != 0 {
		t.Errorf("expected 0 clients, got %d", len(clients))
	}
}
