package packet

type bufConn struct {
	Conn
	readChan  chan *Buffer
	writeChan chan *Buffer
}

func NewBufferedConn(conn Conn, writeChanSize, readChanSize int) Conn {
	return &bufConn{
		Conn:      conn,
		readChan:  make(chan *Buffer, readChanSize),
		writeChan: make(chan *Buffer, writeChanSize),
	}
}
