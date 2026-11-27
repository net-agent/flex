# FLEX 测试整改与复评报告

> 日期：2026-10-26
> 对象：`refactor/wire-link` 工作区（基线 commit `299cca7`）
> 环境：macOS arm64（Apple M4），Go 1.25.1
> 关联基线评估：[test-assessment-zh.md](test-assessment-zh.md)
> 复现：`./scripts/verify.sh`

---

## 一、结论

**整改后总评：4.8 / 5。**

| 维度 | 基线 | 整改后 | 依据 |
|---|---:|---:|---|
| 功能覆盖完整性 | 82.7% | **97.5%** | 全仓语句覆盖率（不含 examples），全部被测包 ≥95% |
| 边界条件覆盖 | 参差 | **4.9/5** | domain/framing/packet/stream/wire 边界矩阵；新增 236 个测试函数 |
| 稳定性 | 6 次全量 4 次红 | **4.9/5** | 全量 `-count=3` 绿；`-race -shuffle` 绿且 0 DATA RACE；Churn 10 连跑绿 |
| 可维护性 | 3.2/5 | **4.7/5** | 985 处 testify 断言、235 个 `t.Run`、TESTING.md；45 处历史 sleep 仍待清理 |
| 性能测试 | 2.0/5 | **4.4/5** | 12 个 benchmark、fuzz/基准入口与 CI 门禁；stream/node 基准仍不足 |
| 框架/基础设施 | 3.0/5 | **4.9/5** | testify 统一、synctest、fuzz、覆盖率门槛、verify 脚本、CI 对齐 |

加权：功能 25% × 4.9 + 边界 20% × 4.9 + 稳定 25% × 4.9 + 可维护 15% × 4.7 + 性能 10% × 4.4 + 框架 5% × 4.9 ≈ **4.82 → 4.8**。

### 关键数字

- 测试函数：**426 → 662**；测试文件：**60 → 99**
- 语句覆盖率：**82.7% → 97.5%**（`COVERAGE_MIN=95 ./coverage_test.sh` 通过）
- `require` 断言：**95 → 985**；`t.Run` 子测试：**67 → 235**
- fuzz target：**0 → 2**（`FuzzNormalize`、`FuzzDecodeHeaderRoundTrip`）
- `time.Sleep`：46 → 45（新增用例 0 处；存量替换为 synctest/channel/Eventually 的改造已开始）
- `-race`：**0 DATA RACE**；全量 race + shuffle 绿
- 全量测试：`go test -count=3 ./...` 绿；`go test -race -count=3 ./...` 绿

---

## 二、覆盖率前后对比

| 包 | 基线 | 整改后 |
|---|---:|---:|
| `internal/domain` | 0% | **100%** |
| `internal/framing` | 0% | **100%** |
| `internal/idpool` | 95.3% | **100%** |
| `internal/pending` | 88.9% | **100%** |
| `internal/sched` | 48.3% | **100%** |
| `packet` | 91.2% | **100%** |
| `packet/ws` | 77.0% | **98.9%** |
| `wire` | 71.5% | **100%** |
| `link` | 69.7% | **95.0%** |
| `stream` | 90.2% | **95.5%** |
| `node` | 85.5% | **98.3%** |
| `switcher` | 88.1% | **98.0%** |
| **总计（不含 examples）** | **82.7%** | **97.5%** |

> `examples/*` 是 `package main` 的独立命令，由 `scripts/check-docs.sh` 的编译与 Example 测试覆盖；`coverage_test.sh` 明确排除它们，避免用不可执行的 CLI `main` 稀释库覆盖率。

---

## 三、消除的缺陷

### 3.1 产品缺陷（已修复并加回归测试）

1. **公平调度关闭队列丢帧** — `link/fair.go`
   `processStream` 在 `q.closed` 时无条件删除队列，若 Close/AckClose 已在队列中或刚到，会被丢弃，导致对端 `Close` 等待 2s 超时。修复为“队列为空才删除”。回归：`TestFairWriterDoesNotDropFramesQueuedBehindClosedStream`。
2. **晚到 Close 不回 Ack** — `node/streamhub.go`
   流已 detach 时 frame-native `handleClose` 直接丢弃 Close，对端一直等到 close-ack 超时；现在与 legacy 路径一致，回 `AckClose`。回归：`TestStreamHubLateCloseAfterDetachRepliesAckClose`。
3. **ping ACK 关闭竞态 panic** — `switcher/context.go`
   `ping` 超时先 `Delete` 再 `close(ch)`，`deliverPingResponse` 可能已 Load 到 ch 并向已关闭 channel 发送，panic 发生在 dispatcher goroutine 且不被 `ServeConn` recover 覆盖。改为 `pingWaiter` 互斥 + closed 标记，close 时排空未消费帧。回归：`TestPingWaiterDeliverCloseRace`（1000 轮并发）。
4. **packet batch nil panic** — `packet/writer.go`
   `WriteBufferBatch` 遇到 nil 元素直接解引用 `buf.Head`；现在与单写一致，跳过 nil。
5. **`PutBuffer(nil)` panic** — `packet/buffer.go`
   增加 nil 保护。
6. **WS 写 flush 错误被吞** — `packet/ws/writer.go`
   `defer nw.Close()` 丢弃 flush 错误；现在返回首错，关闭连接后的第一次写即可报错。
7. **visitLimit 零值除零风险** — `internal/sched/config.go`
   先 `normalized()` 再计算，并删除 normalize 后不可达的 `n<1` 分支。
8. **`Session.SetFrameTap(nil)` 语义不对称** — `node/session.go`
   nil 现在会立即从存活 Node 上摘除 tap，而不只影响下一次重连。
9. **legacy stream 端点池化缓冲区被接收方复用** — `stream/endpoint.go`
   `legacyEndpoint.SendFrame` 把池化 `packet.Buffer` 交还给 `packet.Pipe` 后立即 Put，接收方仍可能在拷贝同一 backing array（`-race` 下 `TestConcurrentProducerConsumer` 报 DATA RACE）。兼容路径改为每次分配独立 buffer，杜绝所有权歧义；node 热路径不受影响。
10. **`stream.Pipe` demux 与路由 goroutine 共享池化 buffer** — `stream/pipe.go`
    `demuxPackets` 把 `ReadBuffer` 得到的池化 buffer 直接交给 `routeCmd/routeAck`，下一次 `ReadBuffer` 可能复用同一 backing array（`-race` 下 `TestStateCountersAbsoluteValues` 报 DATA RACE）。现在先复制为私有 buffer，再交所有权给路由 goroutine。
11. **CloseAck 在同步关闭时可能晚于 detach** — `stream/handler.go`
    `HandleClose` 原先在 `closeRead/CloseWrite` 之后才发送 AckClose；对端若已先 `Close()`，`closeRead` 会立即触发 detach，Pipe 风格的回调可能在 ack 写出前关闭底层连接，导致双方都等到 close-ack 超时。现在先发 AckClose，再做读/写关闭。
12. **`stream.Pipe` 泄漏 demux/route goroutine** — `stream/pipe.go`
    底层 `packet.Pipe` 从不关闭，测试结束后 demux 仍可能从池里读数据，与下一用例竞争。现在两端都 detach 后统一关闭底层 conn，goroutine 自然退出；关闭放在“两端都 detach”之后，避免提前关掉被动端而丢失 CloseAck。

### 3.2 测试缺陷（已修复）

- `TestRouterPingDomain` 用零值 `packet.NewBuffer()`（Cmd=0=CmdAdmit）当数据帧，实际发送握手帧导致链路关闭；改为 `NewBufferWithCmd(CmdPushStreamData)` + 正确 SrcIP。
- 公平性测试改为“每轮所有 stream 各写一包”的 barrier，`maxRun` 与 Jain 使用同一裁剪序列，消除生产者启动顺序造成的 1.0/0.1332 两极抖动。
- Churn 的 2s close-ack 丢失根因由 fair writer 修复解决；原始 12 workers/8s/64KB 负载连续 10 次通过。
- `TestDirectModeHeartbeatKeepsNodeAlive` 不再修改包级 `DefaultHeartbeatInterval`，改用 `NewWithOptions` 注入，消除跨测试 data race。
- fake presence switcher 不再在 goroutine 里 `t.Fatalf`；网络关闭/重连期间的 `link.ErrWriterClosed` 视为预期 teardown。
- packet 测试中所有 goroutine 内 `require` 改为 errCh 回收后断言。
- `TestServerRun` 改用 `127.0.0.1:0`，不再占用固定端口。
- `TestSessionCloseInterruptsHandshake` 的 1s 断言在高负载 race 下会到 1.4s；断言放宽到 3s（仍远小于 10s 握手超时），保留“Close 能中断握手”的语义。
- 三个重复运行下的假失败：`TestFairWriterProcessStreamEdges` 不再要求关闭后第二次 Send 成功；`TestHandleCmdData/closed` 改为直接构造已关闭流，避免等待异步 Close 传播；`TestWriteSerializesConcurrentCallsByInvocationOrder` 等第一条 Write 真正进入发送路径后再启动第二条，消除调度顺序假失败。
- 删除探针文件、锁定缺陷的测试与“已知缺陷 subprocess”用例。

---

## 四、统一后的测试框架

| 能力 | 选择 | 状态 |
|---|---|---|
| 断言 | `testify/require` + `assert` | 985 处 require；新测试统一 |
| 异步等待 | `require.Eventually` / channel | 新测试无 sleep |
| 假时钟 | `testing/synctest`（Go 1.25） | backoff、pending、handshake 等 |
| 泄漏检查 | 现有 NumGoroutine 检查 + 显式 cleanup | 未引入新依赖 |
| 解析健壮性 | 原生 `testing.F` fuzz | 2 个 target，3s 冒烟通过 |
| 覆盖率门禁 | `COVERAGE_MIN=95 ./coverage_test.sh` | 通过（97.5%） |
| CI | Go 版本对齐、`-count=1 -shuffle=on`、覆盖率门槛 | 已更新 |
| 规范 | [TESTING.md](../TESTING.md) | 已新增 |

未引入 Ginkgo/Gomega、testify/suite 全仓推广：它们会增加 DSL/串行 fixture 成本，与本仓库表格驱动 + 并发测试的风格不匹配。

---

## 五、验证记录（本机）

```bash
export GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache

# 1) 全量重复
go test -count=3 -timeout 900s ./...              # PASS
# 2) 全量 race + shuffle
go test -count=1 -race -shuffle=on -timeout 900s ./...  # PASS，0 DATA RACE
go test -count=3 -race -timeout 1200s ./...       # PASS
# 3) 覆盖率门槛
COVERAGE_MIN=95 ./coverage_test.sh                # total 97.5%
# 4) 定向稳定性
go test ./test/integration -run 'TestIntegration_Default/Churn' -count=10   # PASS
go test -race ./test/integration -run 'TestIntegration_Default' -count=3    # PASS
# 5) 格式/静态检查
gofmt -l .（排除缓存）                             # 空
go vet ./...                                      # PASS
./scripts/check-docs.sh                           # PASS
```

| 检查 | 结果 |
|---|---|
| `coverage_test.sh`（阈值 95） | **97.5%**，通过 |
| `go test -race -shuffle=on ./...` | 通过，**0 DATA RACE** |
| `go test -count=3 ./...` | 通过 |
| `go test -race -count=3 ./...` | 通过 |
| `gofmt` / `go vet` | 通过 |
| `check-docs.sh` | 通过 |

---

## 六、仍存在的限制

1. **link 95.0% / stream 95.5%**：剩余语句多为不可达防御分支（`crypto/rand` 熵源失败、固定长度构造错误、`CanInterruptWrite()==false` 的 interrupter 协程体），以及需要生产 hook 才能稳定命中的 CAS 双检分支。继续提升需要重构测试注入点，收益低于风险。
2. **性能基准仍偏少**：12 个 benchmark 集中在 packet/link；stream/node/switcher 尚无路径级基准。CI 已有覆盖率门禁，但 benchmark 仍建议 nightly + benchstat，而不是阻塞每个 PR。
3. **45 处历史 `time.Sleep`**：本次新增用例 0 处；存量集中在 `stream` 的 deadline/stress 测试与 `node/session_test.go`。已建立 TESTING.md 约束，后续按模块逐步替换为 synctest/Eventually。
4. **examples 不计入语句覆盖率**：由 `check-docs.sh` 编译 + Example 验证；若需 CLI 端到端覆盖，需单独引入 testscript 类工具。
5. **测试评分的主观性**：4.8 是按上述六维加权的结果；其中性能测试 4.4 是保守估计，若补齐路径级 benchmark 可到 4.7+。
