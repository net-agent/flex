package node

func appendWindowSize(buf []byte, size int32) []byte {
	return append(buf,
		byte(size>>24),
		byte(size>>16),
		byte(size>>8),
		byte(size),
	)
}

func readUint32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func writeUint32(b []byte, v uint32) {
	b[0] = byte(v >> 24)
	b[1] = byte(v >> 16)
	b[2] = byte(v >> 8)
	b[3] = byte(v)
}
