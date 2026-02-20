package warning

import (
	"fmt"
	"runtime"
)

type Message struct {
	Caller string
	Info   string
	Error  string
}

func New(skipCallStack int, info, err string) *Message {
	pc, _, _, _ := runtime.Caller(skipCallStack + 1)
	caller := runtime.FuncForPC(pc).Name()

	return &Message{
		Caller: caller,
		Info:   info,
		Error:  err,
	}
}

func (m *Message) String() string {
	if m.Error == "" {
		return m.Info
	}
	return fmt.Sprintf("%v, err='%v'", m.Info, m.Error)
}
