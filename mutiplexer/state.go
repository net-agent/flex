package multiplexer

const (
	// 初始状态，此状态通过OpenStream或HandleCmdOpenStream进行改变
	STATE_INIT = iota

	// 主动发送Open命令，并等待对端应答OpenAck。此状态通过HandleCmdOpenStreamAck进行改变
	STATE_WAIT_OPEN_ACK

	// 收发数据的状态，PushData和PushDataAck都不会改变状态。此状态通过CloseStream或HandleCmdCloseStream进行改变
	STATE_ESTABLISHED

	// 主动发送Close命令，并等待对端应答CloseAck。此状态通过HandleCmdCloseAck进行改变
	STATE_WAIT_CLOSE_ACK

	// 终止状态
	STATE_CLOSED
)
