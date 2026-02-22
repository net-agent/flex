package node

import (
	"log/slog"
	"sync"

	"github.com/net-agent/flex/v3/packet"
)

type Dispatcher struct {
	cmdChan  chan *packet.Buffer
	dataChan chan *packet.Buffer
	running  bool
	chanMut  sync.RWMutex

	cmdHandlers  map[byte]func(*packet.Buffer)
	dataHandlers map[byte]func(*packet.Buffer)
	logger       *slog.Logger
}

func (d *Dispatcher) init(host *Node) {
	d.logger = host.logger
	d.cmdHandlers = map[byte]func(*packet.Buffer){
		packet.AckPushStreamData: host.StreamHub.handleAckPushStreamData,
		packet.CmdOpenStream:     host.ListenHub.handleCmdOpenStream,
		packet.CmdPingDomain:     host.Pinger.handleCmdPingDomain,
		packet.AckPingDomain:     host.Pinger.handleAckPingDomain,
	}
	d.dataHandlers = map[byte]func(*packet.Buffer){
		packet.CmdPushStreamData: host.StreamHub.handleCmdPushStreamData,
		packet.AckOpenStream:     host.Dialer.handleAckOpenStream,
		packet.CmdCloseStream:    host.StreamHub.handleCmdCloseStream,
		packet.AckCloseStream:    host.StreamHub.handleAckCloseStream,
	}
}

// RegisterCmdHandler 注册命令通道的处理函数
func (d *Dispatcher) RegisterCmdHandler(cmd byte, handler func(*packet.Buffer)) {
	d.cmdHandlers[cmd] = handler
}

// RegisterDataHandler 注册数据通道的处理函数
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
	// 根据是否需要有序分为：Cmd和Data两种类型
	d.cmdChan = make(chan *packet.Buffer, 4096)  // 用于缓存OpenStream、PushDataAck的队列，这两个无需保证顺序
	d.dataChan = make(chan *packet.Buffer, 1024) // 与数据传输有关的包，OpenStreamAck、PushData、Close、CloseAck，需要保证顺序

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

func (d *Dispatcher) dispatch(pbuf *packet.Buffer) error {
	d.chanMut.RLock()
	defer d.chanMut.RUnlock()

	if !d.running {
		d.logger.Warn("dispatch buffer on stopped node", "cmd", pbuf.Cmd(), "header", pbuf.HeaderString())
		return ErrNodeIsStopped
	}

	switch pbuf.Cmd() {
	case packet.CmdPushStreamData:
		d.dataChan <- pbuf // PushData是最常见的，设置最短比较路径
	case packet.CmdOpenStream,
		packet.AckPushStreamData,
		packet.CmdPingDomain,
		packet.AckPingDomain:
		d.cmdChan <- pbuf
	default:
		d.dataChan <- pbuf
	}

	return nil
}

func (d *Dispatcher) processCmdChan() {
	for pbuf := range d.cmdChan {
		if handler, ok := d.cmdHandlers[pbuf.Cmd()]; ok {
			handler(pbuf)
		} else {
			d.logger.Warn("unknown cmd", "cmd", pbuf.Cmd(), "header", pbuf.HeaderString())
		}
	}
}

func (d *Dispatcher) processDataChan() {
	for pbuf := range d.dataChan {
		if handler, ok := d.dataHandlers[pbuf.Cmd()]; ok {
			handler(pbuf)
		} else {
			d.logger.Warn("unknown cmd", "cmd", pbuf.Cmd(), "header", pbuf.HeaderString())
		}
	}
}
