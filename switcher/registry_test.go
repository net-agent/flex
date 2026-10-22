package switcher

import (
	"log"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/internal/admit"
	"github.com/net-agent/flex/v3/internal/idpool"
	"github.com/net-agent/flex/v3/packet"
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

	// 冲突失败同样回滚域名认领
	if _, err := s.registry.lookupByDomain("test3"); err != errDomainNotFound {
		t.Errorf("expected domain claim rolled back, got %v", err)
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
	s.registry.ipm, _ = idpool.New(9, 9)
	var err error

	err = s.registry.attach(NewContext(1, nil, "test1", "", nil))
	if err != nil {
		t.Error(err)
		return
	}
	ctx2 := NewContext(2, nil, "test2", "", nil)
	err = s.registry.attach(ctx2)
	if err != errGetFreeContextIPFailed {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// 失败回滚：域名认领被撤销，不留下僵尸占位
	if _, err := s.registry.lookupByDomain("test2"); err != errDomainNotFound {
		t.Errorf("expected domain claim rolled back, got %v", err)
	}
	if ctx2.isAttached() {
		t.Error("failed ctx should not stay attached")
	}
}

func TestGetContextByDomain(t *testing.T) {
	var err error
	s := NewServer("", nil, nil)

	// 错误分支：domain不正确
	_, err = s.registry.lookupByDomain("")
	if err != admit.ErrInvalidDomain {
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
	_, err = s.registry.lookupByIP(packet.LocalIP)
	if err != errInvalidContextIP {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
	_, err = s.registry.lookupByIP(packet.SwitcherIP)
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

// 域名在 ping 探测期间被第三方接管时，本次 attach 必须放弃而不是覆盖新持有者
// （旧实现会在 else 分支直接覆盖域名槽位，并泄漏被替换 ctx 的 ipIndex 条目）。
func TestAttachRace_DomainTakenDuringProbe(t *testing.T) {
	s := NewServer("", nil, nil)
	s.registry.domainPingTimeout = 100 * time.Millisecond

	// ctxA 持有域名但不应答 ping（net.Pipe 对端不读，写阻塞至超时）
	connA, connB := packet.Pipe()
	defer connA.Close()
	defer connB.Close()
	ctxA := NewContext(1, connA, "test", "", nil)
	ctxA.IP = 10
	ctxA.setAttached(true)
	s.registry.domainMu.Lock()
	s.registry.domainIndex["test"] = ctxA
	s.registry.domainMu.Unlock()

	// 在 ping 进行中，用 ctxC 抢占域名槽位
	ctxC := NewContext(3, nil, "test", "", nil)
	ctxC.setAttached(true)
	go func() {
		time.Sleep(20 * time.Millisecond)
		s.registry.domainMu.Lock()
		s.registry.domainIndex["test"] = ctxC
		s.registry.domainMu.Unlock()
	}()

	ctxB := NewContext(2, nil, "test", "", nil)
	err := s.registry.attach(ctxB)
	if err != errReplaceDomainFailed {
		t.Fatalf("expected errReplaceDomainFailed, got %v", err)
	}

	s.registry.domainMu.Lock()
	current := s.registry.domainIndex["test"]
	s.registry.domainMu.Unlock()
	if current != ctxC {
		t.Error("domain slot must stay with the context that took it over")
	}
	if ctxB.isAttached() || ctxB.IP != 0 {
		t.Error("losing attach must not register the new context")
	}
}

// 回归：域名替换路径中 IP 曾被 detach 与 attach 各释放一次。
// 现在 IP 由租约持有，替换后池内占用数必须与活跃 ctx 数一致。
func TestAttachReplace_ReleasesIPOnce(t *testing.T) {
	s := NewServer("", nil, nil)
	s.registry.domainPingTimeout = 50 * time.Millisecond

	conn1, peer1 := packet.Pipe()
	defer peer1.Close()
	ctx1 := NewContext(1, conn1, "dup", "", nil)
	if err := s.registry.attach(ctx1); err != nil {
		t.Fatalf("attach ctx1 failed: %v", err)
	}
	ip1 := ctx1.IP

	// 让 ctx1 的存活探测失败
	ctx1.release()

	ctx2 := NewContext(2, nil, "dup", "", nil)
	if err := s.registry.attach(ctx2); err != nil {
		t.Fatalf("attach ctx2 failed: %v", err)
	}

	if got := s.registry.ipm.InUse(); got != 1 {
		t.Fatalf("expected exactly 1 IP in use after replace, got %d", got)
	}
	s.registry.ipMu.Lock()
	_, staleIP := s.registry.ipIndex[ip1]
	curIP := s.registry.ipIndex[ctx2.IP]
	s.registry.ipMu.Unlock()
	if staleIP {
		t.Error("replaced context must be removed from ipIndex")
	}
	if curIP != ctx2 {
		t.Error("new context must own its ipIndex slot")
	}
	s.registry.domainMu.Lock()
	curDomain := s.registry.domainIndex["dup"]
	s.registry.domainMu.Unlock()
	if curDomain != ctx2 {
		t.Error("domain slot must point to the new context")
	}
}
