package unicorn

import (
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrNestedStart is returned when a hook callback starts another engine
	// on the same thread. Hooks must DeferAndStop the work instead so the
	// outer uc_emu_start can return first.
	ErrNestedStart = errors.New("nested unicorn start from a hook")

	hookMu    sync.Mutex
	hookStack []*Engine
)

func enterHook(e *Engine) {
	hookMu.Lock()
	hookStack = append(hookStack, e)
	hookMu.Unlock()
}

func leaveHook() {
	hookMu.Lock()
	if n := len(hookStack); n > 0 {
		hookStack = hookStack[:n-1]
	}
	hookMu.Unlock()
}

func hookEngine() *Engine {
	hookMu.Lock()
	defer hookMu.Unlock()
	if len(hookStack) == 0 {
		return nil
	}
	return hookStack[len(hookStack)-1]
}

func rejectNestedStart(inner *Engine) error {
	outer := hookEngine()
	if outer == nil {
		return nil
	}
	return fmt.Errorf("%w: inner=%p outer=%p", ErrNestedStart, inner, outer)
}

func (e *Engine) pushFrame() error {
	if e == nil {
		return errEngineClosed
	}

	e.stateMu.Lock()
	closed := e.closing || e.handle == 0
	e.stateMu.Unlock()
	if closed {
		return errEngineClosed
	}

	e.frameMu.Lock()
	defer e.frameMu.Unlock()
	e.frames = append(e.frames, &startFrame{})
	return nil
}

func (e *Engine) popFrame() {
	e.frameMu.Lock()
	defer e.frameMu.Unlock()
	if len(e.frames) == 0 {
		return
	}
	e.frames = e.frames[:len(e.frames)-1]
}

func (e *Engine) takeDeferred() (func() error, bool) {
	e.frameMu.Lock()
	defer e.frameMu.Unlock()
	if len(e.frames) == 0 {
		return nil, false
	}
	frame := e.frames[len(e.frames)-1]
	work := frame.deferred
	frame.deferred = nil
	return work, work != nil
}

// DeferAndStop requests that work run only after the current uc_emu_start
// returns, then stops the engine. Hooks must use this instead of starting
// another engine on the same thread. If this engine is not currently in
// uc_emu_start, work runs immediately.
func (e *Engine) DeferAndStop(work func() error) error {
	if e == nil {
		return errEngineClosed
	}
	if work == nil {
		return errors.New("deferred unicorn hook work is nil")
	}

	e.frameMu.Lock()
	if len(e.frames) == 0 {
		e.frameMu.Unlock()
		return work()
	}
	frame := e.frames[len(e.frames)-1]
	if frame.deferred != nil {
		e.frameMu.Unlock()
		return errors.New("unicorn hook already deferred work for this emulation")
	}
	frame.deferred = work
	e.frameMu.Unlock()

	return e.Stop()
}

// DeferAndStopThenReturn defers body, then resumes the outer guest at LR so a
// hook on a RET stub is not re-entered.
func (e *Engine) DeferAndStopThenReturn(body func() error) error {
	if body == nil {
		return errors.New("deferred unicorn hook work is nil")
	}
	return e.DeferAndStop(func() error {
		if err := body(); err != nil {
			return err
		}
		link, err := e.RegRead(RegLR)
		if err != nil {
			return fmt.Errorf("read hook return address: %w", err)
		}
		return e.RegWrite(RegPC, link)
	})
}
