#include "macho.h"

#include <fcntl.h>
#include <libkern/OSByteOrder.h>
#include <mach-o/fat.h>
#include <mach-o/loader.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <unistd.h>

uint32_t cpusubtype_base(uint32_t subtype) {
    return subtype & ~(uint32_t)CPU_SUBTYPE_MASK;
}

int slice_matches_runtime(const slice_meta_t *slice, const runtime_image_t *rt) {
    return slice->is_64 == rt->is_64 &&
           slice->cputype == rt->cputype &&
           cpusubtype_base(slice->cpusubtype) == cpusubtype_base(rt->cpusubtype);
}

int slice_needs_dump(const selected_slice_t *sel) {
    if (sel->selected.crypt.has_crypt && sel->selected.crypt.cryptid != 0) return 1;
    return sel->is_fat && sel->any_slice_encrypted;
}

int parse_thin_slice(const uint8_t *slice, size_t slice_len,
                     off_t slice_off, uint64_t slice_size,
                     mach_slice_t *out) {
    if (slice_len < sizeof(struct mach_header)) return 0;

    struct mach_header mh;
    memcpy(&mh, slice, sizeof(mh));

    int is_64;
    size_t hdr_sz;
    uint32_t ncmds, szcmds;
    if (mh.magic == MH_MAGIC_64) {
        if (slice_len < sizeof(struct mach_header_64)) return 0;
        struct mach_header_64 mh64;
        memcpy(&mh64, slice, sizeof(mh64));
        is_64 = 1;
        ncmds = mh64.ncmds; szcmds = mh64.sizeofcmds; hdr_sz = sizeof(mh64);
        out->slice.cputype = mh64.cputype;
        out->slice.cpusubtype = mh64.cpusubtype;
    } else if (mh.magic == MH_MAGIC) {
        is_64 = 0;
        ncmds = mh.ncmds; szcmds = mh.sizeofcmds; hdr_sz = sizeof(mh);
        out->slice.cputype = mh.cputype;
        out->slice.cpusubtype = mh.cpusubtype;
    } else {
        return 0;
    }
    if (hdr_sz + szcmds > slice_len) return -1;

    out->slice.slice_offset = slice_off;
    out->slice.slice_size = slice_size;
    out->slice.is_64 = is_64;
    out->crypt.has_crypt = 0;
    out->cs.has_cs = 0;

    const uint8_t *lc_end = slice + hdr_sz + szcmds;
    const uint8_t *lc_ptr = slice + hdr_sz;
    uint32_t want_crypt = is_64 ? LC_ENCRYPTION_INFO_64 : LC_ENCRYPTION_INFO;
    for (uint32_t i = 0; i < ncmds; i++) {
        if ((size_t)(lc_end - lc_ptr) < sizeof(struct load_command)) return -1;
        struct load_command lc;
        memcpy(&lc, lc_ptr, sizeof(lc));
        if (lc.cmdsize == 0 || (size_t)(lc_end - lc_ptr) < lc.cmdsize) return -1;

        if (lc.cmd == want_crypt) {
            if (lc.cmdsize < sizeof(struct encryption_info_command)) return -1;
            struct encryption_info_command eic;
            memcpy(&eic, lc_ptr, sizeof(eic));
            if ((uint64_t)eic.cryptoff + eic.cryptsize > slice_size) return -1;
            out->crypt.has_crypt = 1;
            out->crypt.cryptoff = eic.cryptoff;
            out->crypt.cryptsize = eic.cryptsize;
            out->crypt.cryptid = eic.cryptid;
            out->crypt.cryptid_file_offset = slice_off + (lc_ptr - slice) +
                offsetof(struct encryption_info_command, cryptid);
        } else if (lc.cmd == LC_CODE_SIGNATURE) {
            if (lc.cmdsize < sizeof(struct linkedit_data_command)) return -1;
            struct linkedit_data_command ldc;
            memcpy(&ldc, lc_ptr, sizeof(ldc));
            if ((uint64_t)ldc.dataoff + ldc.datasize > slice_size) return -1;
            out->cs.has_cs = 1;
            out->cs.cs_offset = ldc.dataoff;
            out->cs.cs_size = ldc.datasize;
        }
        lc_ptr += lc.cmdsize;
    }
    return 1;
}

static int fat_slice_range(const uint8_t *base, size_t file_sz,
                           int is_fat64, int swap, uint32_t idx,
                           uint64_t *out_off, uint64_t *out_size) {
    uint64_t arch_size = is_fat64 ? sizeof(struct fat_arch_64)
                                  : sizeof(struct fat_arch);
    uint64_t entry_off = sizeof(struct fat_header) + arch_size * idx;
    if (entry_off + arch_size > file_sz) return -1;

    uint64_t off, sz;
    if (is_fat64) {
        struct fat_arch_64 fa;
        memcpy(&fa, base + entry_off, sizeof(fa));
        off = swap ? OSSwapBigToHostInt64(fa.offset) : fa.offset;
        sz  = swap ? OSSwapBigToHostInt64(fa.size)   : fa.size;
    } else {
        struct fat_arch fa;
        memcpy(&fa, base + entry_off, sizeof(fa));
        off = swap ? OSSwapBigToHostInt32(fa.offset) : fa.offset;
        sz  = swap ? OSSwapBigToHostInt32(fa.size)   : fa.size;
    }
    if (sz > file_sz || off > file_sz - sz) return -1;
    *out_off = off;
    *out_size = sz;
    return 0;
}

int select_runtime_slice(const char *path,
                         const runtime_image_t *rt,
                         selected_slice_t *out) {
    memset(out, 0, sizeof(*out));

    int fd = open(path, O_RDONLY);
    if (fd < 0) return -1;
    struct stat st;
    if (fstat(fd, &st) < 0) { close(fd); return -1; }
    if (st.st_size < (off_t)sizeof(struct fat_header)) { close(fd); return 0; }

    size_t file_sz = (size_t)st.st_size;
    void *map = mmap(NULL, file_sz, PROT_READ, MAP_PRIVATE, fd, 0);
    close(fd);
    if (map == MAP_FAILED) return -1;

    int rc = 0;
    const uint8_t *base = map;
    uint32_t magic;
    memcpy(&magic, base, 4);

    if (magic == FAT_MAGIC || magic == FAT_CIGAM ||
        magic == FAT_MAGIC_64 || magic == FAT_CIGAM_64) {
        int is_fat64 = (magic == FAT_MAGIC_64 || magic == FAT_CIGAM_64);
        int swap = (magic == FAT_CIGAM || magic == FAT_CIGAM_64);
        struct fat_header fh;
        memcpy(&fh, base, sizeof(fh));
        uint32_t nfat = swap ? OSSwapBigToHostInt32(fh.nfat_arch) : fh.nfat_arch;
        uint64_t arch_size = is_fat64 ? sizeof(struct fat_arch_64)
                                      : sizeof(struct fat_arch);
        if (nfat == 0 || nfat > (file_sz - sizeof(struct fat_header)) / arch_size) {
            rc = -1; goto done;
        }

        out->is_fat = 1;
        for (uint32_t i = 0; i < nfat; i++) {
            uint64_t s_off, s_sz;
            if (fat_slice_range(base, file_sz, is_fat64, swap, i, &s_off, &s_sz) != 0) {
                rc = -1; goto done;
            }
            mach_slice_t slice;
            int sr = parse_thin_slice(base + s_off, (size_t)s_sz,
                                      (off_t)s_off, s_sz, &slice);
            if (sr < 0) { rc = -1; goto done; }
            if (sr == 0) continue;
            if (slice.crypt.has_crypt && slice.crypt.cryptid != 0)
                out->any_slice_encrypted = 1;
            if (rc == 0 && slice_matches_runtime(&slice.slice, rt)) {
                out->selected = slice;
                rc = 1;
            }
        }
    } else {
        mach_slice_t slice;
        int sr = parse_thin_slice(base, file_sz, 0, file_sz, &slice);
        if (sr <= 0) { rc = sr; goto done; }
        if (!slice_matches_runtime(&slice.slice, rt)) goto done;
        out->any_slice_encrypted = slice.crypt.has_crypt && slice.crypt.cryptid != 0;
        out->selected = slice;
        rc = 1;
    }

done:
    munmap(map, file_sz);
    return rc;
}

// ----- LC_LOAD_*_DYLIB + LC_RPATH extraction --------------------------------

static int strvec_push(char ***vec, int *count, const char *s, size_t slen) {
    if (!s || slen == 0) return -1;
    char *dup = malloc(slen + 1);
    if (!dup) return -1;
    memcpy(dup, s, slen);
    dup[slen] = '\0';
    // NUL might already be inside; trim by strlen so caller sees a clean
    // C string (dylib_command name fields are NUL-padded to cmdsize).
    dup[strlen(dup)] = '\0';
    char **n = realloc(*vec, sizeof(char *) * (*count + 1));
    if (!n) { free(dup); return -1; }
    n[*count] = dup;
    *vec = n;
    (*count)++;
    return 0;
}

static int collect_deps_from_slice(const uint8_t *slice, size_t slice_len,
                                   macho_deps_t *out) {
    if (slice_len < sizeof(struct mach_header)) return 0;
    struct mach_header mh;
    memcpy(&mh, slice, sizeof(mh));
    size_t hdr_sz;
    uint32_t ncmds, szcmds;
    if (mh.magic == MH_MAGIC_64) {
        if (slice_len < sizeof(struct mach_header_64)) return 0;
        struct mach_header_64 mh64;
        memcpy(&mh64, slice, sizeof(mh64));
        ncmds = mh64.ncmds; szcmds = mh64.sizeofcmds; hdr_sz = sizeof(mh64);
    } else if (mh.magic == MH_MAGIC) {
        ncmds = mh.ncmds; szcmds = mh.sizeofcmds; hdr_sz = sizeof(mh);
    } else {
        return 0;
    }
    if (hdr_sz + szcmds > slice_len) return -1;

    const uint8_t *lc_ptr = slice + hdr_sz;
    const uint8_t *lc_end = slice + hdr_sz + szcmds;
    for (uint32_t i = 0; i < ncmds; i++) {
        if ((size_t)(lc_end - lc_ptr) < sizeof(struct load_command)) return -1;
        struct load_command lc;
        memcpy(&lc, lc_ptr, sizeof(lc));
        if (lc.cmdsize == 0 || (size_t)(lc_end - lc_ptr) < lc.cmdsize) return -1;

        int is_dylib = (lc.cmd == LC_LOAD_DYLIB || lc.cmd == LC_LOAD_WEAK_DYLIB ||
                        lc.cmd == LC_REEXPORT_DYLIB || LC_LOAD_UPWARD_DYLIB == lc.cmd ||
                        lc.cmd == LC_LAZY_LOAD_DYLIB);
        if (is_dylib && lc.cmdsize >= sizeof(struct dylib_command)) {
            struct dylib_command dc;
            memcpy(&dc, lc_ptr, sizeof(dc));
            uint32_t off = dc.dylib.name.offset;
            if (off < lc.cmdsize) {
                const char *name = (const char *)(lc_ptr + off);
                size_t maxlen = lc.cmdsize - off;
                strvec_push(&out->deps, &out->dep_count, name, maxlen);
            }
        } else if (lc.cmd == LC_RPATH && lc.cmdsize >= sizeof(struct rpath_command)) {
            struct rpath_command rc;
            memcpy(&rc, lc_ptr, sizeof(rc));
            uint32_t off = rc.path.offset;
            if (off < lc.cmdsize) {
                const char *p = (const char *)(lc_ptr + off);
                size_t maxlen = lc.cmdsize - off;
                strvec_push(&out->rpaths, &out->rpath_count, p, maxlen);
            }
        }
        lc_ptr += lc.cmdsize;
    }
    return 1;
}

int macho_collect_deps(const char *path, const runtime_image_t *rt,
                       macho_deps_t *out) {
    memset(out, 0, sizeof(*out));
    int fd = open(path, O_RDONLY);
    if (fd < 0) return -1;
    struct stat st;
    if (fstat(fd, &st) < 0) { close(fd); return -1; }
    if (st.st_size < (off_t)sizeof(struct fat_header)) { close(fd); return 0; }
    size_t file_sz = (size_t)st.st_size;
    void *map = mmap(NULL, file_sz, PROT_READ, MAP_PRIVATE, fd, 0);
    close(fd);
    if (map == MAP_FAILED) return -1;

    int rc = 0;
    const uint8_t *base = map;
    uint32_t magic;
    memcpy(&magic, base, 4);

    if (magic == FAT_MAGIC || magic == FAT_CIGAM ||
        magic == FAT_MAGIC_64 || magic == FAT_CIGAM_64) {
        int is_fat64 = (magic == FAT_MAGIC_64 || magic == FAT_CIGAM_64);
        int swap = (magic == FAT_CIGAM || magic == FAT_CIGAM_64);
        struct fat_header fh;
        memcpy(&fh, base, sizeof(fh));
        uint32_t nfat = swap ? OSSwapBigToHostInt32(fh.nfat_arch) : fh.nfat_arch;
        uint64_t arch_size = is_fat64 ? sizeof(struct fat_arch_64)
                                      : sizeof(struct fat_arch);
        if (nfat == 0 || nfat > (file_sz - sizeof(struct fat_header)) / arch_size) {
            rc = -1; goto done;
        }
        for (uint32_t i = 0; i < nfat; i++) {
            uint64_t s_off, s_sz;
            if (fat_slice_range(base, file_sz, is_fat64, swap, i, &s_off, &s_sz) != 0) {
                rc = -1; goto done;
            }
            mach_slice_t slice;
            int sr = parse_thin_slice(base + s_off, (size_t)s_sz,
                                      (off_t)s_off, s_sz, &slice);
            if (sr <= 0) continue;
            if (!slice_matches_runtime(&slice.slice, rt)) continue;
            rc = collect_deps_from_slice(base + s_off, (size_t)s_sz, out);
            break;
        }
    } else {
        rc = collect_deps_from_slice(base, file_sz, out);
    }

done:
    munmap(map, file_sz);
    if (rc < 0) { macho_deps_free(out); }
    return rc;
}

void macho_deps_free(macho_deps_t *d) {
    if (!d) return;
    for (int i = 0; i < d->dep_count; i++) free(d->deps[i]);
    free(d->deps);
    for (int i = 0; i < d->rpath_count; i++) free(d->rpaths[i]);
    free(d->rpaths);
    memset(d, 0, sizeof(*d));
}
