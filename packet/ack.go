package packet

func (buf *Buffer) SwapSrcDist() {
	var addr [4]byte
	copy(addr[:], buf.Head[1:5])
	copy(buf.Head[1:5], buf.Head[5:9])
	copy(buf.Head[5:9], addr[:])
}

func (buf *Buffer) SetOpenACK(msg string) *Buffer {
	buf.Head[0] |= CmdACKFlag
	buf.SwapSrcDist()

	if msg == "" {
		buf.SetPayload(nil)
	} else {
		buf.SetPayload([]byte(msg))
	}
	return buf
}
