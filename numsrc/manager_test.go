package numsrc

import "testing"

func TestManagerNormalUseCase(t *testing.T) {
	m, err := NewManager(0, 1000, 65535)
	if err != nil {
		t.Error(err)
		return
	}

	port1, err := m.GetFreeNumberSrc()
	if err != nil {
		t.Error(err)
		return
	}
	if port1 != 1000 {
		t.Errorf("expect 1000 but get %v\n", port1)
		return
	}

	port2, err := m.GetFreeNumberSrc()
	if err != nil {
		t.Error(err)
		return
	}
	if port2 != 1001 {
		t.Errorf("expected 1001 but get %v\n", port2)
		return
	}

	err = m.ReleaseNumberSrc(port1)
	if err != nil {
		t.Error(err)
		return
	}
	err = m.ReleaseNumberSrc(port2)
	if err != nil {
		t.Error(err)
		return
	}

	port3 := uint16(1002)
	err = m.GetNumberSrc(port3)
	if err != nil {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	port4, _ := m.GetFreeNumberSrc()
	if port4 != 1003 {
		t.Errorf("expected 1003 but get %v\n", port4)
		return
	}
}

func TestErrInvalidRange(t *testing.T) {
	_, err := NewManager(100, 100, 200)
	if err != nil {
		t.Error(err)
		return
	}
	_, err = NewManager(100, 200, 200)
	if err == nil {
		t.Error("unexpected nil err")
		return
	}
	_, err = NewManager(100, 99, 200)
	if err == nil {
		t.Error("unexpected nil err")
		return
	}
	_, err = NewManager(100, 201, 200)
	if err == nil {
		t.Error("unexpected nil err")
		return
	}
	_, err = NewManager(100, 99, 101)
	if err == nil {
		t.Error("unexpected nil err")
		return
	}

	m, _ := NewManager(0, 90, 100)

	// 最大可取值
	err = m.GetNumberSrc(100)
	if err != nil {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// 越界
	err = m.GetNumberSrc(101)
	if err != ErrInvalidNumberRange {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	err = m.ReleaseNumberSrc(101)
	if err != ErrInvalidNumberRange {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
}

func TestErrFreePortChClosed(t *testing.T) {
	m, _ := NewManager(0, 99, 100)

	// 错误条件一：主动关闭chan（理论上不可能出现）
	close(m.freeChan)

	// 错误条件二：且当前chan为空
	_, err := m.GetFreeNumberSrc()
	if err != nil {
		t.Error(err)
		return
	}

	// 报错
	_, err = m.GetFreeNumberSrc()
	if err != ErrFreeNumberChClosed {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
}

func TestErrGetFreePortTimeout(t *testing.T) {
	m, _ := NewManager(0, 99, 100)

	// 错误条件一：且当前chan为空，且持续1秒钟没有补充
	_, err := m.GetFreeNumberSrc()
	if err != nil {
		t.Error(err)
		return
	}

	// 报错
	_, err = m.GetFreeNumberSrc()
	if err != ErrGetFreeNumberTimeout {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
}

func TestErrPortStateNotFound(t *testing.T) {
	m, _ := NewManager(0, 90, 100)
	err := m.ReleaseNumberSrc(10)
	if err != ErrNumberStateNotFound {
		t.Error("unexpected err")
		return
	}
}

func TestErrInvalidPortState(t *testing.T) {
	m, _ := NewManager(0, 90, 100)
	m.GetNumberSrc(80)
	err := m.ReleaseNumberSrc(80)
	if err != nil {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
	err = m.ReleaseNumberSrc(80)
	if err != ErrInvalidNumberState {
		t.Error("unexpected err")
		return
	}
}

func TestErrFreePortsChOverflow(t *testing.T) {
	// 正常的代码逻辑下应该不会出现这个错误
	// 在像freeChan送入数据前的状态检查应该能够保证
	// 此处为认为修改freeChan后出现的错误
	m, _ := NewManager(0, 90, 100)
	port, _ := m.GetFreeNumberSrc()
	m.freeChan <- port
	err := m.ReleaseNumberSrc(port)
	if err != ErrFreeNumberChOverflow {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
}
