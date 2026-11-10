package unicorn

import "errors"

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
