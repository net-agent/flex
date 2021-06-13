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
		{CmdOpenStream, 1024, 80, nil},
		{CmdCloseStream, 1024, 80, []byte("hello world")},
		{CmdPushStreamData, 1024, 80, []byte("hello world")},
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
	payload := make([]byte, 1024*1024)
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
