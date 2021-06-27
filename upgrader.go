package flex

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"regexp"
	"sync/atomic"
	"time"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

var randDomainIndex int32
var ErrReconnected = errors.New("reconnected")
var ErrCtxidNotFound = errors.New("ctxid not found")

func RandDomain() string {
	return fmt.Sprintf("rnd_%x_%v", rand.Int63(), atomic.AddInt32(&randDomainIndex, 1))
}

type HostRequest struct {
	Domain string
	Mac    string
	Ctxid  uint64
}

type HostResponse struct {
	IP HostIP
}

func UpgradeToHost(pc *PacketConn, req *HostRequest, autoRun bool) (retHost *Host, retCtxid uint64, retErr error) {
	var pb = NewPacketBufs()
	//
	// send request
	//
	data, err := json.Marshal(req)
	if err != nil {
		return nil, 0, err
	}
	pb.SetPayload(data)
	err = pc.WritePacket(pb)
	if err != nil {
		return nil, 0, err
	}

	err = pc.ReadPacket(pb)
	if err != nil {
		return nil, 0, err
	}
	if len(pb.payload) != 10 {
		return nil, 0, errors.New("unexpected resp size")
	}

	ip := binary.BigEndian.Uint16(pb.payload[0:2])
	ctxid := binary.BigEndian.Uint64(pb.payload[2:10])

	// 如果ctxid为0，代表是重连成功。否则代表新创建了ctxid
	if ctxid == 0 {
		return nil, req.Ctxid, ErrReconnected
	}

	if autoRun {
		return NewHostAndRun(pc, ip), ctxid, nil
	}
	return NewHost(pc, ip), ctxid, nil

}

// UpgradeToContext 把连接升级为Host，并返回对端HostIP
func (switcher *Switcher) UpgradeToContext(pc *PacketConn) (retCtx *SwContext, retErr error) {
	pb := NewPacketBufs()
	err := pc.ReadPacket(pb)
	if err != nil {
		return nil, err
	}

	var req HostRequest
	err = json.Unmarshal(pb.payload, &req)
	if err != nil {
		return nil, err
	}

	var respIP HostIP
	var respCtxid uint64
	var respCtx *SwContext

	// 尝试执行恢复逻辑
	oldctx, respErr := switcher.RecoverCtx(&req, pc)

	switch respErr {
	case nil:
		respErr = ErrReconnected
		// 恢复成功，返回原有ctx
		respIP = oldctx.ip
		respCtxid = oldctx.id
		respCtx = oldctx

	case ErrCtxidNotFound:
		respErr = nil
		// 恢复失败，继续按照正常流程进行upgrade

		if req.Domain == "" {
			req.Domain = RandDomain()
		} else if !isValidDomain(req.Domain) {
			return nil, errors.New("invalid domain")
		}

		respIP, err = switcher.selectIP(req.Mac)
		if err != nil {
			return nil, err
		}
		respCtxid = switcher.NextCtxID()
		respCtx = &SwContext{
			host:           NewSwitcherHost(switcher, pc),
			id:             respCtxid,
			ip:             respIP,
			domain:         req.Domain,
			mac:            req.Mac,
			chanRecoverErr: make(chan error, 1),
		}

	default:
		// 恢复失败，出现意料之外的错误，终止upgrade
		return nil, respErr
	}

	//
	// send response
	//
	resp := make([]byte, 10)
	binary.BigEndian.PutUint16(resp[0:2], respIP)
	binary.BigEndian.PutUint64(resp[2:10], respCtxid)
	pb.SetPayload(resp)

	err = pc.WritePacket(pb)
	if err != nil {
		return nil, err
	}

	return respCtx, respErr
}

// RecoverCtx 根据ctxid和pconn，恢复正在等待重连的ctx
func (switcher *Switcher) RecoverCtx(req *HostRequest, pc *PacketConn) (*SwContext, error) {
	it, found := switcher.ctxids.LoadAndDelete(req.Ctxid)
	if !found {
		_, domainExist := switcher.domainCtxs.Load(req.Domain)
		if domainExist {
			return nil, errors.New("domain conflict")
		}
		return nil, ErrCtxidNotFound
	}

	ctx, ok := it.(*SwContext)
	if !ok {
		return nil, errors.New("convert to ctx failed")
	}

	err := ctx.host.Replace(pc)
	if err != nil {
		return nil, err
	}

	// 通知TryRecover协程
	select {
	case ctx.chanRecoverErr <- nil:
	default:
	}

	return ctx, nil
}

func isValidDomain(domain string) bool {
	re := regexp.MustCompile(`^[a-zA-Z]([a-zA-Z0-9_.]){2,255}[a-zA-Z0-9_]$`)
	return re.MatchString(domain)
}

func (sw *Switcher) NextCtxID() uint64 {
	r := rand.Uint32()
	i := atomic.AddUint32(&sw.ctxIndex, 1)

	return (uint64(r) << 32) | uint64(i)
}
