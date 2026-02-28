// integration/scheduling_fairness_test.go
//
// 这个文件是“调度公平性”专项集成测试（TDD RED 用例），目标是验证：
// 在复用同一条 TCP 物理连接时，多条虚拟 Stream 的数据包是否被“均匀、公平”地发送。
//
// 为什么在 integration 层做这个测试：
// 1. 单元测试只能验证调度器局部逻辑，无法覆盖 Session/Node/Switcher/Stream 的真实联动路径。
// 2. 本测试在真实端到端链路上发压，记录发送侧写包顺序，用结果评估调度是否公平。
// 3. 当前测试明确“不接入 FairConn”，用于先固化验收标准，再驱动后续实现。
//
// 测试设计：
// 1. 建立 sender/receiver 两个 Session，共享同一个 switcher。
// 2. sender 在同一条物理连接上并发建立 N 条 stream（N 由测试参数决定）。
// 3. 每条 stream 发送相同数量、相同大小的数据包，排除业务负载差异干扰。
// 4. 在 sender 侧包装 recordingPacketConn，拦截 WriteBuffer，记录每个包的 SID 与命令类型。
// 5. receiver 侧仅做 io.Copy(io.Discard) 排空，确保写压主要反映发送调度行为。
//
// 公平性指标：
// 1. 包量一致性：每个 SID 的 CmdPushStreamData 包数必须一致（基础正确性）。
// 2. 窗口 Jain 指数（局部公平）：按固定窗口切片计算 Jain Index，关注 min/avg，越接近 1 越公平。
// 3. 最大连续同 SID 包长（局部突发）：限制单 stream 连续霸占发送机会的长度，越小越公平。
//
// 测试参数与报告导出：
// - 参数封装在 fairnessTestProfile，可统一管理阈值与负载规模。
// - 支持可选导出可视化报告（HTML），用于直观查看：
//  1. 总览卡片（阈值/结果/通过状态）
//  2. 各窗口 Jain 指数条形图
//  3. 各 SID 包数分布
//  4. 前若干数据包的 SID 时间线色块
//     - 启用方式：
//     FLEX_FAIRNESS_REPORT=1 go test -run TestIntegration_SchedulingFairness ./examples/integration
//     - 可选路径：
//     FLEX_FAIRNESS_REPORT_PATH=/abs/or/rel/path/report.html
package integration

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/node"
	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/switcher"
	"github.com/stretchr/testify/require"
)

type fairnessTestProfile struct {
	Port             uint16
	StreamCount      int
	PacketsPerStream int
	PayloadSize      int
	WindowSize       int
	MinWindowJain    float64
	MaxRunLength     int

	WaitReadyTimeout time.Duration
	DrainTimeout     time.Duration

	ExportReport      bool
	ReportPath        string
	ReportPreviewSize int
}

func defaultFairnessTestProfile() fairnessTestProfile {
	p := fairnessTestProfile{
		Port:             22334,
		StreamCount:      12,
		PacketsPerStream: 192,
		PayloadSize:      1024,
		MinWindowJain:    0.93,
		MaxRunLength:     24,
		WaitReadyTimeout: 5 * time.Second,
		DrainTimeout:     15 * time.Second,

		ExportReport:      envBool("FLEX_FAIRNESS_REPORT", true),
		ReportPath:        "../../docs/scheduling_fairness_integration_report.html",
		ReportPreviewSize: 360,
	}
	p.WindowSize = p.StreamCount * 16

	if v := strings.TrimSpace(os.Getenv("FLEX_FAIRNESS_REPORT_PATH")); v != "" {
		p.ReportPath = v
	}
	if v := strings.TrimSpace(os.Getenv("FLEX_FAIRNESS_REPORT_PREVIEW")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			p.ReportPreviewSize = n
		}
	}
	return p
}

type packetWriteEvent struct {
	Cmd  byte
	SID  uint64
	Time time.Time
}

type recordingPacketConn struct {
	packet.Conn
	mu     sync.Mutex
	events []packetWriteEvent
}

func (c *recordingPacketConn) WriteBuffer(buf *packet.Buffer) error {
	c.mu.Lock()
	c.events = append(c.events, packetWriteEvent{
		Cmd:  buf.Cmd(),
		SID:  buf.SID(),
		Time: time.Now(),
	})
	c.mu.Unlock()
	return c.Conn.WriteBuffer(buf)
}

func (c *recordingPacketConn) Snapshot() []packetWriteEvent {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]packetWriteEvent, len(c.events))
	copy(out, c.events)
	return out
}

type recorderHolder struct {
	mu   sync.Mutex
	conn *recordingPacketConn
}

func (h *recorderHolder) Set(c *recordingPacketConn) {
	h.mu.Lock()
	h.conn = c
	h.mu.Unlock()
}

func (h *recorderHolder) Get() *recordingPacketConn {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.conn
}

func recordingTCPConnector(addr string, holder *recorderHolder) node.ConnectFunc {
	return func() (packet.Conn, error) {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		rec := &recordingPacketConn{Conn: packet.NewWithConn(conn)}
		holder.Set(rec)
		return rec, nil
	}
}

type drainCollector struct {
	done chan struct{}

	mu    sync.Mutex
	bytes int64
	errs  []error
}

func startDrainCollector(ln net.Listener, expectedConn int) *drainCollector {
	dc := &drainCollector{done: make(chan struct{})}
	go func() {
		defer close(dc.done)

		var wg sync.WaitGroup
		for i := 0; i < expectedConn; i++ {
			conn, err := ln.Accept()
			if err != nil {
				dc.appendErr(fmt.Errorf("accept #%d failed: %w", i+1, err))
				return
			}

			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer c.Close()

				n, err := io.Copy(io.Discard, c)
				dc.appendBytes(n)
				if err != nil && !isConnEOF(err) {
					dc.appendErr(fmt.Errorf("drain stream failed: %w", err))
				}
			}(conn)
		}

		wg.Wait()
	}()
	return dc
}

func (dc *drainCollector) appendBytes(n int64) {
	dc.mu.Lock()
	dc.bytes += n
	dc.mu.Unlock()
}

func (dc *drainCollector) appendErr(err error) {
	dc.mu.Lock()
	dc.errs = append(dc.errs, err)
	dc.mu.Unlock()
}

func (dc *drainCollector) Bytes() int64 {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	return dc.bytes
}

func (dc *drainCollector) Errors() []error {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	out := make([]error, len(dc.errs))
	copy(out, dc.errs)
	return out
}

type fairnessMetrics struct {
	TotalDataPackets int
	PacketCountBySID map[uint64]int

	MaxRunLength  int
	WindowJains   []float64
	MinWindowJain float64
	AvgWindowJain float64
}

func analyzeFairness(events []packetWriteEvent, windowSize int) fairnessMetrics {
	seq := make([]uint64, 0, len(events))
	for _, ev := range events {
		if ev.Cmd == packet.CmdPushStreamData {
			seq = append(seq, ev.SID)
		}
	}

	packetCountBySID := make(map[uint64]int)
	for _, sid := range seq {
		packetCountBySID[sid]++
	}

	metrics := fairnessMetrics{
		TotalDataPackets: len(seq),
		PacketCountBySID: packetCountBySID,
		MaxRunLength:     maxRunLength(seq),
	}

	windowJains := windowJainIndexes(trimMiddle(seq), windowSize)
	metrics.WindowJains = windowJains
	metrics.MinWindowJain = minFloat64(windowJains)
	metrics.AvgWindowJain = avgFloat64(windowJains)

	return metrics
}

func trimMiddle(seq []uint64) []uint64 {
	if len(seq) < 100 {
		return seq
	}
	cut := len(seq) / 10
	return seq[cut : len(seq)-cut]
}

func maxRunLength(seq []uint64) int {
	if len(seq) == 0 {
		return 0
	}

	maxRun := 1
	run := 1
	last := seq[0]

	for _, sid := range seq[1:] {
		if sid == last {
			run++
			if run > maxRun {
				maxRun = run
			}
			continue
		}
		run = 1
		last = sid
	}

	return maxRun
}

func windowJainIndexes(seq []uint64, windowSize int) []float64 {
	if len(seq) == 0 {
		return nil
	}
	if windowSize <= 0 {
		windowSize = len(seq)
	}

	sidSet := make(map[uint64]struct{})
	for _, sid := range seq {
		sidSet[sid] = struct{}{}
	}

	sids := make([]uint64, 0, len(sidSet))
	for sid := range sidSet {
		sids = append(sids, sid)
	}
	slices.Sort(sids)

	index := make(map[uint64]int, len(sids))
	for i, sid := range sids {
		index[sid] = i
	}

	if windowSize < len(sids) {
		windowSize = len(sids)
	}

	windows := make([]float64, 0, len(seq)/windowSize+1)
	for start := 0; start+windowSize <= len(seq); start += windowSize {
		counts := make([]int, len(sids))
		for _, sid := range seq[start : start+windowSize] {
			counts[index[sid]]++
		}
		windows = append(windows, jainIndex(counts))
	}

	if len(windows) == 0 {
		counts := make([]int, len(sids))
		for _, sid := range seq {
			counts[index[sid]]++
		}
		windows = append(windows, jainIndex(counts))
	}

	return windows
}

func jainIndex(counts []int) float64 {
	if len(counts) == 0 {
		return 0
	}

	var sum float64
	var sumSq float64
	for _, c := range counts {
		x := float64(c)
		sum += x
		sumSq += x * x
	}

	if sumSq == 0 {
		return 0
	}
	return (sum * sum) / (float64(len(counts)) * sumSq)
}

func minFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	minVal := values[0]
	for _, v := range values[1:] {
		if v < minVal {
			minVal = v
		}
	}
	return minVal
}

func avgFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

// TDD (RED): 定义“同一 TCP 连接上多 stream 公平调度”的验收标准。
// 当前不接入 FairConn，本测试用于先固化期望行为，再驱动后续实现。
func TestIntegration_SchedulingFairness(t *testing.T) {
	password := "test-password"
	cfg := defaultFairnessTestProfile()

	logCfg := &switcher.LogConfig{
		Server:   slog.LevelWarn,
		Registry: slog.LevelWarn,
		Router:   slog.LevelWarn,
		Context:  slog.LevelWarn,
	}
	srv := switcher.NewServer(password, nil, logCfg)
	defer srv.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	go srv.Serve(ln)

	var recorder recorderHolder
	sender := node.NewSession(recordingTCPConnector(addr, &recorder), node.SessionConfig{
		Domain:   "sender",
		Password: password,
	})
	defer sender.Close()
	go sender.Serve()

	receiver := node.NewSession(tcpConnector(addr), node.SessionConfig{
		Domain:   "receiver",
		Password: password,
	})
	defer receiver.Close()
	go receiver.Serve()

	recvLn, err := receiver.Listen(cfg.Port)
	require.NoError(t, err)
	defer recvLn.Close()

	// Session 是懒连接，sender 需要先触发一次 Listen/Dial 才会开始建连。
	senderTriggerLn, err := sender.Listen(cfg.Port + 1)
	require.NoError(t, err)
	defer senderTriggerLn.Close()

	drain := startDrainCollector(recvLn, cfg.StreamCount)

	require.NoError(t, sender.WaitReady(cfg.WaitReadyTimeout))
	require.NoError(t, receiver.WaitReady(cfg.WaitReadyTimeout))
	require.NotNil(t, recorder.Get(), "recorder not initialized")

	streams := make([]net.Conn, 0, cfg.StreamCount)
	for i := 0; i < cfg.StreamCount; i++ {
		st, err := sender.Dial(fmt.Sprintf("receiver:%d", cfg.Port))
		require.NoError(t, err)
		streams = append(streams, st)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(len(streams))
	for i, conn := range streams {
		go func(idx int, c net.Conn) {
			defer wg.Done()
			defer c.Close()

			payload := make([]byte, cfg.PayloadSize)
			for j := range payload {
				payload[j] = byte((idx + j) % 251)
			}

			<-start
			for n := 0; n < cfg.PacketsPerStream; n++ {
				written, err := c.Write(payload)
				if err != nil {
					t.Errorf("stream[%d] write failed: %v", idx, err)
					return
				}
				if written != len(payload) {
					t.Errorf("stream[%d] short write: got=%d want=%d", idx, written, len(payload))
					return
				}
			}
		}(i, conn)
	}

	close(start)
	wg.Wait()

	select {
	case <-drain.done:
	case <-time.After(cfg.DrainTimeout):
		t.Fatalf("timeout waiting receiver drain to finish")
	}

	for _, derr := range drain.Errors() {
		require.NoError(t, derr)
	}

	expectedBytes := int64(cfg.StreamCount * cfg.PacketsPerStream * cfg.PayloadSize)
	require.Equal(t, expectedBytes, drain.Bytes(), "receiver drained bytes mismatch")

	events := recorder.Get().Snapshot()
	metrics := analyzeFairness(events, cfg.WindowSize)
	maybeExportFairnessReport(t, cfg, metrics, events)

	require.Equal(t, cfg.StreamCount*cfg.PacketsPerStream, metrics.TotalDataPackets, "total stream-data packet count mismatch")
	require.Len(t, metrics.PacketCountBySID, cfg.StreamCount, "active stream count mismatch")
	for sid, cnt := range metrics.PacketCountBySID {
		require.Equal(t, cfg.PacketsPerStream, cnt, "sid=%d packet count mismatch", sid)
	}

	t.Logf("fairness metrics: streams=%d total=%d maxRun=%d minJain=%.4f avgJain=%.4f windows=%d",
		len(metrics.PacketCountBySID), metrics.TotalDataPackets, metrics.MaxRunLength,
		metrics.MinWindowJain, metrics.AvgWindowJain, len(metrics.WindowJains))

	require.GreaterOrEqual(t, metrics.MinWindowJain, cfg.MinWindowJain,
		"window fairness below target (min Jain)")
	require.LessOrEqual(t, metrics.MaxRunLength, cfg.MaxRunLength,
		"single stream dominates too long (max run length)")
}

func envBool(key string, defaultVal bool) bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if raw == "" {
		return defaultVal
	}
	switch raw {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return defaultVal
	}
}

func maybeExportFairnessReport(t *testing.T, cfg fairnessTestProfile, metrics fairnessMetrics, events []packetWriteEvent) {
	if !cfg.ExportReport {
		return
	}

	reportPath, err := exportFairnessReport(cfg, metrics, events)
	if err != nil {
		t.Errorf("export fairness report failed: %v", err)
		return
	}
	t.Logf("fairness report written to %s", reportPath)
}

type fairnessReportData struct {
	GeneratedAt string
	Profile     fairnessTestProfile
	Metrics     fairnessMetrics

	TotalEvents     int
	PreviewCount    int
	ThresholdJainOK bool
	ThresholdRunOK  bool
	OverallOK       bool

	SIDRows      []fairnessSIDRow
	WindowRows   []fairnessWindowRow
	SequenceRows []fairnessSequenceRow
	TimelineRows []fairnessTimelineRow
	TotalDuration string
}

type fairnessSIDRow struct {
	SID      uint64
	Count    int
	Expected int
	Delta    int
	Percent  float64
	Color    string
}

type fairnessWindowRow struct {
	Index   int
	Jain    float64
	Percent float64
	Pass    bool
}

type fairnessSequenceRow struct {
	Start int
	End   int
	Cells []fairnessSequenceCell
}

type fairnessSequenceCell struct {
	Index int
	SID   uint64
	Color string
}

type fairnessTimelineRow struct {
	SID     uint64
	Color   string
	Markers []fairnessTimelineMarker
}

type fairnessTimelineMarker struct {
	LeftPercent  float64
	WidthPercent float64
}

func exportFairnessReport(cfg fairnessTestProfile, metrics fairnessMetrics, events []packetWriteEvent) (string, error) {
	outPath, err := resolveReportPath(cfg.ReportPath)
	if err != nil {
		return "", err
	}

	dataSeq := extractDataSequence(events)
	sids := make([]uint64, 0, len(metrics.PacketCountBySID))
	maxCount := 1
	for sid, cnt := range metrics.PacketCountBySID {
		sids = append(sids, sid)
		if cnt > maxCount {
			maxCount = cnt
		}
	}
	slices.Sort(sids)

	sidRows := make([]fairnessSIDRow, 0, len(sids))
	for _, sid := range sids {
		cnt := metrics.PacketCountBySID[sid]
		sidRows = append(sidRows, fairnessSIDRow{
			SID:      sid,
			Count:    cnt,
			Expected: cfg.PacketsPerStream,
			Delta:    cnt - cfg.PacketsPerStream,
			Percent:  float64(cnt) * 100 / float64(maxCount),
			Color:    sidColor(sid),
		})
	}

	windowRows := make([]fairnessWindowRow, 0, len(metrics.WindowJains))
	for i, j := range metrics.WindowJains {
		windowRows = append(windowRows, fairnessWindowRow{
			Index:   i + 1,
			Jain:    j,
			Percent: j * 100,
			Pass:    j >= cfg.MinWindowJain,
		})
	}

	previewCount := cfg.ReportPreviewSize
	if previewCount <= 0 || previewCount > len(dataSeq) {
		previewCount = len(dataSeq)
	}
	sequenceRows := buildSequenceRows(dataSeq[:previewCount], 60)
	timelineRows, totalDur := buildTimelineRows(events)

	report := fairnessReportData{
		GeneratedAt: time.Now().Format(time.RFC3339),
		Profile:     cfg,
		Metrics:     metrics,

		TotalEvents:     len(events),
		PreviewCount:    previewCount,
		ThresholdJainOK: metrics.MinWindowJain >= cfg.MinWindowJain,
		ThresholdRunOK:  metrics.MaxRunLength <= cfg.MaxRunLength,
		SIDRows:         sidRows,
		WindowRows:      windowRows,
		SequenceRows:    sequenceRows,
		TimelineRows:    timelineRows,
		TotalDuration:   totalDur.String(),
	}
	report.OverallOK = report.ThresholdJainOK && report.ThresholdRunOK

	tmpl, err := template.New("fairness-report").Funcs(template.FuncMap{
		"safeCSS": func(s string) template.CSS { return template.CSS(s) },
	}).Parse(fairnessReportHTMLTemplate)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, report); err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(outPath, buf.Bytes(), 0o644); err != nil {
		return "", err
	}
	return outPath, nil
}

func resolveReportPath(path string) (string, error) {
	clean := strings.TrimSpace(path)
	if clean == "" {
		clean = "../../docs/scheduling_fairness_integration_report.html"
	}
	if filepath.IsAbs(clean) {
		return clean, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(wd, clean), nil
}

func extractDataSequence(events []packetWriteEvent) []uint64 {
	seq := make([]uint64, 0, len(events))
	for _, ev := range events {
		if ev.Cmd == packet.CmdPushStreamData {
			seq = append(seq, ev.SID)
		}
	}
	return seq
}

func buildSequenceRows(seq []uint64, cols int) []fairnessSequenceRow {
	if cols <= 0 {
		cols = 60
	}
	rows := make([]fairnessSequenceRow, 0, len(seq)/cols+1)
	for start := 0; start < len(seq); start += cols {
		end := start + cols
		if end > len(seq) {
			end = len(seq)
		}
		cells := make([]fairnessSequenceCell, 0, end-start)
		for i := start; i < end; i++ {
			cells = append(cells, fairnessSequenceCell{
				Index: i,
				SID:   seq[i],
				Color: sidColor(seq[i]),
			})
		}
		rows = append(rows, fairnessSequenceRow{
			Start: start,
			End:   end - 1,
			Cells: cells,
		})
	}
	return rows
}

func buildTimelineRows(events []packetWriteEvent) ([]fairnessTimelineRow, time.Duration) {
	dataEvents := make([]packetWriteEvent, 0, len(events))
	for _, ev := range events {
		if ev.Cmd == packet.CmdPushStreamData {
			dataEvents = append(dataEvents, ev)
		}
	}
	if len(dataEvents) == 0 {
		return nil, 0
	}

	tStart := dataEvents[0].Time
	tEnd := dataEvents[len(dataEvents)-1].Time
	totalDur := tEnd.Sub(tStart)
	if totalDur <= 0 {
		totalDur = time.Millisecond
	}

	// group events by SID
	sidEvents := make(map[uint64][]time.Time)
	for _, ev := range dataEvents {
		sidEvents[ev.SID] = append(sidEvents[ev.SID], ev.Time)
	}

	sids := make([]uint64, 0, len(sidEvents))
	for sid := range sidEvents {
		sids = append(sids, sid)
	}
	slices.Sort(sids)

	// merge adjacent events into segments for each SID
	// events within gapThreshold of each other are merged into one segment
	gapThreshold := totalDur / 200
	if gapThreshold < time.Microsecond {
		gapThreshold = time.Microsecond
	}
	minWidth := 0.3 // minimum marker width percent for visibility

	rows := make([]fairnessTimelineRow, 0, len(sids))
	for _, sid := range sids {
		times := sidEvents[sid]
		markers := make([]fairnessTimelineMarker, 0)

		segStart := times[0]
		segEnd := times[0]
		for _, t := range times[1:] {
			if t.Sub(segEnd) <= gapThreshold {
				segEnd = t
			} else {
				left := float64(segStart.Sub(tStart)) / float64(totalDur) * 100
				w := float64(segEnd.Sub(segStart)) / float64(totalDur) * 100
				if w < minWidth {
					w = minWidth
				}
				markers = append(markers, fairnessTimelineMarker{LeftPercent: left, WidthPercent: w})
				segStart = t
				segEnd = t
			}
		}
		// flush last segment
		left := float64(segStart.Sub(tStart)) / float64(totalDur) * 100
		w := float64(segEnd.Sub(segStart)) / float64(totalDur) * 100
		if w < minWidth {
			w = minWidth
		}
		markers = append(markers, fairnessTimelineMarker{LeftPercent: left, WidthPercent: w})

		rows = append(rows, fairnessTimelineRow{
			SID:     sid,
			Color:   sidColor(sid),
			Markers: markers,
		})
	}

	return rows, totalDur
}

func sidColor(sid uint64) string {
	h := int((sid*47 + 29) % 360)
	return fmt.Sprintf("hsl(%d 70%% 45%%)", h)
}

const fairnessReportHTMLTemplate = `<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Scheduling Fairness Report</title>
<style>
  :root {
    --bg: #f4f6f8;
    --card: #ffffff;
    --ink: #1f2937;
    --muted: #5f6b7a;
    --line: #dde3ea;
    --ok: #14804a;
    --bad: #c92828;
    --jain: #2563eb;
    --sid: #0d9488;
  }
  body {
    margin: 0;
    background: linear-gradient(160deg, #f9fbfd, var(--bg));
    color: var(--ink);
    font-family: "SF Pro Text", "PingFang SC", "Segoe UI", sans-serif;
  }
  .page {
    max-width: 1200px;
    margin: 0 auto;
    padding: 20px;
  }
  h1 {
    margin: 0 0 6px;
    font-size: 28px;
  }
  .subtitle {
    margin: 0 0 20px;
    color: var(--muted);
    font-size: 14px;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
    gap: 14px;
  }
  .card {
    background: var(--card);
    border: 1px solid var(--line);
    border-radius: 12px;
    padding: 14px 16px;
    box-shadow: 0 8px 22px rgba(15, 23, 42, 0.05);
    margin-bottom: 14px;
  }
  .card h2 {
    margin: 0 0 10px;
    font-size: 16px;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 13px;
  }
  th, td {
    border-bottom: 1px solid var(--line);
    padding: 6px 8px;
    text-align: left;
    vertical-align: middle;
  }
  th {
    color: var(--muted);
    font-weight: 600;
    background: #f8fafc;
  }
  .status-ok {
    color: var(--ok);
    font-weight: 700;
  }
  .status-bad {
    color: var(--bad);
    font-weight: 700;
  }
  .bar-wrap {
    width: 100%;
    height: 12px;
    border-radius: 999px;
    overflow: hidden;
    background: #edf2f7;
  }
  .bar {
    height: 100%;
    background: linear-gradient(90deg, #60a5fa, var(--jain));
  }
  .sid-bar {
    height: 10px;
    border-radius: 999px;
  }
  .mono {
    font-family: "SF Mono", "Menlo", "Consolas", monospace;
  }
  .seq-row {
    display: grid;
    grid-template-columns: 110px 1fr;
    gap: 10px;
    align-items: center;
    margin-bottom: 6px;
  }
  .seq-range {
    color: var(--muted);
    font-size: 12px;
  }
  .seq-cells {
    display: grid;
    grid-template-columns: repeat(60, minmax(6px, 1fr));
    gap: 2px;
  }
  .seq-cell {
    display: block;
    height: 11px;
    border-radius: 2px;
  }
  .legend {
    margin-top: 8px;
    color: var(--muted);
    font-size: 12px;
  }
  .metric-desc {
    color: var(--muted);
    font-size: 12px;
    line-height: 1.6;
    margin: 0 0 10px;
    padding: 8px 10px;
    background: #f8fafc;
    border-radius: 6px;
    border-left: 3px solid var(--jain);
  }
  .timeline-axis {
    display: flex;
    justify-content: space-between;
    padding: 0 120px 4px 120px;
    color: var(--muted);
    font-size: 11px;
  }
  .timeline-row {
    display: grid;
    grid-template-columns: 110px 1fr;
    gap: 10px;
    align-items: center;
    margin-bottom: 4px;
  }
  .timeline-label {
    color: var(--muted);
    font-size: 11px;
    text-align: right;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .timeline-track {
    position: relative;
    height: 14px;
    background: #edf2f7;
    border-radius: 3px;
    overflow: hidden;
  }
  .timeline-marker {
    position: absolute;
    top: 0;
    height: 100%;
    border-radius: 2px;
    opacity: 0.85;
  }
</style>
</head>
<body>
<div class="page">
  <h1>Scheduling Fairness Report</h1>
  <p class="subtitle">Generated at {{.GeneratedAt}} | Export mode: {{if .Profile.ExportReport}}enabled{{else}}disabled{{end}}</p>

  <div class="grid">
    <section class="card">
      <h2>Test Profile</h2>
      <p class="metric-desc">测试参数配置。Stream count: 并发虚拟流数量；Packets/stream: 每条流发送的数据包数；Payload size: 每个数据包的负载大小；Window size: 计算窗口 Jain 指数时的滑动窗口长度（包数）。</p>
      <table>
        <tr><th>Stream count</th><td class="mono">{{.Profile.StreamCount}}</td></tr>
        <tr><th>Packets/stream</th><td class="mono">{{.Profile.PacketsPerStream}}</td></tr>
        <tr><th>Payload size</th><td class="mono">{{.Profile.PayloadSize}} B</td></tr>
        <tr><th>Window size</th><td class="mono">{{.Profile.WindowSize}}</td></tr>
      </table>
    </section>

    <section class="card">
      <h2>Fairness Summary</h2>
      <p class="metric-desc">
        <strong>Total events</strong>: 记录到的所有包事件数（含控制包）。
        <strong>Data packets</strong>: 仅 CmdPushStreamData 数据包的数量。
        <strong>Min Jain</strong>: 所有窗口中最低的 Jain 指数，反映最差局部公平性。
        <strong>Avg Jain</strong>: 所有窗口 Jain 指数的算术平均值。
        <strong>Max run length</strong>: 数据包序列中同一 SID 连续出现的最大次数，越大说明某条流越"霸占"发送机会。
      </p>
      <table>
        <tr><th>Total events</th><td class="mono">{{.TotalEvents}}</td></tr>
        <tr><th>Data packets</th><td class="mono">{{.Metrics.TotalDataPackets}}</td></tr>
        <tr><th>Min Jain</th><td class="mono">{{printf "%.4f" .Metrics.MinWindowJain}}</td></tr>
        <tr><th>Avg Jain</th><td class="mono">{{printf "%.4f" .Metrics.AvgWindowJain}}</td></tr>
        <tr><th>Max run length</th><td class="mono">{{.Metrics.MaxRunLength}}</td></tr>
      </table>
    </section>

    <section class="card">
      <h2>Threshold Check</h2>
      <p class="metric-desc">验收阈值检查。Min Jain 阈值: 所有窗口的最低 Jain 指数必须达标；Max run 阈值: 单流最大连续包数不得超过上限。两项均 PASS 则 Overall 为 PASS。</p>
      <table>
        <tr>
          <th>Min Jain >= {{printf "%.2f" .Profile.MinWindowJain}}</th>
          <td>{{if .ThresholdJainOK}}<span class="status-ok">PASS</span>{{else}}<span class="status-bad">FAIL</span>{{end}}</td>
        </tr>
        <tr>
          <th>Max run <= {{.Profile.MaxRunLength}}</th>
          <td>{{if .ThresholdRunOK}}<span class="status-ok">PASS</span>{{else}}<span class="status-bad">FAIL</span>{{end}}</td>
        </tr>
        <tr>
          <th>Overall</th>
          <td>{{if .OverallOK}}<span class="status-ok">PASS</span>{{else}}<span class="status-bad">FAIL</span>{{end}}</td>
        </tr>
      </table>
    </section>
  </div>

  <section class="card">
    <h2>Window Jain Index</h2>
    <p class="metric-desc">
      <strong>Jain 公平指数</strong> (Jain's Fairness Index): 衡量资源分配公平性的经典指标。
      公式: J = (&#8721;x<sub>i</sub>)&sup2; / (n &middot; &#8721;x<sub>i</sub>&sup2;)，其中 x<sub>i</sub> 是窗口内第 i 条流的包数，n 是流总数。
      取值范围 [1/n, 1]，值为 1 表示完全公平（所有流获得相同份额），值为 1/n 表示极度不公平（所有包都属于同一条流）。
      每行为一个窗口（大小 = {{.Profile.WindowSize}} 包），Bar 长度按百分比可视化。
    </p>
    <table>
      <tr><th>Window</th><th>Jain</th><th>Bar</th><th>Status</th></tr>
      {{range .WindowRows}}
      <tr>
        <td class="mono">{{.Index}}</td>
        <td class="mono">{{printf "%.4f" .Jain}}</td>
        <td>
          <div class="bar-wrap">
            <div class="bar" style="width: {{printf "%.2f" .Percent}}%;"></div>
          </div>
        </td>
        <td>{{if .Pass}}<span class="status-ok">PASS</span>{{else}}<span class="status-bad">FAIL</span>{{end}}</td>
      </tr>
      {{end}}
    </table>
  </section>

  <section class="card">
    <h2>Packet Count by SID</h2>
    <p class="metric-desc">
      每条流（SID）的数据包总量统计。Count: 实际发送的 CmdPushStreamData 包数；Expected: 预期包数（Packets/stream）；Delta: 实际与预期之差（0 为正常）；Distribution: 各流包数占比的可视化条形图，颜色区分不同 SID。
    </p>
    <table>
      <tr><th>SID</th><th>Count</th><th>Expected</th><th>Delta</th><th>Distribution</th></tr>
      {{range .SIDRows}}
      <tr>
        <td class="mono">{{.SID}}</td>
        <td class="mono">{{.Count}}</td>
        <td class="mono">{{.Expected}}</td>
        <td class="mono">{{.Delta}}</td>
        <td>
          <div class="bar-wrap">
            <div class="sid-bar" style="width: {{printf "%.2f" .Percent}}%; background: {{safeCSS .Color}};"></div>
          </div>
        </td>
      </tr>
      {{end}}
    </table>
  </section>

  <section class="card">
    <h2>Packet Sequence Preview</h2>
    <p class="subtitle">Showing first {{.PreviewCount}} CmdPushStreamData packets (color = SID).</p>
    <p class="metric-desc">
      数据包发送时序的可视化。每个色块代表一个 CmdPushStreamData 包，颜色对应其所属的 SID。连续同色块越长，说明该 SID 连续霸占发送机会越久，局部公平性越差。理想情况下颜色应交替均匀分布。
    </p>
    {{range .SequenceRows}}
    <div class="seq-row">
      <div class="seq-range mono">#{{.Start}} - #{{.End}}</div>
      <div class="seq-cells">
        {{range .Cells}}<span class="seq-cell" style="background: {{safeCSS .Color}};" title="index={{.Index}} sid={{.SID}}"></span>{{end}}
      </div>
    </div>
    {{end}}
    <div class="legend">Tip: 连续同色越长，表示同一 SID 连续发送越多，局部公平性越差。</div>
  </section>

  <section class="card">
    <h2>SID Timeline (TCP Occupancy)</h2>
    <p class="metric-desc">
      每条流（SID）在整个传输时段内占用底层 TCP 的时间窗口可视化（总时长: {{.TotalDuration}}）。
      横轴表示从第一个数据包到最后一个数据包的完整时间跨度，每行对应一个 SID。高亮色块表示该 SID 在对应时间段内有数据包被写入底层 TCP。
      色块密集/宽大表示该流在该时段大量占用发送机会；空白区域表示该流未活动。理想的公平调度下，各流的色块应均匀分散在时间轴上。
    </p>
    <div class="timeline-axis">
      <span class="mono">0</span>
      <span class="mono">{{.TotalDuration}}</span>
    </div>
    {{range $row := .TimelineRows}}
    <div class="timeline-row">
      <div class="timeline-label mono">{{$row.SID}}</div>
      <div class="timeline-track">
        {{range $row.Markers}}<div class="timeline-marker" style="left: {{printf "%.4f" .LeftPercent}}%; width: {{printf "%.4f" .WidthPercent}}%; background: {{safeCSS $row.Color}};"></div>{{end}}
      </div>
    </div>
    {{end}}
    <div class="legend">Tip: 色块表示该 SID 的数据包被写入 TCP 的时间段。色块越分散、越均匀，调度公平性越好。</div>
  </section>
</div>
</body>
</html>
`
