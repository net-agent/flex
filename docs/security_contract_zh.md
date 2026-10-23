# flex 安全契约（v4）

本文是 flex 的**可执行契约**：它解释每一类包为什么要这样处理、哪些东西被隐藏、哪些没有被隐藏，以及新增命令时必须遵守的规则。代码中的 `wire`、`link` 和测试是最终裁决者；本文是“为什么”。

## 1. 威胁模型与信任边界

- **被动 DPI**：能看到字节流、包长和时序，不能解密。目标是隐藏协议结构、控制面元数据和寻址信息。
- **主动探测/篡改**：能注入、丢弃、重放。v4 握手有抗重放；数据面没有 flex 级完整性，应用需要自行 AEAD/TLS。
- **Switcher**：路由可信点，能读**控制面**（域名、presence、MAC）和 11 字节头；不能读应用数据（除非应用自己没加密）。
- **节点**：共享网络 PSK。任何持有 PSK 的节点都能解密抓到的握手元数据，也能冒充 Switcher；E CDHE 只保护 `K_link` 的前向保密。要防内部人需要服务端长期公钥/pinning。
- **应用**：负责数据面的机密性与完整性。flex 不加密、不检查、不修改 `CmdPushStreamData` 的 payload。

## 2. 分层与组合根

```text
net.Conn / websocket.Conn
        │
   Transport          // internal/framing：按帧读写字节，两阶段 ReadHeader/ReadPayload
        │
   Link               // 唯一组合根：handshake + secure codec + fair writer
        │
   Node / Context     // 语义：流、路由、presence、统计
        │
   Stream             // net.Conn
```

- `wire.Frame` 是不可变包 IR：字段不导出，构造后不能改命令/类别/地址。
- `link.Link` 是唯一知道“握手、加密、调度如何组合”的地方；Node/Context 不接触 `net.Conn`、密钥或 11 字节头。
- `internal/framing` 是 `packet`（兼容传输）和 `link` 共用的 framing 实现。

## 3. 包分类契约

| Cmd | 值 | 类别 | payload 处理 | Switcher 可见 | 说明 |
|---|---|---|---|---|---|
| `CmdAdmit` | 0x00 | handshake | 固定 512B，bootstrap AEAD | 终止 | 仅握手首包 |
| `CmdOpenStream` | 0x02 | control | 链路 AEAD + 64B padding | 解密/改写/重加密 | 域名路由 |
| `AckOpenStream` | 0x03 | control | 链路 AEAD + padding | 解密/转发 | 窗口/错误 |
| `CmdCloseStream` | 0x04 | control | 无 payload | 路由 | 仅头加密 |
| `AckCloseStream` | 0x05 | control | 无 payload | 路由 | 仅头加密 |
| `CmdPushStreamData` | 0x06 | **data** | **完全不碰** | 仅头 | 应用数据 |
| `AckPushStreamData` | 0x07 | data | 无 payload；ACK 数在头里 | 路由 | 仅头加密 |
| `CmdPushMessage` | 0x08 | control | 预留；默认按控制处理 | 视实现 | 未实现 |
| `CmdPingDomain` | 0x0A | control | 链路 AEAD + padding | 解密/转发 | 心跳/域名探活 |
| `AckPingDomain` | 0x0B | control | 链路 AEAD + padding | 解密/转发 | |
| `CmdSubscribePresence` | 0x0C | control | 链路 AEAD + padding | 解密/过滤 | |
| `AckSubscribePresence` | 0x0D | control | 链路 AEAD + padding | 解密/转发 | 状态快照 |
| `CmdNotifyPresence` | 0x0E | control | 链路 AEAD + padding | 解密/过滤 | |

**硬规则**

1. 只有 `CmdPushStreamData` 是 data；其余带 payload 的命令都是 control，必须加密。
2. 新增命令必须用 `wire.ControlCmd`/`DataCmd`/`EmptyCmd` 声明，并加入 `wire` 命令表与端点注册表；AST 测试会拒绝未登记/类型不一致的命令。
3. 控制 payload 不允许以明文出现在线上；数据 payload 必须逐字节原样出现。`link/secure_test.go` 用 canary 断言这两条。
4. 未知命令直接关闭连接；不猜类别。
5. `AckPushStreamData` 的 ACK 数在加密头里，不能用 payload 传。

## 4. 密钥体系与握手 v4

```text
PSK ──Argon2id(64MB,3,4) 或 HKDF──> K_boot
客户端/服务端 X25519 临时密钥
  Z = X25519(priv, peer_pub)
  K_resp = HKDF(Z, "flex-resp", transcript0)
  K_link = HKDF(Z, "flex-link", transcript1)
      ├─ hdr-c2s / hdr-s2c   ChaCha20 连续 11B keystream
      └─ ctl-c2s / ctl-s2c   ChaCha20-Poly1305
```

- **握手帧**：11B 头（`ClassHandshake`/`CmdAdmit`/Size=512）+ 固定 512B payload；请求/响应等长，无长度 oracle。
- **请求**：`K_boot` AEAD；明文含 version、domain、mac、timestamp、16B nonce、32B client pubkey；padding 到 484B。
- **响应**：`serverPub(32B)` 明文 + `K_resp` AEAD（IP、version、caps、16B server nonce）；padding 到 452B。
- **抗重放**：timestamp ±5 分钟 + 16B nonce 缓存（默认 TTL 5 分钟、上限 1M，满了 fail-closed）。
- **前向保密**：每条连接一次 ECDHE；PSK 泄露不影响历史 `K_link`。
- **无额外 RTT**：请求/响应就是一次往返；握手完成后直接切到 `K_link`，不额外协商。
- **KDF**：人设口令默认 Argon2id（启动/首次握手派生一次并缓存）；高熵机器 secret 可用 `link.KDFHKDF`。

## 5. 线格式与加密细节

### 头

11 字节：`Cmd(1) | DstIP(2) | DstPort(2) | SrcIP(2) | SrcPort(2) | Size(2)`。

- `Cmd` bit0 = ACK，bits1-6 = 命令类型，bit7 = 类别位（1=加密 control/handshake，0=data）。
- 整个 11B 头用 ChaCha20 连续 keystream 加密；TCP 有序，收发按帧数同步推进，不需要每包传 nonce。
- 接收端先解密头、按 `Size` 读 body；未知命令/类别不一致/长度非法立即关闭。
- 头没有独立 MAC（用户决策）；控制 payload 的 AEAD 把头作为 AAD；数据面没有 flex 级完整性。

### 控制 payload

- 线上：`nonce(12B) || AEAD( [2B len][payload][padding] )`；padding 到 64B 桶；`Size` = 12+密文长度。
- AAD = 明文头（含最终 wire size）；接收端先解密头，再用同一份头做 AAD 解密。
- 无 payload 的控制命令（Close/AckClose）不加密 body，仅头加密；`AckPushStreamData` 的 ACK 数在头里。

### 数据 payload

- 仅头加密；payload 原样透传，relay 零解密、零拷贝、零修改。
- 包长仍为 `11 + len(payload)`；批量传输仍是 16395B。**长度指纹仍在**，本版本不做数据长度 padding。

## 6. 隐藏了什么、没隐藏什么

**隐藏（对被动 DPI）**

- 协议首包不再以 `0x00` 开头；握手固定 523B，domain 长度/成功失败长度不可见。
- 命令、SID、虚拟 IP/端口、payload 长度都在加密头里。
- 控制 payload（域名、presence、MAC）在线上是 AEAD 密文 + padding。
- 应用数据是否可读完全取决于应用：应用不加密，DPI 仍能读 payload。

**没隐藏**

- 包长：数据包 16395B、ACK 11B、控制包 padding 后的长度仍可统计。
- 时序：ACK 合批窗口、心跳周期、批量突发。
- relay 的控制面元数据：Switcher 必须读域名才能路由。
- 流量总量/方向/流数量。
- 应用未加密的数据内容。

## 7. 性能模型

- **relay 每包**：解密 11B 头 + 重新加密 11B 头；数据 payload 不碰。约几十 ns/包，相对现有转发管线是个位数百分比。
- **控制包**：AEAD + padding + 28B 开销；控制包稀少，可忽略。
- **端节点**：数据路径只有头加解密；控制路径 AEAD；握手 Argon2id 每进程一次。
- **握手**：1 RTT + 固定 512B×2；无额外 RTT。
- **不要**在 relay 上终止 TLS 或对数据 payload 做 per-byte 加密；那会把转发器变成加解密网关。

## 8. 新增命令 checklist

1. 在 `wire/wire.go` 用 `ControlCmd`/`DataCmd`/`EmptyCmd` 声明常量。
2. 加入 `wire.commandList`（名称+类别）。
3. 在 `node.Dispatcher`/`switcher.router` 注册 frame handler；控制 lane 的 handler 不得保留 frame，数据 lane 的 handler 负责释放。
4. 控制命令的 payload 用 `wire.NewControl` 构造；数据命令用 `wire.NewData`。
5. 跑 `go test ./wire`（AST 穷尽性）和 `./link`（canary）。
6. 更新本文的包分类表。

## 9. 已知限制

- 数据 payload 不加密；需要机密性请 `tls.Client(stream)`/`tls.Server` 或应用层 AEAD。
- 无数据完整性；控制/头的篡改检测有限（控制 AEAD 覆盖头，数据头无 MAC）。
- Switcher 是控制面可信点；PSK 共享，节点可冒充 Switcher。
- 包长/时序未整形；数据长度 padding 未实现。
- v4 与旧版本不兼容；`packet.Buffer`/`Node.WriteBuffer` 仅作为兼容 API 保留，内部热路径已使用 `wire.Frame`。
- 直连模式 `node.New` 仍是明文 + legacy wire；只有 `Connect`/`Session`/`Switcher` 使用 v4。

## 10. 代码地图

| 关注点 | 文件 |
|---|---|
| 包 IR/线格式/命令表 | `wire/wire.go`、`wire/frame.go`、`wire/codec.go`、`wire/registry.go` |
| 传输 framing | `internal/framing/framing.go` |
| Link/公平写 | `link/link.go`、`link/fair.go` |
| v4 握手/密钥 | `link/handshake.go`、`link/keys.go` |
| 头/控制加密 | `link/secure.go` |
| Node 帧处理 | `node/dispatcher.go`、`node/node.go`、`node/session.go` |
| Switcher 帧路由 | `switcher/context.go`、`switcher/router.go`、`switcher/server.go` |
| 契约测试 | `wire/contract_test.go`、`link/secure_test.go`、`link/fair_test.go` |

## 11. 性能实测（v4，Apple M4 / Go 1.25.1）

微基准（`go test ./link -bench . -benchmem`，不含内核 I/O）：

| 场景 | 时间 | 分配 |
|---|---|---|
| 数据帧头加密（64B payload） | ~28 ns/帧 | 0 alloc |
| 数据帧头加密（16KB payload） | ~28 ns/帧 | 0 alloc |
| 控制帧 32B（padding+AEAD） | ~266 ns/帧 | 3 alloc |
| 控制帧 1KB（padding+AEAD） | ~1.1 µs/帧 | 3 alloc |
| 11B 头 encode+decode（无加密） | ~15.5 ns | 0 alloc |

端到端对照（`node.TestStreamList`，10 条流 × 64MB，直连模式，同一台机器）：

- v3 基线（main）：约 400 ms 完成全部发送。
- v4 分支：约 310 ms（帧化 + 单一 fair writer + 池化接收缓冲）。
- v4 安全路径只多出每包 11B 头加解密（~28ns），数据 payload 不参与加密；握手 Argon2id 每连接一次，可用 `link.KDFHKDF` 换成高熵 secret 的快速派生。
