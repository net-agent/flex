package switcher

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/net-agent/flex/packet"
)

type Request struct {
	Domain    string
	Mac       string
	Timestamp int64
	Sum       string
}

// GenSum sha256(domain + mac + timestamp + password)
func (req *Request) CalcSum(password string) string {
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("CalcSumStart,%v,%v,%v,CalcSumEnd", req.Domain, req.Mac, req.Timestamp)))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

type Response struct {
	IP uint16
}

// ServeConn
func (s *Server) ServeConn(pc packet.Conn) error {
	defer pc.Close()

	pbuf, err := pc.ReadBuffer()
	if err != nil {
		return err
	}

	var req Request
	err = json.Unmarshal(pbuf.Payload, &req)
	if err != nil {
		return err
	}
	if req.Sum != req.CalcSum(s.password) {
		return errors.New("invalid checksum detected")
	}

	ip, err := s.GetIP(req.Domain)
	if err != nil {
		return err
	}
	defer s.FreeIP(ip)

	ctx := NewContext(req.Domain, req.Mac, ip, pc)

	err = s.AttachCtx(ctx)
	if err != nil {
		return err
	}
	defer s.DetachCtx(ctx)

	// response
	var resp Response
	resp.IP = ip
	respBuf, err := json.Marshal(&resp)
	if err != nil {
		return err
	}

	pbuf.SetPayload(respBuf)
	err = pc.WriteBuffer(pbuf)
	if err != nil {
		return err
	}

	start := time.Now()
	pbufChan := make(chan *packet.Buffer, 128)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		s.routeLoop(ctx, pbufChan) // close(pbufChan) to stop this loop
		log.Printf("node route loop stopped. domain='%v' id='%v'\n", ctx.Domain, ctx.id)
		wg.Done()
	}()

	log.Printf("node connected. domain='%v' id='%v'\n", req.Domain, ctx.id)

	// read loop
	for {
		pbuf, err = pc.ReadBuffer()
		if err != nil {
			log.Printf("node read loop stopped. domain='%v' id='%v'\n", req.Domain, ctx.id)
			break
		}

		pbufChan <- pbuf
	}
	close(pbufChan)
	ctx.Conn.Close()

	wg.Wait()
	log.Printf("node disconnected. domain='%v' id='%v' dur='%v'\n", req.Domain, ctx.id,
		time.Since(start).Round(time.Second))
	return nil
}

func (s *Server) routeLoop(ctx *Context, pbufChan <-chan *packet.Buffer) {
	for pbuf := range pbufChan {
		if pbuf.Cmd() == packet.CmdAlive {
			continue
		}
		if pbuf.Cmd() == packet.CmdOpenStream {
			go s.ResolveOpenCmd(ctx, pbuf)
			continue
		}

		s.RouteBuffer(pbuf)
	}
	ctx.Conn.Close()
}
