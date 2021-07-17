package packet

import (
	"net"

	"github.com/gorilla/websocket"
)

func WriteToConn(conn net.Conn, pb *Buffer) error {
	_, err := conn.Write(pb.head[:])
	if err != nil {
		return err
	}
	_, err = conn.Write(pb.payload)
	if err != nil {
		return err
	}

	return nil
}

func WriteToWs(wsconn *websocket.Conn, pb *Buffer) error {
	w, err := wsconn.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return err
	}
	_, err = w.Write(pb.head[:])
	if err != nil {
		return err
	}
	_, err = w.Write(pb.payload)
	if err != nil {
		return err
	}

	return nil
}
