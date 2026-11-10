package unicorn

import "fmt"

// Context is a saved register snapshot of an engine, used to nest host-side
// invocations on a paused engine without clobbering the outer call.
type Context struct {
	engine *Engine
	handle uintptr
}

// SaveContext captures the engine's current register state.
func (e *Engine) SaveContext() (*Context, error) {
	handle, done, err := e.beginOperation()
	if err != nil {
		return nil, err
	}
	defer done()

	var ctx uintptr
	if err := e.err(e.api.contextAlloc(handle, &ctx)); err != nil {
		return nil, fmt.Errorf("allocate unicorn context: %w", err)
	}
	if err := e.err(e.api.contextSave(handle, ctx)); err != nil {
		_ = e.err(e.api.contextFree(ctx))
		return nil, fmt.Errorf("save unicorn context: %w", err)
	}

	return &Context{engine: e, handle: ctx}, nil
}

// RestoreContext writes a previously saved snapshot back to the engine.
func (e *Engine) RestoreContext(ctx *Context) error {
	if ctx == nil || ctx.handle == 0 {
		return fmt.Errorf("unicorn context is nil")
	}
	if ctx.engine != e {
		return fmt.Errorf("unicorn context belongs to a different engine")
	}

	handle, done, err := e.beginOperation()
	if err != nil {
		return err
	}
	defer done()

	if err := e.err(e.api.contextRestore(handle, ctx.handle)); err != nil {
		return fmt.Errorf("restore unicorn context: %w", err)
	}
	return nil
}

// Close releases the native snapshot.
func (c *Context) Close() error {
	if c == nil || c.handle == 0 {
		return nil
	}
	engine := c.engine
	handle := c.handle
	c.handle = 0
	c.engine = nil
	if engine == nil {
		return nil
	}

	op, done, err := engine.beginOperation()
	if err != nil {
		return err
	}
	defer done()
	_ = op

	return engine.err(engine.api.contextFree(handle))
}
