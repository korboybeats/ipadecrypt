#include "fs.h"
#include "log.h"

#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <mach-o/loader.h>
#include <mach-o/fat.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>

int fs_mkdirs(const char *path) {
    char buf[4096];
    strncpy(buf, path, sizeof(buf) - 1); buf[sizeof(buf) - 1] = '\0';
    for (char *p = buf + 1; *p; p++) {
        if (*p == '/') {
            *p = '\0';
            mkdir(buf, 0755);
            *p = '/';
        }
    }
    mkdir(buf, 0755);
    return 0;
}

int fs_rm_rf(const char *path) {
    struct stat st;
    if (lstat(path, &st) != 0) return 0;
    if (S_ISDIR(st.st_mode) && !S_ISLNK(st.st_mode)) {
        DIR *d = opendir(path);
        if (d) {
            struct dirent *e;
            while ((e = readdir(d))) {
                if (strcmp(e->d_name, ".") == 0 || strcmp(e->d_name, "..") == 0) continue;
                char sub[4096];
                snprintf(sub, sizeof(sub), "%s/%s", path, e->d_name);
                fs_rm_rf(sub);
            }
            closedir(d);
        }
        return rmdir(path);
    }
    return unlink(path);
}

int fs_write_all(int fd, const void *buf, size_t len) {
    const uint8_t *p = buf;
    while (len > 0) {
        ssize_t n = write(fd, p, len);
        if (n < 0) {
            if (errno == EINTR) continue;
            return -1;
        }
        if (n == 0) { errno = EIO; return -1; }
        p += n;
        len -= (size_t)n;
    }
    return 0;
}

int fs_read_full(int fd, void *buf, size_t len) {
    uint8_t *p = buf;
    while (len > 0) {
        ssize_t n = read(fd, p, len);
        if (n < 0) {
            if (errno == EINTR) continue;
            return -1;
        }
        if (n == 0) { errno = EIO; return -1; }
        p += n;
        len -= (size_t)n;
    }
    return 0;
}

int fs_copy_file(const char *src, const char *dst) {
    int in = open(src, O_RDONLY);
    if (in < 0) return -1;
    struct stat st;
    if (fstat(in, &st) != 0) { close(in); return -1; }
    int out = open(dst, O_WRONLY | O_CREAT | O_TRUNC, st.st_mode & 0777);
    if (out < 0) { close(in); return -1; }
    char buf[64 * 1024];
    ssize_t n = 0;
    for (;;) {
        n = read(in, buf, sizeof(buf));
        if (n < 0 && errno == EINTR) continue;
        if (n <= 0) break;
        if (fs_write_all(out, buf, (size_t)n) != 0) { close(in); close(out); return -1; }
    }
    close(in); close(out);
    return n < 0 ? -1 : 0;
}

int fs_copy_tree(const char *src, const char *dst) {
    struct stat st;
    if (lstat(src, &st) != 0) return -1;
    if (S_ISLNK(st.st_mode)) {
        char target[4096];
        ssize_t n = readlink(src, target, sizeof(target) - 1);
        if (n < 0) return -1;
        target[n] = '\0';
        return symlink(target, dst) == 0 ? 0 : -1;
    }
    if (S_ISDIR(st.st_mode)) {
        mkdir(dst, st.st_mode & 0777);
        DIR *d = opendir(src);
        if (!d) return -1;
        struct dirent *e;
        int rc = 0;
        while ((e = readdir(d))) {
            if (strcmp(e->d_name, ".") == 0 || strcmp(e->d_name, "..") == 0) continue;
            char s[4096], t[4096];
            snprintf(s, sizeof(s), "%s/%s", src, e->d_name);
            snprintf(t, sizeof(t), "%s/%s", dst, e->d_name);
            if (fs_copy_tree(s, t) != 0) rc = -1;
        }
        closedir(d);
        return rc;
    }
    if (link(src, dst) == 0) return 0;
    return fs_copy_file(src, dst);
}

int fs_is_macho(const char *path) {
    int fd = open(path, O_RDONLY);
    if (fd < 0) return 0;
    uint32_t m = 0;
    ssize_t n = read(fd, &m, sizeof(m));
    close(fd);
    return n == (ssize_t)sizeof(m) &&
        (m == MH_MAGIC || m == MH_MAGIC_64 ||
         m == FAT_MAGIC || m == FAT_CIGAM ||
         m == FAT_MAGIC_64 || m == FAT_CIGAM_64);
}

void fs_ensure_executable(const char *path) {
    struct stat st;
    if (stat(path, &st) != 0) return;
    mode_t want = st.st_mode | S_IXUSR | S_IXGRP | S_IXOTH;
    if (want == st.st_mode) return;
    if (chmod(path, want) == 0) {
        attrs_t a; attrs_init(&a);
        attrs_str(&a, "path", path);
        attrs_fmt(&a, "old_mode", "%o", st.st_mode & 0777);
        emit(LOG_DEBUG, "spawn.chmod", &a, "chmod +x %s", path);
    }
}

int fs_path_equiv(const char *a, const char *b) {
    if (strcmp(a, b) == 0) return 1;
    const char *pre = "/private";
    size_t plen = strlen(pre);
    if (strncmp(a, pre, plen) == 0 && strcmp(a + plen, b) == 0) return 1;
    if (strncmp(b, pre, plen) == 0 && strcmp(b + plen, a) == 0) return 1;
    return 0;
}
