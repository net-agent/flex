package flex_test

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

func TestChanClose(t *testing.T) {
	ch := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ch
		fmt.Println("ch 1")
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		val := <-ch
		fmt.Println("ch 2", val)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		val, ok := <-ch
		fmt.Println("ch 3", val, ok)
	}()

	time.After(time.Microsecond * 100)
	close(ch)

	wg.Wait()
}

func TestChanSelect(t *testing.T) {
	closeEvents := make(chan int, 10)
	valueEvents := make(chan int, 10)

	close(closeEvents)
	valueEvents <- 1
	valueEvents <- 2
	valueEvents <- 3

	select {
	case <-closeEvents:
		fmt.Println("close event")
	case <-valueEvents:
		fmt.Println("value event")
	}
}

func TestPipe(t *testing.T) {
	c1, _ := net.Pipe()

	fmt.Println(c1.LocalAddr().String())
}
