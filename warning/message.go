package warning

import (
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
