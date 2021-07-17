package node

import (
	"errors"
	"io"
	"net"
	"time"

	"github.com/net-agent/flex/packet"
)

type Node interface {
	LocalAddr() net.Addr
	RemoteAddr() net.Addr

	State()
	Pause()
	Start()
	Stop()
	Replace() error

	SendPacket(pb *packet.Buffer, timeout time.Duration) error
	WaitPacket(timeout time.Duration) (*packet.Buffer, error)
}

type NodeImpl struct {
	conn        net.Conn
	rbufchan    chan *packet.Buffer
	wbufchan    chan *packet.Buffer
	writtingBuf *packet.Buffer // IO写入失败后，未成功写入的buf存在此处
}

func (n *NodeImpl) LocalAddr() net.Addr {
	return n.conn.LocalAddr()
}

func (n *NodeImpl) RemoteAddr() net.Addr {
	return n.conn.LocalAddr()
}

func (n *NodeImpl) SendPacket(pb *packet.Buffer, timeout time.Duration) error {
	select {
	case n.wbufchan <- pb:
		return nil
	case <-time.After(timeout):
		return errors.New("send timeout")
	}
}

// doWrite 把channel里面的数据逐个写入conn中
func (n *NodeImpl) doWrite() {
	//
	// 先把未写成功的数据发送出去
	//
	if n.writtingBuf != nil {
		err := packet.WriteConn(n.writtingBuf, n.conn)
		if err != nil {
			return
		}
		n.writtingBuf = nil
	}

	for pb := range n.wbufchan {
		err := packet.WriteConn(pb, n.conn)
		if err != nil {
			n.writtingBuf = pb
			return
		}
	}
}

// doRead 从conn中不断读取buffer，传入channel中，channel对端负责进行派发
func (n *NodeImpl) doRead() {
	for {
		pb, err := packet.ReadConn(n.conn)
		if err != nil {
			// retry
			break
		}

		n.rbufchan <- pb
	}
}

func (n *NodeImpl) WaitPacket(timeout time.Duration) (*packet.Buffer, error) {
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
