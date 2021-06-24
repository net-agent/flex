package flex

import (
	"bytes"
	"crypto/rand"
	"io"
	"net"
	"sync"
	"testing"
)

func TestStreamOpen(t *testing.T) {
	c1, c2 := net.Pipe()

	go func() {
		host := NewHostAndRun(NewTcpPacketConn(c1), 1)
		host.Dial("0:1024")
	}()

	// 解析Dial请求
	var head packetHeader
	_, err := io.ReadFull(c2, head[:])
	if err != nil {
		t.Error(err)
		return
	}
	if head.Cmd() != CmdOpenStream {
		t.Error("not equal")
		return
	}
	if head.DistPort() != 1024 {
		t.Error("not equal")
		return
	}
}

func TestStreamClose(t *testing.T) {
	c1, c2 := net.Pipe()
	go func() {
		host := NewHostAndRun(NewTcpPacketConn(c1), 0)
		stream := NewStream(host, true)
		stream.SetAddr(0, 1000, 1, 80)
		stream.Close()
	}()

	var head packetHeader
	_, err := io.ReadFull(c2, head[:])
	if err != nil {
		t.Error(err)
		return
	}
	if head.Cmd() != CmdCloseStream {
		t.Error("not equal")
		return
	}
	if head.DistPort() != 80 {
		t.Error("not equal")
		return
	}
	s := NewStream(nil, false)
	s.SetAddr(1, 80, 0, 1000)
	if head.StreamDataID() != s.dataID {
		t.Error("not equal")
		return
	}
}

// TestStreamDataWrite 构造一个Host和Stream，然后用原始net.Conn去读取并验证发送的数据是否正确
func TestStreamDataWrite(t *testing.T) {
	c1, c2 := net.Pipe()
	payload := make([]byte, 10)
	n, err := rand.Read(payload)
	if err != nil {
		t.Error(err)
		return
	}
	if n != len(payload) {
		t.Error("not equal")
		return
	}

	// 构造Host和Stream
	// 然后发送数据
	go func() {
		host := NewHostAndRun(NewTcpPacketConn(c1), 0)
		stream := NewStream(host, true)
		stream.SetAddr(0, 1024, 1, 80)
		host.streams.Store(stream.dataID, stream)

		wn, err := stream.Write(payload)
		if wn != len(payload) {
			t.Error("not equal")
			return
		}
		if err != nil && err != io.EOF {
			t.Error(err)
			return
		}
	}()

	// 直接读取c2所收到的数据，并进行验证
	var head packetHeader
	s := &Stream{
		localPort:  80,
		remotePort: 1024,
		isClient:   false,
	}
	s.SetAddr(1, 80, 0, 1024)

	_, err = io.ReadFull(c2, head[:])
	if err != nil {
		t.Error(err)
		return
	}
	if head.Cmd() != CmdPushStreamData {
		t.Error("not equal")
		return
	}
	if head.DistPort() != 80 {
		t.Error("not equal")
		return
	}
	if head.StreamDataID() != s.dataID {
		t.Error("not equal")
		return
	}
	if int(head.PayloadSize()) != len(payload) {
		t.Error("not equal")
		return
	}
	buf := make([]byte, head.PayloadSize())
	_, err = io.ReadFull(c2, buf)
	if err != nil {
		t.Error(err)
		return
	}
	if !bytes.Equal(buf, payload) {
		t.Error(err)
		return
	}
}

func TestStreamDataRead(t *testing.T) {
	c1, c2 := net.Pipe()
	payload := make([]byte, 10)
	n, err := rand.Read(payload)
	if err != nil {
		t.Error(err)
		return
	}
	if n != len(payload) {
		t.Error("not equal")
		return
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		host := NewHostAndRun(NewTcpPacketConn(c1), 0)
		stream := NewStream(host, true)
		stream.SetAddr(0, 1024, 1, 80)
		host.streams.Store(stream.dataID, stream)

		wn, err := stream.Write(payload)
		if wn != len(payload) {
			t.Error("not equal")
			return
		}
		if err != nil && err != io.EOF {
			t.Error(err)
			return
		}
		t.Log("[TestStreamDataRead] write done")
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		host := NewHostAndRun(NewTcpPacketConn(c2), 1)
		stream := NewStream(host, false)
		stream.SetAddr(1, 80, 0, 1024)
		host.streams.Store(stream.dataID, stream)

		buf := make([]byte, len(payload))
		rn, err := io.ReadFull(stream, buf)
		if err != nil && err != io.EOF {
			t.Error(err)
			return
		}
		if rn != len(buf) {
			t.Error("not equal", rn, len(buf))
			return
		}
		if !bytes.Equal(buf, payload) {
			t.Error("not equal")
			return
		}
	}()

	wg.Wait()
}
