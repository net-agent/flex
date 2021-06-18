package flex

import "testing"

func TestPacketRead(t *testing.T) {
	packet := &Packet{}
	smallBuf := make([]byte, packetHeaderSize-1)
	bigBuf := make([]byte, 0x10000)

	_, err := packet.Read(smallBuf)
	if err == nil {
		t.Error("unexpected error")
		return
	}

	_, err = packet.Read(bigBuf)
	if err != nil {
		t.Error(err)
		return
	}

	packet.payload = make([]byte, 0x20000)
	_, err = packet.Read(bigBuf)
	if err == nil {
		t.Error("unexpected err")
		return
	}
}

func TestPacketCmd(t *testing.T) {
	packet := &Packet{}

	packet.cmd = CmdOpenStream
	if packet.CmdStr() != "[cmd][open]" {
		t.Error("not equal", packet.CmdStr())
		return
	}
	packet.cmd |= CmdACKFlag
	if packet.CmdStr() != "[ack][open]" {
		t.Error("not equal", packet.CmdStr())
		return
	}

	var head packetHeader
	_, err := packet.Read(head[:])
	if err != nil {
		t.Error(err)
		return
	}
	if packet.CmdStr() != head.CmdStr() {
		t.Error("not equal")
		return
	}
}
