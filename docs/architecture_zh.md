# flex 架构文档

flex 是一个基于单条 TCP/WebSocket 连接的虚拟网络多路复用库。它在一条物理连接上承载多个双向 Stream，提供类似 `net.Conn` 的编程接口，并内置域名路由、流控、连接混淆和公平调度。

整体分层自底向上为：**Transport → Packet → Middleware → Switcher → Node → Stream → Session**。

## 分层架构总览

```
┌─────────────────────────────────────┐
│  Application  (Dial / Listen / Accept) │
├─────────────────────────────────────┤
│  Session 层     断线重连、持久监听       │
├─────────────────────────────────────┤
│  Node 层        多路复用管理            │
│  ┌─Dispatcher─┬─StreamHub─┬─Dialer─┐ │
│  │ ListenHub  │ Pinger    │Heartbeat│ │
│  └────────────┴───────────┴─────────┘ │
├─────────────────────────────────────┤
│  Stream 层      虚拟连接、流控          │
├─────────────────────────────────────┤
│  Switcher 层    路由转发、握手认证       │
├─────────────────────────────────────┤
│  Middleware 层  Obfuscate + Fair       │
├─────────────────────────────────────┤
│  Packet 层      11 字节二进制协议       │
├─────────────────────────────────────┤
│  Transport 层   TCP / WebSocket       │
└─────────────────────────────────────┘
```

---

## 第一层：Transport → Packet.Conn

### packet.Conn 接口

```go
type Conn interface {
    io.Closer
    Reader   // ReadBuffer() (*Buffer, error)
    Writer   // WriteBuffer(buf *Buffer) error
    GetRawConn() net.Conn
}
```

两种实现：
- **TCP** — `NewWithConn(net.Conn)` → 内部使用 `connReader` / `connWriter`
- **WebSocket** — `NewWithWs(*websocket.Conn)` → 内部使用 `wsReader` / `wsWriter`

TCP 写入支持 `WriteBufferBatch`，利用 `net.Buffers`（writev）减少系统调用。

### Header 二进制格式（11 字节）

```
 字节偏移:  0      1      3      5      7      9     11
         ┌──────┬──────┬──────┬──────┬──────┬──────┐
         │ Cmd  │DistIP│DistPt│SrcIP │SrcPt │Size  │
         │ 1B   │ 2B   │ 2B   │ 2B   │ 2B   │ 2B   │
         └──────┴──────┴──────┴──────┴──────┴──────┘
```

- **Cmd**：命令字节。bit 0 为 ACK 标志，bit 1-7 为命令类型
- **DistIP / DistPort**：目标虚拟 IP 和端口
- **SrcIP / SrcPort**：源虚拟 IP 和端口
- **Size**：union 字段 — 普通包表示 PayloadSize，`AckPushStreamData` 表示已确认数据量

主要命令：

| 命令 | 值 | ACK | 说明 |
|------|-----|-----|------|
| CmdOpenStream | 0x02 | 0x03 | 打开 Stream |
| CmdCloseStream | 0x04 | 0x05 | 关闭 Stream |
| CmdPushStreamData | 0x06 | 0x07 | 推送数据 / DataACK |
| CmdPushMessage | 0x08 | 0x09 | 推送消息 |
| CmdPingDomain | 0x0A | 0x0B | 域名 Ping |

特殊 IP：`LocalIP = 0`，`SwitcherIP = DNSIP = 0xFFFF`。

---

## 第二层：Middleware 封装

物理连接建立后，按以下顺序逐层包装：

```
net.Conn → packet.Conn → ObfuscatedConn → FairConn
```

### ObfuscatedConn

对 Header 的前 9 字节（Cmd + DistIP + DistPort + SrcIP + SrcPort）进行 ChaCha20 流密码 XOR 混淆。**不加密** 字节 [9:11]（PayloadSize），以便内层 Reader 正确读取载荷长度。

密钥派生：`SHA-256(password ‖ clientNonce ‖ serverNonce)` → 32 字节 ChaCha20 密钥，nonce 固定为零（每连接独立 cipher 实例，计数器单调递增）。读写各持有独立 cipher，写端加 mutex 保证计数器与线序一致。

### FairConn

包装 `ObfuscatedConn`，替换写路径为 `FairWriter`：

- **控制包**（非 `CmdPushStreamData`）→ 高优先级 `controlCh`，立即发送
- **数据包** → 按 SID 分流到 `StreamQueue`，round-robin 调度，每轮每 Stream 发送 quantum（默认 4）个包

调度循环：优先排空 `controlCh` → 取一个就绪 Stream → 发送 quantum 个包 → 若队列仍有数据则重新入队。

---

## 第三层：Switcher 路由

Switcher 是中心中继服务器，负责接受客户端连接、认证、分配虚拟 IP 并路由数据包。

### 连接生命周期

```
Client                          Server
  │── TCP/WS connect ──────────→│
  │── admit.Request ───────────→│  验证 version/password/domain/timestamp
  │←── admit.Response (IP) ─────│  分配虚拟 IP
  │                              │  双方派生 obfKey，启用 ObfuscatedConn + FairConn
  │←═══ 数据包路由 ═══════════→│
```

### 核心组件

- **Server**：监听端口，对每个连接执行 `ServeConn`（握手 → 注册 → 路由循环）
- **Context**：表示一个已连接节点，持有 `packet.Conn`、Domain、IP、统计信息。内部有独立的 `forwardCh` + 转发 goroutine 保证包序
- **contextRegistry**：按 Domain 和 IP 双索引管理 Context，支持域名抢占（先 ping 旧持有者，超时则替换）
- **packetRouter**：读包循环 — `DistIP ≠ SwitcherIP` 时按 IP 转发；`DistIP == SwitcherIP` 时由 Switcher 自身处理（域名解析、Ping 等）

---

## 第四层：Node 多路复用

Node 是客户端侧的核心，组合了以下子模块：

```go
type Node struct {
    packet.Conn                // 底层连接
    Dispatcher                 // 包分流
    StreamHub                  // Stream 管理
    ListenHub                  // 监听管理
    Dialer                     // 拨号
    Pinger                     // Ping
    Heartbeat                  // 心跳保活
}
```

### Dispatcher 分流

读循环从 `packet.Conn` 读包，按命令类型分发到两个 channel：

| Channel | 容量 | 路由的命令 | 特点 |
|---------|------|-----------|------|
| cmdChan | 4096 | OpenStream, AckPushStreamData, PingDomain, AckPingDomain | 无序处理 |
| dataChan | 1024 | PushStreamData, AckOpenStream, CloseStream, AckCloseStream | 有序处理 |

`PushStreamData` 走最短比较路径（switch 第一个 case），因为它是最高频命令。

---

## 第五层：Stream 虚拟连接

每个 Stream 实现 `net.Conn` 接口，由 SID（DistIP:DistPort:SrcIP:SrcPort 组成的 uint64）唯一标识。

### 流控机制

```
发送端                                接收端
  │                                    │
  │  bucketSz > 0 ?                    │
  │  是 → 发送 CmdPushStreamData ────→ │  写入 bytesChan
  │       bucketSz -= len(data)        │
  │                                    │  应用层 Read() 消费数据
  │  ←── AckPushStreamData(n) ─────────│  回复已消费字节数
  │  bucketSz += n                     │
  │  bucketSz ≤ 0 ?                    │
  │  是 → 阻塞等待 bucketEv 信号       │
```

- **窗口大小**：默认 `DefaultBucketSize = 2MB`，Dial/Accept 时双方协商取较小值
- **分片**：Write 自动按 `DefaultSplitSize = 63KB` 分片
- **bytesChan**：缓冲 channel，容量 = `4 × windowSize / splitSize`

### 关闭握手

```
主动端                    被动端
  │── CloseWrite ──┐       │
  │── CmdClose ────────→   │
  │                    │── CmdCloseAck ──→│
  │←── closeAckCh ─────│   │
  │── CloseRead ───┘       │
```

超时（默认 2s）未收到 CloseAck 则强制 CloseRead。

---

## 第六层：Session 断线重连

Session 包装 Node，提供跨重连的持久 Listen/Dial 语义。

### 重连循环

```
Session.Serve()
  │
  ├─ 等待首次 Listen/Dial 触发（懒连接）
  │
  └─ loop {
       connector() → packet.Conn
       admit.Handshake() → IP, obfKey
       NewObfuscatedConn(conn, obfKey)
       node = New(wrappedConn)
       重新注册所有持久 Listener
       node.Serve()          // 阻塞直到断线
       退避重连 (1s → 30s)
     }
```

### SessionListener

`SessionListener` 实现 `net.Listener`，跨 Node 重连存活。每次 Node 重建后，通过 `bridge` goroutine 将 Node 内部 Listener 的 Accept 结果转发到 SessionListener 的 channel。Node 断线时 bridge 自然退出，下一轮重连创建新的 bridge。

---

## 端到端流程示例

以 A 通过 Switcher 向 B 发起连接并传输数据为例：

```
  Node A                    Switcher                   Node B
    │                          │                          │
    │  Dial("B:80")            │                          │  Listen(80)
    │                          │                          │
    │─ CmdOpenStream ─────────→│                          │
    │  (DistIP=SwitcherIP,     │─ CmdOpenStream ─────────→│
    │   payload=domain+window) │  (解析域名→B的IP,转发)    │
    │                          │                          │  创建 Stream
    │                          │←─ AckOpenStream ─────────│  (先 attach 再回复)
    │←─ AckOpenStream ─────────│                          │
    │  创建 Stream              │                          │
    │                          │                          │
    │═ CmdPushStreamData ═════→│═════════════════════════→│  写入 bytesChan
    │                          │                          │  Read() 消费
    │←═ AckPushStreamData ═════│←═════════════════════════│  回复 DataACK
    │  bucketSz += n           │                          │
    │                          │                          │
    │─ CmdCloseStream ────────→│─────────────────────────→│
    │                          │←─ AckCloseStream ────────│
    │←─ AckCloseStream ────────│                          │
    │  关闭完成                 │                          │  关闭完成
```
