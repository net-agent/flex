package stream

import (
	"bytes"
	"io"
	"log"
	"math/rand"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v2/packet"
	"github.com/stretchr/testify/assert"
)

func TestPipe(t *testing.T) {
	payload := make([]byte, 1024*1024*10)
	rand.Read(payload)

	s1, s2 := Pipe()

	log.Println(s1.String(), s1.GetState().String())
	log.Println(s2.String(), s2.GetState().String())

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()

		_, err := s1.Write(payload)
		assert.Nil(t, err)

		err = s1.Close()
		assert.Nil(t, err)

		<-time.After(time.Millisecond * 100)
	}()

	buf := make([]byte, len(payload))
	_, err := io.ReadFull(s2, buf)
	assert.Nil(t, err)
	assert.True(t, bytes.Equal(payload, buf))

	wg.Wait()
}

func TestReadAndRoutePbufFailed(t *testing.T) {
	c, _ := net.Pipe()
	pc := packet.NewWithConn(c)
	pc.SetReadTimeout(time.Millisecond * 100)

	readAndRoutePbuf(nil, nil, pc)
}

func TestRouteErr(t *testing.T) {
	badPbuf := packet.NewBufferWithCmd(0)
	cmdCh := make(chan *packet.Buffer, 10)
	ackCh := make(chan *packet.Buffer, 10)

	cmdCh <- badPbuf
	ackCh <- badPbuf
	go routeCmd(nil, cmdCh)
	go routeAck(nil, ackCh)

	close(cmdCh)
	close(ackCh)
}

//
// Gemini Prompt:
//   我基于net.Conn协议实现了一个自己的类，并构造了Pipe函数，可以通过Pipe函数来创建两个可以互相读写的conn。
//   请帮我实现一个测试场景（func TestPipeDataTransport）：
//   1、s1以极慢的速度读取信息
//   2、s2一直会以4k大小的包向s1发送随机数据，直到对端关闭连接为止
//   3、无论基于什么原因停止传输数据，都需要比对s1接收的数据和s2是否完全一直
//   测试过程中的错误应该要打印出来
//

// TestPipeDataTransport implements the requested test scenario.
func TestPipeDataTransport(t *testing.T) {
	// 1. 创建 Pipe 连接
	s1, s2 := Pipe()
	log.Printf("s1 created: %v", s1.LocalAddr())
	log.Printf("s2 created: %v", s2.LocalAddr())

	var wg sync.WaitGroup
	var sentData, receivedData bytes.Buffer

	// 2. 启动 s2 (发送端) goroutine
	// 它会持续快速地发送4KB的随机数据块
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			// 在发送逻辑结束后关闭 s2
			log.Println("s2: Closing connection.")
			s2.Close()
		}()

		chunk := make([]byte, DefaultBucketSize/1600) // 4k大小的包
		count := 0
		for {
			// 生成随机数据
			_, err := rand.Read(chunk)
			if err != nil {
				t.Errorf("s2: Failed to generate random data: %v", err)
				return
			}

			// 发送数据
			log.Printf("s2: Write chunk, index=%v\n", count)
			s2.SetWriteDeadline(time.Now().Add(time.Second * 2))
			n, err := s2.Write(chunk)
			if err != nil {
				// 当 s1 关闭连接后，s2 的写入会失败，这是正常的退出路径
				// net.ErrClosed 是一个常见的错误，也可以是 io.EOF 或其他自定义的 pipe 关闭错误
				log.Printf("s2: Write failed, assuming peer closed. Error: %v. Stopping.", err)
				break
			}
			log.Printf("s2: Write chunk ok, index=%v\n", count)
			count++

			// 记录已成功发送的数据
			sentData.Write(chunk[:n])
		}
	}()

	// 3. 启动 s1 (接收端) goroutine
	// 它会以极慢的速度（每次只读一个字节）来接收数据
	wg.Add(1)
	go func() {
		defer wg.Done()

		// 模拟慢速读取
		readBuffer := make([]byte, 1) // 每次只读取一个字节
		readed := 0
		for {
			n, err := s1.Read(readBuffer)
			if err != nil {
				// io.EOF 表示连接被对端（s2）正常关闭，这是预期的结束信号
				if err == io.EOF {
					log.Println("s1: Received EOF. Peer closed connection cleanly.")
				} else {
					// 其他错误需要报告
					t.Errorf("s1: Read error: %v", err)
				}
				break
			}
			readed += n
			log.Printf("s1: Recv chunk ok, readed=%v\n", readed)

			// 记录接收到的数据
			receivedData.Write(readBuffer[:n])

			// 通过短暂睡眠来模拟慢速处理
			time.Sleep(time.Second)
		}
	}()

	// 4. 主测试 goroutine 控制测试时长
	// 运行一小段时间，让数据充分传输和缓冲
	log.Println("Test running for 1 second to allow data transfer...")
	time.Sleep(DefaultAppendDataTimeout * 5)

	// 时间到了，关闭 s1，这将向 s2 发出停止信号
	log.Println("Main: Time's up. Closing s1 to terminate the data flow.")
	err := s1.Close()
	assert.NoError(t, err, "s1 should close without error")

	// 5. 等待两个 goroutine 都执行完毕
	log.Println("Main: Waiting for all goroutines to complete...")
	wg.Wait()
	log.Println("Main: Goroutines finished.")

	// 6. 最终数据一致性校验
	log.Printf("Main: Verifying data consistency. Sent: %d bytes, Received: %d bytes", sentData.Len(), receivedData.Len())

	// 断言发送和接收的数据量大于0，确保测试有效
	assert.Greater(t, sentData.Len(), 0, "Should have sent some data")
	assert.Greater(t, receivedData.Len(), 0, "Should have received some data")

	// 断言发送和接收的数据完全一致
	assert.True(t, bytes.Equal(sentData.Bytes(), receivedData.Bytes()), "Sent and received data should be identical")

	log.Println("Main: Data verification successful.")
}
