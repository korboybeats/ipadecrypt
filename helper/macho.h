#ifndef HELPER_MACHO_H
#define HELPER_MACHO_H

#include <stdint.h>
#include <sys/types.h>

// Arch fingerprint of a loaded image, read from the target task.
typedef struct {
    uint32_t cputype;
    uint32_t cpusubtype;
    int      is_64;
} runtime_image_t;

// Thin Mach-O slice geometry within a file (whole file for thin Mach-O).
typedef struct {
    off_t    slice_offset;
    uint64_t slice_size;
    int      is_64;
    uint32_t cputype;
    uint32_t cpusubtype;
} slice_meta_t;

// LC_ENCRYPTION_INFO[_64] findings. has_crypt=0 means no such load command.
typedef struct {
    int      has_crypt;
    uint32_t cryptoff;
    uint32_t cryptsize;
    uint32_t cryptid;
    off_t    cryptid_file_offset;
} crypt_meta_t;

// LC_CODE_SIGNATURE findings, kept on every parsed slice for diagnostics.
typedef struct {
    int      has_cs;
    uint32_t cs_offset;
    uint32_t cs_size;
} cs_meta_t;

typedef struct {
    slice_meta_t slice;
    crypt_meta_t crypt;
    cs_meta_t    cs;
} mach_slice_t;

typedef struct {
    mach_slice_t selected;
    int          is_fat;
    int          any_slice_encrypted;
} selected_slice_t;

// Strip CPU capability bits so arm64/arm64e and signed variants compare
// on their base subtype.
uint32_t cpusubtype_base(uint32_t subtype);

int slice_matches_runtime(const slice_meta_t *slice, const runtime_image_t *rt);

// True when the matched slice is encrypted, or when the file is fat with
// any encrypted slice (we will thin to drop the sibling).
int slice_needs_dump(const selected_slice_t *sel);

// Parse one thin Mach-O slice. Returns 1 on success, 0 if the bytes
// aren't a Mach-O, -1 if malformed.
int parse_thin_slice(const uint8_t *slice, size_t slice_len,
                     off_t slice_off, uint64_t slice_size,
                     mach_slice_t *out);

// Open a Mach-O file (thin or fat[64]) and pick the slice matching `rt`.
// Returns 1 with *out filled, 0 if no slice matches or file isn't
// Mach-O, -1 on I/O or malformed input.
int select_runtime_slice(const char *path,
                         const runtime_image_t *rt,
                         selected_slice_t *out);

// LC_LOAD_* dylib names + LC_RPATH search paths from a single Mach-O.
// `deps` are raw name strings from LC_LOAD_DYLIB/WEAK_DYLIB/REEXPORT_DYLIB/
// UPWARD_DYLIB/LAZY_LOAD_DYLIB (may start with @rpath/@loader_path/
// @executable_path or be absolute). `rpaths` are raw LC_RPATH values.
// Caller frees with macho_deps_free().
typedef struct {
    char   **deps;
    int      dep_count;
    char   **rpaths;
    int      rpath_count;
} macho_deps_t;

int macho_collect_deps(const char *path, const runtime_image_t *rt,
                       macho_deps_t *out);
void macho_deps_free(macho_deps_t *d);

#endif // HELPER_MACHO_H
