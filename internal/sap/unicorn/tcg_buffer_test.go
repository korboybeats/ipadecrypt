package unicorn

import (
	"testing"
	"time"
)

func startRet(t *testing.T, e *Engine) {
	t.Helper()
	mapRet(t, e, testPage)
	if err := e.RegWrite(RegLR, testPage+4); err != nil {
		t.Fatal(err)
	}
	if err := e.RegWrite(RegSP, testPage+0x800); err != nil {
		t.Fatal(err)
	}
	if err := e.StartBounded(testPage, testPage+4, time.Second, 0); err != nil {
		t.Fatal(err)
	}
}

func TestNewEngineInitializesWithCappedTCGBuffer(t *testing.T) {
	// If the 1 GiB default were still in use, four engines would already be
	// risky; this just checks a single engine still translates after the cap.
	startRet(t, newTestEngine(t))
}

func TestSequentialEnginesReuseProcessBudget(t *testing.T) {
	// OpenModernADI uses four engines; sign-in can repeat that in one process.
	for i := 0; i < 8; i++ {
		e := newTestEngine(t)
		startRet(t, e)
		if err := e.Close(); err != nil {
			t.Fatalf("iter %d close: %v", i, err)
		}
	}
}
