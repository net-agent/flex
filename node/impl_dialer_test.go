package node

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/vars"
	"github.com/stretchr/testify/assert"
)

func runEchoForListener(l net.Listener) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go io.Copy(c, c)
	}
}

func TestDial(t *testing.T) {
	port := uint16(80)
	payloadSize := 1024 * 1024 * 64
	node1, node2 := Pipe("test1", "test2")

	// 第一步：监听端口
	l, err := node1.Listen(port)
	assert.Nil(t, err, "listen port should ok")

	// 第二步：创建简单echo服务
	go runEchoForListener(l)

	// 第三步：从客户端创建连接
	conn, err := node2.DialIP(node1.GetIP(), port)
	assert.Nil(t, err, "dial ip should ok")
	defer conn.Close()

	// 第四步：准备客户端数据
	payload := make([]byte, payloadSize)
	rand.Read(payload)

	// 第五步：将数据发送至服务端
	var writerGroup sync.WaitGroup
	writerGroup.Add(1)
	go func() {
		defer writerGroup.Done()
		n, err := conn.Write(payload)
		assert.Nil(t, err, "write payload should ok")
		assert.Equal(t, n, len(payload), "write size should ok")
	}()

	// 第六步，读取服务端返回的数据，并校验正确性
	resp := make([]byte, payloadSize)
	_, err = io.ReadFull(conn, resp)
	assert.Nil(t, err, "read payload should ok")
	assert.True(t, bytes.Equal(payload, resp), "resp should equal to payload")

	writerGroup.Wait()
}

// TestDialConcurrency 多stream并发下的传输测试
func TestDialConcurrency(t *testing.T) {
	n1, n2 := Pipe("test1", "test2")
	port := uint16(80)
	l, err := n2.Listen(port)
	assert.Nil(t, err, "listen should be ok")
	go runEchoForListener(l)

	sizes := []int{
		10,
		1024 * 1024 * 64,
		1024 * 1024 * 64,
		1024 * 1024 * 64,
		1024 * 1024 * 64,
		0,
		1,
		10,
		1024,
		1024 * 1024,
	}

	var wg sync.WaitGroup
	for _, size := range sizes {
		wg.Add(1)
		go func(size int) {
			defer wg.Done()

			payload := make([]byte, size)
			rand.Read(payload) // 随机填充

			// DialDomain需要服务端配合
			c, err := n1.DialIP(n2.GetIP(), port)
			assert.Nil(t, err, "dial should be ok")
			go func() {
				n, err := c.Write(payload)
				assert.Nil(t, err, "write should be ok")
				assert.Equal(t, n, size, "")
			}()

			result := make([]byte, size)
			n, err := io.ReadFull(c, result)
			assert.Nil(t, err, "ReadFull should be ok")
			assert.Equal(t, n, size, "read size equal")
			assert.True(t, bytes.Equal(result, payload), "result should equal to payload")

			c.Close()
		}(size)
	}

	wg.Wait()
	n1.Close()
	n2.Close()
}

func TestDialAddr(t *testing.T) {
	n1, n2 := Pipe("test1", "test2")
	assert.NotNil(t, n1, "node should not be nil")
	assert.NotNil(t, n2, "node should not be nil")

	port := uint16(80)
	l, err := n2.Listen(port)
	assert.Nil(t, err, "listen should be ok")
	go runEchoForListener(l)

	_, err = n1.Dial("test2:80")
	assert.Nil(t, err, "dial with domain")

	_, err = n1.Dial("test2:1080")
	assert.NotNil(t, err, "test dial error")

	// cover: 测试Dial/parseAddress err
	_, err = n1.Dial("invalidaddr")
	assert.NotNil(t, err, "cover test: Dial/parseAddress")

	// cover: 测试DialDomain/DialIP branch
	_, err = n2.Dial("local:80")
	assert.Nil(t, err, "dial local should be ok")
}

func TestPing(t *testing.T) {
	n1, _ := Pipe("test1", "test2")

	_, err := n1.PingDomain("invaliddomain", time.Second)
	if err == nil {
		t.Error("unexpected nil error")
		return
	}

	d, err := n1.PingDomain("test2", time.Second)
	if err != nil {
		t.Error(err)
		return
	}

	log.Printf("ping domain duration=%v\n", d)
}

func Test_parseAddress(t *testing.T) {
	type args struct {
		addr string
	}
	tests := []struct {
		name         string
		args         args
		wantIsDomain bool
		wantDomain   string
		wantIp       uint16
		wantPort     uint16
		wantErr      bool
	}{
		{"split host port failed", args{"invalidhostport"}, false, "", 0, 0, true},
		{"invalid port1", args{"test:a123"}, false, "", 0, 0, true},
		{"invalid port2", args{"test:"}, false, "", 0, 0, true},
		{"port out of range", args{"test:-1"}, false, "", 0, 0, true},
		{"port out of range", args{"test:65536"}, false, "", 0, 0, true},
		{"invalid ip number", args{"-1:100"}, false, "", 0, 100, true},
		{"invalid ip number", args{fmt.Sprintf("%v:100", int(vars.MaxIP)+1)}, false, "", 0, 100, true},
		{"domain address", args{"testdomain:1080"}, true, "testdomain", 0, 1080, false},
		{"ip address", args{"1001:443"}, false, "", 1001, 443, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotIsDomain, gotDomain, gotIp, gotPort, err := parseAddress(tt.args.addr)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseAddress() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotIsDomain != tt.wantIsDomain {
				t.Errorf("parseAddress() gotIsDomain = %v, want %v", gotIsDomain, tt.wantIsDomain)
			}
			if gotDomain != tt.wantDomain {
				t.Errorf("parseAddress() gotDomain = %v, want %v", gotDomain, tt.wantDomain)
			}
			if gotIp != tt.wantIp {
				t.Errorf("parseAddress() gotIp = %v, want %v", gotIp, tt.wantIp)
			}
			if gotPort != tt.wantPort {
				t.Errorf("parseAddress() gotPort = %v, want %v", gotPort, tt.wantPort)
			}
		})
	}
}

func TestHandleCmdOpenAck(t *testing.T) {
	n := New(nil)
	port := uint16(1234)
	pbuf := packet.NewBufferWithCmd(packet.CmdOpenStream | packet.CmdACKFlag)
	pbuf.SetDistPort(port)

	// branch: not found error
	n.HandleCmdOpenStreamAck(pbuf)

	// branch: convert failed
	n.responses.Store(port, 1234)
	n.HandleCmdOpenStreamAck(pbuf)

	// branch: ok
	n.responses.Store(port, make(chan *dialresp, 1))
	n.HandleCmdOpenStreamAck(pbuf)

	// branch: attach stream failed
	n.responses.Store(port, make(chan *dialresp, 1))
	n.HandleCmdOpenStreamAck(pbuf)
}
