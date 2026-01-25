package switcher

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdminServer(t *testing.T) {
	// Setup Server
	server := NewServer("test-pwd")
	// Add some dummy context if possible, but empty is fine for status 200 check

	admin := NewAdminServer(server, ":0")

	// Test Stats
	req := httptest.NewRequest("GET", "/api/v1/stats", nil)
	w := httptest.NewRecorder()
	admin.handleStats(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for stats, got %d", w.Code)
	}
	var stats StatsResponse
	if err := json.NewDecoder(w.Body).Decode(&stats); err != nil {
		t.Errorf("decode stats failed: %v", err)
	}

	// Test Clients
	req = httptest.NewRequest("GET", "/api/v1/clients", nil)
	w = httptest.NewRecorder()
	admin.handleListClients(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for clients, got %d", w.Code)
	}
}
