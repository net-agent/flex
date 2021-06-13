package flex

import (
	"bytes"
	"crypto/rand"
	"io"
	"net"
	"sync"
	"testing"
)

func TestHostWritePacket(t *testing.T) {
	c1, c2 := net.Pipe()

	host1 := NewHost(c1)

	ps := []*Packet{
		{CmdOpenStream, 1024, 80, nil, 0},
		{CmdCloseStream, 1024, 80, []byte("hello world"), 0},
		{CmdPushStreamData, 1024, 80, []byte("hello world"), 0},
	}

	go func() {
		for _, p := range ps {
			err := host1.writePacket(p.cmd, p.srcPort, p.distPort, p.payload)
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

		client := NewHost(c1)
		stream, err := client.Dial(80)
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

	server := NewHost(c2)
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
		host := NewHost(c1)
		stream, err := host.Dial(80)
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

	host := NewHost(c2)
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

	if host.streamsLen != 0 {
		t.Error("not equal")
		return
	}
	wg.Done()

	stream, err := l.Accept()
	if err != nil {
		t.Error(err)
		return
	}
	if host.streamsLen != 1 {
		t.Error("not equal")
		return
	}
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
