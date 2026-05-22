#ifndef HELPER_DYLD_PATCH_H
#define HELPER_DYLD_PATCH_H

#include "mach_compat.h"

#include <mach-o/dyld_images.h>

// Patch dyld in the target task so cross-OS bind-fail and platform-binary
// cohesion don't terminate the process before the dump pass can run.
//
// Three concurrent passes:
//   forces — rewrite resolveSymbol's CBZ-Wn weakImport check → NOP, so
//            missing symbols always take the bind-to-NULL branch.
//   kills  — NOP `movz x16,#N; svc #0x80` for process-killing syscalls.
//   skips  — rewrite `bl <halt>; brk #1` halt callers to `b +8` so dyld
//            falls through the noreturn marker. Only fires when forces>0
//            (false-positive risk on non-cross-OS apps).
//
// Returns total patches applied. Sets g_dyld_force_weak_active iff forces>0
// (gates run_and_suspend's fault recovery loop).
int dyld_patch_apply(task_t task,
                     struct dyld_image_info *imgs,
                     uint32_t img_count,
                     mach_vm_address_t target_libdyld_fb,
                     mach_vm_address_t helper_libdyld_fb);

// Revert patches whose category matches `cat_mask`. Bits:
//   0x1 = force_weak (resolveSymbol CBZ→NOP)
//   0x2 = kill_svc   (movz x16,#N; svc → NOP NOP)
//   0x4 = skip_halt  (bl halt; brk #1 → b +8) — main land-mine source
//   0x8 = abort sym  (abort/abort_with_payload entry → ret)
//
// Typical call after dyld.settled: revert skip_halt + abort_sym to
// stop those land mines from poisoning subsequent dlopen calls in the
// inject phase. Keep force_weak + kill_svc so dyld bind doesn't die
// on the genuinely-missing iOS17-on-iOS16 symbols.
int dyld_patch_revert(task_t task, int cat_mask);

// Locate every static-VA os_unfair_lock_t that dyld's lock primitives
// (os_unfair_lock_lock, _trylock, _unlock, recursive variants) are
// invoked on. Scans dyld __TEXT for functions matching the lock body
// signature (MRS TPIDRRO_EL0 + LDAXR/LDXR on [X0] + STXR), then counts
// ADRP+ADD-x0 setups before BLs targeting those functions. Each X0
// target VA is a candidate lock that may be held across a failed
// dlopen and needs resetting to unblock the next dlopen.
//
// Writes up to `max` VAs into `out`, returns count written. 0 means
// scan found no signal (cross-version: at minimum the global API lock
// always shows up since iOS 12).
int dyld_find_api_locks(task_t task, mach_vm_address_t *out, int max);

#endif // HELPER_DYLD_PATCH_H
