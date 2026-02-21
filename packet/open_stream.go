package packet

import "encoding/binary"

// OpenStreamRequest is the payload of a CmdOpenStream packet.
type OpenStreamRequest struct {
	Domain     string
	WindowSize uint32
}

// Encode serializes the request as [domain][0x00][windowSize(4B)].
func (r *OpenStreamRequest) Encode() []byte {
	buf := make([]byte, len(r.Domain)+5)
	copy(buf, r.Domain)
	buf[len(r.Domain)] = 0
	binary.BigEndian.PutUint32(buf[len(r.Domain)+1:], r.WindowSize)
	return buf
}

// DecodeOpenStreamRequest parses a CmdOpenStream payload.
func DecodeOpenStreamRequest(payload []byte) OpenStreamRequest {
	for i, b := range payload {
		if b == 0 {
			var ws uint32
			if len(payload) >= i+5 {
				ws = binary.BigEndian.Uint32(payload[i+1 : i+5])
			}
			return OpenStreamRequest{Domain: string(payload[:i]), WindowSize: ws}
		}
	}
	return OpenStreamRequest{Domain: string(payload)}
}

// OpenStreamACK is the payload of an AckOpenStream packet.
type OpenStreamACK struct {
	OK         bool
	WindowSize uint32
	Error      string
}

// Encode serializes the ACK. Success: [0x00][windowSize(4B)]. Failure: [error string].
func (a *OpenStreamACK) Encode() []byte {
	if !a.OK {
		return []byte(a.Error)
	}
	buf := make([]byte, 5)
	buf[0] = 0
	binary.BigEndian.PutUint32(buf[1:], a.WindowSize)
	return buf
}

// DecodeOpenStreamACK parses an AckOpenStream payload.
func DecodeOpenStreamACK(payload []byte) OpenStreamACK {
	if len(payload) == 0 {
		return OpenStreamACK{OK: true}
	}
	if payload[0] == 0 {
		var ws uint32
		if len(payload) >= 5 {
			ws = binary.BigEndian.Uint32(payload[1:5])
		}
		return OpenStreamACK{OK: true, WindowSize: ws}
	}
	return OpenStreamACK{Error: string(payload)}
}
