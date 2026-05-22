#ifndef HELPER_DUMP_H
#define HELPER_DUMP_H

#include "mach_compat.h"
#include "macho.h"

// dump_image outcomes. Negative values map to event=image.failed
// reason= attrs so users get actionable context instead of a silent skip.
typedef enum {
    DUMP_OK = 0,
    DUMP_OPEN_SRC_FAIL,
    DUMP_READ_SRC_FAIL,
    DUMP_VM_READ_FAIL,
    DUMP_ZERO_PAGES,
    DUMP_OPEN_DST_FAIL,
    DUMP_WRITE_DST_FAIL,
    DUMP_OOM,
} dump_result_t;

const char *dump_reason(dump_result_t r);

// Decrypt the matched slice into a thin Mach-O at `dst`. Fat siblings
// are dropped on the way out. For encrypted slices, verifies plaintext
// was actually returned (vs zero-fill or unchanged ciphertext) before
// committing the write  false-positive defense for iOS 15 Dopamine
// where FairPlay decrypt-on-fault doesn't fire reliably.
dump_result_t dump_image(const char *src, const char *dst, task_t task,
                         mach_vm_address_t image_base,
                         const selected_slice_t *sel);

#endif // HELPER_DUMP_H
