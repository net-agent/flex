# switcher

switcher 是 flex 的中继服务器，负责接收多个 Node 客户端的连接，为每个客户端分配虚拟 IP，并在客户端之间路由数据包。

## 架构概览

```
Node A ──ws/tcp──┐
                  │
Node B ──ws/tcp──┤  Switcher
                  │  ├── Registry  (域名/IP 注册与查找)
Node C ──ws/tcp──┤  ├── Router    (数据包路由与分发)
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

switcher 内部有 4 个子模块，各自拥有独立的日志级别和 `module` 字段：

| 模块 | 默认级别 | 典型日志内容 |
|------|---------|-------------|
| server | Info | 握手失败、attach 失败、连接结束 |
| registry | Warn | attach/detach、域名替换、IP 冲突 |
| router | Warn | 路由失败、转发失败、域名解析失败 |
| context | Warn | forward write 失败 |

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

## 域名冲突处理

当新连接使用已被占用的域名时，Registry 会 ping 现有持有者：

- ping 成功 → 拒绝新连接（`errReplaceDomainFailed`）
- ping 超时/失败 → 踢掉旧连接，新连接接管域名

这保证了断线重连时客户端能重新获取自己的域名。
