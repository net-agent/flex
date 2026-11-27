package node

import (
	"fmt"
	"sync"

	"github.com/net-agent/flex/v4/packet"
	"github.com/net-agent/flex/v4/wire"
)

// Dispatcher routes inbound frames to control/data lanes. Control handlers keep
// the legacy packet.Buffer signature for source compatibility; the data path
// uses wire.Frame directly so bulk payloads are not copied or re-encoded.
type Dispatcher struct {
	cmdChan  chan *wire.Frame
	dataChan chan *wire.Frame
	running  bool
	chanMut  sync.RWMutex

	// frameDataHandlers is the new, zero-copy data path.
	frameDataHandlers map[wire.Cmd]frameHandler
	// frameCmdHandlers is reserved for control handlers migrated to frames.
	frameCmdHandlers map[wire.Cmd]frameHandler

	cmdHandlers  map[byte]func(*packet.Buffer)
	dataHandlers map[byte]func(*packet.Buffer)
	host         *Node
}

type frameHandler func(*wire.Frame) error

func (d *Dispatcher) init(host *Node) {
	d.host = host
	// Legacy maps are kept for RegisterCmdHandler/RegisterDataHandler users.
	// Built-in commands are handled by the frame maps below.
	d.cmdHandlers = make(map[byte]func(*packet.Buffer))
	d.dataHandlers = make(map[byte]func(*packet.Buffer))

	d.frameCmdHandlers = map[wire.Cmd]frameHandler{
		wire.Cmd(wire.CmdOpenStream):        host.listenHub.handleOpenFrame,
		wire.Cmd(wire.CmdPingDomain):        host.pinger.handlePingFrame,
		wire.Cmd(wire.AckPingDomain):        host.pinger.handleAckFrame,
		wire.Cmd(wire.AckSubscribePresence): host.watcher.handleAckFrame,
		wire.Cmd(wire.CmdNotifyPresence):    host.watcher.handleNotifyFrame,
	}
	d.frameDataHandlers = map[wire.Cmd]frameHandler{
		wire.Cmd(wire.CmdPushStreamData): host.streamHub.handleData,
		wire.Cmd(wire.AckPushStreamData): host.streamHub.handleAckData,
		wire.Cmd(wire.AckOpenStream):     host.dialer.handleAckOpenStreamFrame,
		wire.Cmd(wire.CmdCloseStream):    host.streamHub.handleClose,
		wire.Cmd(wire.AckCloseStream):    host.streamHub.handleCloseAck,
	}
}

// RegisterCmdHandler 注册控制包处理函数（兼容旧 API）。
func (d *Dispatcher) RegisterCmdHandler(cmd byte, handler func(*packet.Buffer)) {
	d.cmdHandlers[cmd] = handler
}

// RegisterDataHandler 注册有序数据包处理函数（兼容旧 API）。
func (d *Dispatcher) RegisterDataHandler(cmd byte, handler func(*packet.Buffer)) {
	d.dataHandlers[cmd] = handler
}

func (d *Dispatcher) start() error {
	d.chanMut.Lock()
	defer d.chanMut.Unlock()

	if d.running {
		return ErrRepeatRun
	}
	d.running = true

	// 创建不同的缓存队列，避免不同功能的数据包相互影响
	// 根据是否需要有序分为：Cmd 和 Data 两种类型
	d.cmdChan = make(chan *wire.Frame, 4096)  // OpenStream、PushDataAck 等无需保序
	d.dataChan = make(chan *wire.Frame, 1024) // PushData、OpenAck、Close 需要保序

	return nil
}

func (d *Dispatcher) stop() {
	d.chanMut.Lock()
	if d.running {
		d.running = false
		close(d.cmdChan)
		close(d.dataChan)
	}
	d.chanMut.Unlock()
}

func (d *Dispatcher) isRunning() bool {
	d.chanMut.RLock()
	defer d.chanMut.RUnlock()
	return d.running
}

// dispatch converts a legacy packet.Buffer and routes it. The buffer is not
// consumed; its payload is copied into the frame.
func (d *Dispatcher) dispatch(pbuf *packet.Buffer) error {
	f, err := bufferToFrame(pbuf)
	if err != nil {
		return err
	}
	return d.dispatchFrame(f)
}

// dispatchFrame always consumes f, including on error.
func (d *Dispatcher) dispatchFrame(f *wire.Frame) error {
	d.chanMut.RLock()
	defer d.chanMut.RUnlock()

	if !d.running {
		d.host.logger.Warn("dispatch buffer on stopped node", "cmd", f.Cmd(), "header", f, "domain", d.host.domain)
		f.Release()
		return ErrNodeIsStopped
	}

	switch f.Cmd() {
	case wire.Cmd(wire.CmdPushStreamData), wire.Cmd(wire.AckPushStreamData):
		d.dataChan <- f // PushData is the hot path; ACKs own/release their frame
	case wire.Cmd(wire.CmdOpenStream),
		wire.Cmd(wire.CmdPingDomain),
		wire.Cmd(wire.AckPingDomain),
		wire.Cmd(wire.AckSubscribePresence),
		wire.Cmd(wire.CmdNotifyPresence):
		d.cmdChan <- f
	default:
		d.dataChan <- f
	}

	return nil
}

func (d *Dispatcher) processCmdChan() {
	for f := range d.cmdChan {
		cmd := f.Cmd()

		// A custom legacy handler overrides the built-in frame handler.
		if handler, ok := d.cmdHandlers[byte(cmd)]; ok {
			b := frameToBuffer(f)
			f.Release()
			if b == nil {
				d.host.logger.Warn("unexpected cmd", "cmd", cmd, "domain", d.host.domain)
				continue
			}
			handler(b)
			// 命令类处理器只做同步解析/就地应答，不保留 buffer 所有权。
			packet.PutBuffer(b)
			continue
		}

		if handler, ok := d.frameCmdHandlers[cmd]; ok {
			if err := handler(f); err != nil {
				d.host.logger.Warn("frame cmd handler failed", "cmd", cmd, "error", err)
			}
			// Control frame handlers must not retain f.
			f.Release()
			continue
		}

		d.host.logger.Warn("unknown cmd", "cmd", cmd, "domain", d.host.domain)
		f.Release()
	}
}

func (d *Dispatcher) processDataChan() {
	for f := range d.dataChan {
		cmd := f.Cmd()

		// A custom legacy handler overrides the built-in frame handler.
		if handler, ok := d.dataHandlers[byte(cmd)]; ok {
			b := frameToBuffer(f)
			f.Release()
			if b == nil {
				d.host.logger.Warn("unexpected data cmd", "cmd", cmd, "domain", d.host.domain)
				continue
			}
			handler(b)
			packet.PutBuffer(b)
			continue
		}

		if handler, ok := d.frameDataHandlers[cmd]; ok {
			if err := handler(f); err != nil {
				d.host.logger.Warn("frame data handler failed", "cmd", cmd, "error", err)
			}
			// Data frame handlers take ownership of f.
			continue
		}

		d.host.logger.Warn("unknown cmd", "cmd", cmd, "domain", d.host.domain)
		f.Release()
	}
}

// frameToBuffer converts a frame to the legacy packet.Buffer view. The payload
// is copied because the legacy handlers treat the buffer as owned and the
// frame is released immediately afterwards.
// bufferToFrame converts a legacy packet.Buffer into a frame. It copies the
// payload so the caller keeps ownership of pbuf.
func bufferToFrame(pbuf *packet.Buffer) (*wire.Frame, error) {
	if pbuf == nil {
		return nil, nil
	}
	cmd := wire.Cmd(pbuf.Cmd() &^ 0x80)
	class, ok := wire.ClassOf(cmd)
	if !ok {
		return nil, fmt.Errorf("node: unknown command 0x%02x", byte(cmd))
	}
	var payload []byte
	var ackSize uint16
	if cmd == wire.Cmd(wire.AckPushStreamData) {
		ackSize = pbuf.DataACKSize()
	} else {
		payload = append([]byte(nil), pbuf.Payload...)
	}
	return wire.NewFrame(
		class, cmd,
		wire.Addr{IP: pbuf.SrcIP(), Port: pbuf.SrcPort()},
		wire.Addr{IP: pbuf.DistIP(), Port: pbuf.DistPort()},
		payload, ackSize,
	)
}

func frameToBuffer(f *wire.Frame) *packet.Buffer {
	b := packet.GetBuffer()
	if err := (wire.Codec{}).EncodeHeader(f.Header(), b.Head[:]); err != nil {
		packet.PutBuffer(b)
		return nil
	}
	// The legacy path predates the class bit; its switches use packet.Cmd*.
	b.Head[0] &^= 0x80
	b.Payload = append(b.Payload[:0], f.Payload()...)
	return b
}
