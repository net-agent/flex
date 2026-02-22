package sched

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v2/packet"
)

// RecordEntry captures a single packet write event.
type RecordEntry struct {
	Index int    `json:"index"`
	SID   uint64 `json:"sid"`
	Cmd   byte   `json:"cmd"`
}

// RecordWriter captures all written packets in order.
type RecordWriter struct {
	Entries []RecordEntry
	mu      sync.Mutex
	delay   time.Duration
	idx     int
}

func (rw *RecordWriter) WriteBuffer(buf *packet.Buffer) error {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.delay > 0 {
		time.Sleep(rw.delay)
	}
	rw.Entries = append(rw.Entries, RecordEntry{
		Index: rw.idx,
		SID:   buf.SID(),
		Cmd:   buf.Cmd(),
	})
	rw.idx++
	return nil
}

func (rw *RecordWriter) SetWriteTimeout(time.Duration) {}

func (rw *RecordWriter) Reset() {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	rw.Entries = nil
	rw.idx = 0
}

func (rw *RecordWriter) Snapshot() []RecordEntry {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	out := make([]RecordEntry, len(rw.Entries))
	copy(out, rw.Entries)
	return out
}

// makeBuf creates a test Buffer with given cmd and stream id.
func makeBuf(cmd byte, sid uint16) *packet.Buffer {
	buf := packet.NewBufferWithCmd(cmd)
	buf.SetDistIP(sid)
	return buf
}

// Metrics holds computed fairness metrics for a packet sequence.
type Metrics struct {
	MaxRunLength  int            `json:"maxRunLength"`
	JainIndex     float64        `json:"jainIndex"`
	PacketsPerSID map[uint64]int `json:"packetsPerSID"`
}

func calcMetrics(entries []RecordEntry) Metrics {
	m := Metrics{PacketsPerSID: make(map[uint64]int)}
	if len(entries) == 0 {
		return m
	}

	// Max run length
	run, lastSID := 1, entries[0].SID
	m.PacketsPerSID[lastSID]++
	for _, e := range entries[1:] {
		m.PacketsPerSID[e.SID]++
		if e.SID == lastSID {
			run++
		} else {
			if run > m.MaxRunLength {
				m.MaxRunLength = run
			}
			run = 1
			lastSID = e.SID
		}
	}
	if run > m.MaxRunLength {
		m.MaxRunLength = run
	}

	// Jain's Fairness Index: (sum(xi))^2 / (n * sum(xi^2))
	n := float64(len(m.PacketsPerSID))
	var sum, sumSq float64
	for _, cnt := range m.PacketsPerSID {
		x := float64(cnt)
		sum += x
		sumSq += x * x
	}
	if n > 0 && sumSq > 0 {
		m.JainIndex = (sum * sum) / (n * sumSq)
	}
	return m
}

// ScenarioResult holds raw vs fair comparison data.
type ScenarioResult struct {
	Name        string        `json:"name"`
	RawMetrics  Metrics       `json:"rawMetrics"`
	FairMetrics Metrics       `json:"fairMetrics"`
	RawSeq      []RecordEntry `json:"rawSeq"`
	FairSeq     []RecordEntry `json:"fairSeq"`
}

// runFairness sends N packets from each of numStreams streams, returns raw and fair sequences.
func runFairness(numStreams, packetsPerStream int) ScenarioResult {
	delay := 10 * time.Microsecond

	// --- Raw ---
	rawW := &RecordWriter{delay: delay}
	var wg sync.WaitGroup
	wg.Add(numStreams)
	for s := 1; s <= numStreams; s++ {
		go func(sid uint16) {
			defer wg.Done()
			for i := 0; i < packetsPerStream; i++ {
				rawW.WriteBuffer(makeBuf(packet.CmdPushStreamData, sid))
			}
		}(uint16(s))
	}
	wg.Wait()

	// --- Fair ---
	fairW := &RecordWriter{delay: delay}
	fw := NewFairWriter(fairW)
	wg.Add(numStreams)
	for s := 1; s <= numStreams; s++ {
		go func(sid uint16) {
			defer wg.Done()
			for i := 0; i < packetsPerStream; i++ {
				fw.WriteBuffer(makeBuf(packet.CmdPushStreamData, sid))
			}
		}(uint16(s))
	}
	wg.Wait()
	time.Sleep(50 * time.Millisecond) // drain
	fw.Close()

	rawSeq := rawW.Snapshot()
	fairSeq := fairW.Snapshot()
	return ScenarioResult{
		Name:        fmt.Sprintf("Fairness %d streams × %d pkts", numStreams, packetsPerStream),
		RawMetrics:  calcMetrics(rawSeq),
		FairMetrics: calcMetrics(fairSeq),
		RawSeq:      rawSeq,
		FairSeq:     fairSeq,
	}
}

// runPriority floods data packets and inserts control packets, measures control packet positions.
func runPriority(dataStreams, dataPerStream, controlCount int) ScenarioResult {
	delay := 10 * time.Microsecond

	// --- Raw ---
	rawW := &RecordWriter{delay: delay}
	var wg sync.WaitGroup
	wg.Add(dataStreams + 1)
	for s := 1; s <= dataStreams; s++ {
		go func(sid uint16) {
			defer wg.Done()
			for i := 0; i < dataPerStream; i++ {
				rawW.WriteBuffer(makeBuf(packet.CmdPushStreamData, sid))
			}
		}(uint16(s))
	}
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond) // let data start flowing
		for i := 0; i < controlCount; i++ {
			rawW.WriteBuffer(makeBuf(packet.CmdPingDomain, 0))
		}
	}()
	wg.Wait()

	// --- Fair ---
	fairW := &RecordWriter{delay: delay}
	fw := NewFairWriter(fairW)
	wg.Add(dataStreams + 1)
	for s := 1; s <= dataStreams; s++ {
		go func(sid uint16) {
			defer wg.Done()
			for i := 0; i < dataPerStream; i++ {
				fw.WriteBuffer(makeBuf(packet.CmdPushStreamData, sid))
			}
		}(uint16(s))
	}
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		for i := 0; i < controlCount; i++ {
			fw.WriteBuffer(makeBuf(packet.CmdPingDomain, 0))
		}
	}()
	wg.Wait()
	time.Sleep(50 * time.Millisecond)
	fw.Close()

	return ScenarioResult{
		Name:    "Priority: control packet position",
		RawSeq:  rawW.Snapshot(),
		FairSeq: fairW.Snapshot(),
	}
}

// runStarvation: 1 high-freq stream + N low-freq streams.
func runStarvation(lowStreams, highCount, lowCount int) ScenarioResult {
	delay := 10 * time.Microsecond

	run := func(useFair bool) []RecordEntry {
		rec := &RecordWriter{delay: delay}
		var write func(*packet.Buffer)
		var fw *FairWriter
		if useFair {
			fw = NewFairWriter(rec)
			write = func(buf *packet.Buffer) { fw.WriteBuffer(buf) }
		} else {
			write = func(buf *packet.Buffer) { rec.WriteBuffer(buf) }
		}

		var wg sync.WaitGroup
		// High-freq stream (sid=1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < highCount; i++ {
				write(makeBuf(packet.CmdPushStreamData, 1))
			}
		}()
		// Low-freq streams
		for s := 2; s <= lowStreams+1; s++ {
			wg.Add(1)
			go func(sid uint16) {
				defer wg.Done()
				for i := 0; i < lowCount; i++ {
					write(makeBuf(packet.CmdPushStreamData, sid))
					time.Sleep(100 * time.Microsecond)
				}
			}(uint16(s))
		}
		wg.Wait()
		if fw != nil {
			time.Sleep(50 * time.Millisecond)
			fw.Close()
		}
		return rec.Snapshot()
	}

	rawSeq := run(false)
	fairSeq := run(true)
	return ScenarioResult{
		Name:        fmt.Sprintf("Starvation: 1 high(%d) + %d low(%d)", highCount, lowStreams, lowCount),
		RawMetrics:  calcMetrics(rawSeq),
		FairMetrics: calcMetrics(fairSeq),
		RawSeq:      rawSeq,
		FairSeq:     fairSeq,
	}
}

// runThroughput measures packets/sec for raw vs fair using mock writer.
type ThroughputResult struct {
	Name     string  `json:"name"`
	RawPPS   float64 `json:"rawPPS"`
	FairPPS  float64 `json:"fairPPS"`
	BatchPPS float64 `json:"batchPPS,omitempty"`
}

func runThroughput(numStreams, total int) ThroughputResult {
	perStream := total / numStreams

	// Raw
	rawW := &RecordWriter{}
	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(numStreams)
	for s := 1; s <= numStreams; s++ {
		go func(sid uint16) {
			defer wg.Done()
			for i := 0; i < perStream; i++ {
				rawW.WriteBuffer(makeBuf(packet.CmdPushStreamData, sid))
			}
		}(uint16(s))
	}
	wg.Wait()
	rawDur := time.Since(start)

	// Fair
	fairW := &RecordWriter{}
	fw := NewFairWriter(fairW)
	start = time.Now()
	wg.Add(numStreams)
	for s := 1; s <= numStreams; s++ {
		go func(sid uint16) {
			defer wg.Done()
			for i := 0; i < perStream; i++ {
				fw.WriteBuffer(makeBuf(packet.CmdPushStreamData, sid))
			}
		}(uint16(s))
	}
	wg.Wait()
	time.Sleep(50 * time.Millisecond)
	fw.Close()
	fairDur := time.Since(start)

	label := fmt.Sprintf("%d streams × %d pkts", numStreams, perStream)
	return ThroughputResult{
		Name:    label,
		RawPPS:  float64(total) / rawDur.Seconds(),
		FairPPS: float64(total) / fairDur.Seconds(),
	}
}

// runRealThroughput measures throughput over real net.Pipe connections,
// comparing raw Conn, FairConn (no batch), and FairConn (with batch via connWriter).
func runRealThroughput(numStreams, total int) ThroughputResult {
	perStream := total / numStreams

	measure := func(useFair bool) float64 {
		c1, c2 := packet.Pipe()
		// Drain reader in background
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				if _, err := c2.ReadBuffer(); err != nil {
					return
				}
			}
		}()

		var writer packet.Writer = c1
		var fw *FairWriter
		if useFair {
			fw = NewFairWriter(c1)
			writer = nil
			_ = writer
		}

		var wg sync.WaitGroup
		wg.Add(numStreams)
		start := time.Now()
		for s := 1; s <= numStreams; s++ {
			go func(sid uint16) {
				defer wg.Done()
				for i := 0; i < perStream; i++ {
					buf := makeBuf(packet.CmdPushStreamData, sid)
					if fw != nil {
						fw.WriteBuffer(buf)
					} else {
						c1.WriteBuffer(buf)
					}
				}
			}(uint16(s))
		}
		wg.Wait()
		if fw != nil {
			time.Sleep(100 * time.Millisecond) // drain scheduler
			fw.Close()
		}
		dur := time.Since(start)
		c1.Close()
		c2.Close()
		<-done
		return float64(total) / dur.Seconds()
	}

	return ThroughputResult{
		Name:     fmt.Sprintf("Real %d streams × %d pkts", numStreams, perStream),
		RawPPS:   measure(false),
		FairPPS:  measure(true),
	}
}

// Report aggregates all scenario results for HTML rendering.
type Report struct {
	Fairness   []ScenarioResult  `json:"fairness"`
	Priority   ScenarioResult    `json:"priority"`
	Starvation ScenarioResult    `json:"starvation"`
	Throughput []ThroughputResult `json:"throughput"`
}

// controlPositions extracts normalized positions (0..1) of control packets in a sequence.
func controlPositions(entries []RecordEntry) []float64 {
	if len(entries) == 0 {
		return nil
	}
	var pos []float64
	for i, e := range entries {
		if e.Cmd != packet.CmdPushStreamData {
			pos = append(pos, float64(i)/float64(len(entries)))
		}
	}
	return pos
}

func TestGenerateReport(t *testing.T) {
	t.Log("Running fairness scenarios...")
	fair2 := runFairness(2, 100)
	fair5 := runFairness(5, 100)
	fair10 := runFairness(10, 50)

	t.Log("Running priority scenario...")
	prio := runPriority(3, 200, 10)

	t.Log("Running starvation scenario...")
	starv := runStarvation(4, 500, 20)

	t.Log("Running throughput scenarios...")
	tp1 := runThroughput(1, 10000)
	tp5 := runThroughput(5, 10000)
	tp10 := runThroughput(10, 10000)

	t.Log("Running real conn throughput scenarios...")
	rtp1 := runRealThroughput(1, 5000)
	rtp5 := runRealThroughput(5, 5000)

	report := Report{
		Fairness:   []ScenarioResult{fair2, fair5, fair10},
		Priority:   prio,
		Starvation: starv,
		Throughput: []ThroughputResult{tp1, tp5, tp10, rtp1, rtp5},
	}

	// Log summary
	for _, f := range report.Fairness {
		t.Logf("[%s] Raw maxRun=%d jain=%.4f | Fair maxRun=%d jain=%.4f",
			f.Name, f.RawMetrics.MaxRunLength, f.RawMetrics.JainIndex,
			f.FairMetrics.MaxRunLength, f.FairMetrics.JainIndex)
	}
	for _, tp := range report.Throughput {
		t.Logf("[%s] Raw=%.0f pps | Fair=%.0f pps", tp.Name, tp.RawPPS, tp.FairPPS)
	}

	// Generate HTML
	reportJSON, _ := json.Marshal(report)
	prioRawPos, _ := json.Marshal(controlPositions(prio.RawSeq))
	prioFairPos, _ := json.Marshal(controlPositions(prio.FairSeq))

	data := map[string]template.JS{
		"ReportJSON":  template.JS(reportJSON),
		"RawCtrlPos":  template.JS(prioRawPos),
		"FairCtrlPos": template.JS(prioFairPos),
	}

	tmpl, err := template.New("report").Parse(reportHTML)
	if err != nil {
		t.Fatalf("template parse: %v", err)
	}

	outDir := filepath.Join("..", "..", "docs")
	os.MkdirAll(outDir, 0o755)
	outPath := filepath.Join(outDir, "fair_scheduling_report.html")
	f, err := os.Create(outPath)
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	defer f.Close()

	if err := tmpl.Execute(f, data); err != nil {
		t.Fatalf("template execute: %v", err)
	}
	t.Logf("Report written to %s", outPath)
}

const reportHTML = `<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="UTF-8">
<title>FairConn vs Raw Conn 公平调度对比报告</title>
<script src="https://cdn.jsdelivr.net/npm/chart.js@4"></script>
<style>
  body { font-family: -apple-system, sans-serif; max-width: 1200px; margin: 0 auto; padding: 20px; background: #f5f5f5; }
  h1 { text-align: center; color: #333; }
  h2 { color: #555; border-bottom: 2px solid #ddd; padding-bottom: 8px; }
  .card { background: #fff; border-radius: 8px; padding: 20px; margin: 16px 0; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
  .grid { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
  canvas { max-height: 350px; }
  .summary { display: flex; gap: 16px; flex-wrap: wrap; }
  .stat { background: #e8f4fd; border-radius: 6px; padding: 12px 16px; flex: 1; min-width: 200px; }
  .stat .label { font-size: 12px; color: #666; }
  .stat .value { font-size: 24px; font-weight: bold; color: #1a73e8; }
  .timeline { overflow-x: auto; }
  .timeline canvas { min-width: 800px; }
</style>
</head>
<body>
<h1>FairConn vs Raw Conn 公平调度对比报告</h1>

<div class="card">
<h2>1. 公平性对比 (Fairness)</h2>
<div class="grid">
  <div><canvas id="fairRunLen"></canvas></div>
  <div><canvas id="fairJain"></canvas></div>
</div>
</div>

<div class="card">
<h2>2. 包序列时间线 (Packet Timeline)</h2>
<div class="grid">
  <div><canvas id="timelineRaw"></canvas></div>
  <div><canvas id="timelineFair"></canvas></div>
</div>
</div>

<div class="card">
<h2>3. 控制包优先级 (Priority)</h2>
<div class="grid">
  <div><canvas id="prioRaw"></canvas></div>
  <div><canvas id="prioFair"></canvas></div>
</div>
</div>

<div class="card">
<h2>4. 饥饿测试 (Starvation)</h2>
<div class="grid">
  <div><canvas id="starvRaw"></canvas></div>
  <div><canvas id="starvFair"></canvas></div>
</div>
</div>

<div class="card">
<h2>5. 吞吐量对比 (Throughput)</h2>
<div><canvas id="throughput"></canvas></div>
</div>

<script>
const report = {{.ReportJSON}};
const rawCtrlPos = {{.RawCtrlPos}};
const fairCtrlPos = {{.FairCtrlPos}};
const colors = ['#4285f4','#ea4335','#fbbc04','#34a853','#ff6d01','#46bdc6','#7b1fa2','#c2185b','#00897b','#6d4c41'];

// 1. Fairness bar charts
(() => {
  const labels = report.fairness.map(f => f.name);
  new Chart('fairRunLen', { type:'bar', data:{
    labels, datasets:[
      {label:'Raw maxRun', data:report.fairness.map(f=>f.rawMetrics.maxRunLength), backgroundColor:'#ea4335'},
      {label:'Fair maxRun', data:report.fairness.map(f=>f.fairMetrics.maxRunLength), backgroundColor:'#4285f4'}
    ]}, options:{plugins:{title:{display:true,text:'最大连续包数 (越小越公平)'}}}
  });
  new Chart('fairJain', { type:'bar', data:{
    labels, datasets:[
      {label:'Raw Jain Index', data:report.fairness.map(f=>f.rawMetrics.jainIndex), backgroundColor:'#ea4335'},
      {label:'Fair Jain Index', data:report.fairness.map(f=>f.fairMetrics.jainIndex), backgroundColor:'#4285f4'}
    ]}, options:{plugins:{title:{display:true,text:"Jain's Fairness Index (越接近1越公平)"}}, scales:{y:{min:0,max:1.05}}}
  });
})();

// 2. Timeline scatter (first fairness scenario)
(() => {
  function timelineData(seq, title, canvasId) {
    const sids = [...new Set(seq.map(e=>e.sid))];
    const datasets = sids.map((sid,i) => ({
      label: 'Stream '+sid, backgroundColor: colors[i%colors.length],
      data: seq.filter(e=>e.sid===sid).map(e=>({x:e.index, y:i})),
      pointRadius: 2
    }));
    new Chart(canvasId, { type:'scatter', data:{datasets},
      options:{plugins:{title:{display:true,text:title}}, scales:{x:{title:{display:true,text:'包序号'}}, y:{title:{display:true,text:'Stream'},ticks:{callback:(v)=>'S'+(sids[v]||v)}}}}
    });
  }
  const f = report.fairness[0];
  timelineData(f.rawSeq.slice(0,200), 'Raw 包序列 (前200)', 'timelineRaw');
  timelineData(f.fairSeq.slice(0,200), 'Fair 包序列 (前200)', 'timelineFair');
})();

// 3. Priority: control packet position histogram
(() => {
  function histChart(positions, canvasId, title) {
    const bins = 20;
    const counts = new Array(bins).fill(0);
    positions.forEach(p => { counts[Math.min(Math.floor(p*bins), bins-1)]++; });
    const labels = counts.map((_,i) => ((i/bins)*100).toFixed(0)+'%');
    new Chart(canvasId, { type:'bar', data:{labels, datasets:[{label:'控制包数量',data:counts,backgroundColor:'#fbbc04'}]},
      options:{plugins:{title:{display:true,text:title}}, scales:{x:{title:{display:true,text:'在输出序列中的位置'}},y:{title:{display:true,text:'数量'}}}}
    });
  }
  histChart(rawCtrlPos, 'prioRaw', 'Raw: 控制包位置分布');
  histChart(fairCtrlPos, 'prioFair', 'Fair: 控制包位置分布 (应靠前)');
})();

// 4. Starvation: per-stream packet distribution
(() => {
  function distChart(seq, canvasId, title) {
    const counts = {};
    seq.forEach(e => { counts[e.sid] = (counts[e.sid]||0)+1; });
    const sids = Object.keys(counts).sort((a,b)=>a-b);
    new Chart(canvasId, { type:'bar', data:{
      labels: sids.map(s=>'Stream '+s),
      datasets:[{label:'包数量', data:sids.map(s=>counts[s]), backgroundColor:sids.map((_,i)=>colors[i%colors.length])}]
    }, options:{plugins:{title:{display:true,text:title}}}});
  }
  distChart(report.starvation.rawSeq, 'starvRaw', 'Raw: 各Stream包分布');
  distChart(report.starvation.fairSeq, 'starvFair', 'Fair: 各Stream包分布');
})();

// 5. Throughput
(() => {
  new Chart('throughput', { type:'bar', data:{
    labels: report.throughput.map(t=>t.name),
    datasets:[
      {label:'Raw (pps)', data:report.throughput.map(t=>Math.round(t.rawPPS)), backgroundColor:'#ea4335'},
      {label:'Fair (pps)', data:report.throughput.map(t=>Math.round(t.fairPPS)), backgroundColor:'#4285f4'}
    ]}, options:{plugins:{title:{display:true,text:'吞吐量对比 (packets/sec)'}}}
  });
})();
</script>
</body>
</html>`

// tcpLoopbackPair creates a TCP loopback connection pair.
func tcpLoopbackPair(tb testing.TB) (net.Conn, net.Conn) {
	tb.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	defer ln.Close()

	var sconn net.Conn
	done := make(chan struct{})
	go func() {
		sconn, _ = ln.Accept()
		close(done)
	}()

	cconn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		tb.Fatal(err)
	}
	<-done
	return cconn, sconn
}

// drainReader reads and discards all packets until error.
func drainReader(r packet.Reader, done chan<- struct{}) {
	defer close(done)
	for {
		if _, err := r.ReadBuffer(); err != nil {
			return
		}
	}
}

// BenchmarkTCP_RawConn: baseline TCP write without scheduling.
func BenchmarkTCP_RawConn(b *testing.B) {
	for _, streams := range []int{1, 5} {
		b.Run(fmt.Sprintf("streams=%d", streams), func(b *testing.B) {
			c, s := tcpLoopbackPair(b)
			wconn := packet.NewWithConn(c)
			rconn := packet.NewWithConn(s)
			done := make(chan struct{})
			go drainReader(rconn, done)

			perStream := b.N / streams
			b.ResetTimer()

			var wg sync.WaitGroup
			wg.Add(streams)
			for sid := 1; sid <= streams; sid++ {
				go func(sid uint16) {
					defer wg.Done()
					for i := 0; i < perStream; i++ {
						wconn.WriteBuffer(makeBuf(packet.CmdPushStreamData, sid))
					}
				}(uint16(sid))
			}
			wg.Wait()
			b.StopTimer()

			wconn.Close()
			<-done
			rconn.Close()
		})
	}
}

// BenchmarkTCP_FairConn: FairWriter with batch write over TCP.
func BenchmarkTCP_FairConn(b *testing.B) {
	for _, streams := range []int{1, 5} {
		b.Run(fmt.Sprintf("streams=%d", streams), func(b *testing.B) {
			c, s := tcpLoopbackPair(b)
			wconn := packet.NewWithConn(c)
			rconn := packet.NewWithConn(s)
			done := make(chan struct{})
			go drainReader(rconn, done)

			fw := NewFairWriter(wconn)
			perStream := b.N / streams
			b.ResetTimer()

			var wg sync.WaitGroup
			wg.Add(streams)
			for sid := 1; sid <= streams; sid++ {
				go func(sid uint16) {
					defer wg.Done()
					for i := 0; i < perStream; i++ {
						fw.WriteBuffer(makeBuf(packet.CmdPushStreamData, sid))
					}
				}(uint16(sid))
			}
			wg.Wait()
			time.Sleep(50 * time.Millisecond)
			b.StopTimer()

			fw.Close()
			wconn.Close()
			<-done
			rconn.Close()
		})
	}
}

// BenchmarkTCP_BatchVsNoBatch: direct comparison of batch vs non-batch write.
func BenchmarkTCP_BatchVsNoBatch(b *testing.B) {
	b.Run("NoBatch", func(b *testing.B) {
		c, s := tcpLoopbackPair(b)
		w := packet.NewConnWriter(c)
		rconn := packet.NewWithConn(s)
		done := make(chan struct{})
		go drainReader(rconn, done)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			w.WriteBuffer(makeBuf(packet.CmdPushStreamData, 1))
		}
		b.StopTimer()

		c.Close()
		<-done
		rconn.Close()
	})

	b.Run("Batch4", func(b *testing.B) {
		c, s := tcpLoopbackPair(b)
		w := packet.NewConnWriter(c)
		bw := w.(packet.BatchWriter)
		rconn := packet.NewWithConn(s)
		done := make(chan struct{})
		go drainReader(rconn, done)

		batch := make([]*packet.Buffer, 4)
		for i := range batch {
			batch[i] = makeBuf(packet.CmdPushStreamData, 1)
		}

		b.ResetTimer()
		for i := 0; i < b.N; i += 4 {
			bw.WriteBufferBatch(batch)
		}
		b.StopTimer()

		c.Close()
		<-done
		rconn.Close()
	})
}
