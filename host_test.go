package flex

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestHostWritePacket(t *testing.T) {
	c1, c2 := net.Pipe()

	host1 := NewHostAndRun(NewTcpPacketConn(c1), 0)

	ps := []*struct {
		cmd      byte
		srcHost  HostIP
		distHost HostIP
		srcPort  uint16
		distPort uint16
		payload  []byte
		ackInfo  uint16
	}{
		{CmdOpenStream, 1, 2, 1024, 80, nil, 0},
		{CmdCloseStream, 1, 2, 1024, 80, []byte("hello world"), 0},
		{CmdPushStreamData, 1, 2, 1024, 80, []byte("hello world"), 0},
	}

	go func() {
		for _, p := range ps {
			err := host1.writePacket(p.cmd, 0, 0, p.srcPort, p.distPort, 0, p.payload)
			if err != nil {
				t.Error(err)
				return
			}
		}
	}()

	for _, p := range ps {
		var head packetHeader
		_, err := io.ReadFull(c2, head[:])
		if err != nil {
			t.Error(err)
			return
		}
		if head.Cmd() != p.cmd {
			t.Error("not equal")
			return
		}
		if head.SrcPort() != p.srcPort {
			t.Error("not equal")
			return
		}
		if head.DistPort() != p.distPort {
			t.Error("not equal")
			return
		}
		if int(head.PayloadSize()) != len(p.payload) {
			t.Error("not equal")
			return
		}

		// read body
		buf := make([]byte, head.PayloadSize())
		_, err = io.ReadFull(c2, buf)
		if err != nil {
			t.Error(err)
			return
		}
		if !bytes.Equal(buf, p.payload) {
			t.Error("not equal")
			return
		}
	}
}

func TestHostDialAndListen(t *testing.T) {
	c1, c2 := net.Pipe()
	payload := make([]byte, 1024*24)
	_, err := rand.Read(payload)
	if err != nil {
		t.Error(err)
		return
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		// 等待服务端就绪
		wg.Wait()

		client := NewHostAndRun(NewTcpPacketConn(c1), 0)
		stream, err := client.Dial("0:80")
		if err != nil {
			t.Error(err)
			return
		}

		_, err = stream.Write(payload)
		if err != nil {
			t.Error(err)
			return
		}

		buf := make([]byte, len(payload))
		_, err = io.ReadFull(stream, buf)
		if err != nil {
			t.Error(err)
			return
		}

		if !bytes.Equal(buf, payload) {
			t.Error("not equal")
			return
		}
	}()

	server := NewHostAndRun(NewTcpPacketConn(c2), 0)
	listener, err := server.Listen(80)
	if err != nil {
		t.Error(err)
		return
	}
	wg.Done() // 让客户端开始

	stream, err := listener.Accept()
	if err != nil {
		t.Error(err)
		return
	}

	buf := make([]byte, len(payload))
	_, err = io.ReadFull(stream, buf)
	if err != nil {
		t.Error(err)
		return
	}
	if !bytes.Equal(buf, payload) {
		t.Error("not equal")
		return
	}
	_, err = stream.Write(buf)
	if err != nil {
		t.Error(err)
		return
	}
}

func TestHostStreamClose(t *testing.T) {
	c1, c2 := net.Pipe()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		wg.Wait()
		host := NewHostAndRun(NewTcpPacketConn(c1), 0)
		stream, err := host.Dial("0:80")
		if err != nil {
			t.Error(err)
			return
		}
		if host.streamsLen != 1 {
			t.Error("not equal")
			return
		}
		stream.Close()
		// if host.streamsLen != 0 {
		// 	t.Error("not equal")
		// 	return
		// }
	}()

	host := NewHostAndRun(NewTcpPacketConn(c2), 0)
	l, err := host.Listen(80)
	if err != nil {
		t.Error(err)
		return
	}
	_, err = host.Listen(80)
	if err == nil {
		t.Error("unexpceted nil error")
		return
	}

	// todo: fix close bug
	// shutdown -> close
	// if host.streamsLen != 0 {
	// 	t.Error("not equal")
	// 	return
	// }
	wg.Done()

	stream, err := l.Accept()
	if err != nil {
		t.Error(err)
		return
	}
	// if host.streamsLen != 1 {
	// 	t.Error("not equal", host.streamsLen)
	// 	return
	// }
	buf := make([]byte, 10)
	rn, err := stream.Read(buf)
	if rn > 0 {
		t.Error("unexpected rn")
		return
	}
	if err != io.EOF {
		t.Error("unexpected error")
		return
	}

	_, err = stream.Read(buf)
	if err == nil || err == io.EOF {
		// EOF应该只会被触发一次
		t.Error("unexpected error")
		return
	}
}

func TestConcurrencyStream(t *testing.T) {
	payloadSize := 1024 * 400
	threadLen := 40
	chanDuration := make(chan time.Duration, threadLen+1)

	payload := make([]byte, payloadSize)
	_, err := rand.Read(payload)
	if err != nil {
		t.Error(err)
		return
	}

	runClient := func(stream *Stream) {
		defer func() {
			// err := stream.Close()
			t.Log("client stream closed")
			if err != nil {
				t.Error(err)
				return
			}
		}()
		wn, err := stream.Write(payload)
		if err != nil {
			t.Error(err)
			return
		}
		if wn != len(payload) {
			t.Error("not equal")
			return
		}
	}

	runServer := func(stream *Stream) {
		startTime := time.Now()
		defer func() {
			chanDuration <- time.Since(startTime)
			stream.Close()
			t.Log("server stream closed")
			if err != nil {
				t.Error(err)
				return
			}
		}()
		buf := make([]byte, len(payload))
		rn, err := io.ReadFull(stream, buf)
		if rn != len(buf) {
			t.Error("not equal", rn, len(buf), err)
			return
		}
		if !bytes.Equal(buf, payload) {
			t.Error("not equal")
			return
		}
		if err != nil {
			t.Error(err)
			return
		}
	}

	c1, c2 := net.Pipe()
	client := NewHostAndRun(NewTcpPacketConn(c1), 0)
	server := NewHostAndRun(NewTcpPacketConn(c2), 0)

	go func() {
		streams := []*Stream{}
		for i := 0; i < threadLen; i++ {
			stream, err := client.Dial("0:80")
			if err != nil {
				t.Error(err)
				return
			}
			streams = append(streams, stream)
		}

		var wg sync.WaitGroup
		for _, stream := range streams {
			wg.Add(1)
			go func(s *Stream) {
				runClient(s)
				wg.Done()
			}(stream)
		}
		wg.Wait()
	}()

	// server side
	l, err := server.Listen(80)
	if err != nil {
		t.Error(err)
		return
	}

	count := 0
	var wg sync.WaitGroup
	for {
		stream, err := l.AcceptStream()
		if err != nil {
			t.Error(err)
			return
		}
		wg.Add(1)
		count++
		go func(s *Stream) {
			runServer(s)
			wg.Done()
		}(stream)
		if count == threadLen {
			break
		}
	}

	wg.Wait()

	// 查看平均耗时差别
	close(chanDuration)
	for {
		dur, ok := <-chanDuration
		if !ok {
			break
		}
		fmt.Printf("dur: %v\n", dur)
	}
}
