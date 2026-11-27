# FLEX 测试用例评估报告（整改前基线）

> 整改后复评见 [test-verification-zh.md](test-verification-zh.md)。本文保留为 2026-10-26 整改前的基线记录。
>
> 评估对象：`refactor/wire-link` 分支，commit `299cca7`
> 评估日期：2026-10-26
> 环境：macOS arm64（Apple M4），Go 1.25.1
> 方法：静态审阅全部测试与相关源码 + 实测运行（全量、`-race`、定向重复、基准、跨包覆盖）
> 覆盖率口径：`go test -coverprofile` 的**按包**统计。跨包路径（例如 switcher 调用 link）不会计入被测包自身覆盖率，因此部分数字会偏低；文中有单独说明。

---

## 一、结论摘要

**总评：3.3 / 5（B-）。** 测试规模、边界用例和协议契约测试在同类网络库中属于中上水平；真正拖后腿的是**可重复性**与**性能回归能力**，而不是用例数量。

| 指标 | 实测值 |
|---|---|
| 测试函数 / Benchmark / Example / Fuzz | 426 / 12 / 5 / 0 |
| 测试代码行数 | 13,763 行 |
| 总语句覆盖率 | **82.8%** |
| `t.Run` / `t.Parallel` / `t.Cleanup` | 67 / **0** / 11 |
| `time.Sleep`（项目测试内） | **46 处** |
| `assert` / `require` / 原始 `t.Fatal`+`t.Error` | 934 / 95 / 554 |
| 全量 `go test ./...` wall time | 约 **30.5s** |
| `go test -race ./...` 最长包 | node **52s**、switcher **44s** |

### 三条最重要的判断

1. **不稳定用例是当前第一优先级，而且根因基本都在测试侧。**
   6 次全量/近全量运行中有 **4 次出现失败**。逐一定位后：
   - `TestRouterPingDomain` 的失败不是产品抖动，而是测试用 `packet.NewBuffer()` 构造了一个 **Cmd=0（CmdAdmit/握手类）** 的非法帧发到已建链路上，服务端安全层按协议拒帧并关链，客户端 `SendFrameSync` 与关链竞态时返回 `writer closed`。
   - `TestIntegration_SchedulingFairness` 的失败是**启动竞态 + 指标口径不一致**：`maxRun` 用未裁剪序列，`Jain` 用裁剪后序列；单次采样，阈值没有统计校准。观测结果呈两极：要么 `minJain=1.0 / maxRun=2`，要么 `minJain=0.1332 / maxRun=192`。
   - `TestIntegration_Default/Churn` 的失败是 **2s close-ack 超时**在高并发（24 workers）和 `-race` 开销下没有余量；单独跑 1/10 失败，`-race` 下 3/3 失败。

2. **性能测试“有代码、无门禁”。**
   12 个 Benchmark 只覆盖 packet/link 的微基准，CI **从不执行** `-bench`；stream/node/switcher 三条核心数据路径 **0 个 Benchmark**；唯一的“公平性性能测试”是一次约 0.1s 级的采样，阈值 0.93 没有推导记录，失败时是灾难值而不是阈值边缘，不能当作回归指标。

3. **功能覆盖总体良好，但安全/兼容/调度边界有明显空洞。**
   link 69.7%、wire 71.5%、internal/sched 48.3%；`internal/domain`、`internal/framing` 没有包内测试（framing 行为被 link/transport 测试间接覆盖）；legacy 编解码、`SendFrameSync`、`packet_transport`、`Frame` 的 release 回调、reconnect backoff、presence resync 失败等关键分支为 0%。

### 建议的最小框架组合

不需要引入“大而全”的测试框架。**testify 已经是依赖**，把用法统一即可；再补四个小而成熟的工具：

`testify/require + Eventually`（已有）→ `go.uber.org/goleak`（goroutine 泄漏）→ `testing/synctest`（Go 1.25，替代 sleep/真实超时）→ 原生 `testing.F` fuzz + `benchstat`（CI 性能基线）。

**不建议**引入 Ginkgo/Gomega（DSL 重、表格驱动不自然）、testify/suite 全局推广（串行 fixture 会阻碍 `t.Parallel`、隐藏状态多）。

---

## 二、模块总览

| 模块 | 测试数 | 覆盖率 | 正常耗时* | 功能 | 性能/稳定 | 边界 | 可维护 | 综合 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| `wire` | 32 | 71.5% | ~1–3s | 3.5 | 2.5 | 3.5 | 3.0 | **3.1** |
| `packet` | 42 | 91.2% | ~1–2s | 4.5 | 3.0 | 4.5 | 3.5 | **3.9** |
| `packet/ws` | 14 | 77.0% | ~0.5–2.5s | 3.5 | 2.0 | 3.5 | 3.5 | **3.2** |
| `link` | 21 | 69.7% | ~0.5–1.2s | 3.0 | 2.5 | 2.5 | 3.0 | **2.8** |
| `stream` | 123 | 90.2% | ~14–15s | 4.5 | 2.5 | 4.5 | 3.5 | **3.8** |
| `node` | 125 | 85.7% | ~29–30s | 4.0 | 2.0 | 4.0 | 3.0 | **3.4** |
| `switcher` | 47 | 88.0% | ~9–15s | 4.0 | 2.0 | 3.5 | 3.0 | **3.3** |
| `internal/*` | 19 | 95.3 / 88.9 / 48.3 / 0 / 0% | <1.5s | 2.5 | 1.5 | 3.0 | 3.0 | **2.5** |
| `test/integration` | 3 | 无语句统计 | ~6–12s | 4.0 | 1.5 | 2.5 | 2.0 | **2.6** |
| **合计** | **426** | **82.8%** | **30.5s** | 3.5 | 2.0 | 3.8 | 3.2 | **3.3** |

\* 单包耗时来自本机实测；同一包在 `-race` 下通常放大 2–4 倍。

一个补充数据：只跑 `test/integration` 并加 `-coverpkg=./...`，可覆盖全仓 **49.0%** 的语句，说明集成套件的“系统级触达”并不差，问题在于它的断言与稳定性。

---

## 三、逐模块评估

> 评分维度：功能覆盖、性能/稳定、边界条件、可维护性（各 5 分制）。严重度定义：**P0** = 会导致 CI 失败或测试结论错误；**P1** = 关键生产路径缺少保护；**P2** = 质量/效率改进项。

### 3.1 `wire` — 3.1 / 5

**做得好的**
- 黄金字节测试锚定了 11 字节线格式（`wire/codec_test.go:23,42`），不是“编码后再解码”的自证循环。
- 对 class 位双向窜用、未知命令、payload/ack 规则的拒绝很完整（`wire/codec_test.go:102-140`、`wire/frame_test.go:61-100`）。
- `wire/contract_test.go` 用 AST 扫描命令常量与注册表，是很好的“新增命令忘记注册”CI 兜底。
- `Frame.Release` 的池化语义有设计，但测试没跟上（见下）。

**主要问题**

| 严重度 | 问题 | 证据 |
|---|---|---|
| P0 | `NewFrameWithRelease`、release 回调、构造失败即归还全部 0% 覆盖；最近一次池化改动没有对应测试 | `wire/frame.go:65-95` |
| P0 | legacy 编解码 0% 覆盖，但它是现役兼容路径 | `wire/codec.go:97-142`；`link/link.go:123,253`；`switcher/context.go:289` |
| P1 | `ErrInvalidClass` 全仓无断言；`SID.Uint64`、`Commands`、`MustRegister*`、`IsData/IsControlCommand` 无测试 | `wire/frame.go:98-100`、`wire/wire.go:149,223-233` |
| P1 | 边界只测了 `MaxPayloadSize+1`，缺 65535 精确值、`ackSize=0xFFFF`、Size=0 数据帧、Release 后状态 | `wire/frame_test.go:95` |
| P2 | 包内 0 Benchmark；唯一的 codec 基准在 `link/bench_test.go:73`，Encode+Decode 合一、无 `SetBytes`、无阈值 | `link/bench_test.go:73-92` |
| P2 | 命令表在 `wire.go`、`wire_test.go`、`codec_test.go`、`contract_test.go` 四处维护；`noPayload` 既是被测规则又被当作测试 oracle | `wire/codec_test.go:65` |

**建议**
1. 为 `NewFrameWithRelease`/`Release` 增加“回调恰好一次、失败归还、Release 幂等”的表驱动用例，这是池化最容易出错的地方。
2. 为 legacy codec 增加 golden 兼容用例（新旧头互读），并把 `ErrInvalidClass` 纳入。
3. 原生 fuzz：`DecodeHeader → EncodeHeader → DecodeHeader` 幂等，断言不 panic、错误可分类（见 §5.4）。

---

### 3.2 `packet` / `packet/ws` — 3.9 / 3.2

**做得好的**
- 主包 91.2%，payload 0/64KB、nil buffer、短读、关闭后读写都有；`limitWriter` 能注入写失败（`packet/buffer_test.go:209-224`）。
- Reader/Writer 大量使用子测试，是仓库里表格驱动最好的包（31 个 `t.Run`）。
- WS 用 `httptest` 建真实连接，并构造非 binary、尺寸不匹配、超限等恶意帧（`packet/ws/ws_test.go:134-221`）。
- `packet/example_test.go` 可运行，CI 会校验 Example 输出。

**主要问题**

| 严重度 | 问题 | 证据 |
|---|---|---|
| P1 | `framing.Transport` 委托方法在包内 0 调用；`ReadPayloadInto`/`WriteBufferBatch` 的 fallback 实际不可达 | `packet/conn.go:46-90`、`packet/ws/conn.go:39-72` |
| P1 | `WriteBufferBatch` 对 nil 元素没有保护，单写有保护而 batch 没有（`buf.Head[:]` 会 panic） | `packet/writer.go:57-71` |
| P1 | `require.NoError` 出现在子 goroutine（`FailNow` 只能在测试 goroutine）——失败时可能挂起或误报 | `packet/writer_test.go:22,35,107`、`packet/ws/ws_test.go:58,78-79,274` |
| P2 | Pool 的 double-Put、污染、`PutBuffer(nil)` 未测；`TestBuffer_Pool` 只有一轮 | `packet/buffer.go:90-93`、`packet/buffer_test.go:246-262` |
| P2 | presence 仍用 `reflect.DeepEqual` + 全 stdlib，与其他包风格不一致，失败信息差 | `packet/presence_test.go` |
| P2 | WS 的 `Dial/Upgrade` 错误被丢弃；`wsWriter` 关闭错误被 defer 吞掉 | `packet/ws/pipe.go:60,73`、`packet/ws/writer.go:36-43` |

**性能测试**
- 7 个基准，形状基本正确：按 payload 分子基准、`ReportAllocs`、`SetBytes`。
- 但 `BenchmarkNewBuffer` 的 `_ = NewBuffer()` 可能被优化掉（文档里的 0.23ns/0 alloc 不可信）；`BenchmarkReadWriteBuffer` 忽略写错误，且用 `net.Pipe` 测的是调度/拷贝而不是 TCP；benchmark 的 pool 使用方式与生产 hot path 不完全一致。
- 没有 ws / presence / batch 的基准；CI 不跑 `-bench`。

**建议**
1. 修掉 goroutine 内的 `require`，改为 `errCh` 收集后在测试 goroutine 断言（integration 的 churn 已经是正确范式）。
2. batch 路径补 nil/部分失败用例；pool 补 double-Put 与污染回归。
3. 若给 `framing` 加单测，packet 侧只保留“委托是否正确”的黑盒契约测试，避免两套 WS 分帧逻辑各自漂移。

---

### 3.3 `link` — 2.8 / 5

**做得好的**
- 安全握手成功路径有真断言：固定 512B、控制 payload 不落明文、padding、错误 PSK、重放拒绝（`link/secure_test.go:116-173,202-223`）。
- 传输层错误矩阵完整：短头、尺寸不匹配、非 binary、deadline、上一帧未消费（`link/transport_test.go:53-245`）。
- 公平调度的两种 Policy 都有批处理上限断言（`link/fair_test.go:47-70`）。
- Benchmark 用 `nopTransport` 隔离内核/管道，思路正确。

**主要问题**

| 严重度 | 问题 | 证据 |
|---|---|---|
| P0 | `packet_transport.go` 在 link 包内 0%（switcher 测试可能间接触达，但没有断言）：这是第三方 `packet.Conn` 的生产兜底路径，涉及 pending 所有权与 `ErrFrameNotConsumed` | `link/packet_transport.go:22-119` |
| P0 | `SendFrameSync → SendSync/writeSync` 在 link 包内 0%：控制帧实际走这条路，写失败/关链竞态没有断言 | `link/link.go:195`、`link/fair.go:202-232`、`node/node.go:449-453` |
| P1 | `SetFrameTap/fireTap` 包内 0%（仅集成测试间接用过）；明文可见性、置 nil、panic 语义未测 | `link/link.go:45-57` |
| P1 | 握手错误矩阵缺版本、时间漂移、replay cache 淘汰/满、二次 `Respond`、domain 规范化失败 | `link/handshake.go:296-312,354-356` |
| P1 | `SetWriteTimeout`、`LocalAddr/RemoteAddr`、并发 `Close`/`Send`、写失败后的 frame release 未测 | `link/link.go:274-288`、`link/fair.go:266-307` |
| P2 | Benchmark 直调 `writeSecureFrames`，绕开 fairWriter、通道和所有权；无读路径、无多流公平、无 allocs | `link/bench_test.go:59` |

**性能测试**
- 现有 5 个基准只测“单帧加密/编码”，无法回答“多流公平调度开销”“锁竞争”“读路径分配”。
- `BenchmarkSecureDataFrame*` 没有 `ReportAllocs`；没有与 `docs/security_contract_zh.md` 中分配数承诺对应的自动校验。
- 没有 benchstat/阈值，CI 不跑，因此文档里的数字无法防止回归。

**建议**
1. 补 `packet_transport` 与 `SendFrameSync` 两条生产路径的单测；这是当前风险最高的空洞。
2. 用 fake Transport + 多 SID 直接驱动 fairWriter，断言每轮包数、`maxRun`、Jain=1；这比集成层的公平性采样稳定得多。
3. 握手测试改用表驱动覆盖拒绝矩阵，KDF 显式使用 `KDFHKDF`（link 测试已经这么做），避免 Argon2id 拖慢。

---

### 3.4 `stream` — 3.8 / 5

**做得好的（本仓库最强项之一）**
- 123 个用例、90.2% 覆盖，边界矩阵非常完整：0 字节读写、部分读、恰好填满、空包、过期 deadline、窗口为 0、并发 Close、超大 payload、ACK 超量、慢消费者。
- 回归/压力用例单独分文件，并标注 P0/P1（`net_conn_writer_regression_test.go`、`net_conn_writer_stress_test.go`）。
- 多处使用总量/确定性断言而不是盲等（`stress_test.go:68-73`、`reader_test.go:454`）。
- `helper_test.go` 提供了可复用的 mock writer / sizing writer / 推送助手。

**主要问题**

| 严重度 | 问题 | 证据 |
|---|---|---|
| P1 | 帧/Endpoint 路径 0%：`NewWithEndpoint`、`NewDialStreamWithEndpoint`、`NewAcceptStreamWithEndpoint`、`ErrNilWriter` 无测试 | `stream/stream.go:121-194`、`stream/endpoint.go:11` |
| P1 | `startSendInterrupter` 协程体 0% 覆盖；两条 P1 回归因 `CanInterruptWrite()==false` 恒 skip | `stream/net_conn_writer.go:174-197`、`stream/sender.go:104`、`net_conn_writer_regression_test.go:81-83,124-126` |
| P1 | ACK 边界缺 `add(0)`、空 take、late-wake、stop 幂等；窗口回绕保护未用 MaxInt32 触发 | `stream/ack.go:58,128-179`、`stream/window_guard.go:38-87` |
| P2 | `SetOnDetach` 一次性语义无单测；`SetLogger` 几乎未测 | `stream/lifecycle.go:10-65` |
| P2 | 26 处 sleep + 28 处 `time.After`；`helper_test.go:88-90` 的 50/100/200ms 同时充当 deadline 与上限 | `stream/helper_test.go:86-91` |
| P2 | 70+ 处直接改内部字段（`SetSize/Signal`、`acks.interval`、`closeAckTimeout`、`state.*`），重构成本高 | `reader_test.go:24`、`writer_test.go:31` |
| P2 | `pipe_test.go` 每次 `Pipe()` 泄漏 6 个 goroutine，注释自认；全文件 21 次调用 | `stream/pipe_test.go:103-106` |
| P2 | 7 个用例无断言；`TestWriteSerializes…` 依赖抢占顺序取锁，会 flake | `writer_test.go:322-336` |

**性能测试**
- 4 类压力场景（高并发写、阻塞写、tiny window、deadline thrash）覆盖了正确性，但都是**墙钟驱动**：1ms ticker 供 256B、200µs sleep、`wg.Wait` 无超时。
- 核心数据路径 **0 Benchmark**：缺读写吞吐、ACK 合并率、窗口竞争、recv 合并、分配数基线。
- 没有 `testing.Short()` 守卫，32MB echo / 10MB pipe 每次必跑。

**建议**
1. 用 `testing/synctest` 重写阻塞/唤醒/ACK flush/closeAck 类用例，可直接消灭大部分 sleep（见 §5.4）。
2. 补 `BenchmarkWriteThroughput`、`BenchmarkAckBatching`、`BenchmarkRecvAppend`，全部 `ReportAllocs`。
3. 给 `Pipe()` 加 `t.Cleanup` 关闭，或提供 `newPipe(t)` 助手；这是泄漏检查可靠性的前提。

---

### 3.5 `node` — 3.4 / 5

**做得好的**
- Session 生命周期主干完整：懒连接、`WaitReady`、`Close` 幂等、断线重连、listener 重注册都有断言。
- 三处 Close 与建连/握手/装 Node 的竞态被专门覆盖（`node/session_test.go:1264,1295,1325`）。
- presence 的 stale/gap/自动重订阅、observe/streamhub/window 的 trace、SID 复用、窗口对称协商都有测试。
- `node/session_test.go` 文件头列了“按函数”的覆盖目标与未覆盖原因，可维护性好。

**主要问题**

| 严重度 | 问题 | 证据 |
|---|---|---|
| P1 | backoff 翻倍/复位未测，文件头自注“性价比低”；只靠一个真实等 1s 的用例间接经过一次 | `node/session_test.go:25,907`、`node/session.go:585-634` |
| P1 | `Session.SetFrameTap` 与重连后重新注入 0% 覆盖 | `node/session.go:152,637` |
| P1 | presence resync 成功/失败、resubscribe 失败、跨 epoch 版本回退未覆盖 | `node/session.go:468-507` |
| P1 | 重连时 listener 重注册失败只记日志、监听器会“死”，无断言；bridge 满缓冲分支未测 | `node/session.go:658,712` |
| P2 | nil connector 只在“不 Serve”用例中构造；一旦 Serve 触发会空指针 | `node/session.go:565-569`、`session_test.go:187` |
| P2 | 心跳超时无行为断言（`TestKeepalive` 无 assert）；重连风暴、并发 Dial+Close 未测 | `node/node_test.go:74-108` |

**耗时与性能**
- 30s 偏高，且**至少约 20s 来自真实超时等待**：`TestConnectHandshakeFailed` 单条 10.00s；`TestNodeCloseStream`、`TestHandleCmdOpenStream_BacklogFullReturnsAckError`、`TestAttachStream_OnDetachReleasesPortAndRecordsState`、`TestTraceStreamClosedOnNodeClose`、`TestNodeCloseStreamsReasonNode` 各约 2.00s（与 2s 级超时等待吻合，例如 `DefaultCloseAckTimeout`）。
- 默认 KDFArgon2id 每次派生实测 **31.8ms / 67MB**（本机，`-benchtime=3x`）；node 测试未显式用 `KDFHKDF`，会叠加若干秒，但不是 30s 的主因。
- 全包 0 Benchmark；`dialer_test.go` 的 10 流并发更像压力冒烟，不是可重复度量。
- 15 处 sleep、0 `t.Parallel`；多个测试使用包级全局状态，直接并行会互相污染。

**建议**
1. 把握手超时、close-ack 超时做成可注入配置，测试用 100–500ms；仅这一项可把 node 包从 30s 降到 10s 量级。
2. backoff 用 `testing/synctest` 做确定性断言（§5.4），不再“绕开”。
3. 前置条件改 `require`，去掉 `sl, _ := s.Listen(80)` 这类吞错；三套轮询助手合并为 `require.Eventually`。

---

### 3.6 `switcher` — 3.3 / 5

**做得好的**
- 限流器注入时钟、纯函数式测试（`switcher/limits_test.go:46-92`），不依赖真实时间。
- Registry 的替换/IP 租约、presence 的 version/stale 规则、写失败注入都有较深覆盖。
- 覆盖率达到 88.0%，核心路由/注册/订阅状态机基本都有用例。

**主要问题**

| 严重度 | 问题 | 证据 |
|---|---|---|
| P0 | `TestRouterPingDomain` 用零值 Buffer（Cmd=0=CmdAdmit）当“未知 IP 数据帧”，实际发送握手帧，服务端拒帧关链；`WriteBuffer` 与关链竞态导致 5%–30% 失败。它想覆盖的 `forward` 路由失败路径并未测到 | `switcher/router_test.go:31-38`；`packet/buffer.go:95-97`；`node/node.go:449-453`；`link/secure.go:151-152` |
| P1 | `initTestEnv` 固定 `<-time.After(50ms)`，且没有 `t.Cleanup`；server/node/pipe 全部泄漏，后续用例在污染环境中运行 | `switcher/helper_test.go:22-46` |
| P1 | `TestServerRun` 使用真实 `localhost:39603`，并且 `Close()` 后继续 `Serve`、重复 `Serve`——为覆盖而固化非法生命周期 | `switcher/server_test.go:18-33` |
| P1 | 关闭语义只轮询 registry，未断言 `Serve` 返回、presence 退出、握手中 Close、Close 后拒连 | `switcher/server_test.go:195-206` |
| P2 | goroutine 泄漏检查用 `runtime.NumGoroutine() <= base+3` 轮询，基线被存量泄漏抬高，小泄漏会漏报 | `switcher/server_test.go:230-246` |
| P2 | 准入/配额只测纯函数，`ServeConn` 的限流拒绝分支从未触发（net.Pipe 下 `remoteIP` 为空而旁路） | `switcher/limits.go:131-143` |

**性能测试**
- 0 Benchmark。
- `scheduler_test.go` 只验证 4KB 数据完整性与配置，没有公平性/吞吐/延迟/分配指标。
- 9–15s 中有相当一部分来自固定等待（helper 50ms、域名替换 3s、ping 1s、Unwatch 500ms），不能作基线。

**建议**
1. 把 `TestRouterPingDomain` 拆成两层：Router 单测直接构造合法 `wire.Frame` 调 `forward`；Session 层用 `require.Eventually` 等就绪，不在“已建链路上发握手帧”。
2. `initTestEnv` 改为 `newTestEnv(t)`，内部 `t.Cleanup` 关闭 server/nodes/pipes；用 `Eventually` 等 registry 就绪，删掉 50ms。
3. 加 goleak `TestMain`，先修存量泄漏，再让泄漏检查真正有效。

---

### 3.7 `internal/*` — 2.5 / 5

| 子包 | 覆盖率 | 结论 |
|---|---:|---|
| `internal/idpool` | 95.3% | 质量最好；`Lease` 幂等回归很扎实（`pool_test.go:157-197`），缺 `Capacity`/`Acquire` 失败返回；并发测试吞错提前 return（`:147-149`） |
| `internal/pending` | 88.9% | 只缺 `Len`；`Complete` 与 `Remove` 同 key 并发未测；测试在 goroutine 内 assert 且用 50/20ms sleep |
| `internal/sched` | **48.3%** | 只有 3 条 `Normalized` 断言；`String`、`DefaultSchedulerConfig`、`visitLimit` 0%；`visitLimit` 全仓无调用，真实上限重写在 `link/fair.go:336`，两份逻辑必然漂移 |
| `internal/domain` | **0%** | 63/64 字节边界、大小写、首尾 `-`/`_`、TrimSpace、非法字符、`local/localhost` 全无单测；只在 switcher 间接碰到空串/localhost |
| `internal/framing` | **0%（包内）** | WS 分帧行为由 `link/transport_test.go:53-245` 端到端覆盖；真正未测的是 TCP 分帧单测、`ReadPayloadInto`、`io.ErrShortBuffer`、WS 长度不匹配、`WriteFrames` 空切片/写失败 |

**额外风险**
- `framing.TCPTransport.WriteFrames` 不校验 `payload` 长度与 header `Size` 是否一致（`internal/framing/framing.go:111-124`）；一旦调用方写错，接收端会按错误长度持续错流，且当前没有测试能发现。
- `packet/ws` 另有一套独立的 WS 分帧实现（`packet/ws/reader.go:47-90`），与 `internal/framing` 的 WS 路径并存；两套逻辑容易漂移，测试也容易误判“已覆盖”。

**建议**
1. `domain.Normalize` 用 10 分钟的表驱动测试补齐（§5.2），并加 `FuzzNormalize` 断言幂等与字符集。
2. sched 补 `String`/`DefaultSchedulerConfig`/`visitLimit`，并删除或复用 `visitLimit`，消除双份上限逻辑。
3. framing 补 TCP 单测与“payload/Size 不一致必须报错”的守卫；这是防协议错位的低成本高收益项。

---

### 3.8 集成测试与性能测试 — 2.6 / 5

**做得好的**
- 真实 TCP + 双域双向多流，Basic/Stress/Churn 三阶段（`test/integration/integration_test.go:118-134`）。
- 自造了一套带 SHA-256 逐帧校验的应用协议（`:408-411,628-660`），不是简单的 echo。
- Churn 的错误通过 channel 收集后在测试 goroutine 断言，是并发测试的正确范式（`:270,305`）。
- cost-budget 策略端到端贯通（`scheduler_cost_test.go`）。
- 公平性用例带 HTML 可视化报告，便于人工分析。

**主要问题**

| 严重度 | 问题 | 证据 |
|---|---|---|
| P0 | 公平性用例单次采样、阈值无校准；观测两极分化（1.0/2 或 0.1332/192），单独跑 1/5 失败 | `scheduling_fairness_test.go:485-492` |
| P0 | `maxRun` 用完整序列，`Jain` 用 `trimMiddle` 后序列；启动/收尾突发只影响前者，指标自相矛盾 | `:228` vs `:239-245` |
| P0 | Churn 把 2s close-ack 超时当失败；24 workers + `-race` 下无余量，单独 1/10、`-race` 3/3 失败 | `integration_test.go:114,317-320`；`stream/stream.go:32` |
| P1 | 公平性 0.93 实际要求“12 条流每个整窗都活跃”；缺 1 条流整窗时 Jain≤11/12=0.9167<0.93，阈值与负载模型脱节 | `:79-86` |
| P1 | 集成只建一次 Session，不覆盖重连；不设 Limits，不覆盖限流拒绝；只用 TCP，不覆盖 WS；只 `Close()`，不覆盖半关闭 | `integration_test.go:153-176,145,703-711` |
| P1 | Basic 失败即 `Skip` 掉 Stress/Churn，后续覆盖静默丢失 | `integration_test.go:122-130` |
| P2 | 1950 行被拆在 3 个文件，但 fairness 单文件 1114 行，其中 316 行 HTML/CSS 和约 275 行报告构建，应移出测试逻辑 | `scheduling_fairness_test.go:523-1114` |
| P2 | 三份 harness 重复；约 220 行自造握手协议与断言同文件 | `integration_test.go:136-310`、`scheduler_cost_test.go:23-70` |

**性能测试判断**
- 公平性测试本质是**一次采样**，不是性能回归测试：无预热 barrier、无重复、无置信区间；“Stress/Churn”命名也没有吞吐/延迟阈值。
- 真正的性能门禁缺失：没有 benchstat 基线，CI 不跑 benchmark，文档中的 M4 数字没有 commit/环境元数据，无法复现。
- 现有 12 个 Benchmark 是微基准，不能回答“多路复用数据路径的吞吐/尾延迟”。

**建议**
1. 把公平性拆成：link 层的确定性调度单测（fake Transport，多 SID，断言每轮≤2、maxRun≤2、Jain=1）+ 集成层的稳态采样（预热、重置记录、重复 5 次取中位数、`-short` 跳过）。两者都应在**同一裁剪序列**上计算所有指标。
2. Churn 使用可注入的 close-ack 超时（测试设 5s），或把 `-race` 下的并发降到 6–8；错误信息带上 worker/耗时。
3. 抽 `newHarness(t)`，统一 `t.Cleanup`；HTML 报告移到 `testdata/` 模板或独立 helper。
4. 集成层补重连、限流拒绝、优雅关闭、WS、CloseWrite/CloseRead 五类旅程。

---

## 四、横向专题

### 4.1 稳定性：实测与根因

| 用例 | 复现命令 | 实测失败率 | 根因判断 |
|---|---|---|---|
| `TestRouterPingDomain` | `-run '^TestRouterPingDomain$' -count=20/100` | 1/20；30/100 | 测试构造 Cmd=0 握手帧；`SendFrameSync` 与关链竞态 |
| `TestIntegration_SchedulingFairness` | `-run … -count=5` | 1/5 | 启动竞态；`maxRun` 未裁剪；单次采样 |
| `TestIntegration_Default/Churn` | `-count=10`；`-race -count=3` | 1/10；3/3 | 2s close-ack 超时 vs 24 workers + race 开销 |
| 全量 `go test ./...` | 6 次（含 cover/race） | **4 次红** | 以上三者 |

补充：
- Churn 的失败目前**无法区分**是“2s 超时过紧”还是“close ack 真的丢失”。建议失败时输出 stream 状态、close reason、worker 进度，并用低并发（6–8 workers）做对照；在区分之前，不应简单把超时调大后当作已修复。
- `go test -race ./...` 未发现 `DATA RACE`，说明这些是**时序/测试逻辑问题**，不是数据竞争。
- 全量 wall time 30.5s，但 `node` 单包 30s、`switcher` 在 `-race` 下 44s；CI 的默认 10min 超时不会触发，但 PR 反馈偏慢。
- 这些失败大多不是“阈值边缘”，而是**灾难值**（Jain 0.13 / maxRun 192 / 直接 close timeout），说明测试在错误条件下才失败，平时缺乏敏感度。

### 4.2 性能测试：覆盖矩阵

| 模块 | Benchmark | 压力/公平性用例 | CI 执行 | 回归阈值 |
|---|---:|---:|---:|---:|
| wire | 0（codec 基准在 link） | 0 | 否 | 无 |
| packet | 7 | 0 | 否 | 无 |
| packet/ws | 0 | 0 | 否 | 无 |
| link | 5 | 2（批处理上限） | 否 | 无 |
| stream | **0** | 4（墙钟） | 是（只作正确性） | 无 |
| node | **0** | 1（10 流冒烟） | 是 | 无 |
| switcher | **0** | 1（4KB 完整性） | 是 | 无 |
| integration | 0 | 公平性 1 次采样 | 是 | 0.93（未校准） |

**合理之处**
- packet 的分子基准、link 的 `nopTransport` 隔离、`SetBytes`/部分 `ReportAllocs` 都是正确方向。
- stream 的 4 类压力场景覆盖了正确性风险，不是纯“跑得快”。

**不合理之处**
1. **没有 CI 门禁**：Benchmark 不跑、无 benchstat、无基线，等于没有性能回归保护。
2. **微基准不可外推**：`net.Pipe` 不是 TCP；`_ = NewBuffer()` 可能被优化；`writeSecureFrames` 绕过调度器；写错误被忽略。
3. **指标口径不一致**：公平性同时用了两套序列；`SetBytes` 含 11B 头但有时不说明。
4. **绝对时间断言**：stream 的 ACK 合批、tiny window、deadline thrash 都依赖墙钟；-race/慢机必抖。
5. **文档数字不可复现**：`docs/new-benchmark.md` 只写 M4，没有 Go 版本、commit、benchstat 输出；HTML 文档还引用了已删除的 `internal/sched/fair_test.go`。

**建议的三层性能体系**
- L1 微基准：wire/packet/link，`b.Loop()` + `ReportAllocs` + `SetBytes`，CI 用 `-benchtime=1x` 冒烟，nightly 用 `-count=6` + benchstat。
- L2 路径基准：stream 读写、ACK 合并、fairWriter 多流、switcher 转发；用 fake transport 隔离 IO。
- L3 端到端：集成层的吞吐/延迟/尾延迟，独立 job、独立机器或 nightly；不与 PR 的 `-race` 混跑。
- 阈值用**相对回归**（如 ns/op +10%、allocs/op 增加即失败），不写绝对 ns。

### 4.3 边界条件：覆盖矩阵

| 模块 | 已覆盖的强项 | 主要缺口（按优先级） |
|---|---|---|
| wire | 截断 <11B、未知命令、class 互窜、MaxPayload+1 | 65535 精确值、ack=0xFFFF、Size=0、Release 后状态、legacy、fuzz |
| packet | 0/64KB、nil、短读、关闭后读写、WS 非 binary | batch nil、double Put、池污染、并发 Close、WS 恰为上限 |
| link | 错误 PSK、重放、短帧、尺寸不匹配、未消费帧 | 握手截断/漂移/版本、replay 满、二次 Respond、并发 Close/Send、tap |
| stream | 0 字节、部分读、并发 Close、窗口 0、超大 payload | Endpoint 构造、ACK 0/空 take、窗口回绕、SetOnDetach、Pipe 泄漏 |
| node | Close 三窗口竞态、重连、presence stale/gap、SID 复用 | backoff、nil connector Serve、重连风暴、心跳超时、resync 失败 |
| switcher | IP 耗尽、domain 冲突、重复订阅、限流边界 | 关闭期新连接、慢/死订阅者、配额释放恢复、并发 attach/detach |
| internal | idpool 双释放、pending Remove、framing WS 恶意帧 | domain 全部、sched 分支、framing TCP 长度校验 |
| integration | 多流、双向、逐帧校验、churn | 重连、限流拒绝、优雅关闭、WS、半关闭、64KB 单帧 |

**结论**：stream/packet 的边界覆盖是“优秀”，wire/link 是“及格但缺关键安全/兼容分支”，internal 与集成层是“结构性缺口”。

### 4.4 逻辑清晰度与可维护性

**优点**
- `wire/contract_test.go`、`stream` 的 regression/stress 分文件、`node/session_test.go` 的覆盖目标注释、`switcher/limits_test.go` 的注入时钟，都是可复用的好范式。
- 命名总体表意，错误断言多用 `errors.Is` 而非字符串比较。
- Example 可运行且被 CI 校验，文档与示例有一定联动。

**问题（按影响排序）**
1. **同步靠 sleep**：46 处 `time.Sleep`；`switcher/helper_test.go` 的 50ms、`node` 的 100/500ms、`stream` 的 50/100/200ms 都是典型。它们既慢又不稳。
2. **断言体系不统一**：934 个 `assert`、95 个 `require`、554 个原始 `t.Fatal/Error`；`node` 全包 0 `require`，失败后继续解引用容易 panic；`packet/ws` 又在 goroutine 里用 `require`。
3. **缺少表格驱动**：426 个测试只有 67 个 `t.Run`；wire/link/switcher 几乎为 0；大量重复的 `if err != nil { t.Fatal }` 样板。
4. **巨型文件**：`node/session_test.go` 1353 行、`integration/scheduling_fairness_test.go` 1114 行（含 316 行 HTML）、`stream/net_conn_reader_test.go` 470 行。按行为拆分比按文件类型拆分更重要。
5. **测试夹具重复**：三套轮询助手、三套环境搭建（integration）、两套 `connectNode`（switcher）；没有 `internal/testutil`。
6. **白盒耦合**：所有功能测试都是同包；仅 4 个 Example 用外部包。stream 有 70+ 处直接改内部字段，重构会大面积改测试。
7. **为覆盖率而测**：`TestServerRun` 的 `s.Close(); go s.Serve(l)`、重复 `Serve`、`registry_test.go` 的空 sleep，固化了非法生命周期，反而误导后来者。
8. **零并行**：0 个 `t.Parallel`；纯单元测试本可并行，node 30s 有下降空间。
9. **命名小问题**：`Test_parseAddress`、`TestDialBufErr_portmAndRepsonse`、`TestCover*`；测试名不表意。
10. **保留的 skip**：stream 有 2 条 P1 用例因 `CanInterruptWrite()==false` 恒 skip，应在代码或测试层面明确产品限制，而不是静默跳过。

### 4.5 CI 与测试基础设施

当前 CI（`.github/workflows/go.yml`）做得好的：gofmt、vet、staticcheck、`-race`、文档一致性、coverage 上传。

问题：
- **Go 版本不一致**：`setup-go` 用 1.24，`go.mod` 要求 1.25.0。Go 会自动下载 toolchain，但缓存/耗时/行为都不确定；`synctest` 也无法使用。
- **无覆盖率门禁**：只上传 Codecov，没有阈值，也没有按包下限；`internal/domain`/`framing` 0% 不会被拦。
- **无 Benchmark / Fuzz 任务**：12 个 benchmark 从不运行；0 个 fuzz。
- **没有 `-count=1` / `-shuffle=on` / `-timeout`**：可能命中测试缓存，也无法发现顺序依赖。
- **集成与单元混跑**：`go test -v -race ./...` 把 30s 的 node、15s 的 stream、12s 的 integration 放在一起，互相抢 CPU，恰好放大了公平性/Churn 的抖动。
- **coverage 又全量跑一遍**：CI 时间翻倍；`coverage_test.sh` 也没有 `-count=1`。
- **没有 flake 统计**：没有 gotestsum/JUnit 产物，无法跟踪“这次红是不是已知 flake”。

---

## 五、是否引入测试框架？——建议与示例

### 5.1 结论

**可以引入，而且应该引入；但不是替换现有栈，而是“补齐 + 统一”。**

- testify 已经是直接依赖（`go.mod:7`），继续用它作为唯一断言库。
- 需要新增的只有 **goleak**（1 个依赖）和可选的 **go-cmp**（1 个依赖）；其余用标准库/官方工具即可。
- testify/suite 只建议在**单个集成 fixture** 中使用，不建议全仓推广；Ginkgo/Gomega 不建议。

### 5.2 选型对比

| 方案 | 解决的问题 | 成熟度/成本 | 建议 | 优先级 |
|---|---|---|---|---|
| **testify require/assert/Eventually** | 断言统一、异步等待、错误链 | 已有依赖，零迁移成本 | 全仓规范：前置条件 `require`，多检查 `assert`，异步 `Eventually` | **P0** |
| **go.uber.org/goleak** | goroutine 泄漏 | 1 个依赖，`TestMain` 一行 | 替换手写 `NumGoroutine` 轮询与 ad-hoc leak 测试 | **P0/P1** |
| **testing/synctest** | 真实时间等待（46 处 sleep、超时、backoff、deadline） | Go 1.25 标准库，零依赖 | 纯时间逻辑优先；涉及真实 `net.Pipe` 的用例不进 bubble | **P1** |
| **原生 fuzz（testing.F）** | 不可信字节解析（wire/framing/domain/ws） | 标准库，零依赖 | 4 个 fuzz target + CI 30s 冒烟 | **P1** |
| **benchstat** | 性能回归 | `golang.org/x/perf` 工具，CI-only | nightly 基线 + 相对阈值 | **P1** |
| **google/go-cmp** | 结构体/快照 diff | 1 个依赖 | 可选：`State`/`Stats`/`PresenceState` 的整值比较 | P2 |
| **testify/suite** | fixture 生命周期 | 已有依赖 | 仅集成层可选；不要用于单元测试 | P2 |
| **testscript** | CLI/examples 端到端 | `go-internal`，学习成本中等 | 当前 examples 简单，暂不需要 | P2/nightly |
| **Ginkgo/Gomega** | BDD DSL | 重、隐式全局、表格驱动不自然 | **不推荐** | — |

### 5.3 统一约定（建议写进 `TESTING.md`）

1. **前置条件用 `require`，独立检查用 `assert`**；失败后不再解引用。
2. **绝不在子 goroutine 里调用 `require`/`t.Fatal`**：用 `errCh` 收集，回到测试 goroutine 断言。
3. **每个资源都 `t.Cleanup`**：server、node、pipe、listener、临时文件；测试之间不共享全局状态。
4. **禁止 `time.Sleep` 等状态**：用 `require.Eventually`、channel、`synctest`。
5. **固定超时命名常量**；测试超时要留足余量，不要贴着产品默认值（例如 close-ack 产品默认 2s，测试可注入 5s）。
6. **表驱动 + `t.Run`**：校验类、解析类、错误矩阵必须表驱动；子测试名描述场景而非编号。
7. **纯函数/无共享状态的测试加 `t.Parallel`**；使用全局变量、真实端口、进程级资源的测试不加。
8. **错误断言用 `ErrorIs/ErrorAs`**，不要只判 `err != nil`；需要区分“错误原因通过”的场景。
9. **随机数固定种子**；不要用 `time.Now().UnixNano()` 作为唯一随机源。
10. **覆盖率按包设下限**（建议 80%，新包 85%），并对修改行做增量检查；0% 包不允许合入核心路径。

### 5.4 具体示例

**（1）表格驱动 + require（domain）**

```go
// package domain（与源码同包；外部包则改为 domain_test + domain. 前缀）
func TestNormalize(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr error
	}{
		{"trim and lower", "  Node-A  ", "node-a", nil},
		{"63 bytes ok", strings.Repeat("a", 63), strings.Repeat("a", 63), nil},
		{"64 bytes rejected", strings.Repeat("a", 64), "", ErrInvalidDomain},
		{"empty", "", "", ErrInvalidDomain},
		{"localhost", "LocalHost", "", ErrInvalidDomain},
		{"leading dash", "-a", "", ErrInvalidDomain},
		{"unicode", "节点", "", ErrInvalidDomain},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Normalize(tt.in)
			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.want, got)
		})
	}
}
```

**（2）`Eventually` + cleanup 替代 50ms sleep（switcher）**

```go
func newTestEnv(t *testing.T, domains ...string) (*Server, []*node.Node) {
	t.Helper()
	s := NewServer("testpswd", quietLogger(), nil) // quietLogger 为 testutil 助手
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	var nodes []*node.Node
	for _, d := range domains {
		pc1, pc2 := packet.Pipe()
		t.Cleanup(func() { _ = pc1.Close() })
		go s.ServeConn(pc2)

		// 服务端默认 KDFArgon2id，客户端不要单独改成 KDFHKDF，否则 PSK 派生不一致。
		lnk, ip, err := link.NewDialer(link.DialConfig{
			PSK: "testpswd", Domain: d,
		}).Dial(linkTransport(pc1))
		require.NoError(t, err)

		n := node.NewFromLink(lnk)
		n.SetDomain(d)
		n.SetIP(ip)
		t.Cleanup(func() { require.NoError(t, n.Close()) })
		go n.Serve()
		nodes = append(nodes, n)
	}
	require.Eventually(t, func() bool {
		return len(s.GetClients()) == len(domains)
	}, 2*time.Second, 5*time.Millisecond, "contexts not registered")
	return s, nodes
}
```

**（3）goleak（所有起 goroutine 的包）**

```go
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
```

注意：现有 `switcher`/`stream` 存在存量泄漏（`pipe_test.go` 自认 6 goroutine/次），必须先修 cleanup，否则 goleak 会直接红。这正是它的价值——把隐性泄漏变成显性失败。

**（4）synctest 替代 backoff 真实等待（node）**

```go
// 示意：实际需要保证 Session 的 connector 不依赖真实网络
func TestSessionBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var at []time.Duration
		start := time.Now()
		s := NewSession(func() (packet.Conn, error) {
			at = append(at, time.Since(start))
			return nil, errors.New("boom")
		}, testSessionConfig())

		go s.Serve()
		s.ensureServing()

		synctest.Wait()                 // 首次失败，进入 1s 退避
		time.Sleep(time.Second)
		synctest.Wait()                 // 第二次失败，进入 2s 退避
		time.Sleep(2 * time.Second)
		synctest.Wait()

		require.Equal(t, []time.Duration{0, time.Second, 3 * time.Second}, at)
		s.Close()
	})
}
```

> 说明：`synctest` 的 `T.Run/T.Parallel/T.Deadline` 在 bubble 内不可用；涉及真实 socket/`net.Pipe` 的 I/O 不保证“durably blocked”，这类用例继续用 `Eventually`。

**（5）原生 fuzz（wire / framing / domain）**

```go
// package wire_test（黑盒 fuzz；同包时去掉 wire. 前缀）
func FuzzDecodeHeader(f *testing.F) {
	f.Add([]byte{0x00, 0x00, 0x50, 0x00, 0x01, 0x03, 0xe8, 0x40, 0x00, 0x00, 0x00})
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) < wire.HeaderSize {
			t.Skip()
		}
		h, err := (wire.Codec{}).DecodeHeader(b[:wire.HeaderSize])
		if err != nil {
			return
		}
		var out [wire.HeaderSize]byte
		require.NoError(t, (wire.Codec{}).EncodeHeader(h, out[:]))
		got, err := (wire.Codec{}).DecodeHeader(out[:])
		require.NoError(t, err)
		require.Equal(t, h, got)
	})
}
```

**（6）benchstat 门禁（nightly）**

```yaml
- name: Benchmarks
  run: |
    go test -run '^$' -bench . -benchmem -count=6 ./link ./packet ./stream > new.txt
    benchstat old.txt new.txt
```

`old.txt` 随版本提交（或从 main 生成），阈值建议：`ns/op` 回退 >10% 或 `allocs/op` 增加即失败；公平性/端到端指标单独 job，不阻塞每个 PR。

**（7）当前 flake 的最小修复示例（router）**

```go
// 原：pbuf := packet.NewBuffer()  // Cmd=0 == CmdAdmit，握手类
pbuf := packet.NewBufferWithCmd(packet.CmdPushStreamData)
pbuf.SetHeader(packet.CmdPushStreamData, 100, 100, node1.GetIP(), 1000)
require.NoError(t, node1.WriteBuffer(pbuf))
```

更推荐直接在 `switcher` 包内用合法 `wire.Frame` 调 `s.router.forward(f)`，验证“未知 IP 被丢弃且 frame 被 Release”，而不是通过 Session 的异步写来间接验证。

---

## 六、改进路线图

### P0：先让 CI 稳定可信（1–2 天）

1. 修 `TestRouterPingDomain`：使用合法 Cmd + 正确 SrcIP；或改为直接调 `router.forward`。
2. 修公平性用例：预热 barrier、同一裁剪序列计算 `maxRun`/Jain、`-count=5` 取中位数、`-short` 跳过。
3. 修 Churn：close-ack 超时可注入（测试 5s），`-race` 下降低并发；错误带 worker/耗时。
4. CI：`go-version-file: go.mod`、`-count=1 -shuffle=on`、拆分 unit/integration、加 `-timeout`。
5. 补 `domain` + `sched` 缺失分支 + `framing` TCP 长度校验的测试。

**验收**：全量连续 10 次绿；`-race` 连续 3 次绿；PR wall time 下降 30% 以上。

### P1：统一测试基础设施（3–5 天）

6. 新增 `internal/testutil`：`newPipe(t)`、`waitReady(t, …)`、`eventually`、`quietLogger`、`freePort`。
7. 引入 goleak `TestMain`，先修存量泄漏（stream Pipe、switcher 环境）。
8. 断言规范化：`require` 前置、`assert` 多检查、禁止 goroutine 内 fatal；删除 `sl, _ :=` 吞错。
9. 用 synctest 替换 node/stream 的时间等待；其余 sleep 改 `Eventually`。
10. 拆分超大文件；给纯单元测试加 `t.Parallel`；按包设 80% 覆盖率下限。

**验收**：`time.Sleep` 降到 <10 处；node 包 <15s；覆盖率不低于 85%；flake 率 <1%。

### P2：建立性能与健壮性门禁（3–5 天）

11. 补 L1/L2/L3 基准与 benchstat 基线；nightly 运行，PR 只做 `-benchtime=1x` 冒烟。
12. 加 4 个 fuzz target（wire codec、framing TCP/WS、domain、presence decode），CI 30s 冒烟 + nightly 长跑。
13. 新增 `TESTING.md`：约定、fixture 用法、flake 排查手册、性能基线更新流程。
14. 清理文档中的过时基准数据/失效链接，保留可复现命令与环境信息。

---

## 七、附录：复现命令与原始数据

```bash
# 覆盖率（按包）
go test -count=1 -coverprofile=/tmp/flex-cover.out -covermode=atomic ./...
go tool cover -func=/tmp/flex-cover.out | tail -1
# total: 82.8%

# 竞态
go test -count=1 -race ./...

# 公平性重复
go test ./test/integration -run TestIntegration_SchedulingFairness -count=5 -v

# Router 重复
go test ./switcher -run '^TestRouterPingDomain$' -count=100 -v

# Churn 重复
go test ./test/integration -run 'TestIntegration_Default/Churn' -count=10 -v

# 跨包覆盖（集成套件）
go test -count=1 -coverpkg=./... -coverprofile=/tmp/integ.out ./test/integration
go tool cover -func=/tmp/integ.out | tail -1
# total: 49.0%（集成套件单独触达）
```

**全量运行记录（本机）**

| # | 命令 | 结果 | 失败用例 |
|---|---|---|---|
| 1 | `go test -cover ./...` | 绿 | — |
| 2 | `go test -coverprofile ./...` | 红 | Router、Fairness |
| 3 | `go test -race ./...` | 红 | Churn |
| 4 | `go test ./...` | 红 | Router、Churn |
| 5 | `go test ./...` | 绿 | — |
| 6 | `time go test ./...` | 红（30.5s） | Churn |

**最慢的 10 个用例（非 race）**

| 耗时 | 用例 |
|---:|---|
| 10.00s | `node.TestConnectHandshakeFailed` |
| 8.22s | `stream.TestWriteHighConcurrencyWithBackpressure` |
| 3.24s | `switcher.TestPresenceDomainReplace` |
| 2.41s | `node.TestStreamList` |
| 2.02s | `node.TestNodeCloseStream` |
| 2.02s | `node.TestHandleCmdOpenStream_BacklogFullReturnsAckError` |
| 2.02s | `node.TestAttachStream_OnDetachReleasesPortAndRecordsState` |
| 2.00s | `node.TestTraceStreamClosedOnNodeClose` |
| 2.00s | `node.TestNodeCloseStreamsReasonNode` |
| 1.16s | `switcher.TestContextPing` |

**Argon2id 派生成本（本机，供测试耗时归因参考）**

```text
BenchmarkArgon2idBootKey-10   3   31,784,083 ns/op   67,122,642 B/op   98 allocs/op
```

即默认 KDF 每次派生约 31.8ms / 67MB；测试若大量创建 acceptor/dialer 会叠加，但它不是 node 包 30s 的主因（主因是 10s 握手 + 多个 2s close-ack 等待）。

**评估局限**
- 失败率是本机单点观测，会随机型/负载变化；但文中列出的根因是确定性的设计问题，换机器只会改变概率，不会改变性质。
- 覆盖率是“按包”口径，跨包行为被低估；例如 `link/packet_transport` 被 switcher 使用，但 link 自身测试未覆盖。
- 未对 Windows/低核数 CI 做验证；部分超时阈值在 2 核机器上可能更紧。

---

*本报告只做评估，未修改任何测试或产品代码。*
