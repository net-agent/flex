package switcher

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
)

type AdminServer struct {
	server *Server
	http   *http.Server
}

func NewAdminServer(s *Server, addr string) *AdminServer {
	return &AdminServer{
		server: s,
		http: &http.Server{
			Addr: addr,
		},
	}
}

func (as *AdminServer) Start() error {
	r := mux.NewRouter()
	v1 := r.PathPrefix("/api/v1").Subrouter()

	v1.HandleFunc("/stats", as.handleStats).Methods("GET")
	v1.HandleFunc("/clients", as.handleListClients).Methods("GET")
	v1.HandleFunc("/clients/{domain}", as.handleKickClient).Methods("DELETE")

	as.http.Handler = r
	return as.http.ListenAndServe()
}

func (as *AdminServer) Stop() error {
	return as.http.Close()
}

// Responses
type StatsResponse struct {
	ActiveConnections int   `json:"active_connections"`
	TotalContexts     int64 `json:"total_contexts"`
	UptimeSeconds     int64 `json:"uptime_seconds"`
}

type ClientInfo struct {
	ID          int         `json:"id"`
	Domain      string      `json:"domain"`
	IP          uint16      `json:"ip"`
	Mac         string      `json:"mac"`
	ConnectedAt time.Time   `json:"connected_at"`
	Stats       ClientStats `json:"stats"`
}

type ClientStats struct {
	StreamCount   int32  `json:"active_streams"`
	BytesReceived int64  `json:"bytes_in"`
	BytesSent     int64  `json:"bytes_out"`
	LastRTT       string `json:"rtt"`
	LastRTTMs     int64  `json:"rtt_ms"`
}

func (as *AdminServer) handleStats(w http.ResponseWriter, r *http.Request) {
	ctxs := as.server.GetActiveContexts()
	resp := StatsResponse{
		ActiveConnections: len(ctxs),
		TotalContexts:     int64(atomic.LoadInt32(&ctxindex)),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (as *AdminServer) handleListClients(w http.ResponseWriter, r *http.Request) {
	ctxs := as.server.GetActiveContexts()
	infos := make([]ClientInfo, 0, len(ctxs))

	for _, ctx := range ctxs {
		rtt := ctx.Stats.LastRTT

		info := ClientInfo{
			ID:          ctx.id,
			Domain:      ctx.Domain,
			IP:          ctx.IP,
			Mac:         ctx.Mac,
			ConnectedAt: ctx.AttachTime,
			Stats: ClientStats{
				StreamCount:   atomic.LoadInt32(&ctx.Stats.StreamCount),
				BytesReceived: atomic.LoadInt64(&ctx.Stats.BytesReceived),
				BytesSent:     atomic.LoadInt64(&ctx.Stats.BytesSent),
				LastRTT:       rtt.String(),
				LastRTTMs:     rtt.Milliseconds(),
			},
		}
		infos = append(infos, info)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(infos)
}

func (as *AdminServer) handleKickClient(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	domain := vars["domain"]

	ctx, err := as.server.GetContextByDomain(domain)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	as.server.DetachCtx(ctx)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Client %s disconnected", domain)
}
