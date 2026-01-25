package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/switcher"
)

var (
	addr      = flag.String("addr", ":8080", "http service address")
	adminAddr = flag.String("admin-addr", ":9090", "admin http service address")
	password  = flag.String("password", "test-pwd", "password for switcher")
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func main() {
	flag.Parse()

	// Initialize Switcher
	s := switcher.NewServer(*password)
	defer s.Close()

	// Handle WebSocket endpoint
	http.HandleFunc("/flex/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Print("upgrade:", err)
			return
		}

		log.Printf("new ws connection from %s", c.RemoteAddr())

		// Wrap WS connection with packet.Conn
		pConn := packet.NewWithWs(c)

		// Handover to switcher
		// Note: The third argument is 'authInfo', which we can leave nil for now or parse from URL/headers
		go s.HandlePacketConn(pConn, func(ctx *switcher.Context) {
			log.Printf("context loop start. domain=%s, id=%v", ctx.Domain, ctx.GetID())
		}, func(ctx *switcher.Context, duration time.Duration) {
			log.Printf("context loop stop. domain=%s, id=%v, dur=%v", ctx.Domain, ctx.GetID(), duration)
		})
	})

	// Serve static files for the web client
	// Assuming running from the root of the repo or where examples/web-client exists
	fs := http.FileServer(http.Dir("./examples/web-client/dist"))
	http.Handle("/", fs)

	log.Printf("Server starting on %s...", *addr)
	log.Printf("WebSocket endpoint: ws://localhost%s/flex/ws", *addr)

	// Start Admin Server
	admin := switcher.NewAdminServer(s, *adminAddr)
	go func() {
		log.Printf("Admin Server starting on %s...", *adminAddr)
		if err := admin.Start(); err != nil {
			log.Printf("Admin Server failed: %v", err)
		}
	}()

	// Start a background server to handle logic if needed, but here we just need the HTTP server
	// The switcher itself doesn't need a separate Run() call if we are injecting connections manually,
	// BUT switcher.Run(listener) is for TCP listener. We are bringing our own connections.

	err := http.ListenAndServe(*addr, nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
