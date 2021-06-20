package flex

import (
	"log"
	"sync/atomic"
)

// onOpenStream 处理创建Stream的请求
func (host *Host) onOpenStream(
	srcHost HostIP, srcPort uint16, distHost HostIP, distPort uint16) {

	it, found := host.localBinds.Load(distPort)
	if !found {
		log.Printf("port=%v not available.\n", distPort)
	}

	stream := NewStream(host, false)
	stream.SetAddr(distHost, distPort, srcHost, srcPort)

	_, found = host.streams.LoadOrStore(stream.dataID, stream)
	if found {
		log.Printf("%v exists\n", stream.desc)
		return
	}

	atomic.AddInt64(&host.streamsLen, 1)

	it.(*Listener).pushStream(stream)
	stream.opened()
}

// onOpenStreamACK 处理创建Stream的应答
func (host *Host) onOpenStreamACK(
	srcIP HostIP, srcPort uint16, distIP HostIP, distPort uint16) {

	it, found := host.localBinds.Load(distPort)
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
	it, found := host.streams.LoadAndDelete(dataID)
	if found {
		stream := it.(*Stream)

		atomic.AddInt64(&host.streamsLen, -1)
		stream.closed()
		host.availablePorts <- stream.localPort

		if debug {
			log.Printf("%v closed\n", stream.desc)
		}
	}
}

// onCloseStreamACK
func (host *Host) onCloseStreamACK(dataID uint64) {

	it, found := host.streams.LoadAndDelete(dataID)
	if found {
		stream := it.(*Stream)
		stream.chanCloseACK <- struct{}{}
		host.availablePorts <- stream.localPort
	} else {
		log.Printf("data ack ignored\n")
	}
}

// onPushData
func (host *Host) onPushData(dataID uint64, payload []byte) {
	it, found := host.streams.Load(dataID)
	if found {
		it.(*Stream).readPipe.append(payload)
	} else {
		log.Printf("data ignored\n")
	}
}

func (host *Host) onPushDataACK(dataID uint64, ackInfo uint16) {

	it, found := host.streams.Load(dataID)
	if !found {
		log.Printf(" data ack ignored\n")
		return
	}

	it.(*Stream).increasePoolSize(ackInfo)
}
