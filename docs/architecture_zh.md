# flex 架构文档（v4）

`flex` 在单条 TCP/WebSocket 连接上多路复用大量逻辑 Stream，并提供域名路由、presence、流控和公平调度。v4 把“包”提升为不可变 IR，把“连接”收敛为一个组合根 `link.Link`，并给控制面加上逐跳加密。

> 安全边界、命令分类和密钥细节见 [security_contract_zh.md](security_contract_zh.md)。

## 分层

```text
net.Conn / websocket.Conn
        │
   Transport            internal/framing：11B 头 + payload 的两阶段读写
        │
   Link                 handshake v4 + secure codec + fair writer
        │
   Node / Context       流、路由、presence、统计
        │
   Stream               net.Conn
```

- `wire`：不可变 `Frame`、11B codec、命令表、端点注册表。
- `link`：唯一知道“握手、加密、调度如何组合”的地方；Node/Context 不接触 `net.Conn`、密钥或字节偏移。
- `internal/framing`：TCP/WS 的字节/消息帧实现，`packet` 与 `link` 共用。

## wire：不可变包 IR

### 头（11B）

```text
0        1        3        5        7        9        11
+--------+--------+--------+--------+--------+--------+
| Cmd 1B | DstIP  | DstPort| SrcIP  | SrcPort| Size   |
+--------+--------+--------+--------+--------+--------+
```

- `Cmd` bit0 = ACK，bits1-6 = 命令类型，bit7 = 类别位（1=加密 control/handshake，0=data）。
- `SID` 由 `Src/Dst` 组成；`SID.Uint64` 保留历史打包顺序，兼容旧的 `packet.Buffer` 查找。
- `AckPushStreamData` 的 `Size` 是 ACK 字节数，不是 payload 长度。

### Frame

`wire.Frame` 字段全部不导出，构造后不可变：

```go
NewControl(cmd ControlCmd, src, dst Addr, payload []byte)
NewData(src, dst Addr, payload []byte)
NewEmpty(cmd EmptyCmd, src, dst Addr)
NewAckData(size uint16, src, dst Addr)
```

- 回复 = 构造新 Frame，不改原 Frame；`SetCmd`/`SwapSrcDist` 这类可变 API 已从热路径移除。
- `Codec.EncodeHeader/DecodeHeader` 是唯一知道字节布局的地方；`DecodeHeader` 用命令表校验类别位，类别不一致直接失败。
- `wire.Registry` 把命令映射到 handler + lane；`RegisterControl/RegisterData/RegisterEmpty` 的参数类型强制选择类别。
- `wire/contract_test.go` 解析源码，保证每个 `ControlCmd/DataCmd/EmptyCmd` 常量都在命令表里。

## link：唯一组合根

```text
Transport (bytes)
  └─ secureCodec   头 ChaCha20、控制 payload AEAD、padding
       └─ fairWriter   按 SID 排队、控制优先、批量 writev
            └─ Link.ReadFrame / SendFrame
```

- `Link` 拥有 transport、握手、密钥、公平写协程和统计；Node/Context 只看到 `ReadFrame`/`SendFrame`。
- `Link.SendFrame` 转移所有权；`SendControl`/`SendData`/`SendAck` 复制调用方 payload。写失败是致命的，连接关闭。
- 控制帧用 `SendFrameSync`（dial/ping/open 等需要同步错误语义）；`Close/AckClose` 留在异步 per-SID 队列，避免 close 越过排队的数据。

### 握手 v4

```text
C→S: HDR(K_boot) | AEAD(K_boot, {version,domain,mac,ts,nonce,client_pub}) | pad   (11+512B)
S→C: HDR(K_boot) | server_pub | AEAD(K_resp, {ip,version,caps,server_nonce}) | pad (11+512B)

Z       = X25519(client_priv, server_pub)
K_resp  = HKDF(Z, "flex-resp", transcript0)
K_link  = HKDF(Z, "flex-link", transcript1)
K_boot  = Argon2id(PSK, salt, 64MB, 3, 4) 或 HKDF(PSK)
```

- 固定 512B payload，请求/响应等长；domain 长度和成功/失败长度不可见。
- timestamp ±5 分钟 + 16B nonce 缓存（TTL 5 分钟，上限 1M，满则 fail-closed）。
- `Dialer.Dial` 返回 `(*Link, ip)`；`Acceptor.Accept` 返回 `Pending`，调用方分配 IP 后 `Respond(ip)` 写响应并得到 `*Link`。这保留“先分配 IP、再应答”的顺序，无需 `connReady` workaround。

### 头/控制加密

- 每方向一条 ChaCha20 keystream；11B 头逐帧 XOR；TCP 有序，收发按帧数同步，无需每包 nonce。
- 控制 payload：`nonce(12) || AEAD([2B len][payload][padding])`；padding 到 64B 桶；头作为 AAD。
- 无 payload 的控制命令（Close/AckClose）只加密头；`AckPushStreamData` 的 ACK 数在头里。
- 数据 payload 完全不碰；relay 只解密/重加密 11B 头。

## Node

- `Node` 持有 `*link.Link`；`SendFrame` 对本地目标走 dispatcher，对远程目标走 link。
- `Dispatcher` 把帧分到 cmd/data lane；内置命令全部是 frame handler：
  - data：`PushStreamData` → `StreamHub.handleData`；`AckPushStreamData` → `handleAckData`；`AckOpenStream` → Dialer；`Close/AckClose` → StreamHub。
  - control：`OpenStream` → ListenHub；`Ping/AckPing` → Pinger；`Subscribe/Notify` → Watcher。
- `Stream` 用 `stream.Endpoint` 发送 data/ack/close；接收队列持有 `*wire.Frame`，大包零拷贝、小包合并。
- `Session` 负责懒连接/指数退避重连，每次重连用 `link.Dialer` 重新握手，并在新 Node 上恢复 listener/presence 意图。

## Switcher

- `Server.ServeConn`：v4 `Accept` → 创建 Context → registry attach（分配 IP）→ `Pending.Respond(ip)` → 切到安全 Link → router。
- `Context` 持有 `*link.Link`；握手应答写完前 `ready` 未关闭，转发帧会等待，保证应答先于数据。
- `router`：`ReadFrame` → `checkSource`（源虚拟 IP）→ 目标不是 SwitcherIP 就 `dist.SendFrame` 转发；否则 `dispatch` 处理 Open/Ping/Presence。
- `presenceCenter` 向订阅者发送 `CmdNotifyPresence` 控制帧；订阅配额和 presence 状态机不变。
- 没有 per-context forward queue：link 的 fair writer 是唯一写队列，流的发送窗口天然提供背压。

## 数据面

- `Stream.Write` 按 `DefaultSplitSize=16KB` 切片；`sender.SendData` 构造 data frame。
- 接收队列按字节计量（默认窗口 2MB，硬上限 16MB），大包零拷贝、小包合并到 16KB chunk；ACK 按 64KB 或 10ms 合批。
- 数据 payload 对 flex 不透明；需要 TLS 的应用直接 `tls.Client(stream)`。

## 兼容层

- `packet.Conn`/`packet.Buffer`、`stream.New`/`NewDialStream`/`NewAcceptStream`、`Node.WriteBuffer`、`Dispatcher.RegisterCmdHandler` 保留为源码兼容 API；内置热路径已使用 `wire.Frame`。
- `packet` 传输同时实现 `internal/framing.Transport`；内置 Node/Switcher 通过该视图读写，第三方 `packet.Conn` 走复制 fallback（`link.NewPacketTransport`）。
- `node.New` 直连模式仍是明文 legacy wire（`Link.Config.LegacyWire`）；只有 `Connect`/`Session`/`Switcher` 使用 v4。
- `SetEnableFairConn` 保留为 no-op；公平调度始终由 `link` 启用。

## 测试与契约

- `wire/contract_test.go`：命令常量穷尽性。
- `link/secure_test.go`：握手固定长度、控制 payload 不明文、数据 payload 原样、错误 PSK、重放拒绝。
- `link/fair_test.go`：成本计费/包数策略的每轮批量上限。
- `node`/`switcher`/`test/integration`：64MB 大流、调度公平性、presence、重连。
