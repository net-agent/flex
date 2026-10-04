package packet

import (
	"bytes"
	"encoding/binary"
)

// Subscribe 请求的操作类型
const (
	SubscribeAdd    = byte(0) // 订阅一组域名的上下线事件
	SubscribeRemove = byte(1) // 取消订阅
)

// SubscribeRequest 是 CmdSubscribePresence 的 payload。
// 编码格式：[op:1B][count:1B][domain][0x00]...
type SubscribeRequest struct {
	Op      byte
	Domains []string
}

func (r *SubscribeRequest) Encode() []byte {
	n := 2
	for _, d := range r.Domains {
		n += len(d) + 1
	}
	buf := make([]byte, n)
	buf[0] = r.Op
	buf[1] = byte(len(r.Domains))
	off := 2
	for _, d := range r.Domains {
		copy(buf[off:], d)
		off += len(d)
		buf[off] = 0
		off++
	}
	return buf
}

// DecodeSubscribeRequest 解析 CmdSubscribePresence 的 payload。
// 对截断或缺失分隔符的输入做容错处理，尾部多余字节被忽略。
func DecodeSubscribeRequest(payload []byte) SubscribeRequest {
	req := SubscribeRequest{Op: SubscribeAdd}
	if len(payload) < 2 {
		return req
	}
	req.Op = payload[0]
	count := int(payload[1])
	rest := payload[2:]
	for i := 0; i < count && len(rest) > 0; i++ {
		idx := bytes.IndexByte(rest, 0)
		if idx < 0 {
			req.Domains = append(req.Domains, string(rest))
			break
		}
		req.Domains = append(req.Domains, string(rest[:idx]))
		rest = rest[idx+1:]
	}
	return req
}

// PresenceState 描述一个 named node 在某一时刻的在线状态。
// Version 是该域名的单调递增版本号：每次真实状态迁移（online/offline 均计）+1，
// 订阅者据此识别 stale 事件与事件缺口。
type PresenceState struct {
	Domain  string
	Online  bool
	IP      uint16
	Mac     string
	Version uint64
}

// SubscribeACK 是 AckSubscribePresence 的 payload。
// 编码格式：[status:1B]
//   - status!=0：后续字节为错误字符串（风格同 OpenStreamACK）
//   - status==0：[count:2B] { [domain][0x00][online:1B][ip:2B][version:8B][mac][0x00] }...
//     即订阅应答携带被订阅域名的当前状态快照
type SubscribeACK struct {
	OK     bool
	Error  string
	States []PresenceState
}

func (a *SubscribeACK) Encode() []byte {
	if !a.OK {
		buf := make([]byte, 1+len(a.Error))
		buf[0] = 1
		copy(buf[1:], a.Error)
		return buf
	}
	n := 3
	for _, s := range a.States {
		n += len(s.Domain) + 1 + 1 + 2 + 8 + len(s.Mac) + 1
	}
	buf := make([]byte, n)
	// buf[0] 保持 0 表示 OK
	binary.BigEndian.PutUint16(buf[1:3], uint16(len(a.States)))
	off := 3
	for _, s := range a.States {
		copy(buf[off:], s.Domain)
		off += len(s.Domain)
		buf[off] = 0
		off++
		if s.Online {
			buf[off] = 1
		}
		off++
		binary.BigEndian.PutUint16(buf[off:off+2], s.IP)
		off += 2
		binary.BigEndian.PutUint64(buf[off:off+8], s.Version)
		off += 8
		copy(buf[off:], s.Mac)
		off += len(s.Mac)
		buf[off] = 0
		off++
	}
	return buf
}

// DecodeSubscribeACK 解析 AckSubscribePresence 的 payload。
func DecodeSubscribeACK(payload []byte) SubscribeACK {
	if len(payload) == 0 {
		return SubscribeACK{Error: "empty subscribe ack payload"}
	}
	if payload[0] != 0 {
		return SubscribeACK{Error: string(payload[1:])}
	}
	ack := SubscribeACK{OK: true}
	if len(payload) < 3 {
		return ack
	}
	count := int(binary.BigEndian.Uint16(payload[1:3]))
	rest := payload[3:]
	for i := 0; i < count; i++ {
		var st PresenceState
		idx := bytes.IndexByte(rest, 0)
		if idx < 0 {
			break
		}
		st.Domain = string(rest[:idx])
		rest = rest[idx+1:]
		if len(rest) < 11 {
			break
		}
		st.Online = rest[0] != 0
		st.IP = binary.BigEndian.Uint16(rest[1:3])
		st.Version = binary.BigEndian.Uint64(rest[3:11])
		rest = rest[11:]
		idx = bytes.IndexByte(rest, 0)
		if idx < 0 {
			st.Mac = string(rest)
			rest = nil
		} else {
			st.Mac = string(rest[:idx])
			rest = rest[idx+1:]
		}
		ack.States = append(ack.States, st)
	}
	return ack
}

// PresenceEvent 是 CmdNotifyPresence 的 payload，表示一次上下线状态迁移。
// 编码格式：[online:1B][ip:2B][version:8B][seq:8B][domain][0x00][mac][0x00]
// Version 是 per-domain 单调版本号，承载一致性语义（stale 识别与缺口检测）；
// Seq 是 switcher 侧的全局单调事件序号，仅供观测；因按域名过滤推送，
// 序号出现间隔是正常的，不代表丢包。
type PresenceEvent struct {
	Seq     uint64
	Version uint64
	Domain  string
	Online  bool
	IP      uint16
	Mac     string
}

func (e *PresenceEvent) Encode() []byte {
	n := 19 + len(e.Domain) + 1 + len(e.Mac) + 1
	buf := make([]byte, n)
	if e.Online {
		buf[0] = 1
	}
	binary.BigEndian.PutUint16(buf[1:3], e.IP)
	binary.BigEndian.PutUint64(buf[3:11], e.Version)
	binary.BigEndian.PutUint64(buf[11:19], e.Seq)
	off := 19
	copy(buf[off:], e.Domain)
	off += len(e.Domain)
	buf[off] = 0
	off++
	copy(buf[off:], e.Mac)
	off += len(e.Mac)
	buf[off] = 0
	return buf
}

// DecodePresenceEvent 解析 CmdNotifyPresence 的 payload。
func DecodePresenceEvent(payload []byte) PresenceEvent {
	var ev PresenceEvent
	if len(payload) < 19 {
		return ev
	}
	ev.Online = payload[0] != 0
	ev.IP = binary.BigEndian.Uint16(payload[1:3])
	ev.Version = binary.BigEndian.Uint64(payload[3:11])
	ev.Seq = binary.BigEndian.Uint64(payload[11:19])
	rest := payload[19:]
	idx := bytes.IndexByte(rest, 0)
	if idx < 0 {
		ev.Domain = string(rest)
		return ev
	}
	ev.Domain = string(rest[:idx])
	rest = rest[idx+1:]
	if idx := bytes.IndexByte(rest, 0); idx < 0 {
		ev.Mac = string(rest)
	} else {
		ev.Mac = string(rest[:idx])
	}
	return ev
}
