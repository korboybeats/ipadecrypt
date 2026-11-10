package unicorn

import (
	"context"
	"errors"
	"testing"
	"time"
)

// ARM64 RET (br x30).
var arm64Ret = []byte{0xc0, 0x03, 0x5f, 0xd6}

const (
	testPage     = uint64(0x10000)
	testHookAddr = testPage
	testReturn   = uint64(0x20000)
)

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	engine, err := New(context.Background())
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine
}

func mapRet(t *testing.T, e *Engine, addr uint64) {
	t.Helper()
	if err := e.MemMap(addr&^0xfff, 0x1000); err != nil {
		t.Fatalf("map %#x: %v", addr, err)
	}
	if err := e.MemWrite(addr, arm64Ret); err != nil {
		t.Fatalf("write ret at %#x: %v", addr, err)
	}
}

func TestSaveRestoreContext(t *testing.T) {
	e := newTestEngine(t)
	if err := e.RegWrite(RegX0, 0x1111); err != nil {
		t.Fatal(err)
	}
	ctx, err := e.SaveContext()
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	defer func() { _ = ctx.Close() }()

	if err := e.RegWrite(RegX0, 0x2222); err != nil {
		t.Fatal(err)
	}
	if err := e.RestoreContext(ctx); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := e.RegRead(RegX0)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0x1111 {
		t.Fatalf("X0 after restore: got %#x, want 0x1111", got)
	}
}

func TestDeferredNestedStartDoesNotRunInsideHook(t *testing.T) {
	outer := newTestEngine(t)
	inner := newTestEngine(t)

	mapRet(t, outer, testHookAddr)
	mapRet(t, outer, testReturn)
	mapRet(t, inner, testPage)

	var innerRan, hookEntered, workRan bool
	var hookDepthDuringInner int

	_, err := outer.AddCodeHook(testHookAddr, testHookAddr, func(uint64, uint32) {
		hookEntered = true
		if err := outer.DeferAndStop(func() error {
			workRan = true
			// Frames of the outer start have not been popped yet, but
			// uc_emu_start has returned, so starting another engine is safe.
			if err := inner.RegWrite(RegLR, testPage+4); err != nil {
				return err
			}
			if err := inner.RegWrite(RegSP, testPage+0x800); err != nil {
				return err
			}
			if err := inner.StartBounded(testPage, testPage+4, time.Second, 0); err != nil {
				return err
			}
			innerRan = true
			if err := outer.RegWrite(RegX0, 0x42); err != nil {
				return err
			}
			lr, err := outer.RegRead(RegLR)
			if err != nil {
				return err
			}
			return outer.RegWrite(RegPC, lr)
		}); err != nil {
			t.Errorf("DeferAndStop: %v", err)
		}
	})
	if err != nil {
		t.Fatalf("hook: %v", err)
	}

	if err := outer.RegWrite(RegLR, testReturn); err != nil {
		t.Fatal(err)
	}
	if err := outer.RegWrite(RegSP, testPage+0x800); err != nil {
		t.Fatal(err)
	}

	_ = hookDepthDuringInner
	if err := outer.StartBounded(testHookAddr, testReturn, 2*time.Second, 0); err != nil {
		t.Fatalf("outer start: %v", err)
	}
	if !hookEntered || !workRan || !innerRan {
		t.Fatalf("expected hook, deferred work, and inner start; hook=%v work=%v inner=%v", hookEntered, workRan, innerRan)
	}
	got, err := outer.RegRead(RegX0)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0x42 {
		t.Fatalf("X0 after resume: got %#x, want 0x42", got)
	}
	pc, err := outer.RegRead(RegPC)
	if err != nil {
		t.Fatal(err)
	}
	if pc != testReturn {
		t.Fatalf("PC after resume: got %#x, want %#x", pc, testReturn)
	}
}

func TestInvokeContextIsolatesNestedCalls(t *testing.T) {
	e := newTestEngine(t)
	if err := e.RegWrite(RegX0, 0xaaaa); err != nil {
		t.Fatal(err)
	}
	if err := e.RegWrite(RegX1, 0xbbbb); err != nil {
		t.Fatal(err)
	}
	ctx, err := e.SaveContext()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ctx.Close() }()

	// Simulate a nested invoke overwriting argument registers.
	if err := e.RegWrite(RegX0, 1); err != nil {
		t.Fatal(err)
	}
	if err := e.RegWrite(RegX1, 2); err != nil {
		t.Fatal(err)
	}
	if err := e.RestoreContext(ctx); err != nil {
		t.Fatal(err)
	}
	x0, _ := e.RegRead(RegX0)
	x1, _ := e.RegRead(RegX1)
	if x0 != 0xaaaa || x1 != 0xbbbb {
		t.Fatalf("registers after nested call restore: X0=%#x X1=%#x", x0, x1)
	}
}

func TestStartBoundedFromHookIsRejected(t *testing.T) {
	outer := newTestEngine(t)
	inner := newTestEngine(t)
	mapRet(t, outer, testHookAddr)
	mapRet(t, inner, testPage)
	if err := outer.RegWrite(RegLR, testReturn); err != nil {
		t.Fatal(err)
	}
	if err := outer.RegWrite(RegSP, testPage+0x800); err != nil {
		t.Fatal(err)
	}

	var nested error
	_, err := outer.AddCodeHook(testHookAddr, testHookAddr, func(uint64, uint32) {
		nested = inner.StartBounded(testPage, testPage+4, time.Second, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := outer.StartBounded(testHookAddr, testReturn, 2*time.Second, 0); err != nil {
		t.Fatalf("outer start: %v", err)
	}
	if !errors.Is(nested, ErrNestedStart) {
		t.Fatalf("nested start error = %v, want %v", nested, ErrNestedStart)
	}
}
