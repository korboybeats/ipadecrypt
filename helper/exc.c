#include "exc.h"
#include "log.h"

#include <string.h>

mach_msg_header_t g_pending_exc_hdr;
int               g_pending_exc_valid  = 0;
thread_act_t      g_pending_exc_thread = MACH_PORT_NULL;
int               g_dyld_force_weak_active = 0;

mach_port_t exc_make_port(task_t task) {
    mach_port_t port = MACH_PORT_NULL;
    if (mach_port_allocate(mach_task_self(), MACH_PORT_RIGHT_RECEIVE, &port) != KERN_SUCCESS) return MACH_PORT_NULL;
    if (mach_port_insert_right(mach_task_self(), port, port, MACH_MSG_TYPE_MAKE_SEND) != KERN_SUCCESS) {
        mach_port_mod_refs(mach_task_self(), port, MACH_PORT_RIGHT_RECEIVE, -1);
        return MACH_PORT_NULL;
    }
    // EXCEPTION_STATE_IDENTITY (vs EXCEPTION_DEFAULT) sends thread state
    // in the message and lets us reply with new state. iOS 16's
    // EXCEPTION_DEFAULT-flavor reply silently drops thread_set_state
    // changes for EXC_CRASH'd threads (verified empirically on Swift
    // Playgrounds); STATE_IDENTITY forces the kernel to apply our new
    // state on exception-handled resume.
    if (task_set_exception_ports(task,
        EXC_MASK_CRASH | EXC_MASK_BAD_ACCESS | EXC_MASK_BAD_INSTRUCTION |
        EXC_MASK_SOFTWARE | EXC_MASK_ARITHMETIC | EXC_MASK_BREAKPOINT |
        EXC_MASK_GUARD,
        port, EXCEPTION_STATE_IDENTITY, ARM_THREAD_STATE64) != KERN_SUCCESS) {
        mach_port_mod_refs(mach_task_self(), port, MACH_PORT_RIGHT_RECEIVE, -1);
        return MACH_PORT_NULL;
    }
    return port;
}

const char *exc_tag(int exc) {
    switch (exc) {
    case EXC_BAD_ACCESS:      return "EXC_BAD_ACCESS";
    case EXC_BAD_INSTRUCTION: return "EXC_BAD_INSTRUCTION";
    case EXC_ARITHMETIC:      return "EXC_ARITHMETIC";
    case EXC_EMULATION:       return "EXC_EMULATION";
    case EXC_SOFTWARE:        return "EXC_SOFTWARE";
    case EXC_BREAKPOINT:      return "EXC_BREAKPOINT";
    case EXC_SYSCALL:         return "EXC_SYSCALL";
    case EXC_MACH_SYSCALL:    return "EXC_MACH_SYSCALL";
    case EXC_RPC_ALERT:       return "EXC_RPC_ALERT";
    case EXC_CRASH:           return "EXC_CRASH";
    case EXC_RESOURCE:        return "EXC_RESOURCE";
    case EXC_GUARD:           return "EXC_GUARD";
    case EXC_CORPSE_NOTIFY:   return "EXC_CORPSE_NOTIFY";
    default:                  return "EXC_UNKNOWN";
    }
}

mach_msg_return_t exc_reply_with_state(const mach_msg_header_t *received_hdr,
                                       const arm_thread_state64_t *new_state) {
    if (!received_hdr || received_hdr->msgh_remote_port == MACH_PORT_NULL)
        return MACH_SEND_INVALID_DEST;
    struct {
        mach_msg_header_t hdr;
        char ndr[8];
        int32_t retcode;
        int32_t flavor;
        uint32_t state_count;
        uint32_t state_data[ARM_THREAD_STATE64_COUNT];
    } reply;
    memset(&reply, 0, sizeof(reply));
    reply.hdr.msgh_bits = MACH_MSGH_BITS(MACH_MSG_TYPE_MOVE_SEND_ONCE, 0);
    reply.hdr.msgh_remote_port = received_hdr->msgh_remote_port;
    reply.hdr.msgh_local_port = MACH_PORT_NULL;
    reply.hdr.msgh_id = received_hdr->msgh_id + 100;
    reply.retcode = KERN_SUCCESS;
    reply.flavor = ARM_THREAD_STATE64;
    if (new_state) {
        reply.state_count = ARM_THREAD_STATE64_COUNT;
        memcpy(reply.state_data, new_state, sizeof(arm_thread_state64_t));
        reply.hdr.msgh_size = (mach_msg_size_t)(
            sizeof(mach_msg_header_t) + 8 + 4 + 4 + 4 +
            sizeof(arm_thread_state64_t));
    } else {
        reply.state_count = 0;
        reply.hdr.msgh_size = (mach_msg_size_t)(
            sizeof(mach_msg_header_t) + 8 + 4 + 4 + 4);
    }
    return mach_msg(&reply.hdr, MACH_SEND_MSG, reply.hdr.msgh_size, 0,
             MACH_PORT_NULL, MACH_MSG_TIMEOUT_NONE, MACH_PORT_NULL);
}

int exc_decode(const void *buf, mach_msg_size_t buf_sz,
               int *exc_out, int64_t *code0_out,
               int64_t *code1_out, int *signal_out,
               arm_thread_state64_t *out_state) {
    if (buf_sz < 76) return 0;
    const uint8_t *p = (const uint8_t *)buf;
    mach_msg_header_t hdr;
    memcpy(&hdr, p, sizeof(hdr));
    if (hdr.msgh_id != 2401 && hdr.msgh_id != 2403) return 0;

    int exc; uint32_t code_cnt;
    memcpy(&exc, p + 60, sizeof(exc));
    memcpy(&code_cnt, p + 64, sizeof(code_cnt));

    int32_t c0 = 0, c1 = 0;
    if (code_cnt >= 1 && buf_sz >= 72) memcpy(&c0, p + 68, 4);
    if (code_cnt >= 2 && buf_sz >= 76) memcpy(&c1, p + 72, 4);
    *exc_out = exc;
    *code0_out = c0;
    *code1_out = c1;
    *signal_out = (exc == EXC_CRASH) ? ((c0 >> 24) & 0xff) : 0;

    if (hdr.msgh_id == 2403 && out_state) {
        size_t codes_end = 68 + (size_t)code_cnt * 4;
        if (buf_sz >= codes_end + 8 + sizeof(arm_thread_state64_t)) {
            memcpy(out_state, p + codes_end + 8, sizeof(arm_thread_state64_t));
        }
    }
    return 1;
}

thread_act_t exc_extract_thread(const void *buf, mach_msg_size_t buf_sz) {
    if (buf_sz < 32) return MACH_PORT_NULL;
    thread_act_t t = MACH_PORT_NULL;
    memcpy(&t, (const uint8_t *)buf + 28, sizeof(t));
    return t;
}

void exc_release_msg_ports(const void *buf, mach_msg_size_t buf_sz) {
    if (buf_sz < 44) return;
    mach_port_t task_port = MACH_PORT_NULL;
    memcpy(&task_port, (const uint8_t *)buf + 40, sizeof(task_port));
    if (task_port != MACH_PORT_NULL)
        mach_port_deallocate(mach_task_self(), task_port);
}

void exc_clear_pending(void) {
    if (g_pending_exc_thread != MACH_PORT_NULL) {
        mach_port_deallocate(mach_task_self(), g_pending_exc_thread);
        g_pending_exc_thread = MACH_PORT_NULL;
    }
    g_pending_exc_valid = 0;
}
