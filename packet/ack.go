package packet

func (buf *Buffer) SetOpenACK(err error) {
	buf.Head[0] |= CmdACKFlag
	buf.SetPayload([]byte(err.Error()))
}

func (buf *Buffer) SetPushACK(n uint16) {
	buf.Head[0] |= CmdACKFlag
	buf.SetACKInfo(n)
}
