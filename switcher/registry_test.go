package switcher

import (
	"log"
	"testing"
	"time"

	"github.com/net-agent/flex/v2/handshake"
	"github.com/net-agent/flex/v2/numsrc"
	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/vars"
)

func TestAttachCtx(t *testing.T) {
	s := NewServer("", nil, nil)
	ctx := NewContext(1, nil, "test1", "", nil)

	// 测试分支：正确处理
	err := s.registry.attach(ctx)
	if err != nil {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// 测试分支：替换旧的ctx（还是会返回成功，但是会清理IP）
	err = s.registry.attach(ctx)
	if err != nil {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// 测试分支：domain不同但是ip相同
	ctx2 := NewContext(2, nil, "test2", "", nil)
	err = s.registry.attach(ctx2)
	if err != nil {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// 模拟设置一个未被清理的ip地址（ip的分配是自增的，所以使用ctx2.IP+1来模拟）
	s.registry.ipMu.Lock()
	s.registry.ipIndex[ctx2.IP+1] = nil
	s.registry.ipMu.Unlock()
	ctx3 := NewContext(3, nil, "test3", "", nil)
	err = s.registry.attach(ctx3)
	if err != errContextIPExist {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// print
	table := s.registry.formatRecords()
	for index, row := range table {
		log.Printf("%v: %v\n", index, row)
	}
	<-time.After(time.Millisecond * 100)
	s.registry.detach(ctx2)
	s.registry.formatRecords()
}

func TestAttachErr_GetIP(t *testing.T) {
	s := NewServer("", nil, nil)
	s.registry.ipm, _ = numsrc.NewManager(0, 9, 10)
	var err error

	err = s.registry.attach(NewContext(1, nil, "test1", "", nil))
	if err != nil {
		t.Error(err)
		return
	}
	err = s.registry.attach(NewContext(2, nil, "test2", "", nil))
	if err != errGetFreeContextIPFailed {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
}

func TestGetContextByDomain(t *testing.T) {
	var err error
	s := NewServer("", nil, nil)

	// 错误分支：domain不正确
	_, err = s.registry.lookupByDomain("")
	if err != handshake.ErrInvalidDomain {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// 错误分支：domain不存在
	_, err = s.registry.lookupByDomain("testdomain1")
	if err != errDomainNotFound {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// 正确分支
	ctx := NewContext(1, nil, "testdomain3", "", nil)
	s.registry.domainMu.Lock()
	s.registry.domainIndex[ctx.Domain] = ctx
	s.registry.domainMu.Unlock()
	returnCtx, err := s.registry.lookupByDomain(ctx.Domain)
	if err != nil {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
	if returnCtx != ctx {
		t.Error("unexpected return value")
		return
	}
}

func TestGetContextByIP(t *testing.T) {
	var err error
	s := NewServer("", nil, nil)

	// 错误分支：invalid context ip
	_, err = s.registry.lookupByIP(vars.LocalIP)
	if err != errInvalidContextIP {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
	_, err = s.registry.lookupByIP(vars.SwitcherIP)
	if err != errInvalidContextIP {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// 错误分支：ip not found
	_, err = s.registry.lookupByIP(100)
	if err != errContextIPNotFound {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// 正确分支
	ctx := NewContext(1, nil, "testdomain", "", nil)
	ctx.IP = 200
	s.registry.ipMu.Lock()
	s.registry.ipIndex[ctx.IP] = ctx
	s.registry.ipMu.Unlock()
	returnCtx, err := s.registry.lookupByIP(ctx.IP)
	if err != nil {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
	if returnCtx != ctx {
		t.Error("unexpected return value")
		return
	}
}

func TestDetachNotAttached(t *testing.T) {
	s := NewServer("", nil, nil)
	ctx := NewContext(1, nil, "test", "", nil)
	// ctx 未 attach，detach 应该直接返回，不 panic
	s.registry.detach(ctx)
}

func TestDetachOwnershipMismatch(t *testing.T) {
	s := NewServer("", nil, nil)

	ctx1 := NewContext(1, nil, "test", "", nil)
	err := s.registry.attach(ctx1)
	if err != nil {
		t.Fatal(err)
	}

	// 用另一个 ctx 替换 domain 和 ip 槽位
	ctx2 := NewContext(2, nil, "other", "", nil)
	s.registry.domainMu.Lock()
	s.registry.domainIndex["test"] = ctx2
	s.registry.domainMu.Unlock()
	s.registry.ipMu.Lock()
	s.registry.ipIndex[ctx1.IP] = ctx2
	s.registry.ipMu.Unlock()

	// detach ctx1 — domain 和 ip 槽位已被 ctx2 占据，不应删除
	s.registry.detach(ctx1)

	s.registry.domainMu.Lock()
	if s.registry.domainIndex["test"] != ctx2 {
		t.Error("domain slot should still point to ctx2")
	}
	s.registry.domainMu.Unlock()

	s.registry.ipMu.Lock()
	if s.registry.ipIndex[ctx1.IP] != ctx2 {
		t.Error("ip slot should still point to ctx2")
	}
	s.registry.ipMu.Unlock()
}

func TestAppendRecordPruning(t *testing.T) {
	s := NewServer("", nil, nil)

	// 插入 10001 条已过期的 detached 记录
	oldTime := time.Now().Add(-48 * time.Hour)
	for i := 0; i < 10001; i++ {
		ctx := &Context{
			id:         i,
			Domain:     "old",
			DetachTime: oldTime,
			AttachTime: oldTime.Add(-time.Hour),
		}
		s.registry.records = append(s.registry.records, ctx)
	}

	// 插入一条仍在活跃的记录
	activeCtx := &Context{id: 99999, Domain: "active"}
	activeCtx.setAttached(true)
	s.registry.records = append(s.registry.records, activeCtx)

	// 再 append 一条新记录，触发裁剪
	newCtx := &Context{id: 100000, Domain: "new"}
	s.registry.appendRecord(newCtx)

	s.registry.recordsMu.Lock()
	count := len(s.registry.records)
	s.registry.recordsMu.Unlock()

	// 裁剪后应只保留 activeCtx + newCtx
	if count != 2 {
		t.Errorf("expected 2 records after pruning, got %v", count)
	}
}

func TestAcquireDomainRace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow acquireDomain race test")
	}

	s := NewServer("", nil, nil)

	// 创建 context A，使用真实 conn 使 ping 能写入但永远收不到回复（超时）
	connA, connB := packet.Pipe()
	ctxA := NewContext(1, connA, "test", "", nil)
	ctxA.IP = 10
	ctxA.setAttached(true)

	s.registry.domainMu.Lock()
	s.registry.domainIndex["test"] = ctxA
	s.registry.domainMu.Unlock()

	// connB 端只读不回复，使 ping 超时
	go func() {
		for {
			if _, err := connB.ReadBuffer(); err != nil {
				return
			}
		}
	}()

	// 在 ping 进行中，用 ctxC 替换 domain 槽位，制造竞态
	ctxC := &Context{id: 3, Domain: "test"}
	go func() {
		time.Sleep(100 * time.Millisecond)
		s.registry.domainMu.Lock()
		s.registry.domainIndex["test"] = ctxC
		s.registry.domainMu.Unlock()
	}()

	// ctxB 尝试获取 domain "test"
	ctxB := NewContext(2, nil, "test", "", nil)
	prev, ok := s.registry.acquireDomain(ctxB)

	if !ok {
		t.Error("expected acquireDomain to succeed")
	}
	// prev 应该是最初检查时的 ctxA
	if prev != ctxA {
		t.Errorf("expected prev=ctxA, got id=%v", prev.id)
	}
	// domain 槽位应该指向 ctxB
	s.registry.domainMu.Lock()
	current := s.registry.domainIndex["test"]
	s.registry.domainMu.Unlock()
	if current != ctxB {
		t.Error("expected domain to point to ctxB after race")
	}

	connA.Close()
	connB.Close()
}
