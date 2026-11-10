//go:build ios

package unicorn

import (
	"sync"

	"github.com/ebitengine/purego"
)

// ipd_jit_write_protect_np is provided by the statically linked iOS JIT
// adapter. enabled==0 makes every registered TCG buffer writable (RW);
// enabled!=0 makes them executable (RX). Teardown must switch to RW first:
// uc_hook_del / uc_close can patch or free those pages, and writing an RX
// page is a SIGBUS on iOS.
var (
	jitProtectOnce  sync.Once
	jitWriteProtect func(int32)
)

func loadJITWriteProtect() {
	symbol, err := purego.Dlsym(purego.RTLD_DEFAULT, "ipd_jit_write_protect_np")
	if err != nil {
		return
	}
	purego.RegisterFunc(&jitWriteProtect, symbol)
}

func allowJITWrite() {
	jitProtectOnce.Do(loadJITWriteProtect)
	if jitWriteProtect != nil {
		jitWriteProtect(0)
	}
}
