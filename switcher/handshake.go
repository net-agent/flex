package switcher

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/vars"
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
	h.Write([]byte(fmt.Sprintf("CalcSumStart,%v,%v,%v,%v,CalcSumEnd", req.Domain, req.Mac, password, req.Timestamp)))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

type Response struct {
	ErrCode int
	ErrMsg  string
	IP      uint16
}

// ServeConn
func (s *Server) ServeConn(pc packet.Conn) (retErr error) {
	defer func() {
		if retErr != nil {
			var resp Response
			resp.ErrCode = -1
			resp.ErrMsg = retErr.Error()
			resp.IP = 0
			data, err := json.Marshal(&resp)
			if err == nil {
				pbuf := packet.NewBuffer(nil)
				pbuf.SetPayload(data)
				pc.WriteBuffer(pbuf)
			}
		}
		pc.Close()
	}()

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

	req.Domain = strings.ToLower(req.Domain)
	if req.Domain == "" || strings.HasPrefix(req.Domain, "local") {
		return errors.New("invalid domain")
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
		log.Printf("node handshake failed, WriteBuffer err: %v\n", err)
		return nil
	}

	s.serve(ctx)
	return nil
}

func (s *Server) serve(ctx *Context) {
	start := time.Now()
	pbufChan := make(chan *packet.Buffer, 128)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		s.routeLoop(ctx, pbufChan) // close(pbufChan) to stop this loop
		log.Printf("node route loop stopped. domain='%v' id='%v'\n", ctx.Domain, ctx.id)
		wg.Done()
	}()

	// read loop
	log.Printf("node connected. domain='%v' id='%v'\n", ctx.Domain, ctx.id)
	for {
		pbuf, err := ctx.Conn.ReadBuffer()
		if err != nil {
			log.Printf("node read loop stopped. domain='%v' id='%v'\n", ctx.Domain, ctx.id)
			break
		}

		ctx.LastReadTime = time.Now()
		pbufChan <- pbuf
	}
	close(pbufChan)
	ctx.Conn.Close()

	wg.Wait()
	log.Printf("node disconnected. domain='%v' id='%v' dur='%v'\n", ctx.Domain, ctx.id,
		time.Since(start).Round(time.Second))
}

func (s *Server) routeLoop(ctx *Context, pbufChan <-chan *packet.Buffer) {

	for pbuf := range pbufChan {
		if pbuf.SrcIP() == vars.SwitcherIP {
			go s.handlePacket(ctx, pbuf)
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

func (s *Server) handlePacket(ctx *Context, pbuf *packet.Buffer) {
	beatBuf := packet.NewBuffer(nil)
	beatBuf.SetCmd(packet.CmdAlive | packet.CmdACKFlag)

	if pbuf.Cmd() == packet.CmdAlive {
		ctx.WriteBuffer(beatBuf)
		return
	}

	if pbuf.Cmd() == (packet.CmdAlive | packet.CmdACKFlag) {
		it, found := ctx.pingBack.Load(pbuf.DistPort())
		if !found {
			return
		}
		ch, ok := it.(chan error)
		if !ok {
			return
		}
		ch <- nil
		return
	}
}
