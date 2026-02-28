// integration_test.go 验证 switcher + session 的端到端数据正确性。
//
// 测试分三阶段：
//   - Basic：2 并发，64KB 数据，固定 8KB 分包 — 快速验证基本正确性
//   - Stress：64 并发，512KB 数据，随机切包 — 覆盖高并发与流控路径
//   - Churn：持续循环建连/收发/关闭 — 重点覆盖连接复用与生命周期压力
//
// 推荐执行参数：
//   - go test -timeout 30s ./examples/integration -count=1
//   - 当前默认参数下，全量通常在 8s~18s 内完成（取决于机器负载）
//
// 关键压力参数（可按需调整）：
//   - Stress: connPerDomain / dataSize / minChunk / maxChunk
//   - Churn: workerPerDomain / duration / minPayload / maxPayload / opTimeout
//   - 若上调并发、时长或单连接数据量，建议同步提高 go test timeout（如 45s 或 60s）
//
// 服务端使用一个类 TLS 的多轮协议：
//  1. ClientHello -> ServerHello
//  2. ClientProof -> ServerProof
//  3. AppData(seq) <-> AppAck(seq, digest)
package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	mrand "math/rand"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/node"
	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/switcher"
	"github.com/stretchr/testify/require"
)

const (
	domain1Port = 1234
	domain2Port = 5678

	protoMsgClientHello byte = 1
	protoMsgServerHello byte = 2
	protoMsgClientProof byte = 3
	protoMsgServerProof byte = 4
	protoMsgAppData     byte = 5
	protoMsgAppAck      byte = 6
	protoMsgProtocolErr byte = 7
	protoHeaderSize          = 9
	protoMaxPayloadSize      = 128 * 1024

	protoSuiteFast byte = 1
	protoSuiteSafe byte = 2
)

type testProfile struct {
	name          string
	connPerDomain int
	dataSize      int
	minChunk      int
	maxChunk      int
	connTimeout   time.Duration
}

type churnProfile struct {
	name            string
	workerPerDomain int
	duration        time.Duration
	minPayload      int
	maxPayload      int
	minPause        time.Duration
	maxPause        time.Duration
	opTimeout       time.Duration
}

type protoHandshake struct {
	clientRandom [16]byte
	serverRandom [16]byte
	suite        byte
	sessionKey   [32]byte
}

var (
	basicProfile = testProfile{
		name:          "Basic",
		connPerDomain: 2,
		dataSize:      64 * 1024, // 64KB
		minChunk:      8 * 1024,  // 固定 8KB
		maxChunk:      8 * 1024,
		connTimeout:   30 * time.Second,
	}
	stressProfile = testProfile{
		name:          "Stress",
		connPerDomain: 64,
		dataSize:      512 * 1024, // 512KB
		minChunk:      512,
		maxChunk:      16 * 1024,
		connTimeout:   20 * time.Second,
	}
	churnStressProfile = churnProfile{
		name:            "Churn",
		workerPerDomain: 12,
		duration:        8 * time.Second,
		minPayload:      64,
		maxPayload:      64 * 1024,
		minPause:        0,
		maxPause:        20 * time.Millisecond,
		opTimeout:       3 * time.Second,
	}
)

func TestIntegration_Default(t *testing.T) {
	t.Run(basicProfile.name, func(t *testing.T) {
		runIntegrationTest(t, basicProfile)
	})
	if t.Failed() {
		t.Skipf("skipping Stress: Basic failed")
	}
	t.Run(stressProfile.name, func(t *testing.T) {
		runIntegrationTest(t, stressProfile)
	})
	if t.Failed() {
		t.Skipf("skipping Churn: previous phase failed")
	}
	t.Run(churnStressProfile.name, func(t *testing.T) {
		runIntegrationChurnTest(t, churnStressProfile)
	})
}

func runIntegrationTest(t *testing.T, p testProfile) {
	password := "test-password"

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

	sess1 := node.NewSession(tcpConnector(addr), node.SessionConfig{
		Domain:   "domain1",
		Password: password,
	})
	defer sess1.Close()
	go sess1.Serve()

	sess2 := node.NewSession(tcpConnector(addr), node.SessionConfig{
		Domain:   "domain2",
		Password: password,
	})
	defer sess2.Close()
	go sess2.Serve()

	ln1, err := sess1.Listen(domain1Port)
	require.NoError(t, err)
	go serveProtoService(ln1)

	ln2, err := sess2.Listen(domain2Port)
	require.NoError(t, err)
	go serveProtoService(ln2)

	require.NoError(t, sess1.WaitReady(5*time.Second))
	require.NoError(t, sess2.WaitReady(5*time.Second))

	var wg sync.WaitGroup
	wg.Add(p.connPerDomain * 2)

	for i := range p.connPerDomain {
		go func(idx int) {
			defer wg.Done()
			verifyProtocol(t, sess1, fmt.Sprintf("domain2:%d", domain2Port), idx, "d1->d2", p)
		}(i)
	}

	for i := range p.connPerDomain {
		go func(idx int) {
			defer wg.Done()
			verifyProtocol(t, sess2, fmt.Sprintf("domain1:%d", domain1Port), idx, "d2->d1", p)
		}(i)
	}

	wg.Wait()
}

func runIntegrationChurnTest(t *testing.T, p churnProfile) {
	password := "test-password"

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

	sess1 := node.NewSession(tcpConnector(addr), node.SessionConfig{
		Domain:   "domain1",
		Password: password,
	})
	defer sess1.Close()
	go sess1.Serve()

	sess2 := node.NewSession(tcpConnector(addr), node.SessionConfig{
		Domain:   "domain2",
		Password: password,
	})
	defer sess2.Close()
	go sess2.Serve()

	ln1, err := sess1.Listen(domain1Port)
	require.NoError(t, err)
	go serveProtoService(ln1)

	ln2, err := sess2.Listen(domain2Port)
	require.NoError(t, err)
	go serveProtoService(ln2)

	require.NoError(t, sess1.WaitReady(5*time.Second))
	require.NoError(t, sess2.WaitReady(5*time.Second))

	var okOps atomic.Int64
	var stopOnce sync.Once
	stopCh := make(chan struct{})
	errCh := make(chan error, p.workerPerDomain*4)

	stopAll := func(err error) {
		if err == nil {
			return
		}
		select {
		case errCh <- err:
		default:
		}
		stopOnce.Do(func() { close(stopCh) })
	}

	runUntil := time.Now().Add(p.duration)
	worker := func(sess *node.Session, addr string, wid int, tag string) {
		rng := mrand.New(mrand.NewSource(time.Now().UnixNano() + int64(wid+1)*7919))
		for {
			if time.Now().After(runUntil) {
				return
			}
			select {
			case <-stopCh:
				return
			default:
			}

			if err := churnProtocolOnce(sess, addr, rng, p); err != nil {
				stopAll(fmt.Errorf("[%s#%d] churn op failed: %w", tag, wid, err))
				return
			}
			okOps.Add(1)

			pause := randomDuration(rng, p.minPause, p.maxPause)
			if pause <= 0 {
				continue
			}
			timer := time.NewTimer(pause)
			select {
			case <-timer.C:
			case <-stopCh:
				timer.Stop()
				return
			}
		}
	}

	var wg sync.WaitGroup
	wg.Add(p.workerPerDomain * 2)
	for i := range p.workerPerDomain {
		go func(idx int) {
			defer wg.Done()
			worker(sess1, fmt.Sprintf("domain2:%d", domain2Port), idx, "d1->d2")
		}(i)
		go func(idx int) {
			defer wg.Done()
			worker(sess2, fmt.Sprintf("domain1:%d", domain1Port), idx, "d2->d1")
		}(i)
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}

	minOps := int64(p.workerPerDomain * 2)
	require.GreaterOrEqual(t, okOps.Load(), minOps, "churn operations too low")
}

func churnProtocolOnce(sess *node.Session, addr string, rng *mrand.Rand, p churnProfile) (retErr error) {
	stream, err := sess.Dial(addr)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer func() {
		if cerr := stream.Close(); retErr == nil && cerr != nil && !errors.Is(cerr, net.ErrClosed) {
			retErr = fmt.Errorf("close: %w", cerr)
		}
	}()

	_ = stream.SetDeadline(time.Now().Add(p.opTimeout))

	hs, err := clientHandshake(stream, rng)
	if err != nil {
		return fmt.Errorf("handshake: %w", err)
	}

	payloadSize := p.minPayload
	if p.maxPayload > p.minPayload {
		payloadSize = p.minPayload + rng.Intn(p.maxPayload-p.minPayload+1)
	}
	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = byte(rng.Intn(251))
	}

	if err := writeProtoFrame(stream, protoMsgAppData, 1, payload); err != nil {
		return fmt.Errorf("write app data: %w", err)
	}
	typ, seq, ackPayload, err := readProtoFrame(stream)
	if err != nil {
		return fmt.Errorf("read app ack: %w", err)
	}
	if typ != protoMsgAppAck || seq != 1 {
		return fmt.Errorf("unexpected app ack frame: typ=%d seq=%d", typ, seq)
	}
	if err := verifyAppAck(hs.sessionKey[:], 1, payload, ackPayload); err != nil {
		return fmt.Errorf("verify app ack: %w", err)
	}

	return nil
}

func verifyProtocol(t *testing.T, sess *node.Session, addr string, idx int, tag string, p testProfile) {
	t.Helper()

	stream, err := sess.Dial(addr)
	if err != nil {
		t.Errorf("[%s#%d] dial failed: %v", tag, idx, err)
		return
	}
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(p.connTimeout))

	rng := mrand.New(mrand.NewSource(time.Now().UnixNano() + int64(idx+1)*131))
	hs, err := clientHandshake(stream, rng)
	if err != nil {
		t.Errorf("[%s#%d] handshake failed: %v", tag, idx, err)
		return
	}

	sentHasher := sha256.New()
	chunk := make([]byte, p.maxChunk)
	remaining := p.dataSize
	offset := 0
	seq := uint32(1)
	sentN := 0

	for remaining > 0 {
		chunkSize := randInt(p.minChunk, p.maxChunk)
		if chunkSize > remaining {
			chunkSize = remaining
		}
		fillPattern(chunk[:chunkSize], offset, idx)
		payload := chunk[:chunkSize]

		if err := writeProtoFrame(stream, protoMsgAppData, seq, payload); err != nil {
			t.Errorf("[%s#%d] write frame failed: %v", tag, idx, err)
			return
		}

		typ, ackSeq, ackPayload, err := readProtoFrame(stream)
		if err != nil {
			t.Errorf("[%s#%d] read ack failed: %v", tag, idx, err)
			return
		}
		if typ == protoMsgProtocolErr {
			t.Errorf("[%s#%d] server protocol err: %s", tag, idx, string(ackPayload))
			return
		}
		if typ != protoMsgAppAck || ackSeq != seq {
			t.Errorf("[%s#%d] unexpected ack frame: typ=%d seq=%d want seq=%d", tag, idx, typ, ackSeq, seq)
			return
		}

		if err := verifyAppAck(hs.sessionKey[:], seq, payload, ackPayload); err != nil {
			t.Errorf("[%s#%d] invalid ack payload: %v", tag, idx, err)
			return
		}

		_, _ = sentHasher.Write(payload)
		sentN += chunkSize
		offset += chunkSize
		remaining -= chunkSize
		seq++
	}

	if sentN != p.dataSize {
		t.Errorf("[%s#%d] size mismatch: sent %d/%d", tag, idx, sentN, p.dataSize)
		return
	}
	if seq <= 1 {
		t.Errorf("[%s#%d] no app frames sent", tag, idx)
		return
	}
	if len(sentHasher.Sum(nil)) != sha256.Size {
		t.Errorf("[%s#%d] digest not finalized", tag, idx)
	}
}

func serveProtoService(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go handleProtoConn(conn)
	}
}

func handleProtoConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	hs, err := serverHandshake(conn)
	if err != nil {
		return
	}

	for {
		typ, seq, payload, err := readProtoFrame(conn)
		if err != nil {
			if isConnEOF(err) {
				return
			}
			return
		}

		switch typ {
		case protoMsgAppData:
			ackPayload := buildAppAck(hs.sessionKey[:], seq, payload)
			if err := writeProtoFrame(conn, protoMsgAppAck, seq, ackPayload); err != nil {
				return
			}
		default:
			_ = writeProtoFrame(conn, protoMsgProtocolErr, seq, []byte("unexpected frame type"))
			return
		}
	}
}

func clientHandshake(conn net.Conn, rng *mrand.Rand) (*protoHandshake, error) {
	hs := &protoHandshake{}
	for i := range hs.clientRandom {
		hs.clientRandom[i] = byte(rng.Intn(256))
	}
	suites := []byte{protoSuiteSafe, protoSuiteFast}

	hello := make([]byte, 17+len(suites))
	copy(hello[:16], hs.clientRandom[:])
	hello[16] = byte(len(suites))
	copy(hello[17:], suites)
	if err := writeProtoFrame(conn, protoMsgClientHello, 0, hello); err != nil {
		return nil, err
	}

	typ, _, payload, err := readProtoFrame(conn)
	if err != nil {
		return nil, err
	}
	if typ != protoMsgServerHello {
		return nil, fmt.Errorf("unexpected server hello type: %d", typ)
	}
	if len(payload) != 17 {
		return nil, fmt.Errorf("invalid server hello length: %d", len(payload))
	}
	copy(hs.serverRandom[:], payload[:16])
	hs.suite = payload[16]
	if !containsByte(suites, hs.suite) {
		return nil, fmt.Errorf("unsupported suite selected: %d", hs.suite)
	}

	clientProof := computeProof("client", hs.clientRandom, hs.serverRandom, hs.suite)
	if err := writeProtoFrame(conn, protoMsgClientProof, 0, clientProof[:]); err != nil {
		return nil, err
	}

	typ, _, payload, err = readProtoFrame(conn)
	if err != nil {
		return nil, err
	}
	if typ != protoMsgServerProof {
		return nil, fmt.Errorf("unexpected server proof type: %d", typ)
	}
	if len(payload) != sha256.Size {
		return nil, fmt.Errorf("invalid server proof length: %d", len(payload))
	}

	expectProof := computeProof("server", hs.clientRandom, hs.serverRandom, hs.suite)
	if !bytes.Equal(payload, expectProof[:]) {
		return nil, errors.New("server proof mismatch")
	}

	hs.sessionKey = computeProof("session", hs.clientRandom, hs.serverRandom, hs.suite)
	return hs, nil
}

func serverHandshake(conn net.Conn) (*protoHandshake, error) {
	hs := &protoHandshake{}

	typ, _, payload, err := readProtoFrame(conn)
	if err != nil {
		return nil, err
	}
	if typ != protoMsgClientHello {
		return nil, fmt.Errorf("unexpected client hello type: %d", typ)
	}
	if len(payload) < 17 {
		return nil, errors.New("client hello too short")
	}
	copy(hs.clientRandom[:], payload[:16])
	suiteCount := int(payload[16])
	if len(payload) != 17+suiteCount || suiteCount <= 0 {
		return nil, errors.New("invalid suite list")
	}
	clientSuites := payload[17:]
	hs.suite = chooseSuite(clientSuites)
	if hs.suite == 0 {
		return nil, errors.New("no common suite")
	}

	for i := range hs.serverRandom {
		hs.serverRandom[i] = byte(mrand.Intn(256))
	}
	serverHello := make([]byte, 17)
	copy(serverHello[:16], hs.serverRandom[:])
	serverHello[16] = hs.suite
	if err := writeProtoFrame(conn, protoMsgServerHello, 0, serverHello); err != nil {
		return nil, err
	}

	typ, _, payload, err = readProtoFrame(conn)
	if err != nil {
		return nil, err
	}
	if typ != protoMsgClientProof {
		return nil, fmt.Errorf("unexpected client proof type: %d", typ)
	}
	if len(payload) != sha256.Size {
		return nil, fmt.Errorf("invalid client proof length: %d", len(payload))
	}

	expectClientProof := computeProof("client", hs.clientRandom, hs.serverRandom, hs.suite)
	if !bytes.Equal(payload, expectClientProof[:]) {
		return nil, errors.New("client proof mismatch")
	}

	serverProof := computeProof("server", hs.clientRandom, hs.serverRandom, hs.suite)
	if err := writeProtoFrame(conn, protoMsgServerProof, 0, serverProof[:]); err != nil {
		return nil, err
	}

	hs.sessionKey = computeProof("session", hs.clientRandom, hs.serverRandom, hs.suite)
	return hs, nil
}

func writeProtoFrame(w io.Writer, typ byte, seq uint32, payload []byte) error {
	if len(payload) > protoMaxPayloadSize {
		return fmt.Errorf("payload too large: %d", len(payload))
	}
	var head [protoHeaderSize]byte
	head[0] = typ
	binary.BigEndian.PutUint32(head[1:5], seq)
	binary.BigEndian.PutUint32(head[5:9], uint32(len(payload)))
	if err := writeAll(w, head[:]); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	return writeAll(w, payload)
}

func readProtoFrame(r io.Reader) (typ byte, seq uint32, payload []byte, err error) {
	var head [protoHeaderSize]byte
	if _, err = io.ReadFull(r, head[:]); err != nil {
		return 0, 0, nil, err
	}
	typ = head[0]
	seq = binary.BigEndian.Uint32(head[1:5])
	size := int(binary.BigEndian.Uint32(head[5:9]))
	if size < 0 || size > protoMaxPayloadSize {
		return 0, 0, nil, fmt.Errorf("invalid payload size: %d", size)
	}
	if size == 0 {
		return typ, seq, nil, nil
	}
	payload = make([]byte, size)
	_, err = io.ReadFull(r, payload)
	if err != nil {
		return 0, 0, nil, err
	}
	return typ, seq, payload, nil
}

func buildAppAck(sessionKey []byte, seq uint32, payload []byte) []byte {
	ack := make([]byte, 4+sha256.Size)
	binary.BigEndian.PutUint32(ack[:4], uint32(len(payload)))
	digest := computeRecordDigest(sessionKey, seq, payload)
	copy(ack[4:], digest[:])
	return ack
}

func verifyAppAck(sessionKey []byte, seq uint32, payload []byte, ackPayload []byte) error {
	if len(ackPayload) != 4+sha256.Size {
		return fmt.Errorf("invalid ack payload length: %d", len(ackPayload))
	}
	size := int(binary.BigEndian.Uint32(ackPayload[:4]))
	if size != len(payload) {
		return fmt.Errorf("ack size mismatch: got %d want %d", size, len(payload))
	}
	expectDigest := computeRecordDigest(sessionKey, seq, payload)
	if !bytes.Equal(ackPayload[4:], expectDigest[:]) {
		return errors.New("ack digest mismatch")
	}
	return nil
}

func computeRecordDigest(sessionKey []byte, seq uint32, payload []byte) [32]byte {
	h := sha256.New()
	_, _ = h.Write(sessionKey)
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], seq)
	_, _ = h.Write(b[:])
	_, _ = h.Write(payload)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func computeProof(label string, clientRandom, serverRandom [16]byte, suite byte) [32]byte {
	h := sha256.New()
	_, _ = h.Write(clientRandom[:])
	_, _ = h.Write(serverRandom[:])
	_, _ = h.Write([]byte{suite})
	_, _ = h.Write([]byte(label))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func chooseSuite(clientSuites []byte) byte {
	if containsByte(clientSuites, protoSuiteSafe) {
		return protoSuiteSafe
	}
	if containsByte(clientSuites, protoSuiteFast) {
		return protoSuiteFast
	}
	return 0
}

func containsByte(values []byte, target byte) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

func fillPattern(buf []byte, offset, salt int) {
	for i := range buf {
		buf[i] = byte((offset + i + salt) % 251)
	}
}

func isConnEOF(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed)
}

func tcpConnector(addr string) node.ConnectFunc {
	return func() (packet.Conn, error) {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		return packet.NewWithConn(conn), nil
	}
}

func randInt(minVal, maxVal int) int {
	if minVal >= maxVal {
		return minVal
	}
	return minVal + mrand.Intn(maxVal-minVal+1)
}

func writeAll(w io.Writer, buf []byte) error {
	for len(buf) > 0 {
		n, err := w.Write(buf)
		if n > 0 {
			buf = buf[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	return nil
}

func randomDuration(rng *mrand.Rand, minVal, maxVal time.Duration) time.Duration {
	if maxVal <= minVal {
		return minVal
	}
	delta := int64(maxVal - minVal)
	return minVal + time.Duration(rng.Int63n(delta+1))
}
