# Flex Developer Manual

This manual provides detailed instructions on how to use the `flex` library to build multiplexed network applications.

## Table of Contents
1.  [Core Concepts](#core-concepts)
2.  [Node (The Agent)](#node-the-agent)
3.  [Switcher (The Relay)](#switcher-the-relay)
4.  [Fairness & Scheduling](#fairness--scheduling)
5.  [Observability](#observability)

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
    "github.com/net-agent/flex/v3/node"
    "github.com/net-agent/flex/v3/packet"
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
import "github.com/net-agent/flex/v3/switcher"

s := switcher.NewServer("secret-password", nil, nil)

// Optional lifecycle hooks:
// s.OnContextStart = func(ctx *switcher.Context) { ... }
// s.OnContextStop  = func(ctx *switcher.Context, d time.Duration) { ... }

// In your TCP/WS accept loop:
go s.ServeConn(pconn)
```

### Context & Routing
When a Node connects to a Switcher, it becomes a `Context`. The Switcher maintains a routing table of Domains -> Contexts.

---

## Fairness & Scheduling

Flex v2 introduces **Fair Queuing**.
-   **Problem**: In v1, a large file transfer could block ACKs or Pings, causing timeouts.
-   **Solution**: `FairWriter` queues packets from different streams separately and services them in a round-robin fashion.
-   **Usage**: Enabled by default where connections are managed for you: `switcher.Server` wraps every accepted connection, and `node.Session` wraps its connection. Disable with `SetEnableFairConn(false)` on either. A bare `node.New` uses the connection as-is.

---

## Observability

Flex exposes runtime state programmatically. There is no built-in HTTP server;
you decide how to expose the data (JSON over HTTP, metrics, logs). All
snapshot types carry JSON tags, so `encoding/json` works out of the box.

### Node: Pull-style Snapshots

```go
info := n.GetInfo()         // domain, IP, network, uptime, bytes read/written
listeners := n.GetListeners()
running := n.IsRunning()    // whether the dispatcher is serving

snap := n.Inspect()         // consistent point-in-time snapshot
```

`Inspect()` returns a `Snapshot`:

| Field | Content |
| --- | --- |
| `at` | sample time (used for rate calculation) |
| `info` | node identity and cumulative traffic counters |
| `running` | dispatcher state |
| `listeners` | active virtual listeners (port, addr) |
| `streams` | states of all active streams |
| `port_pools` | virtual port usage (`shared` and `pinger` pools: in_use/capacity) |
| `pending` | requests waiting for a peer answer (dial, ping) |
| `heartbeat` | probe interval, last write time, last measured RTT (`last_rtt`) |
| `failures` | cumulative failure counters (see below) |

`failures` complements the one-shot `Trace` hooks — a hook you miss is gone, a
counter is always there:

| Counter | Meaning |
| --- | --- |
| `dial_timeout` | Dial timed out waiting for the ack |
| `dial_rejected` | Dial rejected by the peer |
| `dial_write_failed` | Dial request could not be written |
| `ping_failed` | Ping timed out, was rejected or failed to write |
| `heartbeat_failed` | Liveness probe failed |
| `port_exhausted` | Virtual port pool exhausted (shared + pinger) |

Per-stream state (`stream.State`) includes direction, both ends'
domain/IP/port, created/closed timestamps, bytes read/written, ack totals and
in-flight buffer counts.

```go
states := n.GetStreamStates() // active streams

// Closed streams are kept in a ring buffer (latest 1024). Pull incrementally:
var pos int64
closed, pos := n.GetClosedStates(pos) // returns only records newer than pos
```

### Node: Rates

Counters are cumulative; compute rates between two snapshots:

```go
snap1 := n.Inspect()
time.Sleep(time.Second)
snap2 := n.Inspect()

rates := snap2.RatesSince(snap1) // nil if the interval is not positive
fmt.Println(rates.BytesReadPerSec, rates.BytesWrittenPerSec)
for _, sr := range rates.Streams { // streams present in both snapshots
    fmt.Printf("stream %d: %.0f B/s up\n", sr.Index, sr.BytesWrittenPerSec)
}
```

### Node: Push-style Events

```go
n.SetTrace(&node.Trace{ // call before Serve
    StreamOpen:    func(st *stream.State) { /* ... */ },
    StreamClosed:  func(st *stream.State) { /* ... */ },
    HeartbeatFail: func(err error) { /* ... */ },
    PortExhausted: func(pool string, err error) { /* ... */ },
})
```

### Node: Active Probing

```go
rtt, err := n.PingDomain("target-agent", time.Second) // RTT to a peer
rtt, err = n.PingDomain("", time.Second)              // RTT to the switcher
```

### Node: Presence Subscription

Instead of polling with `PingDomain`, `Watch` lets the switcher push notifications when target nodes come online or go offline:

```go
// Subscribe to presence of named nodes; returns a snapshot of current states
states, err := n.Watch(3*time.Second, "agent-1", "agent-2")

// Subsequent state transitions are pushed to the callback in real time
n.SetPresenceHandler(func(ev packet.PresenceEvent) {
    log.Printf("%s -> online=%v ip=%v mac=%v version=%v",
        ev.Domain, ev.Online, ev.IP, ev.Mac, ev.Version)
})

// Local state view, maintained incrementally from snapshots and events
st, ok := n.GetPresence("agent-1") // single domain
all := n.ListPresence()            // all subscribed domains

// Unsubscribe (also removes the domain from the local view)
err = n.Unwatch(3*time.Second, "agent-1")
```

- The subscribe ACK carries a snapshot of current states; the snapshot and later events are atomically stitched on the switcher side, so no transition is missed in between.
- Events fire on state transitions and are strictly ordered per domain; a domain replacement yields an ordered offline (old IP) + online (new IP) pair.
- Consistency is carried by a per-domain version: it increments monotonically on every transition (including offline), and offline entries survive with the last known IP/MAC. The local view drops stale events (`version <= current`), automatically resyncs a domain when a version jump (event gap) is detected, and never lets an older snapshot regress newer local state.
- Notifications are informational only — the protocol stack takes no automatic action. For fail-fast semantics (e.g. closing streams to an offline node or aborting in-flight dials), compose them yourself in the callback.
- The callback runs synchronously on the dispatcher goroutine and must not block.

### Session: Presence Across Reconnects

`Session` exposes the same presence API as `Node` (`Watch` / `Unwatch` / `SetPresenceHandler` / `GetPresence` / `ListPresence`) and removes the subscription gap caused by reconnects:

- Subscription intents survive reconnects: after every reconnect the session resubscribes all domains automatically and reconciles its local view with the new snapshot. The snapshot is applied with authoritative-reset semantics — a version regression can only mean a switcher restart or eviction of a long-dead offline entry, in which case the new snapshot is the current truth.
- Calling `Watch` while disconnected records the intent and returns `ErrSessionDisconnected`; it takes effect once the session is back online. `Unwatch` is symmetric.
- Like `Listen`/`Dial`, the first `Watch` call triggers `Serve` to start connecting.

```go
sess.SetPresenceHandler(func(ev packet.PresenceEvent) {
    log.Printf("%s -> online=%v version=%v", ev.Domain, ev.Online, ev.Version)
})
_, err := sess.Watch(3*time.Second, "agent-1") // no need to call again after reconnects
```

### Session Observability

`Session` (the auto-reconnecting Node proxy) exposes its connection lifecycle:

```go
state := sess.GetState()         // ready / idle / connecting / online / closed
errText := sess.GetLastErr()     // last error, e.g. why it is reconnecting
n := sess.GetReconnectCount()    // cumulative disconnect count

sess.OnStateChange(func(old, new node.SessionState) { // call before Serve
    log.Printf("session %v -> %v", old, new)
})

err := sess.WaitReady(5 * time.Second) // block until online
node := sess.GetNode()                 // current *Node (may be nil); all Node
                                       // observability APIs apply to it
```

`sess.SetLogger` / `sess.SetTrace` are re-injected into the rebuilt `Node`
after every reconnect, so configuration survives reconnects.

### Switcher Observability

```go
stats := srv.GetStats()   // active_connections, total_contexts, uptime_seconds
clients := srv.GetClients() // online agents: domain, IP, mac, connected_at,
                            // per-client stream count, bytes in/out, last RTT

srv.OnContextStart = func(ctx *switcher.Context) { /* agent attached */ }
srv.OnContextStop  = func(ctx *switcher.Context, d time.Duration) { /* detached */ }
```

Log verbosity is configurable per module with `switcher.LogConfig`
(server/registry/router/context levels), passed to `NewServer`.

### Example: Exposing State over HTTP

Using only the standard library:

```go
http.HandleFunc("/inspect", func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(n.Inspect())
})
go http.ListenAndServe(":9091", nil)
```

See `examples/ws-gate` for a working example exposing `GetStats()` and
`GetClients()` of a Switcher as `/api/stats` and `/api/clients`.
