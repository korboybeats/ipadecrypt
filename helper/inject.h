#ifndef HELPER_INJECT_H
#define HELPER_INJECT_H

#include "mach_compat.h"
#include "macho.h"

#include <mach-o/dyld_images.h>

// Hijack one already-suspended thread to run `dlopen(path, RTLD_NOW)` in
// the target. Recursively walks `bundle_src` for every encrypted Mach-O
// the target hasn't already mapped, dlopens it, then enumerates the
// image list to find its new base. dyld's set_code_unprotect goes
// through AMFI as a platform-binary call which iOS 15+ accepts.
//
// Returns count of images successfully decrypted+written under bundle_dst.
int inject_missing_frameworks(task_t task, mach_port_t exc,
                              const char *bundle_src,
                              const char *bundle_dst,
                              struct dyld_image_info *imgs,
                              uint32_t img_count,
                              const runtime_image_t *runtime);

#endif // HELPER_INJECT_H
