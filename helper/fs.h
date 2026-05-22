#ifndef HELPER_FS_H
#define HELPER_FS_H

#include <sys/types.h>
#include <stddef.h>

// File-system helpers.

// Recursively create a directory path, ignoring already-exists.
int fs_mkdirs(const char *path);

// Recursively remove a path. Returns 0 on success.
int fs_rm_rf(const char *path);

// Like write(2)/read(2) but loop until len bytes are moved.
int fs_write_all(int fd, const void *buf, size_t len);
int fs_read_full(int fd, void *buf, size_t len);

// Copy a single regular file. Returns 0 on success.
int fs_copy_file(const char *src, const char *dst);

// Copy a tree. Regular files are hardlinked when possible (same fs);
// directories and symlinks are recreated. write_output() must unlink()
// before O_CREAT to break any hardlinks before overwriting decrypted
// Mach-Os, so the original installed bundle's inodes aren't touched.
int fs_copy_tree(const char *src, const char *dst);

// True if path looks like a Mach-O (thin or fat magic in first 4 bytes).
int fs_is_macho(const char *path);

// chmod 0755 if file exists; logged on failure.
void fs_ensure_executable(const char *path);

// True if paths are equal modulo a /private prefix on either side.
int fs_path_equiv(const char *a, const char *b);

#endif // HELPER_FS_H
