package node

import (
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
	host         *Node
}

func (d *Dispatcher) init(host *Node) {
	d.host = host
	d.cmdHandlers = map[byte]func(*packet.Buffer){
		packet.AckPushStreamData:    host.streamHub.handleAckPushStreamData,
		packet.CmdOpenStream:        host.listenHub.handleCmdOpenStream,
		packet.CmdPingDomain:        host.pinger.handleCmdPingDomain,
		packet.AckPingDomain:        host.pinger.handleAckPingDomain,
		packet.AckSubscribePresence: host.watcher.handleAckSubscribePresence,
		packet.CmdNotifyPresence:    host.watcher.handleCmdNotifyPresence,
	}
	d.dataHandlers = map[byte]func(*packet.Buffer){
		packet.CmdPushStreamData: host.streamHub.handleCmdPushStreamData,
		packet.AckOpenStream:     host.dialer.handleAckOpenStream,
		packet.CmdCloseStream:    host.streamHub.handleCmdCloseStream,
		packet.AckCloseStream:    host.streamHub.handleAckCloseStream,
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

func (d *Dispatcher) isRunning() bool {
	d.chanMut.RLock()
	defer d.chanMut.RUnlock()
	return d.running
}

func (d *Dispatcher) dispatch(pbuf *packet.Buffer) error {
	d.chanMut.RLock()
	defer d.chanMut.RUnlock()

	if !d.running {
		d.host.logger.Warn("dispatch buffer on stopped node", "cmd", pbuf.Cmd(), "header", pbuf.HeaderString(), "domain", d.host.domain)
		return ErrNodeIsStopped
	}

	switch pbuf.Cmd() {
	case packet.CmdPushStreamData:
		d.dataChan <- pbuf // PushData是最常见的，设置最短比较路径
	case packet.CmdOpenStream,
		packet.AckPushStreamData,
		packet.CmdPingDomain,
		packet.AckPingDomain,
		packet.AckSubscribePresence,
		packet.CmdNotifyPresence:
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
			d.host.logger.Warn("unknown cmd", "cmd", pbuf.Cmd(), "header", pbuf.HeaderString(), "domain", d.host.domain)
		}
		// 命令类处理器只做同步解析/就地应答，不保留 buffer 所有权。
		// 统一在此归还，使读路径的 payload 数组可以在池中复用。
		packet.PutBuffer(pbuf)
	}
}

func (d *Dispatcher) processDataChan() {
	for pbuf := range d.dataChan {
		// 必须在调用 handler 前捕获命令字：
		// handler 可能消费 buffer（数据包入队/合并后就地归还，Head 被清零），
		// 若在返回后再读 pbuf.Cmd() 会读到 0，导致同一 buffer 被二次归还，
		// 池中出现重复所有权、并发读写同一 Buffer。
		cmd := pbuf.Cmd()

		if handler, ok := d.dataHandlers[cmd]; ok {
			handler(pbuf)
		} else {
			d.host.logger.Warn("unknown cmd", "cmd", cmd, "header", pbuf.HeaderString(), "domain", d.host.domain)
		}

		// CmdPushStreamData 的所有权已转移给接收队列（由流在消费后归还），
		// 其余数据命令（OpenAck/Close/CloseAck）在处理器内同步消费完毕，在此归还。
		if cmd != packet.CmdPushStreamData {
			packet.PutBuffer(pbuf)
		}
	}
}
