# Flex 开发者手册

本手册详细介绍了如何使用 `flex` 库构建多路复用网络应用程序。

## 目录
1.  [核心概念](#核心概念)
2.  [Node (代理节点)](#node-代理节点)
3.  [Switcher (中继服务)](#switcher-中继服务)
4.  [公平性与调度](#公平性与调度)
5.  [可观测性](#可观测性)

---

## 核心概念

### Packet (数据包)
传输的基本单位。`flex` 将流数据切分为小块（Packet），以便在物理连接上交错传输。这使得多个流可以公平地共享带宽。

### Stream (流)
虚拟的、可靠的、有序的连接（实现了 `net.Conn` 接口）。你可以在单个 `flex` 连接上创建数千个流。

### Node (节点)
`flex` 网络中的端点。Node 可以：
-   **Dial (拨号)**: 向其他节点发起流。
-   **Listen (监听)**: 接收来自其他节点的流。
-   **Ping**: 检测到其他节点的延迟。

### Switcher (交换机)
中心中继服务器。它连接多个 Node，并根据 **Domain (域名)** 或虚拟 IP 在它们之间路由数据包。

---

## Node (代理节点)

`node` 包提供了客户端逻辑。

### 初始化
要创建一个 node，你需要一个底层的 `packet.Conn`（它包装了 `net.Conn` 或 `websocket.Conn`）。

```go
import (
    "github.com/net-agent/flex/v3/node"
    "github.com/net-agent/flex/v3/packet"
)

// 包装物理连接
pconn := packet.NewWithConn(netConn)
// 或者用于 WebSocket
// pconn := packet.NewWithWs(wsConn)

// 创建 Node
n := node.New(pconn)
n.SetDomain("my-agent") // 设置唯一名称
go n.Serve()            // 启动处理循环
```

### Listen (虚拟端口监听)
Flex 支持虚拟端口 (uint16)。你可以像 TCP 端口一样监听它们。

```go
listener, err := n.Listen(8080)
if err != nil {
    panic(err)
}

for {
    conn, err := listener.Accept() // conn 是 *stream.Stream
    go handle(conn)
}
```

### Dial (拨号)
你可以通过 **Domain** 或 **IP** 连接其他节点。

```go
// 通过域名连接
conn, err := n.Dial("target-agent:8080")

// 通过虚拟 IP 连接
// conn, err := n.Dial("10.0.0.5:8080")
```

---

## Switcher (中继服务)

`switcher` 包允许你构建网关服务器。

### 基础设置
Switcher 本身不监听 TCP 端口；它处理你传递给它的 `packet.Conn` 对象。

```go
import "github.com/net-agent/flex/v3/switcher"

s := switcher.NewServer("secret-password", nil, nil)

// 可选的生命周期钩子:
// s.OnContextStart = func(ctx *switcher.Context) { ... }
// s.OnContextStop  = func(ctx *switcher.Context, d time.Duration) { ... }

// 在你的 TCP/WS accept 循环中:
go s.ServeConn(pconn)
```

### 上下文与路由
当一个 Node 连接到 Switcher 时，它就成为一个 `Context`（上下文）。Switcher 维护着一张 Domain -> Contexts 的路由表。

---

## 公平性与调度

Flex v2 引入了 **公平队列 (Fair Queuing)**。
-   **问题**: 在 v1 版本中，大文件传输可能会阻塞 ACK 或 Ping 包，导致超时。
-   **解决方案**: `FairWriter` 分别对来自不同流的数据包进行排队，并以轮询方式进行服务。
-   **使用**: 在托管连接的场景默认启用：`switcher.Server` 会包装每个接入的连接，`node.Session` 会包装其连接。两者都可用 `SetEnableFairConn(false)` 关闭。直接使用 `node.New` 时连接保持原样，不做包装。

---

## 可观测性

Flex 以编程方式暴露运行时状态，不内置 HTTP 服务——由你决定如何暴露
（JSON over HTTP、metrics、日志）。所有快照类型都带 JSON tag，
可直接用 `encoding/json` 序列化。

### Node：拉取式状态快照

```go
info := n.GetInfo()         // 域名、IP、网络类型、运行时长、累计收发字节
listeners := n.GetListeners()
running := n.IsRunning()    // dispatcher 是否正在服务

snap := n.Inspect()         // 某一时刻的一致性快照
```

`Inspect()` 返回 `Snapshot`：

| 字段 | 内容 |
| --- | --- |
| `at` | 采样时间（用于计算速率） |
| `info` | 节点身份与累计流量计数 |
| `running` | dispatcher 状态 |
| `listeners` | 活跃虚拟监听器（port、addr） |
| `streams` | 全部活跃流的状态 |
| `port_pools` | 虚拟端口池占用（`shared` 与 `pinger` 两个池：in_use/capacity） |
| `pending` | 等待对端应答的请求数（dial、ping） |
| `heartbeat` | 探活间隔、最近写入时间、最近测得的 RTT（`last_rtt`） |
| `failures` | 失败事件累计计数（见下表） |

`failures` 与一次性的 `Trace` 事件钩子互补——钩子漏接就丢了，计数器始终可查：

| 计数器 | 含义 |
| --- | --- |
| `dial_timeout` | Dial 等待应答超时 |
| `dial_rejected` | Dial 被对端拒绝 |
| `dial_write_failed` | Dial 请求写入底层连接失败 |
| `ping_failed` | Ping 超时、被拒或写失败 |
| `heartbeat_failed` | 心跳探活失败 |
| `port_exhausted` | 虚拟端口池耗尽（shared 与 pinger 合计） |

单条流的状态（`stream.State`）包含方向、双端 domain/IP/port、创建/关闭时间、
收发字节数、ACK 累计和在途 buffer 数。

```go
states := n.GetStreamStates() // 活跃流

// 已关闭流保留在环形缓冲区（最近 1024 条），支持增量拉取：
var pos int64
closed, pos := n.GetClosedStates(pos) // 只返回比 pos 新的记录
```

### Node：速率

计数器是累计值，用两个快照计算速率：

```go
snap1 := n.Inspect()
time.Sleep(time.Second)
snap2 := n.Inspect()

rates := snap2.RatesSince(snap1) // 采样间隔非正时返回 nil
fmt.Println(rates.BytesReadPerSec, rates.BytesWrittenPerSec)
for _, sr := range rates.Streams { // 两个快照中都存在的流
    fmt.Printf("stream %d: %.0f B/s up\n", sr.Index, sr.BytesWrittenPerSec)
}
```

### Node：推送式事件钩子

```go
n.SetTrace(&node.Trace{ // 仅应在 Serve 之前调用
    StreamOpen:    func(st *stream.State) { /* ... */ },
    StreamClosed:  func(st *stream.State) { /* ... */ },
    HeartbeatFail: func(err error) { /* ... */ },
    PortExhausted: func(pool string, err error) { /* ... */ },
})
```

### Node：主动探测

```go
rtt, err := n.PingDomain("target-agent", time.Second) // 到对端节点的 RTT
rtt, err = n.PingDomain("", time.Second)              // 到中转节点的 RTT
```

### Node：状态订阅（Presence）

相比轮询 `PingDomain`，Watch 让 switcher 在目标节点上下线时主动推送通知：

```go
// 订阅一组 named node 的上下线状态，返回当前状态快照
states, err := n.Watch(3*time.Second, "agent-1", "agent-2")

// 之后的状态迁移通过回调实时推送
n.SetPresenceHandler(func(ev packet.PresenceEvent) {
    log.Printf("%s -> online=%v ip=%v mac=%v version=%v",
        ev.Domain, ev.Online, ev.IP, ev.Mac, ev.Version)
})

// 本地状态视图：由订阅快照与后续事件增量维护
st, ok := n.GetPresence("agent-1") // 单个域名
all := n.ListPresence()            // 全部已订阅域名

// 取消订阅（同时从本地视图移除）
err = n.Unwatch(3*time.Second, "agent-1")
```

- 订阅应答携带当前状态快照，快照与后续事件在 switcher 侧原子衔接，不存在变更空洞。
- 事件按状态迁移发送，同一域名的多次变更严格保序；域名替换时按序收到 offline（旧 IP）+ online（新 IP）。
- 一致性由 per-domain version 承载：每个域名的 version 随每次状态迁移（含 offline）单调 +1，offline 后条目存续（保留最后已知 IP/MAC）。本地视图据此丢弃 stale 事件（`version <= 当前`），在发现 version 跳变（事件缺口）时自动重同步该域名，且更旧的快照不会回退本地视图。
- 通知仅为信息告知，协议栈不做自动动作；如需 fail-fast（如主动关闭指向 offline 节点的 stream、取消进行中的 Dial），在回调中自行组合。
- 回调在 dispatcher 的 goroutine 中同步执行，不得阻塞。

### Session：状态订阅（跨重连）

`Session` 暴露与 Node 一致的 presence API（`Watch` / `Unwatch` / `SetPresenceHandler` / `GetPresence` / `ListPresence`），并消除了重连带来的订阅中断：

- 订阅意图跨重连存活：每次重连成功后自动重订阅全部域名，并用新快照 reconcile 本地视图（快照以权威重置语义应用——version 回退只可能来自 switcher 重启或离线条目淘汰，此时新快照即当前真相）。
- 离线时调用 `Watch` 会记录意图并返回 `ErrSessionDisconnected`，重连后自动生效；`Unwatch` 对称。
- 首次调用 `Watch` 与 `Listen`/`Dial` 一样会触发 Serve 开始连接。

```go
sess.SetPresenceHandler(func(ev packet.PresenceEvent) {
    log.Printf("%s -> online=%v version=%v", ev.Domain, ev.Online, ev.Version)
})
_, err := sess.Watch(3*time.Second, "agent-1") // 重连后无需再次调用
```

### Session 观测

`Session`（带自动重连的 Node 代理）暴露连接生命周期：

```go
state := sess.GetState()         // ready / idle / connecting / online / closed
errText := sess.GetLastErr()     // 最近一次错误，例如重连原因
n := sess.GetReconnectCount()    // 累计断线次数

sess.OnStateChange(func(old, new node.SessionState) { // 仅应在 Serve 之前调用
    log.Printf("session %v -> %v", old, new)
})

err := sess.WaitReady(5 * time.Second) // 阻塞直到 online
node := sess.GetNode()                 // 当前 *Node（可能为 nil），
                                       // Node 的全部观测 API 对它适用
```

`sess.SetLogger` / `sess.SetTrace` 会在每次重连重建 Node 时自动注入，
配置跨重连存活。

### Switcher 观测

```go
stats := srv.GetStats()     // active_connections、total_contexts、uptime_seconds
clients := srv.GetClients() // 在线节点：域名、IP、mac、接入时间，
                            // 每节点的流数量、收发字节、最近 RTT

srv.OnContextStart = func(ctx *switcher.Context) { /* 节点接入 */ }
srv.OnContextStop  = func(ctx *switcher.Context, d time.Duration) { /* 节点断开 */ }
```

日志粒度可通过 `switcher.LogConfig` 按模块（server/registry/router/context）
分别配置，作为参数传给 `NewServer`。

### 示例：通过 HTTP 暴露状态

仅用标准库：

```go
http.HandleFunc("/inspect", func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(n.Inspect())
})
go http.ListenAndServe(":9091", nil)
```

`examples/ws-gate` 中有完整示例，将 Switcher 的 `GetStats()` 与
`GetClients()` 暴露为 `/api/stats` 和 `/api/clients`。
