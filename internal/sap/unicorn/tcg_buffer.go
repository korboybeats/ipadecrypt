package unicorn

import "fmt"

func (e *Engine) setTCGBufferSize(size uint32) error {
	if size == 0 {
		return fmt.Errorf("unicorn TCG buffer size must be positive")
	}
	handle, done, err := e.beginOperation()
	if err != nil {
		return err
	}
	defer done()

	if err := e.err(e.api.ctl(handle, controlTCGBufferSizeWrite, size)); err != nil {
		return fmt.Errorf("set unicorn TCG buffer size to %d: %w", size, err)
	}
	return nil
}
