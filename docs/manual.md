# Flex Developer Manual

This manual provides detailed instructions on how to use the `flex` library to build multiplexed network applications.

## Table of Contents
1.  [Core Concepts](#core-concepts)
2.  [Node (The Agent)](#node-the-agent)
3.  [Switcher (The Relay)](#switcher-the-relay)
4.  [Fairness & Scheduling](#fairness--scheduling)
5.  [Observability & Control](#observability--control)

---

## Core Concepts

### Packet
The fundamental unit of transmission. `flex` chops stream data into small chunks (Packets) to interleave them over the physical connection. This allows multiple streams to share bandwidth fairly.

### Stream
A virtual, reliable, ordered connection (implementing `net.Conn`). You can create thousands of streams over a single `flex` connection.

### Node
An endpoint in the `flex` network. A Node can:
-   **Dial**: Initiate streams to other Nodes.
-   **Listen**: Accept streams from other Nodes.
-   **Ping**: Check latency to other Nodes.

### Switcher
A central relay server. It connects multiple Nodes and routes packets between them based on **Domain** names or Virtual IPs.

---

## Node (The Agent)

The `node` package provides the client-side logic.

### Initialization
To create a node, you need an underlying `packet.Conn` (which wraps a `net.Conn` or `websocket.Conn`).

```go
import (
    "github.com/net-agent/flex/v2/node"
    "github.com/net-agent/flex/v2/packet"
)

// Wrap your physical connection
pconn := packet.NewWithConn(netConn)
// or for WebSocket
// pconn := packet.NewWithWs(wsConn)

// Create Node
n := node.New(pconn)
n.SetDomain("my-agent") // Set a unique name
go n.Serve()            // Start processing loop
```

### Listening (Virtual Ports)
Flex supports virtual ports (uint16). You can listen on them just like TCP ports.

```go
listener, err := n.Listen(8080)
if err != nil {
    panic(err)
}

for {
    conn, err := listener.Accept() // conn is *stream.Stream
    go handle(conn)
}
```

### Dialing
You can dial other nodes by **Domain** or **IP**.

```go
// Dial by Domain
conn, err := n.Dial("target-agent:8080")

// Dial by Virtual IP
// conn, err := n.Dial("10.0.0.5:8080")
```

---

## Switcher (The Relay)

The `switcher` package allows you to build a gateway server.

### Basic Setup
The Switcher doesn't listen on a TCP port itself; it handles `packet.Conn` objects you hand to it.

```go
import "github.com/net-agent/flex/v2/switcher"

s := switcher.NewServer("secret-password")

// In your TCP/WS accept loop:
go s.HandlePacketConn(pconn, onStart, onStop)
```

### Context & Routing
When a Node connects to a Switcher, it becomes a `Context`. The Switcher maintains a routing table of Domains -> Contexts.

---

## Fairness & Scheduling

Flex v2 introduces **Fair Queuing**.
-   **Problem**: In v1, a large file transfer could block ACKs or Pings, causing timeouts.
-   **Solution**: `FairWriter` queues packets from different streams separately and services them in a round-robin fashion.
-   **Usage**: Enabled automatically. `node.New` and `switcher.NewServer` wrap connections in `FairConn` by default.

---

## Observability & Control

Flex provides a built-in HTTP Admin API for deep visibility.

### Node Admin
Start the admin server for a Node:

```go
admin := node.NewAdminServer(n, ":9091")
go admin.Start()
```

**Endpoints**:
-   `GET /api/v1/info`: Basic stats (traffic, uptime).
-   `GET /api/v1/streams`: List active streams with RTT and buffer state.
-   `GET /api/v1/listeners`: List active virtual listeners.

### Switcher Admin
Start the admin server for a Switcher:

```go
admin := switcher.NewAdminServer(s, ":9090")
go admin.Start()
```

**Endpoints**:
-   `GET /api/v1/clients`: List all connected agents with real-time metrics (Stream count, Bandwidth, RTT).
-   `DELETE /api/v1/clients/{domain}`: Kick an agent.

### Dependencies
The Admin API uses `github.com/gorilla/mux`. Ensure you have it installed:
```bash
go get -u github.com/gorilla/mux
```
