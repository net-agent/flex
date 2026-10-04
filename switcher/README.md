# switcher

switcher 是 flex 的中继服务器，负责接收多个 Node 客户端的连接，为每个客户端分配虚拟 IP，并在客户端之间路由数据包。

## 架构概览

```
Node A ──ws/tcp──┐
                  │
Node B ──ws/tcp──┤  Switcher
                  │  ├── Registry  (域名/IP 注册与查找)
Node C ──ws/tcp──┤  ├── Router    (数据包路由与分发)
                  │  ├── Presence  (上下线事件订阅与分发)
                  │  └── Context   (单连接生命周期管理)
                  │
```

一个 Node 连接进来后经历以下阶段：

1. **握手** — 客户端发送域名、MAC、密码签名；服务端验证后分配虚拟 IP
2. **注册** — Context 被写入 Registry 的域名索引和 IP 索引
3. **路由循环** — Router 不断从 Context 读取数据包，按目标 IP 转发或就地处理控制命令
4. **断开** — 连接关闭后从 Registry 注销，释放虚拟 IP

## 快速开始

```go
package main

import (
    "log"
    "net"

    "github.com/net-agent/flex/v3/switcher"
)

func main() {
    s := switcher.NewServer("my-password", nil, nil)

    l, err := net.Listen("tcp", ":9000")
    if err != nil {
        log.Fatal(err)
    }
    log.Fatal(s.Serve(l))
}
```

如果使用 WebSocket，可以在 HTTP handler 中手动调用 `ServeConn`：

```go
pConn := packet.NewWithWs(wsConn)
go s.ServeConn(pConn)
```

完整示例见 [examples/ws-gate](../examples/ws-gate)。

## 核心 API

### NewServer

```go
func NewServer(password string, logger *slog.Logger, logCfg *LogConfig) *Server
```

- `password` — 握手认证密码，客户端必须使用相同密码才能接入
- `logger` — 基础 slog.Logger，传 nil 使用 `slog.Default()`
- `logCfg` — 各子模块日志级别配置，传 nil 使用 `DefaultLogConfig()`

### Server 方法

| 方法 | 说明 |
|------|------|
| `Serve(l net.Listener) error` | 在 listener 上接受连接并阻塞运行 |
| `Close() error` | 关闭 listener，`Serve` 会返回 nil |
| `ServeConn(pc packet.Conn) error` | 处理单个 packet 连接的完整生命周期（握手→注册→路由→清理） |
| `GetStats() *StatsResponse` | 返回活跃连接数和累计 Context 数 |
| `GetClients() []ClientInfo` | 返回所有在线客户端的详细信息 |

### 生命周期回调

```go
s := switcher.NewServer("pwd", nil, nil)

s.OnContextStart = func(ctx *switcher.Context) {
    log.Printf("connected: %s (id=%d, ip=%d)", ctx.Domain, ctx.GetID(), ctx.IP)
}

s.OnContextStop = func(ctx *switcher.Context, duration time.Duration) {
    log.Printf("disconnected: %s after %v", ctx.Domain, duration)
}
```

## 子模块日志级别控制

switcher 内部有 5 个子模块，各自拥有独立的日志级别和 `module` 字段：

| 模块 | 默认级别 | 典型日志内容 |
|------|---------|-------------|
| server | Info | 握手失败、attach 失败、连接结束 |
| registry | Warn | attach/detach、域名替换、IP 冲突 |
| router | Warn | 路由失败、转发失败、域名解析失败 |
| context | Warn | forward write 失败 |
| presence | Warn | presence 通知入队失败、订阅应答失败 |

默认配置下 registry 和 router 的 Info 日志（如每次 attach/detach）会被过滤，避免生产环境海量输出。

### 使用默认配置

```go
// server=Info, registry/router/context=Warn
s := switcher.NewServer("pwd", nil, nil)
```

### 全部开启 Debug

```go
cfg := &switcher.LogConfig{
    Server:   slog.LevelDebug,
    Registry: slog.LevelDebug,
    Router:   slog.LevelDebug,
    Context:  slog.LevelDebug,
}
s := switcher.NewServer("pwd", myLogger, cfg)
```

### 只保留 server Info，其余仅 Error

```go
cfg := &switcher.LogConfig{
    Server:   slog.LevelInfo,
    Registry: slog.LevelError,
    Router:   slog.LevelError,
    Context:  slog.LevelError,
}
s := switcher.NewServer("pwd", nil, cfg)
```

日志输出示例：

```
level=INFO msg="context serve ended" module=server ctx_id=3 domain=node-a error="read: connection reset"
level=WARN msg="route pbuf failed" module=router src_ip=2 dist_ip=99 error="context ip not found"
```

## 数据包路由规则

Router 从每个 Context 读取数据包后按以下规则处理：

- **目标 IP ≠ SwitcherIP** → 按 IP 查找目标 Context，转发（保序）
- **目标 IP = SwitcherIP** → 控制命令，按 Cmd 分发：
  - `CmdOpenStream` — 解析目标域名，转发建流请求
  - `CmdPingDomain` — 域名 ping（空域名直接回复，否则转发到目标）
  - `CmdPingDomain ACK` — 将 ping 响应投递给等待方
  - `CmdSubscribePresence` — 订阅/退订一组域名的上下线事件，应答携带当前状态快照

## 写路径与背压

发往某个 Context 的所有包——数据转发、OpenStream 转发与应答、Ping 应答与转发、Presence 通知与订阅应答、域名替换探测——统一经该 Context 的 `forwardCh` 排队，由唯一的 forward goroutine 顺序写出，不存在绕过队列的直写路径，per-context 包序由单写者结构保证。

背压策略：

- 队列未满 → 非阻塞入队
- 队列满 → 最多等待 5 秒
- 超时 → 判定对端消费停滞。由于 flex 流层没有重传，丢包会造成静默的流损坏，因此采用 fail-loud：释放该 Context（断开连接，由 Registry 完成后续清理），而不是丢包

## 域名冲突处理

当新连接使用已被占用的域名时，Registry 会 ping 现有持有者：

- ping 成功 → 拒绝新连接（`errReplaceDomainFailed`）
- ping 超时/失败 → 踢掉旧连接，新连接接管域名

这保证了断线重连时客户端能重新获取自己的域名。

## Presence 订阅与通知

Node 可以向 switcher 订阅一组域名的上下线状态，替代基于 `PingDomain` 的轮询：

- 订阅请求（`CmdSubscribePresence`）的应答携带这些域名的当前状态快照；快照与后续事件在 switcher 侧原子衔接，不存在"查状态到订阅生效之间"的变更空洞
- 之后每次状态迁移（attach / detach / 域名替换）都以 `CmdNotifyPresence` 实时推送给订阅者，事件携带域名、虚拟 IP、MAC、per-domain 版本号和全局单调序号
- 域名替换时，订阅者按序收到 offline（旧 IP）+ online（新 IP）事件对
- 订阅关系随订阅者连接消亡，断连后自动清理

一致性由 per-domain version 承载：

- 每个域名维护独立的单调 version，每次状态迁移（含 offline）+1；快照与事件都携带它
- 节点 offline 后其状态条目仍然存续（保留最后已知 IP/MAC 与 version），因此节点重新上线时 version 连续递增，不会因"删条目重建"而回退
- 为防止 state 无界增长，"offline 且持续无订阅者超过 1 小时"的条目会被定时清扫回收，version 重新计数。客户端对此天然免疫：重连后的重订阅快照以权威重置语义应用（无条件覆盖本地视图），同一条规则也覆盖了 switcher 重启导致的 version 全局重置
- 订阅者侧据此判别：`version <= 本地` 的是 stale 事件，丢弃；`version` 跳变说明中间有事件缺失，触发一次重同步（重新 Watch 拉快照）。快照同理——比本地视图更旧的快照条目不覆盖（快照与事件在节点侧由不同 goroutine 应用，线上顺序无法传导到本地应用顺序）

实现上由单 goroutine 的 presenceCenter 串行处理所有事件：Registry 在状态突变点（持锁）入队事件，presenceCenter 维护含 version 的状态视图作为快照数据源，并向订阅者逐一投递，因此同一域名的状态迁移严格按因果序到达。

通知仅为信息告知：node 协议栈不会对 offline 节点做任何自动动作。需要 fail-fast 语义（如主动关闭指向死节点的 stream、取消进行中的 Dial）的应用，可在事件回调中自行组合。
