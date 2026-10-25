/* Created by IPA Decryption bot */
/*
 * Force-included into every Unicorn translation unit for iOS builds.
 *
 * Unicorn 2.x has no interpreter, so TCG must execute generated code. iOS
 * does not export pthread_jit_write_protect_np, and RWX pages are never
 * executable. Instead we keep the code buffer W^X and flip it with mprotect,
 * which iOS permits once the process is marked CS_DEBUGGED (done through
 * Dopamine's jbctl on first allocation, see ipd_ios_jit.c).
 *
 * HAVE_PTHREAD_JIT_PROTECT is forced on so Unicorn's existing Apple-silicon
 * write/execute switching points call into our replacement.
 */
#ifndef IPD_IOS_JIT_H
#define IPD_IOS_JIT_H

#include <pthread.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif
void ipd_jit_write_protect_np(int enabled);
int ipd_jit_register(void *buf, size_t size);
void ipd_jit_unregister(void *buf);
#ifdef __cplusplus
}
#endif

#undef pthread_jit_write_protect_np
#define pthread_jit_write_protect_np(e) ipd_jit_write_protect_np(e)
#undef pthread_jit_write_protect_supported_np
#define pthread_jit_write_protect_supported_np() 0

#ifndef HAVE_PTHREAD_JIT_PROTECT
#define HAVE_PTHREAD_JIT_PROTECT 1
#endif

#endif
