package node

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
)

type AdminServer struct {
	node *Node
	http *http.Server
}

func NewAdminServer(node *Node, addr string) *AdminServer {
	return &AdminServer{
		node: node,
		http: &http.Server{
			Addr: addr,
		},
	}
}

func (as *AdminServer) Start() error {
	r := mux.NewRouter()
	v1 := r.PathPrefix("/api/v1").Subrouter()

	v1.HandleFunc("/info", as.handleInfo).Methods("GET")
	v1.HandleFunc("/streams", as.handleStreams).Methods("GET")
	v1.HandleFunc("/listeners", as.handleListeners).Methods("GET")

	as.http.Handler = r
	return as.http.ListenAndServe()
}

func (as *AdminServer) Stop() error {
	return as.http.Close()
}

// Responses

type NodeInfo struct {
	Domain       string `json:"domain"`
	IP           uint16 `json:"ip"`
	Network      string `json:"network"`
	Uptime       int64  `json:"uptime_seconds"`
	BytesRead    int64  `json:"bytes_read"`
	BytesWritten int64  `json:"bytes_written"`
}

type ListenerInfo struct {
	Port uint16 `json:"port"`
	Addr string `json:"addr"`
}

func (as *AdminServer) handleInfo(w http.ResponseWriter, r *http.Request) {
	read, written := as.node.GetReadWriteSize()

	info := NodeInfo{
		Domain:       as.node.GetDomain(),
		IP:           as.node.GetIP(),
		Network:      as.node.GetNetwork(),
		BytesRead:    read,
		BytesWritten: written,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

func (as *AdminServer) handleStreams(w http.ResponseWriter, r *http.Request) {
	streams := as.node.GetStreamStateList()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(streams)
}

func (as *AdminServer) handleListeners(w http.ResponseWriter, r *http.Request) {
	listeners := as.node.GetActiveListeners()
	infos := make([]ListenerInfo, 0, len(listeners))
	for _, l := range listeners {
		infos = append(infos, ListenerInfo{
			Port: l.port,
			Addr: l.str,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(infos)
}
