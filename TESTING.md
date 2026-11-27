# 测试规范（TESTING）

本文件约定 flex 的测试分层、断言风格、资源清理、时间控制和 CI 门禁。新增测试默认遵循本文；例外需在 PR 描述中说明。

## 1. 分层与命令

| 层级 | 范围 | 命令 | CI |
|---|---|---|---|
| 单元 | 各包内部逻辑、协议解析、错误分支 | `go test -race -count=1 ./...` | 每个 PR |
| 集成 | `test/integration`，真实 TCP + 双 Session | `go test -race -count=1 ./test/integration` | 每个 PR |
| 稳定性 | 高并发、churn、公平性 | `go test -race -count=10 ./test/integration` | nightly |
| 覆盖率 | 全仓语句覆盖率，门槛 95% | `./coverage_test.sh` | 每个 PR |
| 基准 | `link`/`packet`/`stream` 热路径 | `go test -run '^$' -bench . -benchmem ./...` | nightly / 手动 |
| Fuzz | wire/framing/domain/presence 解析 | `go test -run '^$' -fuzz=Fuzz -fuzztime=30s ./...` | nightly / 手动 |

本地建议设置仓库内缓存，避免写用户目录：

```bash
export GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache
go test -race -count=1 ./...
```

## 2. 断言风格

- 统一使用 `github.com/stretchr/testify`。
- **前置条件/必须成功**用 `require`；失败后测试无法继续。
- **多个独立检查**用 `assert`；一个失败不影响后续断言。
- 错误断言用 `require.ErrorIs` / `require.ErrorAs`，不要只判 `err != nil`。
- 结构体/快照比较优先用 `require.Equal`；字段多时再考虑 `go-cmp`。
- 禁止在子 goroutine 中调用 `require` / `t.Fatal` / `t.FailNow`：用 `errCh` 收集后回到测试 goroutine 断言。

```go
errCh := make(chan error, 1)
go func() { errCh <- doWork() }()
require.NoError(t, <-errCh)
```

## 3. 资源与隔离

- 每个 server、node、listener、pipe、临时文件都用 `t.Cleanup` 注册关闭；测试之间不得共享可变全局状态。
- 不允许出现“为覆盖率”调用 `Close()` 后再 `Serve()` 之类的非法生命周期；覆盖率不能以错误语义为代价。
- 需要 goroutine 泄漏检查时，优先在包级 `TestMain` 中使用 `go.uber.org/goleak`（引入前先清理存量泄漏）。
- 真实端口一律用 `127.0.0.1:0`；虚拟端口用独立常量，避免跨测试复用。

## 4. 时间与并发

- **不要用 `time.Sleep` 等状态**。可选方案：
  - `require.Eventually(t, cond, 2*time.Second, 10*time.Millisecond, msg)`
  - channel / `sync.WaitGroup` 显式同步
  - Go 1.25 `testing/synctest`（纯时间逻辑，如 backoff、心跳、deadline、ACK flush）
- 测试超时要留余量，不要贴着产品默认值；需要时可注入更短/更长的超时。
- 需要并发写同一资源时，明确断言最终状态（总量、顺序、幂等），不要依赖 goroutine 调度顺序。
- 随机数据固定种子；不要用 `time.Now().UnixNano()` 作为唯一随机源。

## 5. 表驱动与命名

- 校验/解析/错误矩阵用表驱动 + `t.Run`；子测试名描述场景，不写编号。
- 顶层测试名使用 `TestXxx`；避免 `Test_parseAddress`、拼写错误、`TestCover*` 这类命名。
- 每个测试只验证一个行为；一个测试里超过 ~80 行时考虑拆分。

```go
func TestNormalize(t *testing.T) {
    tests := []struct{ name, in, want string; wantErr error }{
        {"empty", "", "", ErrInvalidDomain},
        {"63 bytes", strings.Repeat("a", 63), strings.Repeat("a", 63), nil},
        {"64 bytes", strings.Repeat("a", 64), "", ErrInvalidDomain},
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

## 6. 覆盖率

- 总语句覆盖率门槛：**95%**（`./coverage_test.sh`，不含 `examples/`；examples 由 `scripts/check-docs.sh` 编译并运行 Example）。
- 新增/修改代码的分支应被覆盖；错误分支、关闭路径、超时路径与 happy path 同等重要。
- 不接受通过删除断言、跳过测试、`t.Skip` 或空调用刷覆盖率。
- 覆盖率按包统计；跨包调用（如 switcher 使用 link）需在对应包内补直接测试，或使用 `-coverpkg=./...` 做系统级观察。

## 7. Flake 处理

发现 flake 时按以下顺序处理，禁止直接重试或放宽阈值：

1. 连续运行 `-count=50` 复现，记录失败率与失败形态。
2. 判断是测试数据错误、同步错误、超时过紧，还是产品竞态。
3. 用 channel/`Eventually`/`synctest` 消除 sleep；用 `t.Cleanup` 消除泄漏。
4. 修复后至少 `-count=10`（必要时 `-race -count=3`）验证，并记录在 PR 中。
5. 若确认为产品缺陷，先加最小回归测试，再修产品代码。

## 8. CI 门禁

- Go 版本以 `go.mod` 为准（`go-version-file: go.mod`）。
- 单元与集成：`go test -count=1 -race -shuffle=on -timeout 5m ./...`。
- 覆盖率：`COVERAGE_MIN=95 ./coverage_test.sh`。
- 格式：`gofmt -l .` 必须为空；`go vet ./...` 必须通过。
- 文档/Example：`./scripts/check-docs.sh`。
- 性能与 fuzz 不阻塞每个 PR，由 nightly 或手动 job 运行；阈值使用相对回归（benchstat）。
