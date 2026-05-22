#include "dyld_patch.h"
#include "exc.h"
#include "log.h"
#include "target.h"

#include <dlfcn.h>
#include <mach-o/loader.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

// Scan dyld __TEXT for:
//   - resolveSymbol's CBZ-Wn → NOP (forces)
//   - `movz x16,#N; svc #0x80` process-killers → NOP NOP (kills)
//   - `bl <halt>; brk #1` halt callers → `b +8` (skips, gated on forces)
//
// abort_addrs[] (NULL-terminated, max 7 entries) lists target addresses
// to EXCLUDE from the skip pass  halt's own body ends with
// `bl abort_with_payload; brk #1` and falling through lands in padding.
static int scan_dyld_text(task_t task, mach_vm_address_t dyld_base,
                          const mach_vm_address_t *abort_addrs) {
    if (!dyld_base) {
        attrs_t a; attrs_init(&a);
        attrs_str(&a, "reason", "no_dyld_base");
        emit(LOG_WARN, "patch.scan_skipped", &a,
             "dyld scan skipped: target dyld base unknown");
        return 0;
    }
    struct mach_header_64 mh;
    mach_vm_size_t got = 0;
    kern_return_t kr = mach_vm_read_overwrite(task, dyld_base, sizeof(mh),
            (mach_vm_address_t)(uintptr_t)&mh, &got);
    if (kr != KERN_SUCCESS) {
        attrs_t a; attrs_init(&a);
        attrs_str(&a, "reason", "read_mh_fail");
        attrs_int(&a, "kr", kr);
        attrs_hex(&a, "base", (unsigned long long)dyld_base);
        emit(LOG_WARN, "patch.scan_skipped", &a,
             "dyld scan skipped: can't read header at 0x%llx (kr=%d)",
             (unsigned long long)dyld_base, kr);
        return 0;
    }
    if (mh.magic != MH_MAGIC_64) {
        attrs_t a; attrs_init(&a);
        attrs_str(&a, "reason", "bad_magic");
        attrs_hex(&a, "magic", mh.magic);
        emit(LOG_WARN, "patch.scan_skipped", &a, NULL);
        return 0;
    }

    // Parse load commands → find __TEXT,__text section bounds. Scanning
    // beyond __text into __const/__cstring/__data matches the bl+brk
    // and movz+svc byte patterns inside format strings and constant
    // tables, then we patch READ-ONLY DATA as if it were code. On iOS
    // 16.1.1 we hit one such false positive at dyld+0x79650 (the C
    // string ` => "%s"\n\0tried: '%s' (%s)\0...`)  patching the first
    // 4 bytes corrupted dyld's error formatter and the target died with
    // SIGKILL as soon as it tried to format any message.
    //
    // We do NOT assume any specific dyld version layout: parse the
    // mach-O on the fly so iOS 14 / 15 / 16 / 17 + Dopamine sidecar +
    // checkra1n + palera1n all share one code path.
    if (mh.cputype != CPU_TYPE_ARM64 || mh.sizeofcmds == 0 ||
        mh.sizeofcmds > 0x100000 /* 1 MB cap on load commands */) {
        attrs_t a; attrs_init(&a);
        attrs_hex(&a, "cputype", mh.cputype);
        attrs_int(&a, "sizeofcmds", mh.sizeofcmds);
        emit(LOG_WARN, "patch.scan_skipped", &a,
             "dyld scan skipped: implausible mach_header");
        return 0;
    }
    size_t lc_total = mh.sizeofcmds;
    uint8_t *lc_buf = malloc(lc_total);
    if (!lc_buf) return 0;
    mach_vm_size_t lc_got = 0;
    if (mach_vm_read_overwrite(task, dyld_base + sizeof(mh), lc_total,
            (mach_vm_address_t)(uintptr_t)lc_buf, &lc_got) != KERN_SUCCESS ||
        lc_got != lc_total) {
        free(lc_buf);
        emit(LOG_WARN, "patch.scan_skipped", NULL,
             "dyld scan skipped: load command read failed");
        return 0;
    }
    uint64_t text_off  = 0;  // VM offset of __text section relative to dyld_base
    uint64_t text_size = 0;  // byte length of __text section
    {
        const uint8_t *p = lc_buf;
        const uint8_t *end = lc_buf + lc_total;
        for (uint32_t i = 0; i < mh.ncmds; i++) {
            if (p + sizeof(struct load_command) > end) break;
            struct load_command lc; memcpy(&lc, p, sizeof(lc));
            if (lc.cmdsize < sizeof(struct load_command) ||
                lc.cmdsize > (uint32_t)(end - p)) break;
            if (lc.cmd == LC_SEGMENT_64 &&
                lc.cmdsize >= sizeof(struct segment_command_64)) {
                struct segment_command_64 sc;
                memcpy(&sc, p, sizeof(sc));
                // segname is char[16], null-padded but not necessarily
                // null-terminated. strncmp with 16 handles both.
                if (strncmp(sc.segname, "__TEXT", 16) == 0) {
                    const uint8_t *sp = p + sizeof(sc);
                    for (uint32_t s = 0; s < sc.nsects; s++) {
                        if (sp + sizeof(struct section_64) > end) break;
                        struct section_64 sec;
                        memcpy(&sec, sp, sizeof(sec));
                        if (strncmp(sec.sectname, "__text", 16) == 0) {
                            // Use VM offset (addr - segment vmaddr) since
                            // mach_vm_read is by VA, not file offset.
                            text_off  = sec.addr - sc.vmaddr;
                            text_size = sec.size;
                            break;
                        }
                        sp += sizeof(sec);
                    }
                    break;
                }
            }
            p += lc.cmdsize;
        }
    }
    free(lc_buf);
    if (text_size == 0) {
        emit(LOG_WARN, "patch.scan_skipped", NULL,
             "dyld scan skipped: __TEXT,__text section not found");
        return 0;
    }
    // Defensive cap: a normal dyld __text is ~500 KB; anything above
    // 4 MB suggests a parse error or a non-dyld binary.
    if (text_size > 4 * 1024 * 1024) {
        attrs_t a; attrs_init(&a);
        attrs_int(&a, "text_size", (int)text_size);
        emit(LOG_WARN, "patch.scan_skipped", &a,
             "dyld scan skipped: __text section implausibly large (%llu bytes)",
             (unsigned long long)text_size);
        return 0;
    }

    // Read only the __text section bytes. Pages may be lazily mapped
    // pre-resume so chunk it; a single read of the whole section fails
    // on the first unfaulted page.
    size_t text_cap = (size_t)text_size;
    uint8_t *buf = malloc(text_cap);
    if (!buf) return 0;
    size_t total = 0;
    const size_t chunk = 0x10000;
    while (total < text_cap) {
        size_t want = text_cap - total;
        if (want > chunk) want = chunk;
        mach_vm_size_t cgot = 0;
        if (mach_vm_read_overwrite(task, dyld_base + text_off + total, want,
                (mach_vm_address_t)(uintptr_t)(buf + total), &cgot) != KERN_SUCCESS ||
            cgot == 0) break;
        total += cgot;
        if (cgot < want) break;
    }
    if (total < 8) {
        free(buf);
        attrs_t a; attrs_init(&a);
        attrs_str(&a, "reason", "no_text_bytes");
        emit(LOG_WARN, "patch.scan_skipped", &a, NULL);
        return 0;
    }
    got = total;

    // Pass 1: resolveSymbol force-weak-NULL (cross-OS bind-fail).
    // The 12-byte tail is byte-identical across every dyld build observed:
    //   MOV W9,#2 / STR W9,[X8,#0x40] / STR XZR,[X8,#0x38]
    // We validate i-4 loads to X8 (LDR or LDUR) and i-8 is CBZ Wn, then
    // NOP the CBZ. iOS 15.4 19E258: 3 sites. iOS 16.1.1 Dopamine sidecar: 2.
    static const uint8_t bind_absolute_tail[12] = {
        0x49, 0x00, 0x80, 0x52,
        0x09, 0x41, 0x00, 0xb9,
        0x1f, 0x1d, 0x00, 0xf9,
    };
    static const uint32_t nop_op = 0xd503201fu;
    int forces = 0;
    for (size_t i = 8; i + 12 <= got; i += 4) {
        if (memcmp(buf + i, bind_absolute_tail, 12) != 0) continue;
        uint32_t load;
        memcpy(&load, buf + i - 4, 4);
        if ((load & 0x1fu) != 8u) continue;
        int is_ldr  = (load & 0xffc00000u) == 0xf9400000u;
        int is_ldur = (load & 0xffe00c00u) == 0xf8400000u;
        if (!is_ldr && !is_ldur) continue;
        uint32_t cbz;
        memcpy(&cbz, buf + i - 8, 4);
        if ((cbz & 0xff000000u) != 0x34000000u) continue;
        char tag[64];
        snprintf(tag, sizeof(tag), "force_weak_%d", forces);
        if (target_patch_bytes(task, dyld_base + text_off + i - 8, &nop_op,
                sizeof(nop_op), tag) == 0) forces++;
    }

    // Pass 2: NOP `movz x16,#N; svc #0x80` for *process-killing* syscalls
    // dyld calls when something goes wrong. Strictly process-exit only;
    // touching libdispatch / libsystem auxiliary syscalls (kqueue,
    // psynch, workloop) breaks dyld init and the target gets SIGKILL'd
    // by the runtime watchdog. Only enabled when force_weak fired,
    // i.e. the target actually has cross-OS bind risk  for everything
    // else these patches are pure downside.
    //
    // SYS_exit               = 1
    // SYS_kill               = 37  (0x25)
    // SYS___pthread_kill     = 328 (0x148)
    // SYS_terminate_with_payload = 520 (0x208)
    // SYS_abort_with_payload     = 521 (0x209)
    int kills = 0;
    if (forces > 0) {
        static const uint8_t svc_pattern[4] = { 0x01, 0x10, 0x00, 0xd4 };
        static const uint32_t kill_imms[] = { 1, 37, 328, 520, 521 };
        for (size_t i = 4; i + 4 <= got; i += 4) {
            if (memcmp(buf + i, svc_pattern, 4) != 0) continue;
            uint32_t prev;
            memcpy(&prev, buf + i - 4, 4);
            // movz x16,#imm OR movz w16,#imm (some dyld builds use the
            // 32-bit form for low immediates).
            if (((prev & 0xffe0001f) != (0xd2800000 | 16)) &&
                ((prev & 0xffe0001f) != (0x52800000 | 16))) continue;
            uint32_t imm = (prev >> 5) & 0xffff;
            int wanted = 0;
            for (size_t k = 0; k < sizeof(kill_imms)/sizeof(kill_imms[0]); k++) {
                if (imm == kill_imms[k]) { wanted = 1; break; }
            }
            if (!wanted) continue;
            static const uint32_t nop_pair[2] = { 0xd503201fu, 0xd503201fu };
            char tag[64];
            snprintf(tag, sizeof(tag), "kill_svc_%u_%d", imm, kills);
            if (target_patch_bytes(task, dyld_base + text_off + i - 4, nop_pair,
                    sizeof(nop_pair), tag) == 0) kills++;
        }
    }

    // Pass 3 (only when forces > 0): rewrite `bl <dyld_halt>; brk #1`
    // → `b +8` so halt callers fall through the noreturn marker.
    //
    // We don't have dyld_halt's address (no symbol resolution from raw
    // bytes), but it's structurally the bl-target that appears MOST
    // often in `bl X; brk #1` pairs inside dyld  every internal
    // assert/error path BLs into the same halt routine before the
    // brk marker. Build a frequency tally over the matches, pick the
    // single most-common bl_target (with at least 3 callers), and
    // patch only that one. False positives in __cstring no longer
    // happen (we scan only __text), and within __text bl_target
    // distribution is heavily skewed  dyld_halt wins by >10x in
    // every tested version (16.1.1: 1 random match vs 44 halt
    // callers; 16.7.11: similar).
    int skips = 0;
    if (forces > 0) {
        // Two-pass: collect candidate sites, tally bl_target counts,
        // pick consensus winner, then patch only sites pointing there.
        typedef struct { size_t i; mach_vm_address_t tgt; } cand_t;
        size_t cap = 256;
        cand_t *cands = malloc(cap * sizeof(cand_t));
        size_t ncands = 0;
        if (cands) {
            for (size_t i = 0; i + 8 <= got; i += 4) {
                uint32_t insn, next_insn;
                memcpy(&insn, buf + i, 4);
                memcpy(&next_insn, buf + i + 4, 4);
                if (next_insn != 0xd4200020u) continue;
                if ((insn & 0xfc000000u) != 0x94000000u) continue;
                int32_t imm26 = (int32_t)(insn & 0x03ffffffu);
                if (imm26 & 0x02000000) imm26 |= (int32_t)0xfc000000u;
                mach_vm_address_t bl_target =
                    dyld_base + text_off + i + (int64_t)imm26 * 4;
                if (ncands == cap) {
                    cap *= 2;
                    cand_t *gr = realloc(cands, cap * sizeof(cand_t));
                    if (!gr) break;
                    cands = gr;
                }
                cands[ncands].i = i;
                cands[ncands].tgt = bl_target;
                ncands++;
            }
            // Tally → find most common bl_target.
            mach_vm_address_t halt_addr = 0;
            int halt_count = 0;
            for (size_t a = 0; a < ncands; a++) {
                int c = 0;
                for (size_t b = 0; b < ncands; b++) {
                    if (cands[b].tgt == cands[a].tgt) c++;
                }
                if (c > halt_count) {
                    halt_count = c;
                    halt_addr  = cands[a].tgt;
                }
            }
            attrs_t hd; attrs_init(&hd);
            attrs_hex(&hd, "halt_addr", (unsigned long long)halt_addr);
            attrs_int(&hd, "halt_count", halt_count);
            attrs_int(&hd, "candidates", (int)ncands);
            emit(LOG_DEBUG, "patch.halt_consensus", &hd,
                 "skip_halt consensus: halt=0x%llx callers=%d/%zu",
                 (unsigned long long)halt_addr, halt_count, ncands);
            // Only patch if winner has a clear majority  at least 3
            // callers AND at least 3x the second-most-common. Avoids
            // false positives when dyld is missing patterns entirely.
            int second_count = 0;
            for (size_t a = 0; a < ncands; a++) {
                if (cands[a].tgt == halt_addr) continue;
                int c = 0;
                for (size_t b = 0; b < ncands; b++) {
                    if (cands[b].tgt == cands[a].tgt) c++;
                }
                if (c > second_count) second_count = c;
            }
            if (halt_count >= 3 && halt_count >= second_count * 3) {
                static const uint32_t b_plus_8 = 0x14000002u;
                for (size_t k = 0; k < ncands; k++) {
                    if (cands[k].tgt != halt_addr) continue;
                    char tag[64];
                    snprintf(tag, sizeof(tag), "skip_halt_%d", skips);
                    if (target_patch_bytes(task,
                            dyld_base + text_off + cands[k].i,
                            &b_plus_8, sizeof(b_plus_8),
                            tag) == 0) skips++;
                }
            }
            free(cands);
        }
    }
    free(buf);

    // Always reassign so prior bundle's value can't leak.
    g_dyld_force_weak_active = (forces > 0) ? 1 : 0;

    attrs_t a; attrs_init(&a);
    attrs_int(&a, "kills", kills);
    attrs_int(&a, "forces", forces);
    attrs_int(&a, "skips", skips);
    attrs_hex(&a, "bytes", (unsigned long long)got);
    emit(LOG_INFO, "patch.applied", &a,
         "dyld patches: kills=%d forces=%d skips=%d", kills, forces, skips);
    return kills + forces + skips;
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
    mach_vm_address_t abort_addrs[8] = {0};
    int n_abort = 0;

    // Symbol-level patches (libsystem abort entries → ret) are belt-and-
    // suspenders on top of the __TEXT scan. Only attempt them when we
    // have both libdyld bases to compute the slide.
    if (target_libdyld_fb && helper_libdyld_fb) {
        for (int i = 0; names[i]; i++) {
            void *helper_fn = dlsym(RTLD_DEFAULT, names[i]);
            if (!helper_fn) continue;
            mach_vm_address_t a = target_slide_func(task, imgs, img_count,
                helper_fn, target_libdyld_fb, helper_libdyld_fb);
            if (a && n_abort < 7) abort_addrs[n_abort++] = a;
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
    hits += scan_dyld_text(task, dyld_base, abort_addrs);
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
