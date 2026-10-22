package stream_test

import (
	"fmt"
	"io"

	"github.com/net-agent/flex/v3/stream"
)

// 一对内存 Stream 的读写。Stream 实现 net.Conn。
func ExamplePipe() {
	s1, s2 := stream.Pipe()
	defer s1.Close()
	defer s2.Close()

	go func() {
		_, _ = s1.Write([]byte("hello"))
	}()

	buf := make([]byte, 5)
	if _, err := io.ReadFull(s2, buf); err != nil {
		fmt.Println("read:", err)
		return
	}
	fmt.Println(string(buf))

	// Output:
	// hello
}
