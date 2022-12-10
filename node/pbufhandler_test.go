package node

import (
	"testing"

	"github.com/net-agent/flex/v2/stream"
)

func TestGetStreamBySID(t *testing.T) {
	n := New(nil)
	var err error

	// 测试分支：stream not found
	_, err = n.GetStreamBySID(100, false)
	if err != errStreamNotFound {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// 测试分支：convert failed
	n.streams.Store(uint64(100), 1234)
	loadAndDelete := true
	_, err = n.GetStreamBySID(100, loadAndDelete)
	if err != errConvertStreamFailed {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
	_, loaded := n.streams.Load(uint64(100))
	if loaded {
		t.Errorf("load and delete flag not work\n")
		return
	}

	// 测试分支：正常通过
	s := stream.New(false)
	n.streams.Store(uint64(200), s)
	loadAndDelete = false
	retStream, err := n.GetStreamBySID(uint64(200), loadAndDelete)
	if err != nil {
		t.Error(err)
		return
	}
	if retStream != s {
		t.Errorf("unexpected return stream")
		return
	}
	_, loaded = n.streams.Load(uint64(200))
	if !loaded {
		t.Errorf("load and delete flag not work\n")
		return
	}
}
