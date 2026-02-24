package packet

import (
	"net"
	"time"
)

// ObfuscatedConn 包装 Conn，对 header 前 9 字节做 XOR 混淆/解混淆。
// 跳过 Head[9:11]（PayloadSize/ACKSize），使 inner 的 ReadBuffer 能正确读取 payload。
type ObfuscatedConn struct {
	inner Conn
	state *ObfuscateState
}

// NewObfuscatedConn 创建混淆连接包装。
func NewObfuscatedConn(inner Conn, key []byte) *ObfuscatedConn {
	return &ObfuscatedConn{inner: inner, state: NewObfuscateState(key)}
}

func (c *ObfuscatedConn) ReadBuffer() (*Buffer, error) {
	buf, err := c.inner.ReadBuffer()
	if err != nil {
		return nil, err
	}
	mask := c.state.MaskForRead()
	ObfuscateHeader(&buf.Head, &mask)
	return buf, nil
}

// WriteBuffer 在 writeMu 保护下生成 mask 并写入，保证计数器顺序与线路顺序一致。
func (c *ObfuscatedConn) WriteBuffer(buf *Buffer) error {
	c.state.writeMu.Lock()
	mask := c.state.maskForWrite()
	ObfuscateHeader(&buf.Head, &mask)
	err := c.inner.WriteBuffer(buf)
	c.state.writeMu.Unlock()
	ObfuscateHeader(&buf.Head, &mask) // 还原，保护调用方 buffer
	return err
}

// WriteBufferBatch 在 writeMu 保护下批量混淆并写入。
func (c *ObfuscatedConn) WriteBufferBatch(bufs []*Buffer) error {
	masks := make([][obfuscateSize]byte, len(bufs))

	c.state.writeMu.Lock()
	for i, buf := range bufs {
		masks[i] = c.state.maskForWrite()
		ObfuscateHeader(&buf.Head, &masks[i])
	}

	var err error
	if bw, ok := c.inner.(interface{ WriteBufferBatch([]*Buffer) error }); ok {
		err = bw.WriteBufferBatch(bufs)
	} else {
		for _, buf := range bufs {
			if err = c.inner.WriteBuffer(buf); err != nil {
				break
			}
		}
	}
	c.state.writeMu.Unlock()

	for i, buf := range bufs {
		ObfuscateHeader(&buf.Head, &masks[i])
	}
	return err
}

func (c *ObfuscatedConn) SetReadTimeout(d time.Duration) error { return c.inner.SetReadTimeout(d) }
func (c *ObfuscatedConn) SetWriteTimeout(d time.Duration)      { c.inner.SetWriteTimeout(d) }
func (c *ObfuscatedConn) Close() error                         { return c.inner.Close() }
func (c *ObfuscatedConn) GetRawConn() net.Conn                 { return c.inner.GetRawConn() }
