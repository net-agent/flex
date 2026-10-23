// Package wire defines flex's immutable packet IR and its 11-byte wire codec.
//
// The package deliberately has no I/O, no crypto and no connection state. It is
// the single place that knows the byte layout of a flex packet, which command
// bytes exist, and which confidentiality class each command belongs to.
//
// Class is a property of the packet, not of a lookup table at the transport
// layer: a control frame's payload is encrypted by the link, a data frame's
// payload is opaque. The class is encoded in bit 7 of the command byte so a
// receiver can make that decision without understanding the command.
package wire

import "fmt"

const (
	// HeaderSize is the fixed flex packet header size.
	HeaderSize = 11

	// MaxPayloadSize is the maximum payload length representable in the header.
	MaxPayloadSize = 0xFFFF

	// MaxIP is the largest virtual IP and the switcher's sentinel address.
	MaxIP = 0xFFFF
)

// Class is the confidentiality class of a frame's payload.
//
// The wire only distinguishes "encrypted by the link" (bit 7 set) from
// "opaque" (bit 7 clear). ClassHandshake is the encrypted bootstrap phase; it
// shares the encrypted bit but is only valid before the link is established.
type Class uint8

const (
	ClassControl   Class = 1 // payload is encrypted hop-by-hop; the relay may read it
	ClassData      Class = 2 // payload is opaque to flex; the application owns it
	ClassHandshake Class = 3 // bootstrap frame; only valid as the first frame
)

func (c Class) String() string {
	switch c {
	case ClassControl:
		return "control"
	case ClassData:
		return "data"
	case ClassHandshake:
		return "handshake"
	default:
		return fmt.Sprintf("class(%d)", uint8(c))
	}
}

func (c Class) valid() bool {
	return c == ClassControl || c == ClassData || c == ClassHandshake
}

// encrypted reports whether the wire class bit is set.
func (c Class) encrypted() bool {
	return c != ClassData
}

// Cmd is a flex command byte. Bit 0 is the ACK flag; bits 1..6 identify the
// command type; bit 7 is the class and never appears in a Cmd value.
type Cmd byte

// ControlCmd is a command whose payload must be encrypted by the link and may
// be read by the switcher for routing.
type ControlCmd Cmd

// DataCmd is a command whose payload is opaque to flex.
type DataCmd Cmd

// EmptyCmd is a command that carries no payload. Its ACK/status data (if any)
// lives in the header, e.g. AckPushStreamData's acknowledged byte count.
type EmptyCmd Cmd

// Command values. The Go type is the compile-time confidentiality class; use
// Cmd(CmdOpenStream) when an untyped wire value is needed (maps, switches).
const (
	CmdAdmit ControlCmd = 0x00

	CmdOpenStream ControlCmd = 0x02
	AckOpenStream ControlCmd = 0x03

	CmdCloseStream EmptyCmd = 0x04
	AckCloseStream EmptyCmd = 0x05

	CmdPushStreamData DataCmd  = 0x06
	AckPushStreamData EmptyCmd = 0x07

	CmdPushMessage ControlCmd = 0x08
	AckPushMessage EmptyCmd   = 0x09

	CmdPingDomain ControlCmd = 0x0A
	AckPingDomain ControlCmd = 0x0B

	CmdSubscribePresence ControlCmd = 0x0C
	AckSubscribePresence ControlCmd = 0x0D

	CmdNotifyPresence ControlCmd = 0x0E
)

const (
	ackFlag     Cmd  = 0x01
	cmdTypeMask Cmd  = 0x7E
	classFlag   byte = 0x80
)

// IsAck reports whether the ACK bit is set.
func (c Cmd) IsAck() bool { return c&ackFlag != 0 }

// Type returns the command type with the ACK and class bits masked off.
func (c Cmd) Type() Cmd { return c & cmdTypeMask }

// String returns the human-readable command name (including .ack).
func (c Cmd) String() string { return CommandName(c) }

// Addr is a virtual IP/port pair.
type Addr struct {
	IP   uint16
	Port uint16
}

var (
	// SwitcherAddr is the sentinel destination for packets addressed to the relay.
	SwitcherAddr = Addr{IP: MaxIP}
	// LocalAddr is the zero address (local dispatch / unset).
	LocalAddr = Addr{}
)

// IsZero reports whether both fields are zero.
func (a Addr) IsZero() bool { return a.IP == 0 && a.Port == 0 }

// String formats the address as "ip:port".
func (a Addr) String() string { return fmt.Sprintf("%d:%d", a.IP, a.Port) }

// SID is the 8-byte virtual stream identity: source and destination address.
type SID struct {
	Src Addr
	Dst Addr
}

// String formats the SID as "src->dst".
func (s SID) String() string { return s.Src.String() + "->" + s.Dst.String() }

type commandInfo struct {
	cmd   Cmd
	class Class
	name  string
}

// commandList is the single source of truth for command names and classes.
// A new command must be added here; the class must agree with the constructor
// type used at the call site (ControlCmd/DataCmd/EmptyCmd). Tests enforce that
// every command is present and that its class is consistent.
var commandList = []commandInfo{
	{Cmd(CmdAdmit), ClassHandshake, "admit"},

	{Cmd(CmdOpenStream), ClassControl, "open"},
	{Cmd(AckOpenStream), ClassControl, "open.ack"},
	{Cmd(CmdCloseStream), ClassControl, "close"},
	{Cmd(AckCloseStream), ClassControl, "close.ack"},

	{Cmd(CmdPushStreamData), ClassData, "data"},
	{Cmd(AckPushStreamData), ClassData, "data.ack"},

	{Cmd(CmdPushMessage), ClassControl, "message"},
	{Cmd(AckPushMessage), ClassControl, "message.ack"},

	{Cmd(CmdPingDomain), ClassControl, "ping"},
	{Cmd(AckPingDomain), ClassControl, "ping.ack"},

	{Cmd(CmdSubscribePresence), ClassControl, "subscribe"},
	{Cmd(AckSubscribePresence), ClassControl, "subscribe.ack"},
	{Cmd(CmdNotifyPresence), ClassControl, "notify"},
}

var (
	commandClass = make(map[Cmd]Class, len(commandList))
	commandName  = make(map[Cmd]string, len(commandList))
)

func init() {
	for _, info := range commandList {
		commandClass[info.cmd] = info.class
		commandName[info.cmd] = info.name
	}
}

// ClassOf returns the registered class of a command.
func ClassOf(cmd Cmd) (Class, bool) {
	c, ok := commandClass[cmd]
	return c, ok
}

// CommandName returns the human-readable name for a command. Unknown commands
// are rendered as "cmd(0x..)" so logs never lose information.
func CommandName(cmd Cmd) string {
	if name, ok := commandName[cmd]; ok {
		return name
	}
	return fmt.Sprintf("cmd(0x%02x)", byte(cmd))
}

// AllCommands returns every registered command in declaration order.
func AllCommands() []Cmd {
	out := make([]Cmd, 0, len(commandList))
	for _, info := range commandList {
		out = append(out, info.cmd)
	}
	return out
}

// IsDataCommand reports whether cmd belongs to the opaque-data class.
func IsDataCommand(cmd Cmd) bool {
	c, ok := ClassOf(cmd)
	return ok && c == ClassData
}

// IsControlCommand reports whether cmd belongs to the control class (excluding
// the handshake, which is not a routable control command).
func IsControlCommand(cmd Cmd) bool {
	c, ok := ClassOf(cmd)
	return ok && c == ClassControl
}
