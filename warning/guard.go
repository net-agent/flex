package warning

import "log"

type MessageHandler func(msg *Message)
type Guard struct {
	handler MessageHandler
}

func NewGuard(handler MessageHandler) *Guard {
	return &Guard{
		handler: handler,
	}
}

func (g *Guard) SetHandler(handler MessageHandler) {
	g.handler = handler
}

func (g *Guard) PopupMessage(msg *Message) {
	if g.handler == nil {
		if msg.Error == "" {
			log.Printf("[default-guard] %v: %v", msg.Caller, msg.Info)
		} else {
			log.Printf("[default-guard] %v: %v: %v", msg.Caller, msg.Info, msg.Error)
		}
		return
	}

	g.handler(msg)
}

func (g *Guard) PopupInfo(info string)         { g.PopupMessage(New(1, info, "")) }
func (g *Guard) PopupWarning(info, err string) { g.PopupMessage(New(1, info, err)) }
