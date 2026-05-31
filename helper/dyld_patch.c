#include "dyld_patch.h"
#include "exc.h"
#include "log.h"
#include "target.h"

#include <dlfcn.h>
#include <mach-o/loader.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

// Linear-scan LC_SYMTAB for a defined symbol; return its n_value (link-
// time VA) or 0 if absent. symtab is nlist_64[] (16 bytes each).
static uint64_t symtab_lookup(const uint8_t *symtab, uint32_t nsyms,
                              const char *strtab, uint32_t strsize,
                              const char *want) {
    size_t want_len = strlen(want);
    for (uint32_t i = 0; i < nsyms; i++) {
        uint32_t n_strx;
        uint8_t  n_type;
        uint64_t n_value;
        memcpy(&n_strx,  symtab + i * 16 + 0, 4);
        memcpy(&n_type,  symtab + i * 16 + 4, 1);
        memcpy(&n_value, symtab + i * 16 + 8, 8);
        if ((n_type & 0x0e) != 0x0e) continue;     // N_SECT only
        if (n_strx >= strsize) continue;
        const char *name = strtab + n_strx;
        if (strncmp(name, want, want_len) == 0 && name[want_len] == '\0')
            return n_value;
    }
    return 0;
}

// Neutralize every `bl <target_va>` call site in [text, text+text_len).
//   trailing brk #1  -> `b +8`  (skip call + noreturn marker)
//   otherwise        -> NOP the bl (fall through to the next real insn,
//                       which on iOS 15 codegen is the no-error path)
// text_base_va is the runtime VA of text[0]. Returns sites patched.
//
// Symbol-driven cross-OS bind survival, located via the target dyld's
// LC_SYMTAB. dyld 4's resolveSymbol records "Symbol not found" through
// Diagnostics::error (NOT noreturn); ret-patching its entry leaves diag
// empty so the caller's hasError() is false, no C++ throw, and
// resolveSymbol returns the default ResolvedSymbol (bindAbsolute, offset
// 0) - the clean cross-OS NULL bind. That single suppressor is sufficient:
// it severs the symbol-not-found path upstream of every downstream halt,
// so no call-site neutralization is needed (verified on Swift Playgrounds,
// 114/114 images, with only the two Diagnostics::error rets).
//
// All of iOS 15-16 ships dyld 4 - verified empirically on 15.1, 15.4.1
// and 15.8.1, arm64 and arm64e (Diagnostics::error present in every one).
// Absent symbols are skipped. Returns total patches; 0 means a stripped
// dyld (none ship), logged by the caller.
static int dyld_symtab_patch(task_t task, mach_vm_address_t dyld_base) {
    if (!dyld_base) return 0;

    struct mach_header_64 mh;
    mach_vm_size_t got = 0;
    if (mach_vm_read_overwrite(task, dyld_base, sizeof(mh),
            (mach_vm_address_t)(uintptr_t)&mh, &got) != KERN_SUCCESS ||
        got != sizeof(mh) || mh.magic != MH_MAGIC_64) {
        emit(LOG_DEBUG, "patch.symtab.no_header", NULL, NULL);
        return 0;
    }
    if (mh.sizeofcmds == 0 || mh.sizeofcmds > 0x100000) return 0;

    uint8_t *lc_buf = malloc(mh.sizeofcmds);
    if (!lc_buf) return 0;
    if (mach_vm_read_overwrite(task, dyld_base + sizeof(mh), mh.sizeofcmds,
            (mach_vm_address_t)(uintptr_t)lc_buf, &got) != KERN_SUCCESS ||
        got != mh.sizeofcmds) {
        free(lc_buf);
        return 0;
    }

    // Find __TEXT vmaddr (slide), __LINKEDIT (symtab/strtab VAs), and
    // LC_SYMTAB.
    uint64_t text_vmaddr = 0, le_vmaddr = 0, le_fileoff = 0;
    uint32_t symoff = 0, nsyms = 0, stroff = 0, strsize = 0;
    const uint8_t *p = lc_buf;
    const uint8_t *lc_end = lc_buf + mh.sizeofcmds;
    for (uint32_t i = 0; i < mh.ncmds; i++) {
        if (p + sizeof(struct load_command) > lc_end) break;
        struct load_command lc;
        memcpy(&lc, p, sizeof(lc));
        if (lc.cmdsize < sizeof(struct load_command) ||
            lc.cmdsize > (uint32_t)(lc_end - p)) break;
        if (lc.cmd == LC_SEGMENT_64 &&
            lc.cmdsize >= sizeof(struct segment_command_64)) {
            struct segment_command_64 sc;
            memcpy(&sc, p, sizeof(sc));
            if (strncmp(sc.segname, "__TEXT", 16) == 0) {
                text_vmaddr = sc.vmaddr;
            } else if (strncmp(sc.segname, "__LINKEDIT", 16) == 0) {
                le_vmaddr = sc.vmaddr;
                le_fileoff = sc.fileoff;
            }
        } else if (lc.cmd == 0x2 /* LC_SYMTAB */ && lc.cmdsize >= 24) {
            memcpy(&symoff,  p + 8,  4);
            memcpy(&nsyms,   p + 12, 4);
            memcpy(&stroff,  p + 16, 4);
            memcpy(&strsize, p + 20, 4);
        }
        p += lc.cmdsize;
    }
    free(lc_buf);

    if (nsyms == 0 || nsyms > 100000) {
        attrs_t a; attrs_init(&a);
        attrs_str(&a, "reason", "no_symtab");
        emit(LOG_DEBUG, "patch.symtab.skipped", &a, NULL);
        return 0;
    }
    if (le_vmaddr == 0 || strsize == 0 || strsize > 4 * 1024 * 1024) return 0;

    // File-offset within __LINKEDIT -> target VM address.
    //   slide        = dyld_base - text_vmaddr   (text_vmaddr is typically 0)
    //   file_off N   = le_vmaddr + slide + (N - le_fileoff)
    mach_vm_address_t slide     = dyld_base - text_vmaddr;
    mach_vm_address_t symtab_va = le_vmaddr + slide + (symoff - le_fileoff);
    mach_vm_address_t strtab_va = le_vmaddr + slide + (stroff - le_fileoff);

    size_t symtab_sz = (size_t)nsyms * 16;  // sizeof(struct nlist_64) == 16
    if (symtab_sz > 4 * 1024 * 1024) return 0;

    uint8_t *symtab = malloc(symtab_sz);
    char    *strtab = malloc((size_t)strsize + 1);
    if (!symtab || !strtab) { free(symtab); free(strtab); return 0; }

    const size_t chunk = 0x10000;
    int ok = 1;
    for (size_t off = 0; off < symtab_sz && ok; ) {
        size_t want = symtab_sz - off;
        if (want > chunk) want = chunk;
        mach_vm_size_t cgot = 0;
        if (mach_vm_read_overwrite(task, symtab_va + off, want,
                (mach_vm_address_t)(uintptr_t)(symtab + off), &cgot) != KERN_SUCCESS ||
            cgot == 0 || cgot < want) ok = 0;
        else off += cgot;
    }
    for (size_t off = 0; off < strsize && ok; ) {
        size_t want = strsize - off;
        if (want > chunk) want = chunk;
        mach_vm_size_t cgot = 0;
        if (mach_vm_read_overwrite(task, strtab_va + off, want,
                (mach_vm_address_t)(uintptr_t)(strtab + off), &cgot) != KERN_SUCCESS ||
            cgot == 0 || cgot < want) ok = 0;
        else off += cgot;
    }
    if (!ok) {
        free(symtab); free(strtab);
        emit(LOG_DEBUG, "patch.symtab.read_fail", NULL, NULL);
        return 0;
    }
    strtab[strsize] = '\0';

    static const uint32_t ret_op = 0xd65f03c0u;
    int hits = 0;

    // 1) Ret-patch Diagnostics::error (non-noreturn) - the throw suppressor.
    static const char *diag_syms[] = {
        "__ZN11Diagnostics5errorEPKcz",
        "__ZN11Diagnostics5errorEPKcPc",
        NULL
    };
    for (int t = 0; diag_syms[t]; t++) {
        uint64_t v = symtab_lookup(symtab, nsyms, strtab, strsize, diag_syms[t]);
        if (v && target_patch_bytes(task, v + slide, &ret_op,
                sizeof(ret_op), "diag_error") == 0)
            hits++;
    }

    free(symtab);
    free(strtab);

    attrs_t a; attrs_init(&a);
    attrs_int(&a, "hits", hits);
    attrs_int(&a, "nsyms", (int)nsyms);
    attrs_hex(&a, "dyld_base", (unsigned long long)dyld_base);
    emit(LOG_INFO, "patch.symtab", &a,
         "symtab patches: %d (Diagnostics::error ret)", hits);
    return hits;
}

int dyld_patch_apply(task_t task,
                     struct dyld_image_info *imgs,
                     uint32_t img_count,
                     mach_vm_address_t target_libdyld_fb,
                     mach_vm_address_t helper_libdyld_fb) {
    int hits = 0;
    static const char *names[] = { "abort_with_payload", "__abort_with_payload",
                                   "abort", NULL };
    static const uint32_t ret_op = 0xd65f03c0;

    // Symbol-level patches: ret the libsystem abort entries (shared cache)
    // so a framework initializer that calls abort() during load can't kill
    // the target. Only attempt when we have both libdyld bases for the slide.
    if (target_libdyld_fb && helper_libdyld_fb) {
        for (int i = 0; names[i]; i++) {
            void *helper_fn = dlsym(RTLD_DEFAULT, names[i]);
            if (!helper_fn) continue;
            mach_vm_address_t a = target_slide_func(task, imgs, img_count,
                helper_fn, target_libdyld_fb, helper_libdyld_fb);
            if (target_patch_bytes(task, a, &ret_op, sizeof(ret_op),
                    names[i]) == 0)
                hits++;
        }
    }

    // Resolve target's actual dyld __TEXT base via aii. The libdyld base
    // is wrong on iOS 14/15 (separate images) and Dopamine (sidecar dyld
    // outside DSC).
    mach_vm_address_t dyld_base = 0;
    {
        struct task_dyld_info tdi;
        mach_msg_type_number_t cnt = TASK_DYLD_INFO_COUNT;
        if (task_info(task, TASK_DYLD_INFO, (task_info_t)&tdi, &cnt) == KERN_SUCCESS) {
            struct dyld_all_image_infos aii = {0};
            mach_vm_size_t got = 0;
            if (mach_vm_read_overwrite(task, tdi.all_image_info_addr, sizeof(aii),
                    (mach_vm_address_t)(uintptr_t)&aii, &got) == KERN_SUCCESS) {
                dyld_base = (mach_vm_address_t)(uintptr_t)aii.dyldImageLoadAddress;
            }
        }
    }
    if (!dyld_base) dyld_base = target_libdyld_fb;
    if (dyld_base && dyld_base != target_libdyld_fb) {
        attrs_t a; attrs_init(&a);
        attrs_hex(&a, "dyld", (unsigned long long)dyld_base);
        attrs_hex(&a, "libdyld", (unsigned long long)target_libdyld_fb);
        emit(LOG_DEBUG, "patch.dyld_base_diff", &a, NULL);
    }
    // Sanity-check: iOS user dyld addresses live in [0x100000000,
    // 0x800000000000). A freshly posix_spawn'd target in PT_ATTACHEXC
    // can return uninitialised garbage for aii.dyldImageLoadAddress
    // (saw 0x20000000000000); scanning + patching at that base
    // scribbles random VM and the target crashes the moment we resume.
    if (dyld_base && (dyld_base < 0x100000000ULL ||
                      dyld_base >= 0x800000000000ULL)) {
        attrs_t a; attrs_init(&a);
        attrs_hex(&a, "dyld", (unsigned long long)dyld_base);
        emit(LOG_WARN, "patch.dyld_base_bogus", &a,
             "dyld base 0x%llx outside user-VA range  patches skipped",
             (unsigned long long)dyld_base);
        return hits;
    }
    // Cross-OS bind survival, driven entirely by dyld's own LC_SYMTAB
    // (see dyld_symtab_patch). Targets functions by name, so it's stable
    // across compiler builds and iOS 14 dyld 3 / iOS 15+ dyld 4, arm64 and
    // arm64e alike. Every shipping /usr/lib/dyld (incl. the Dopamine
    // procursus sidecar) carries a symbol table; a stripped dyld is the
    // only thing that would defeat it, and none ship.
    int symtab_hits = dyld_symtab_patch(task, dyld_base);
    hits += symtab_hits;

    // Always reassign so a prior bundle's value can't leak (the appex
    // loop runs this per .appex). Arms fault recovery only when we patched.
    g_dyld_force_weak_active = (symtab_hits > 0) ? 1 : 0;
    if (symtab_hits == 0) {
        // No symbol table => no cross-OS patching. Same-OS decrypt still
        // works (it needs no patching); cross-OS would fail at bind.
        emit(LOG_WARN, "patch.no_symtab", NULL,
             "dyld exposes no usable symbol table; cross-OS bind patching "
             "unavailable");
    }
    return hits;
}

// ----- dyld API lock locator (frequency scan) ------------------------------

// Open-addressing hash slot for (target_va → count).
typedef struct {
    mach_vm_address_t addr;
    uint32_t          count;
} lock_slot_t;

static int lock_slot_insert(lock_slot_t *slots, int cap_mask,
                            mach_vm_address_t addr) {
    if (!addr) return -1;
    uint64_t h = (uint64_t)addr * 0x9E3779B97F4A7C15ULL;
    int i = (int)((h >> 48) & cap_mask);
    for (int probes = 0; probes <= cap_mask; probes++) {
        if (slots[i].addr == 0) {
            slots[i].addr = addr;
            slots[i].count = 1;
            return 0;
        }
        if (slots[i].addr == addr) {
            slots[i].count++;
            return 0;
        }
        i = (i + 1) & cap_mask;
    }
    return -1;  // table full
}

// Compute BL target VA from `bl <imm26>` insn at `pc`.
static mach_vm_address_t bl_target(uint32_t insn, mach_vm_address_t pc) {
    int64_t imm26 = (int64_t)(insn & 0x03ffffffu);
    if (imm26 & 0x02000000) imm26 |= (int64_t)0xfc000000u;
    return pc + (int64_t)(imm26 * 4);
}

int dyld_find_api_locks(task_t task, mach_vm_address_t *out, int max) {
    // Resolve dyld base via task_dyld_info → aii.dyldImageLoadAddress.
    mach_vm_address_t dyld_base = 0;
    {
        struct task_dyld_info tdi;
        mach_msg_type_number_t cnt = TASK_DYLD_INFO_COUNT;
        if (task_info(task, TASK_DYLD_INFO, (task_info_t)&tdi, &cnt) != KERN_SUCCESS)
            return 0;
        struct dyld_all_image_infos aii = {0};
        mach_vm_size_t got = 0;
        if (mach_vm_read_overwrite(task, tdi.all_image_info_addr, sizeof(aii),
                (mach_vm_address_t)(uintptr_t)&aii, &got) != KERN_SUCCESS)
            return 0;
        dyld_base = (mach_vm_address_t)(uintptr_t)aii.dyldImageLoadAddress;
    }
    if (!dyld_base) return 0;

    // Read up to 8MB of dyld. Pages may be unfaulted between mapped
    // regions, so try 64KB chunks first then probe at 4KB to skip past
    // single unmapped pages without giving up on the whole scan.
    size_t cap = 8 * 1024 * 1024;
    uint8_t *buf = calloc(1, cap);
    if (!buf) return 0;
    size_t pos = 0;
    size_t total = 0;
    int blank_runs = 0;
    while (pos < cap) {
        mach_vm_size_t want;
        mach_vm_size_t got = 0;
        kern_return_t kr;
        // Try big chunk
        want = (cap - pos < 0x10000) ? (cap - pos) : 0x10000;
        kr = mach_vm_read_overwrite(task, dyld_base + pos, want,
                (mach_vm_address_t)(uintptr_t)(buf + pos), &got);
        if (kr == KERN_SUCCESS && got > 0) {
            pos += got;
            if (pos > total) total = pos;
            blank_runs = 0;
            continue;
        }
        // Try one 4KB page (iOS arm64 may use 16KB but 4KB-aligned reads
        // probe the next mapped page either way)
        want = (cap - pos < 0x1000) ? (cap - pos) : 0x1000;
        got = 0;
        kr = mach_vm_read_overwrite(task, dyld_base + pos, want,
                (mach_vm_address_t)(uintptr_t)(buf + pos), &got);
        if (kr == KERN_SUCCESS && got > 0) {
            pos += got;
            if (pos > total) total = pos;
            blank_runs = 0;
            continue;
        }
        // Skip this 4KB page (leaves zeros  won't match any insn pattern).
        pos += 0x1000;
        blank_runs++;
        if (blank_runs > 64) break;  // 256KB of unmapped → assume end of image
    }
    if (total < 64) { free(buf); return 0; }

    // ---- Pass 1: find os_unfair_lock_lock primitive function(s) ----
    //
    // Verified shape (iOS 16.7.11 /usr/lib/dyld, function at offset 0x63b54):
    //   mrs   x8,  TPIDRRO_EL0       ; thread struct pointer
    //   ldr   wN,  [x8, #0x18]       ; thread port id
    //   ldaxr w8,  [x0]              ; load-acquire-exclusive lock value
    //   cbnz  w8,  slow              ; if non-zero, slow path / bail
    //   stxr  wM,  wN, [x0]          ; try to claim with our tid
    //   cbnz  wM,  retry             ; retry on contention
    //   ret
    //
    // Detection: MRS TPIDRRO_EL0 followed within ≤8 insns by LDAXR/LDXR
    // (32 or 64 bit) on [X0], followed within ≤8 insns by STXR/STLXR on
    // [X0]. Walk backward to find function entry (preceding RET → +4,
    // or PACIBSP/BTI → that position).
    //
    // Cross-version: this lock body shape is the canonical os_unfair_lock_lock
    // implementation; structurally stable from iOS 12+ (libplatform's source
    // is open and dyld statically links it).
    const int prim_mask = 256 - 1;
    lock_slot_t *prims = calloc(prim_mask + 1, sizeof(lock_slot_t));
    if (!prims) { free(buf); return 0; }
    int prim_count = 0;
    for (size_t i = 0; i + 4 <= total; i += 4) {
        uint32_t insn;
        memcpy(&insn, buf + i, 4);
        if ((insn & 0xFFFFFFE0u) != 0xD53BD060u) continue;  // MRS Xn, TPIDRRO_EL0

        // Look ahead ≤8 insns for LDAXR/LDXR on [X0]
        size_t atomic_pos = 0;
        for (size_t k = i + 4; k + 4 <= total && k <= i + 4 + 8 * 4; k += 4) {
            uint32_t ix;
            memcpy(&ix, buf + k, 4);
            // LDAXR Wt, [X0] (32-bit, acquire): 0x885FFC00 | Rt, Rn=0
            // LDXR  Wt, [X0] (32-bit, no acq):  0x885F7C00 | Rt
            // LDAXR Xt, [X0] (64-bit, acquire): 0xC85FFC00 | Rt
            // LDXR  Xt, [X0] (64-bit, no acq):  0xC85F7C00 | Rt
            uint32_t mh = ix & 0xFFFFFFE0u;
            if (mh == 0x885FFC00u || mh == 0x885F7C00u ||
                mh == 0xC85FFC00u || mh == 0xC85F7C00u) {
                atomic_pos = k;
                break;
            }
        }
        if (!atomic_pos) continue;

        // Look ahead ≤8 more for STXR/STLXR on [X0]
        int has_stxr = 0;
        for (size_t k = atomic_pos + 4;
             k + 4 <= total && k <= atomic_pos + 4 + 8 * 4; k += 4) {
            uint32_t ix;
            memcpy(&ix, buf + k, 4);
            // STXR/STLXR encoding: 0x88..7C00 (32-bit) or 0xC8..7C00 (64-bit)
            // STXR  Ws, Wt, [X0]: 0x88007C00 | Ws<<16 | Rt, Rn=0
            // STLXR Ws, Wt, [X0]: 0x8800FC00 | Ws<<16 | Rt
            uint32_t mh = ix & 0xFFE0FFE0u;
            if (mh == 0x88007C00u || mh == 0x8800FC00u ||
                mh == 0xC8007C00u || mh == 0xC800FC00u) {
                has_stxr = 1;
                break;
            }
        }
        if (!has_stxr) continue;

        // Walk backward to find function entry.
        size_t fn_off = i;
        for (size_t back = 4; back <= 32 * 4 && back <= i; back += 4) {
            uint32_t prev;
            memcpy(&prev, buf + i - back, 4);
            if ((prev & 0xfffffc1fu) == 0xd65f0000u) {  // RET → fn starts at +4
                fn_off = i - back + 4;
                break;
            }
            if (prev == 0xd503237fu || prev == 0xd503245fu ||
                prev == 0xd503249fu) {                    // PACIBSP / BTI C / BTI J
                fn_off = i - back;
                break;
            }
        }
        if (prim_count < prim_mask) {
            lock_slot_insert(prims, prim_mask, dyld_base + fn_off);
            prim_count++;
        }
    }

    // ---- Pass 2: count ADRP+ADD-x0 setups before BLs targeting a primitive ----
    const int slot_mask = 8192 - 1;
    lock_slot_t *slots = calloc(slot_mask + 1, sizeof(lock_slot_t));
    if (!slots) { free(prims); free(buf); return 0; }

    int caller_hits = 0;
    for (size_t i = 0; i + 4 <= total; i += 4) {
        uint32_t insn;
        memcpy(&insn, buf + i, 4);
        if ((insn & 0xfc000000u) != 0x94000000u) continue;  // BL imm26

        mach_vm_address_t bl_tgt = bl_target(insn, dyld_base + i);

        // Probe prims hash table
        uint64_t h = (uint64_t)bl_tgt * 0x9E3779B97F4A7C15ULL;
        int probe = (int)((h >> 48) & prim_mask);
        int is_lock_call = 0;
        for (int p = 0; p <= prim_mask; p++) {
            if (prims[probe].addr == 0) break;
            if (prims[probe].addr == bl_tgt) { is_lock_call = 1; break; }
            probe = (probe + 1) & prim_mask;
        }
        if (!is_lock_call) continue;
        caller_hits++;

        // Back-decode the closest ADRP-x0 + ADD-x0 pair within ≤6 insns.
        for (size_t back = 8; back <= 6 * 4 && back <= i; back += 4) {
            uint32_t adrp, add;
            memcpy(&adrp, buf + i - back,     4);
            memcpy(&add,  buf + i - back + 4, 4);
            if ((adrp & 0x9f00001fu) != 0x90000000u) continue;
            if ((add  & 0xff800000u) != 0x91000000u) continue;
            if ((add & 0x1fu) != 0) continue;
            if (((add >> 5) & 0x1fu) != 0) continue;

            int64_t immlo = (adrp >> 29) & 0x3;
            int64_t immhi = (adrp >> 5)  & 0x7ffff;
            int64_t imm21 = (immhi << 2) | immlo;
            if (imm21 & (1LL << 20)) imm21 |= ~((1LL << 21) - 1);
            mach_vm_address_t pc   = dyld_base + (i - back);
            mach_vm_address_t page = (pc & ~0xfffULL) +
                                     ((mach_vm_address_t)imm21 << 12);
            uint64_t imm12 = (add >> 10) & 0xfff;
            if (((add >> 22) & 0x3) == 1) imm12 <<= 12;
            mach_vm_address_t target = page + imm12;
            if (target < 0x100000000ULL) break;
            lock_slot_insert(slots, slot_mask, target);
            break;
        }
    }

    // Collect every slot with ≥1 hit. Each is a distinct static-VA lock
    // that any dyld API entry may be holding when an inject_dlopen times
    // out. Reset all of them on recovery.
    int written = 0;
    for (int i = 0; i <= slot_mask && written < max; i++) {
        if (slots[i].count >= 1) out[written++] = slots[i].addr;
    }

    {
        attrs_t a; attrs_init(&a);
        attrs_int(&a, "found", written);
        attrs_int(&a, "prims", prim_count);
        attrs_int(&a, "callers", caller_hits);
        attrs_hex(&a, "dyld_base", (unsigned long long)dyld_base);
        attrs_int(&a, "scanned", (long long)total);
        emit(LOG_DEBUG, "patch.api_lock_scan", &a, NULL);
    }

    free(slots);
    free(prims);
    free(buf);
    return written;
}
