package node

func (n *Node) Start() {
	go n.doRead()
	go n.doWrite()
}
