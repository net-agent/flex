package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/net-agent/flex/v3/packet/ws"
	"github.com/net-agent/flex/v3/switcher"
)

var (
	addr     = flag.String("addr", ":8080", "http service address")
	password = flag.String("password", "test-pwd", "password for switcher")
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func main() {
	flag.Parse()

	// Initialize Switcher
	s := switcher.NewServer(*password, nil, nil)
	defer s.Close()

	s.OnContextStart = func(ctx *switcher.Context) {
		log.Printf("context loop start. domain=%s, id=%v", ctx.Domain, ctx.GetID())
	}
	s.OnContextStop = func(ctx *switcher.Context, duration time.Duration) {
		log.Printf("context loop stop. domain=%s, id=%v, dur=%v", ctx.Domain, ctx.GetID(), duration)
	}

	// Create Handler
	handler := newHandler(s)

	log.Printf("Server starting on %s...", *addr)
	log.Printf("WebSocket endpoint: ws://localhost%s/flex/ws", *addr)
	log.Printf("Admin stats: http://localhost%s/api/stats", *addr)

	if err := http.ListenAndServe(*addr, handler); err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}

func newHandler(s *switcher.Server) http.Handler {
	mux := http.NewServeMux()

	// Static Files
	fs := http.FileServer(http.Dir("./examples/web-client/dist"))
	mux.Handle("/", fs)

	// WebSocket
	mux.HandleFunc("/flex/ws", handleWS(s))

	// Admin API
	mux.HandleFunc("GET /api/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.GetStats())
	})
	mux.HandleFunc("GET /api/clients", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.GetClients())
	})

	return mux
}

func handleWS(s *switcher.Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Print("upgrade:", err)
			return
		}

		log.Printf("new ws connection from %s", c.RemoteAddr())

		// Wrap WS connection with packet.Conn
		pConn := ws.NewConn(c)

		// Handover to switcher
		go s.ServeConn(pConn)
	}
}
