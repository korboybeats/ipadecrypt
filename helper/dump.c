#include "dump.h"
#include "fs.h"
#include "log.h"
#include "target.h"

#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>

const char *dump_reason(dump_result_t r) {
    switch (r) {
    case DUMP_OPEN_SRC_FAIL:  return "open_src_fail";
    case DUMP_READ_SRC_FAIL:  return "read_src_fail";
    case DUMP_VM_READ_FAIL:   return "vm_read_err";
    case DUMP_ZERO_PAGES:     return "cryptoff_zero_pages";
    case DUMP_OPEN_DST_FAIL:  return "open_dst_fail";
    case DUMP_WRITE_DST_FAIL: return "write_dst_fail";
    case DUMP_OOM:            return "oom";
    default:                  return "ok";
    }
}

// Write the matched slice's bytes for fat input (thinning the output) or
// the whole buffer for thin input.
static dump_result_t write_output(const char *dst, const uint8_t *buf,
                                  size_t file_sz, const selected_slice_t *sel) {
    char parent[4096];
    snprintf(parent, sizeof(parent), "%s", dst);
    char *slash = strrchr(parent, '/');
    if (slash) { *slash = '\0'; fs_mkdirs(parent); }

    // Break any hardlink staging set up by copy_tree before writing,
    // otherwise O_TRUNC would clobber the original installed bundle.
    unlink(dst);
    int fd = open(dst, O_CREAT | O_WRONLY | O_TRUNC, 0755);
    if (fd < 0) { er("open dst %s: %s", dst, strerror(errno)); return DUMP_OPEN_DST_FAIL; }

    const uint8_t *p = sel->is_fat ? buf + sel->selected.slice.slice_offset : buf;
    size_t n = sel->is_fat ? (size_t)sel->selected.slice.slice_size : file_sz;
    if (fs_write_all(fd, p, n) != 0) {
        er("write dst %s: %s", dst, strerror(errno));
        close(fd); return DUMP_WRITE_DST_FAIL;
    }
    close(fd);
    return DUMP_OK;
}

dump_result_t dump_image(const char *src, const char *dst, task_t task,
                         mach_vm_address_t image_base,
                         const selected_slice_t *sel) {
    const mach_slice_t *slice = &sel->selected;

    int fd = open(src, O_RDONLY);
    if (fd < 0) { er("open %s: %s", src, strerror(errno)); return DUMP_OPEN_SRC_FAIL; }
    struct stat st;
    if (fstat(fd, &st) != 0) { close(fd); return DUMP_OPEN_SRC_FAIL; }
    size_t file_sz = (size_t)st.st_size;
    uint8_t *buf = malloc(file_sz);
    if (!buf) { close(fd); return DUMP_OOM; }
    if (fs_read_full(fd, buf, file_sz) != 0) {
        er("read %s: %s", src, strerror(errno));
        free(buf); close(fd); return DUMP_READ_SRC_FAIL;
    }
    close(fd);

    if (slice->crypt.has_crypt && slice->crypt.cryptid != 0 && slice->crypt.cryptsize) {
        // Snapshot on-disk ciphertext to verify vm_read returned plaintext.
        // FairPlay decrypt-on-fault doesn't fire reliably on iOS 15 Dopamine;
        // without this check we'd ship a cryptid=0 IPA that's still encrypted.
        size_t crypt_off = (size_t)slice->slice.slice_offset +
                           (size_t)slice->crypt.cryptoff;
        size_t crypt_sz  = (size_t)slice->crypt.cryptsize;
        uint8_t *raw_copy = malloc(crypt_sz);
        if (!raw_copy) { free(buf); return DUMP_OOM; }
        memcpy(raw_copy, buf + crypt_off, crypt_sz);

        char err_buf[256] = {0};
        if (target_vm_read_crypt(task, image_base, slice, buf,
                                 err_buf, sizeof(err_buf)) != 0) {
            wrn("vm_read failed for %s: %s", src, err_buf);
            free(raw_copy); free(buf); return DUMP_VM_READ_FAIL;
        }
        if (!target_crypt_has_data(buf, slice)) {
            free(raw_copy); free(buf); return DUMP_ZERO_PAGES;
        }
        if (memcmp(raw_copy, buf + crypt_off, crypt_sz) == 0) {
            free(raw_copy); free(buf); return DUMP_VM_READ_FAIL;
        }
        free(raw_copy);
    }

    if (slice->crypt.has_crypt) {
        uint32_t zero = 0;
        memcpy(buf + slice->crypt.cryptid_file_offset, &zero, sizeof(zero));
    }

    dump_result_t dr = write_output(dst, buf, file_sz, sel);
    free(buf);
    return dr;
}
