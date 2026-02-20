# Flex 开发者手册

本手册详细介绍了如何使用 `flex` 库构建多路复用网络应用程序。

## 目录
1.  [核心概念](#核心概念)
2.  [Node (代理节点)](#node-代理节点)
3.  [Switcher (中继服务)](#switcher-中继服务)
4.  [公平性与调度](#公平性与调度)
5.  [可观测性与控制](#可观测性与控制)

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
    "github.com/net-agent/flex/v2/node"
    "github.com/net-agent/flex/v2/packet"
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
import "github.com/net-agent/flex/v2/switcher"

s := switcher.NewServer("secret-password")

// 在你的 TCP/WS accept 循环中:
go s.ServeConn(pconn, onStart, onStop)
```

### 上下文与路由
当一个 Node 连接到 Switcher 时，它就成为一个 `Context`（上下文）。Switcher 维护着一张 Domain -> Contexts 的路由表。

---

## 公平性与调度

Flex v2 引入了 **公平队列 (Fair Queuing)**。
-   **问题**: 在 v1 版本中，大文件传输可能会阻塞 ACK 或 Ping 包，导致超时。
-   **解决方案**: `FairWriter` 分别对来自不同流的数据包进行排队，并以轮询方式进行服务。
-   **使用**: 自动启用。`node.New` 和 `switcher.NewServer` 默认会将连接包装在 `FairConn` 中。

---

## 可观测性与控制

Flex 提供了内置的 HTTP Admin API 以实现深度可视化。

### Node Admin
为 Node 启动管理服务:

```go
admin := node.NewAdminServer(n, ":9091")
go admin.Start()
```

**端点**:
-   `GET /api/v1/info`: 基础统计 (流量, 运行时间)。
-   `GET /api/v1/streams`: 列出活跃流，包含 RTT 和缓冲区状态。
-   `GET /api/v1/listeners`: 列出活跃的虚拟监听器。

### Switcher Admin
为 Switcher 启动管理服务:

```go
admin := switcher.NewAdminServer(s, ":9090")
go admin.Start()
```

**端点**:
-   `GET /api/v1/clients`: 列出所有连接的代理，包含实时指标 (流数量, 带宽, RTT)。
-   `DELETE /api/v1/clients/{domain}`: 踢掉某个代理。

### 依赖
Admin API 使用了 `github.com/gorilla/mux`。确保你已安装：
```bash
go get -u github.com/gorilla/mux
```
