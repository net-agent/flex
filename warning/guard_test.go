package warning

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGuard(t *testing.T) {
	pname := "github.com/net-agent/flex/v2/"
	guard := NewGuard(nil)

	// 测试default的输出。需要在debug模式下才能看到输出的内容
	guard.PopupInfo("test case 1, print in debug mode")
	guard.PopupWarning("test case 2, err is", "nothing")

	// 测试Lambda调用的堆栈
	guard.SetHandler(func(msg *Message) {
		assert.True(t, strings.HasPrefix(msg.Caller, pname+"warning.TestGuard"))
		assert.Equal(t, "a", msg.Info)
	})
	func() {
		guard.PopupInfo("a")
	}()

	// 测试直接调用的堆栈
	guard.SetHandler(func(msg *Message) {
		assert.Equal(t, pname+"warning.TestGuard", msg.Caller)
		assert.Equal(t, "b", msg.Info)
	})
	guard.PopupInfo("b")

	// 测试多级调用的堆栈
	guard.SetHandler(func(msg *Message) {
		assert.Equal(t, pname+"warning.testHelper", msg.Caller)
		assert.Equal(t, "c", msg.Info)
	})
	testHelper(guard)
}

func testHelper(guard *Guard) {
	guard.PopupWarning("c", "err")
}
