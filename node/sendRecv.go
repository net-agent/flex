package node

import (
	"errors"
	"io"
	"time"

	"github.com/net-agent/flex/packet"
)

type Node struct {
	Conn        packet.Conn
	rbufchan    chan *packet.Buffer
	wbufchan    chan *packet.Buffer
	writtingBuf *packet.Buffer // IO写入失败后，未成功写入的buf存在此处
}

func (n *Node) SendPacket(pb *packet.Buffer, timeout time.Duration) error {
	select {
	case n.wbufchan <- pb:
		return nil
	case <-time.After(timeout):
		return errors.New("send timeout")
	}
}

// doWrite 把channel里面的数据逐个写入conn中
func (n *Node) doWrite() {
	//
	// 先把未写成功的数据发送出去
	//
	if n.writtingBuf != nil {
		err := n.Conn.WriteBuffer(n.writtingBuf)
		if err != nil {
			return
		}
		n.writtingBuf = nil
	}

	for pb := range n.wbufchan {
		err := n.Conn.WriteBuffer(pb)
		if err != nil {
			n.writtingBuf = pb
			return
		}
	}
}

// doRead 从conn中不断读取buffer，传入channel中，channel对端负责进行派发
func (n *Node) doRead() {
	for {
		pb, err := n.Conn.ReadBuffer()
		if err != nil {
			return
		}

		n.rbufchan <- pb
	}
}

func (n *Node) WaitPacket(timeout time.Duration) (*packet.Buffer, error) {
	select {
	case pb, ok := <-n.rbufchan:
		if ok {
			return pb, nil
		}
		return nil, io.EOF
	case <-time.After(timeout):
		return nil, errors.New("timeout")
	}
}
