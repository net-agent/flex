package packet_test

import (
	"fmt"

	"github.com/net-agent/flex/v3/packet"
)

// 构造一个数据包并读取头部字段。
func ExampleBuffer() {
	buf := packet.NewBufferWithCmd(packet.CmdPushStreamData)
	buf.SetSrc(1, 1000)
	buf.SetDist(2, 80)
	_ = buf.SetPayload([]byte("hello"))

	fmt.Println(buf.HeaderString())
	fmt.Println(string(buf.Payload))

	// Output:
	// [data][src=1:1000][dist=2:80][size=5]
	// hello
}

// 在内存 pipe 上收发一个数据包。
func ExamplePipe() {
	pc1, pc2 := packet.Pipe()
	defer pc1.Close()
	defer pc2.Close()

	out := packet.NewBufferWithCmd(packet.CmdPingDomain)
	_ = out.SetPayload([]byte("target"))

	go func() { _ = pc1.WriteBuffer(out) }()

	in, err := pc2.ReadBuffer()
	if err != nil {
		fmt.Println("read:", err)
		return
	}
	defer packet.PutBuffer(in)

	fmt.Println(string(in.Payload))

	// Output:
	// target
}
