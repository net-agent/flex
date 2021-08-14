package node

import (
	"sync"
	"testing"
)

func TestListen(t *testing.T) {
	node1, node2 := Pipe()

	l, err := node1.Listen(80)
	if err != nil {
		t.Error(err)
		return
	}

	if l.Addr().Network() != "flex" || l.Addr().String() != "1:80" {
		t.Error("not equal")
		return
	}

	_, err = node1.Listen(80)
	if err.Error() != "port busy now" {
		t.Error("unexpected err")
		return
	}

	var wg sync.WaitGroup

	// accept test
	wg.Add(1)
	go func() {
		defer wg.Done()
		// 第一个连接为正常连接
		// 第二个为关闭错误

		_, err := l.Accept()
		if err != nil {
			t.Error(err)
			return
		}

		l.Close()
		_, err = l.Accept()
		if err == nil {
			t.Error("unexpected nil err")
			return
		}
	}()

	c, err := node2.Dial("1:80")
	if err != nil {
		t.Error(err)
		return
	}
	c.Close()
	wg.Wait()
}
