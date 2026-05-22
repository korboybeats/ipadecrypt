#ifndef HELPER_TARGET_H
#define HELPER_TARGET_H

#include "mach_compat.h"
#include "macho.h"

#include <mach-o/dyld_images.h>

// Walk target VM regions, find the first MH_EXECUTE base. Used to anchor
// the main app's load address before dyld populates aii.infoArray.
int target_find_main_base(task_t task, mach_vm_address_t *out);

// Read mach_header[_64] at a runtime base and capture its arch.
int target_read_runtime_image(task_t task, mach_vm_address_t base,
                              runtime_image_t *out);

// Enumerate images via TASK_DYLD_INFO. Returns heap-allocated infoArray
// + path strings buffer. Caller frees both. On error returns NULL.
struct dyld_image_info *target_list_images(task_t task,
                                           uint32_t *out_count,
                                           char **out_paths);

// Find an image in the target's image list by basename match against
// `helper_path`. Falls back to aii.dyldImageLoadAddress *only* when the
// caller is specifically asking for dyld (basename "dyld")  that
// fallback is the standalone loader's base and would be wrong for any
// other lookup (Dopamine sidecars dyld outside the shared cache, so
// libdyld and dyld differ in base).
mach_vm_address_t target_find_image(task_t task,
                                    struct dyld_image_info *imgs,
                                    uint32_t img_count,
                                    const char *helper_path);

// Slide a helper-linked function pointer into the target task. Returns
// 0 if the function's home dylib can't be located in the target.
mach_vm_address_t target_slide_func(task_t task,
                                    struct dyld_image_info *imgs,
                                    uint32_t img_count,
                                    void *helper_func,
                                    mach_vm_address_t target_libdyld_fb,
                                    mach_vm_address_t helper_libdyld_fb);

// Splice cryptsize plaintext bytes from `image_base+cryptoff` into a
// caller-supplied buffer at the slice's absolute file offset. Returns
// 0 on success, non-zero on VM read failure.
int target_vm_read_crypt(task_t task,
                         mach_vm_address_t image_base,
                         const mach_slice_t *slice,
                         uint8_t *buf,
                         char *err_msg, size_t err_cap);

// True iff the buffer holds any non-zero byte in [cryptoff, cryptoff+cryptsize).
int target_crypt_has_data(const uint8_t *buf, const mach_slice_t *slice);

// COW + write `bytes` at addr in target, then restore RX. Crosses one
// page boundary at most. Per-task because of VM_PROT_COPY semantics.
int target_patch_bytes(task_t task, mach_vm_address_t addr,
                       const void *bytes, size_t len, const char *tag);

#endif // HELPER_TARGET_H
