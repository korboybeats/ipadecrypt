#ifndef HELPER_EXC_H
#define HELPER_EXC_H

#include <mach/mach.h>
#include <mach/arm/thread_status.h>
#include <stdint.h>

// Single-slot stash for the most recent received Mach exception. The
// kernel holds the faulting thread in MACH_RECV_REPLY until reply is
// sent against this message; the inject path needs to know the thread
// to coordinate hijack vs. brk delivery races.
extern mach_msg_header_t g_pending_exc_hdr;
extern int               g_pending_exc_valid;
extern thread_act_t      g_pending_exc_thread;

// Set non-zero when scan_dyld_for_abort_syscalls applied >0 force-weak
// patches to dyld's resolveSymbol. Gates the BAD_ACCESS fault-skip
// loop in run_and_suspend.
extern int g_dyld_force_weak_active;

// Set up a Mach exception port that catches BAD_ACCESS / BAD_INSTRUCTION
// / SOFTWARE / ARITHMETIC / BREAKPOINT / GUARD / CRASH. Uses
// EXCEPTION_STATE_IDENTITY so replies can ship new thread state inline.
mach_port_t exc_make_port(task_t task);

// Map exception_type_t to a short stable tag.
const char *exc_tag(int exc);

// Reply to a received EXCEPTION_STATE_IDENTITY mach_msg. Passing
// new_state=NULL releases the thread without changing its state.
mach_msg_return_t exc_reply_with_state(const mach_msg_header_t *received_hdr,
                                       const arm_thread_state64_t *new_state);

// Decode a received exception_raise message. Returns 1 if parsed. Codes
// arrive as 4-byte values because we don't set MACH_EXCEPTION_CODES; use
// the inline thread state's __pc / __far for full 64-bit fault addresses.
int exc_decode(const void *buf, mach_msg_size_t buf_sz,
               int *exc_out, int64_t *code0_out,
               int64_t *code1_out, int *signal_out,
               arm_thread_state64_t *out_state);

// Extract the thread port from an exception_raise message body.
thread_act_t exc_extract_thread(const void *buf, mach_msg_size_t buf_sz);

// Drop the task port send right (descriptor at offset 40) the kernel
// transferred to us with the exception message; we never need it.
void exc_release_msg_ports(const void *buf, mach_msg_size_t buf_sz);

// Drop the pending thread's send right and clear the stash flag.
void exc_clear_pending(void);

#endif // HELPER_EXC_H
