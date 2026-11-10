//go:build !ios

package main

import "errors"

var errJITNotReady = errors.New("unicorn JIT precondition failed")

func ensureUnicornJITReady() error {
	return nil
}
