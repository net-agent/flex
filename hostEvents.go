package flex

import (
	"log"
)

// onOpenStream 处理创建Stream的请求
func (host *Host) onOpenStream(
	srcHost HostIP, srcPort uint16, distHost HostIP, distPort uint16) {

	it, found := host.localBinds.Load(distPort)
	if !found {
		log.Printf("port=%v not available.\n", distPort)
		return
	}

	stream := NewStream(host, false)
	stream.SetAddr(distHost, distPort, srcHost, srcPort)

	err := host.attach(stream)
	if err != nil {
		log.Printf("%v attach failed: %v\n", stream.desc, err)
		return
	}

	stream.opened()
	it.(*Listener).pushStream(stream)
}

// onOpenStreamACK 处理创建Stream的应答
func (host *Host) onOpenStreamACK(
	srcIP HostIP, srcPort uint16, distIP HostIP, distPort uint16) {

	it, found := host.localBinds.LoadAndDelete(distPort)
	if !found {
		log.Printf("open ack ignored\n")
		return
	}

	stream := it.(*Stream)
	stream.SetAddr(stream.localIP, stream.localPort, srcIP, stream.remotePort)
	host.attach(stream)
	stream.chanOpenACK <- struct{}{}
}

//
// 收到Close消息，可以确定对端不会再有数据包通过这个stream发过来
// 所以可以对StreamID进行清理操作
//
func (host *Host) onCloseStream(dataID uint64) {
	stream, err := host.detach(dataID)
	if err != nil {
		log.Printf("detach failed: %v\n", err)
		return
	}
	if stream.isClient {
		host.availablePorts <- stream.localPort
	}
	stream.closed()
}

// onCloseStreamACK
func (host *Host) onCloseStreamACK(dataID uint64) {
	stream, err := host.detach(dataID)
	if err != nil {
		log.Printf("detach failed: %v\n", err)
		return
	}

	if stream.isClient {
		host.availablePorts <- stream.localPort
	}
	stream.chanCloseACK <- struct{}{}
}

// onPushData
func (host *Host) onPushData(dataID uint64, payload []byte) {
	it, found := host.streams.Load(dataID)
	if !found {
		log.Printf("data ignored\n")
		return
	}
	it.(*Stream).readPipe.append(payload)
}

func (host *Host) onPushDataACK(dataID uint64, ackInfo uint16) {
	it, found := host.streams.Load(dataID)
	if !found {
		log.Printf("data ack ignored\n")
		return
	}
	it.(*Stream).increasePoolSize(ackInfo)
}
