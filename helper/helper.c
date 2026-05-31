#include "args.h"
#include "decrypt.h"
#include "fs.h"
#include "log.h"

#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

#ifndef HELPER_VERSION
#define HELPER_VERSION "ipadecrypt-helper (dev)"
#endif

static int is_stdout_target(const char *path) {
    return path && path[0] == '-' && path[1] == '\0';
}

// Stream one Mach-O file as a framed record on stdout. Frame:
//   [u32be plen][plen-byte path][u64be size][size bytes]
static int emit_macho_frame(const char *relpath, const char *abs_path) {
    int fd = open(abs_path, O_RDONLY);
    if (fd < 0) { er("open %s: %s", abs_path, strerror(errno)); return -1; }

    struct stat st;
    if (fstat(fd, &st) != 0) { close(fd); return -1; }

    uint64_t size = (uint64_t)st.st_size;
    uint32_t plen = (uint32_t)strlen(relpath);

    uint8_t hdr[12];
    hdr[0] = (uint8_t)(plen >> 24);
    hdr[1] = (uint8_t)(plen >> 16);
    hdr[2] = (uint8_t)(plen >> 8);
    hdr[3] = (uint8_t)plen;
    for (int i = 0; i < 8; i++) hdr[4 + i] = (uint8_t)(size >> ((7 - i) * 8));

    if (fs_write_all(1, hdr, 4) != 0) { close(fd); return -1; }
    if (fs_write_all(1, relpath, plen) != 0) { close(fd); return -1; }
    if (fs_write_all(1, hdr + 4, 8) != 0) { close(fd); return -1; }

    char buf[64 * 1024];
    for (;;) {
        ssize_t n = read(fd, buf, sizeof(buf));
        if (n < 0) { if (errno == EINTR) continue; close(fd); return -1; }
        if (n == 0) break;
        if (fs_write_all(1, buf, (size_t)n) != 0) { close(fd); return -1; }
    }

    close(fd);
    return 0;
}

// Walk staging tree, emitting one framed record per Mach-O file we
// actually decrypted. The filter `st_nlink == 1` identifies decrypted
// files: dump.c writes via unlink-then-create, which detaches from the
// fs_copy_tree hardlink and leaves the new file with nlink=1. Pure
// hardlinks-from-source stay at nlink>=2 (source bundle copy +
// staging copy) and are skipped - the host substitutes them from the
// source IPA verbatim.
//
// Per-file errors are best-effort: logged and skipped so a single
// unreadable file doesn't abort the whole stream. A serious I/O error
// inside emit_macho_frame (header write fails) does propagate, since
// after that the stream is desynced and the host can't recover.
static int emit_machos_recursive(const char *root, const char *rel) {
    char abs[4096];
    if (rel[0]) snprintf(abs, sizeof(abs), "%s/%s", root, rel);
    else        snprintf(abs, sizeof(abs), "%s", root);

    struct stat st;
    if (lstat(abs, &st) != 0) return 0;

    if (S_ISDIR(st.st_mode)) {
        DIR *d = opendir(abs);
        if (!d) return 0;

        struct dirent *e;
        int rc = 0;
        while ((e = readdir(d))) {
            if (strcmp(e->d_name, ".") == 0 || strcmp(e->d_name, "..") == 0) continue;

            char child[4096];
            if (rel[0]) snprintf(child, sizeof(child), "%s/%s", rel, e->d_name);
            else        snprintf(child, sizeof(child), "%s", e->d_name);

            if (emit_machos_recursive(root, child) != 0) rc = -1;
        }

        closedir(d);
        return rc;
    }

    if (S_ISREG(st.st_mode) && st.st_nlink == 1 && fs_is_macho(abs)) {
        return emit_macho_frame(rel, abs);
    }

    return 0;
}

static int run_decrypt(const decrypt_args_t *a) {
    // Resolve bundle basename for the staging Payload/ layout.
    char src_copy[4096];
    snprintf(src_copy, sizeof(src_copy), "%s", a->bundle_src);
    char *app_name = strrchr(src_copy, '/');
    app_name = app_name ? app_name + 1 : src_copy;
    if (!*app_name || strchr(app_name, '/') != NULL) {
        er("bad bundle path: %s", a->bundle_src);
        return 1;
    }

    char staging[4096];
    snprintf(staging, sizeof(staging), "/tmp/ipadecrypt-%d", getpid());
    fs_mkdirs(staging);
    char payload[4096];
    snprintf(payload, sizeof(payload), "%s/Payload", staging);
    mkdir(payload, 0755);
    char bundle_dst[4096];
    snprintf(bundle_dst, sizeof(bundle_dst), "%s/%s", payload, app_name);

    {
        attrs_t at; attrs_init(&at);
        attrs_str(&at, "src", a->bundle_src);
        attrs_str(&at, "dst", bundle_dst);
        emit(LOG_DEBUG, "staging.begin", &at,
             "staging %s -> %s", a->bundle_src, bundle_dst);
    }
    if (fs_copy_tree(a->bundle_src, bundle_dst) != 0) {
        er("copy_tree failed");
        fs_rm_rf(staging);
        return 1;
    }

    if (a->bundle_id && a->bundle_id[0]) {
        decrypt_bundle(a->bundle_src, bundle_dst, a->bundle_id);
    }
    if (!a->skip_appex) {
        decrypt_appexes(a->bundle_src, bundle_dst);
    }

    if (a->execs_only) {
        emit(LOG_DEBUG, "stream.begin", NULL, "streaming Mach-O frames on stdout");

        int rc = emit_machos_recursive(staging, "");
        fs_rm_rf(staging);

        if (rc != 0) {
            emit(LOG_ERROR, "stream.failed", NULL, "frame emit failed");
            return 1;
        }

        emit(LOG_INFO, "done", NULL, "done");
        return 0;
    }

    const char *ipa_label = is_stdout_target(a->out_ipa) ? "stdout" : a->out_ipa;

    if (!is_stdout_target(a->out_ipa)) unlink(a->out_ipa);
    {
        attrs_t at; attrs_init(&at);
        attrs_str(&at, "ipa", a->out_ipa);
        emit(LOG_DEBUG, "pack.begin", &at, "packaging IPA -> %s", ipa_label);
    }
    if (run_zip(staging, a->out_ipa) != 0) {
        attrs_t at; attrs_init(&at);
        attrs_str(&at, "ipa", a->out_ipa);
        emit(LOG_ERROR, "pack.failed", &at, "zip failed for %s", ipa_label);
        fs_rm_rf(staging);
        return 1;
    }
    {
        attrs_t at; attrs_init(&at);
        attrs_str(&at, "ipa", a->out_ipa);
        emit(LOG_INFO, "pack.done", &at, "packaged -> %s", ipa_label);
    }
    fs_rm_rf(staging);
    {
        attrs_t at; attrs_init(&at);
        attrs_str(&at, "ipa", a->out_ipa);
        emit(LOG_INFO, "done", &at, "done");
    }
    return 0;
}

int main(int argc, char **argv) {
    // Streaming on stdout (--execs-only frames or `-o -` IPA) would die
    // from default SIGPIPE if the host closes early, skipping fs_rm_rf
    // and leaking /tmp/ipadecrypt-<pid>. Ignore it so write() returns
    // EPIPE and the normal cleanup path runs.
    signal(SIGPIPE, SIG_IGN);

    global_flags_t globals;
    decrypt_args_t da;
    const char *sub = args_parse(argc, argv, &globals, &da);
    if (!sub) return 2;

    log_init(globals.verbose);

    // When streaming binary on stdout (IPA bytes or framed Mach-O records),
    // route events to stderr so they don't corrupt the data channel.
    if (strcmp(sub, "decrypt") == 0 &&
        (is_stdout_target(da.out_ipa) || da.execs_only)) {
        log_set_stream(stderr);
    }

    if (strcmp(sub, "version") == 0) {
        printf("%s\n", HELPER_VERSION);
        return 0;
    }

    if (strcmp(sub, "decrypt") == 0) {
        return run_decrypt(&da);
    }

    er("unknown subcommand: %s", sub);
    return 2;
}
