#include "target.h"
#include "log.h"

#include <dlfcn.h>
#include <mach-o/loader.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

int target_find_main_base(task_t task, mach_vm_address_t *out) {
    mach_vm_address_t addr = 0;
    for (;;) {
        mach_vm_size_t sz = 0;
        vm_region_basic_info_data_64_t info;
        mach_msg_type_number_t cnt = VM_REGION_BASIC_INFO_COUNT_64;
        mach_port_t obj = MACH_PORT_NULL;
        if (mach_vm_region(task, &addr, &sz, VM_REGION_BASIC_INFO_64,
                (vm_region_info_t)&info, &cnt, &obj) != KERN_SUCCESS) return -1;
        if (obj != MACH_PORT_NULL)
            mach_port_deallocate(mach_task_self(), obj);
        struct mach_header_64 hdr;
        mach_vm_size_t n = 0;
        if (mach_vm_read_overwrite(task, addr, sizeof(hdr),
                (mach_vm_address_t)(uintptr_t)&hdr, &n) == KERN_SUCCESS &&
            n == sizeof(hdr) &&
            (hdr.magic == MH_MAGIC_64 || hdr.magic == MH_MAGIC) &&
            hdr.filetype == MH_EXECUTE) {
            *out = addr;
            return 0;
        }
        if (sz == 0 || addr + sz <= addr) return -1;
        addr += sz;
    }
}

int target_read_runtime_image(task_t task, mach_vm_address_t base,
                              runtime_image_t *out) {
    struct mach_header_64 hdr;
    mach_vm_size_t got = 0;
    if (mach_vm_read_overwrite(task, base, sizeof(hdr),
            (mach_vm_address_t)(uintptr_t)&hdr, &got) != KERN_SUCCESS ||
        got != sizeof(hdr)) {
        return -1;
    }
    if (hdr.magic != MH_MAGIC && hdr.magic != MH_MAGIC_64) return -1;
    out->is_64 = (hdr.magic == MH_MAGIC_64);
    out->cputype = hdr.cputype;
    out->cpusubtype = hdr.cpusubtype;
    return 0;
}

struct dyld_image_info *target_list_images(task_t task,
                                            uint32_t *out_count,
                                            char **out_paths) {
    struct task_dyld_info tdi;
    mach_msg_type_number_t cnt = TASK_DYLD_INFO_COUNT;
    if (task_info(task, TASK_DYLD_INFO, (task_info_t)&tdi, &cnt) != KERN_SUCCESS)
        return NULL;
    struct dyld_all_image_infos aii;
    mach_vm_size_t n = 0;
    if (mach_vm_read_overwrite(task, tdi.all_image_info_addr, sizeof(aii),
            (mach_vm_address_t)(uintptr_t)&aii, &n) != KERN_SUCCESS) return NULL;
    if (aii.infoArrayCount == 0 || aii.infoArray == NULL) return NULL;

    size_t arr_sz = sizeof(struct dyld_image_info) * aii.infoArrayCount;
    struct dyld_image_info *infos = malloc(arr_sz);
    if (!infos) return NULL;
    if (mach_vm_read_overwrite(task, (mach_vm_address_t)aii.infoArray, arr_sz,
            (mach_vm_address_t)(uintptr_t)infos, &n) != KERN_SUCCESS) {
        free(infos); return NULL;
    }

    size_t path_cap = aii.infoArrayCount * 4096;
    char *paths = malloc(path_cap);
    if (!paths) { free(infos); return NULL; }
    size_t off = 0;
    for (uint32_t i = 0; i < aii.infoArrayCount; i++) {
        char *slot = paths + off;
        if (off + 4096 > path_cap) { infos[i].imageFilePath = NULL; continue; }
        mach_vm_size_t got = 0;
        if (mach_vm_read_overwrite(task, (mach_vm_address_t)infos[i].imageFilePath,
                4095, (mach_vm_address_t)(uintptr_t)slot, &got) == KERN_SUCCESS && got > 0) {
            slot[got < 4095 ? got : 4094] = '\0';
            size_t len = strnlen(slot, got);
            slot[len] = '\0';
            infos[i].imageFilePath = slot;
            off += len + 1;
        } else {
            infos[i].imageFilePath = NULL;
        }
    }
    *out_count = aii.infoArrayCount;
    *out_paths = paths;
    return infos;
}

mach_vm_address_t target_find_image(task_t task,
                                    struct dyld_image_info *imgs,
                                    uint32_t img_count,
                                    const char *helper_path) {
    if (helper_path) {
        const char *helper_base = strrchr(helper_path, '/');
        helper_base = helper_base ? helper_base + 1 : helper_path;
        for (uint32_t i = 0; i < img_count; i++) {
            const char *p = imgs[i].imageFilePath;
            if (!p) continue;
            const char *b = strrchr(p, '/');
            b = b ? b + 1 : p;
            if (strcmp(b, helper_base) == 0)
                return (mach_vm_address_t)(uintptr_t)imgs[i].imageLoadAddress;
        }
    }
    static const char *needles[] = { "libdyld.dylib", "/usr/lib/dyld", NULL };
    for (int n = 0; needles[n]; n++) {
        for (uint32_t i = 0; i < img_count; i++) {
            const char *p = imgs[i].imageFilePath;
            if (p && strstr(p, needles[n]))
                return (mach_vm_address_t)(uintptr_t)imgs[i].imageLoadAddress;
        }
    }
    // aii.dyldImageLoadAddress is the standalone dyld loader, NOT
    // libdyld.dylib. Only return it for caller-requested "dyld" basename.
    int wants_dyld = 0;
    if (helper_path) {
        const char *b = strrchr(helper_path, '/');
        b = b ? b + 1 : helper_path;
        if (strcmp(b, "dyld") == 0) wants_dyld = 1;
    }
    if (wants_dyld) {
        struct task_dyld_info tdi;
        mach_msg_type_number_t cnt = TASK_DYLD_INFO_COUNT;
        if (task_info(task, TASK_DYLD_INFO, (task_info_t)&tdi, &cnt) == KERN_SUCCESS) {
            struct dyld_all_image_infos aii = {0};
            mach_vm_size_t got = 0;
            if (mach_vm_read_overwrite(task, tdi.all_image_info_addr, sizeof(aii),
                    (mach_vm_address_t)(uintptr_t)&aii, &got) == KERN_SUCCESS &&
                aii.dyldImageLoadAddress) {
                return (mach_vm_address_t)(uintptr_t)aii.dyldImageLoadAddress;
            }
        }
    }
    return 0;
}

mach_vm_address_t target_slide_func(task_t task,
                                    struct dyld_image_info *imgs,
                                    uint32_t img_count,
                                    void *helper_func,
                                    mach_vm_address_t target_libdyld_fb,
                                    mach_vm_address_t helper_libdyld_fb) {
    if (!helper_func) return 0;
    Dl_info info = {0};
    if (dladdr(helper_func, &info) && info.dli_fbase) {
        mach_vm_address_t target_base = target_find_image(task, imgs,
            img_count, info.dli_fname);
        if (target_base) {
            mach_vm_address_t off = (mach_vm_address_t)(uintptr_t)helper_func -
                (mach_vm_address_t)(uintptr_t)info.dli_fbase;
            return target_base + off;
        }
    }
    // Libdyld-slide fallback only valid when helper_func is itself in
    // libdyld (otherwise the offset is wrong by the DSC slot delta).
    if (target_libdyld_fb && helper_libdyld_fb && info.dli_fbase &&
        (mach_vm_address_t)(uintptr_t)info.dli_fbase == helper_libdyld_fb) {
        return target_libdyld_fb +
            ((mach_vm_address_t)(uintptr_t)helper_func - helper_libdyld_fb);
    }
    return 0;
}

int target_vm_read_crypt(task_t task,
                         mach_vm_address_t image_base,
                         const mach_slice_t *slice,
                         uint8_t *buf,
                         char *err_msg, size_t err_cap) {
    mach_vm_address_t src = image_base + slice->crypt.cryptoff;
    mach_vm_address_t dst = (mach_vm_address_t)(uintptr_t)
        (buf + slice->slice.slice_offset + slice->crypt.cryptoff);
    mach_vm_size_t remaining = slice->crypt.cryptsize;
    while (remaining > 0) {
        mach_vm_size_t chunk = remaining > 0x100000 ? 0x100000 : remaining;
        mach_vm_size_t got = 0;
        kern_return_t kr = mach_vm_read_overwrite(task, src, chunk, dst, &got);
        if (kr != KERN_SUCCESS || got == 0) {
            if (err_msg) snprintf(err_msg, err_cap, "vm_read@0x%llx size=0x%llx kr=%d",
                                   (unsigned long long)src,
                                   (unsigned long long)chunk, kr);
            return -1;
        }
        src += got; dst += got; remaining -= got;
    }
    return 0;
}

int target_crypt_has_data(const uint8_t *buf, const mach_slice_t *slice) {
    const uint8_t *p = buf + slice->slice.slice_offset + slice->crypt.cryptoff;
    for (uint32_t i = 0; i < slice->crypt.cryptsize; i++) {
        if (p[i]) return 1;
    }
    return 0;
}

int target_patch_bytes(task_t task, mach_vm_address_t addr,
                       const void *bytes, size_t len, const char *tag) {
    if (!addr) {
        attrs_t a; attrs_init(&a);
        attrs_str(&a, "tag", tag);
        attrs_str(&a, "reason", "no_addr");
        emit(LOG_DEBUG, "patch.skipped", &a, NULL);
        return -1;
    }
    mach_vm_address_t page = addr & ~(mach_vm_address_t)0x3fff;
    mach_vm_size_t span = ((addr + len - page + 0x3fff) & ~(mach_vm_size_t)0x3fff);
    if (mach_vm_protect(task, page, span, FALSE,
            VM_PROT_READ | VM_PROT_WRITE | VM_PROT_COPY) != KERN_SUCCESS) return -1;
    if (mach_vm_write(task, addr, (vm_offset_t)(uintptr_t)bytes,
            (mach_msg_type_number_t)len) != KERN_SUCCESS) return -1;
    mach_vm_protect(task, page, span, FALSE,
        VM_PROT_READ | VM_PROT_EXECUTE);
    attrs_t a; attrs_init(&a);
    attrs_str(&a, "tag", tag);
    attrs_hex(&a, "addr", (unsigned long long)addr);
    emit(LOG_DEBUG, "patch.applied", &a, NULL);
    return 0;
}
