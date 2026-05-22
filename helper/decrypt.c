#include "decrypt.h"
#include "dump.h"
#include "dyld_patch.h"
#include "exc.h"
#include "fs.h"
#include "inject.h"
#include "log.h"
#include "macho.h"
#include "spawn.h"
#include "target.h"

#include <dirent.h>
#include <dlfcn.h>
#include <errno.h>
#include <fcntl.h>
#include <signal.h>
#include <spawn.h>

// ptrace + PT_KILL aren't in any public iOS SDK header; declare here.
extern int ptrace(int, pid_t, void *, int);
#ifndef PT_KILL
#define PT_KILL 8
#endif
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/wait.h>
#include <unistd.h>

extern char **environ;

int decrypt_bundle(const char *bundle_src, const char *bundle_dst,
                   const char *bundle_id) {
    char main_name[512];
    if (spawn_find_main_name(bundle_src, main_name, sizeof(main_name)) != 0) {
        attrs_t a; attrs_init(&a);
        attrs_str(&a, "src", bundle_src);
        attrs_str(&a, "reason", "no_main_exec");
        emit(LOG_WARN, "bundle.skipped", &a,
             "no main exec in %s, skipping", bundle_src);
        return 0;
    }

    {
        attrs_t a; attrs_init(&a);
        attrs_str(&a, "src", bundle_src);
        attrs_str(&a, "main", main_name);
        emit(LOG_INFO, "bundle.begin", &a,
             "decrypting bundle %s", bundle_src);
    }

    char main_src[4096], main_dst[4096];
    snprintf(main_src, sizeof(main_src), "%s/%s", bundle_src, main_name);
    snprintf(main_dst, sizeof(main_dst), "%s/%s", bundle_dst, main_name);

    pid_t pid = 0;
    task_t task = MACH_PORT_NULL;
    int via_ptrace = 0;
    if (spawn_suspended(bundle_id, main_src, &pid, &task, &via_ptrace) != 0) {
        attrs_t a; attrs_init(&a);
        attrs_str(&a, "src", bundle_src);
        emit(LOG_ERROR, "bundle.spawn_failed", &a,
             "spawn failed for %s", bundle_src);
        return 0;
    }

    // 1) Dump the main exec from the freshly-suspended target.
    //    SBS-launched targets have dyld already past the main exec
    //    mapping, so vm_read of the encrypted region succeeds. ptrace-
    //    spawned targets (appex, or SBS-rejected main fallback) are at
    //    PT_ATTACHEXC stop; dump_image's page-fault traversal usually
    //    reaches everything before AMFI catches up.
    runtime_image_t bundle_rt;
    int have_bundle_rt = 0;
    int main_dumped = 0;
    mach_vm_address_t main_base = 0;
    if (target_find_main_base(task, &main_base) == 0 &&
        target_read_runtime_image(task, main_base, &bundle_rt) == 0) {
        have_bundle_rt = 1;
        selected_slice_t sel;
        int sr = select_runtime_slice(main_src, &bundle_rt, &sel);
        if (sr == 1 && slice_needs_dump(&sel)) {
            dump_result_t dr = dump_image(main_src, main_dst, task, main_base, &sel);
            if (dr == DUMP_OK) {
                attrs_t a; attrs_init(&a);
                attrs_str(&a, "name", main_name);
                attrs_str(&a, "kind", "main");
                attrs_str(&a, "source", "vm_read");
                attrs_uint(&a, "size", sel.selected.crypt.cryptsize);
                emit(LOG_INFO, "image.done", &a,
                     "decrypted main %s (%s)", main_name,
                     human_bytes(sel.selected.crypt.cryptsize));
                main_dumped = 1;
            } else if (!via_ptrace) {
                attrs_t a; attrs_init(&a);
                attrs_str(&a, "name", main_name);
                attrs_str(&a, "kind", "main");
                attrs_str(&a, "reason", dump_reason(dr));
                emit(LOG_WARN, "image.failed", &a,
                     "failed to dump main %s (%s)", main_name, dump_reason(dr));
            }
        } else if (sr == 1) {
            main_dumped = 1; // nothing to do (cryptid already 0)
        }
    }

    // ptrace path (appex, or SBS-rejected main): stop here. Main exec
    // is what we got from the PT_ATTACHEXC stop  resuming via
    // PT_CONTINUE would just race AMFI's runtime kill.
    if (via_ptrace) {
        ptrace(PT_KILL, pid, 0, 0);
        task_terminate(task);
        kill(pid, SIGKILL);
        int reaped;
        pid_t rw;
        do { rw = waitpid(pid, &reaped, 0); } while (rw < 0 && errno == EINTR);
        attrs_t a; attrs_init(&a);
        attrs_str(&a, "src", bundle_src);
        attrs_int(&a, "extras", 0);
        emit(LOG_INFO, "bundle.done", &a,
             "bundle done: main only (ptrace, no framework enumeration)");
        return 0;
    }

    // 2) Set exception port + patch dyld halt path BEFORE resume.
    //    force-weak, kill_svc, skip_halt patches NOP the svc/brk/halt
    //    sites that would otherwise SIGKILL the target during the
    //    long dlopen chain (needed for PythonCoding-class apps with
    //    hundreds of dynamically-loaded cross-OS extensions).
    mach_port_t exc = exc_make_port(task);
    {
        uint32_t pre_count = 0;
        char *pre_paths = NULL;
        struct dyld_image_info *pre_imgs = target_list_images(task, &pre_count, &pre_paths);
        Dl_info hi = {0};
        dladdr((void *)dlopen, &hi);
        mach_vm_address_t target_libdyld = target_find_image(task,
            pre_imgs, pre_count, hi.dli_fname);
        mach_vm_address_t helper_libdyld_v =
            (mach_vm_address_t)(uintptr_t)hi.dli_fbase;
        dyld_patch_apply(task, pre_imgs, pre_count,
            target_libdyld, helper_libdyld_v);
        free(pre_paths); free(pre_imgs);
    }

    // 3s upper bound. Mach-exception delivery short-circuits the wait,
    // so healthy targets are the only thing that pays the full timeout.
    spawn_run_and_suspend(task, exc, 3000);

    // Retry the main-exec dump if the first attempt hit unfaulted
    // pages. By now dyld has the whole image mapped.
    if (!main_dumped && have_bundle_rt && main_base != 0) {
        selected_slice_t sel;
        if (select_runtime_slice(main_src, &bundle_rt, &sel) == 1 &&
            slice_needs_dump(&sel)) {
            dump_result_t dr = dump_image(main_src, main_dst, task, main_base, &sel);
            attrs_t a; attrs_init(&a);
            attrs_str(&a, "name", main_name);
            attrs_str(&a, "kind", "main");
            attrs_str(&a, "source", "vm_read_retry");
            if (dr == DUMP_OK) {
                attrs_uint(&a, "size", sel.selected.crypt.cryptsize);
                emit(LOG_INFO, "image.done", &a,
                     "decrypted main %s (%s)", main_name,
                     human_bytes(sel.selected.crypt.cryptsize));
                main_dumped = 1;
            } else {
                attrs_str(&a, "reason", dump_reason(dr));
                emit(LOG_WARN, "image.failed", &a,
                     "failed to dump main %s (%s)", main_name, dump_reason(dr));
            }
        }
    }

    // 3) Enumerate images loaded in the (now suspended) target
    //    and dump every encrypted one whose path is inside this bundle.
    uint32_t img_count = 0;
    char *paths = NULL;
    struct dyld_image_info *imgs = target_list_images(task, &img_count, &paths);
    int extra = 0;

    const char *bs = bundle_src;
    size_t bs_len = strlen(bs);
    char bs_pri[4096];
    snprintf(bs_pri, sizeof(bs_pri), "/private%s", bs);
    size_t bs_alt_len = strlen(bs_pri);

    for (uint32_t i = 0; i < img_count && imgs; i++) {
        const char *ip = imgs[i].imageFilePath;
        if (!ip) continue;
        const char *rel = NULL;
        if (strncmp(ip, bs, bs_len) == 0) rel = ip + bs_len;
        else if (strncmp(ip, bs_pri, bs_alt_len) == 0) rel = ip + bs_alt_len;
        if (!rel) continue;
        while (*rel == '/') rel++;
        if (!*rel) continue;
        if (strcmp(rel, main_name) == 0) continue;

        char rel_src[4096], rel_dst[4096];
        snprintf(rel_src, sizeof(rel_src), "%s/%s", bundle_src, rel);
        snprintf(rel_dst, sizeof(rel_dst), "%s/%s", bundle_dst, rel);

        mach_vm_address_t base = (mach_vm_address_t)(uintptr_t)imgs[i].imageLoadAddress;
        runtime_image_t img_rt;
        if (target_read_runtime_image(task, base, &img_rt) != 0) continue;
        if (!have_bundle_rt) { bundle_rt = img_rt; have_bundle_rt = 1; }

        selected_slice_t sel;
        if (select_runtime_slice(rel_src, &img_rt, &sel) != 1) continue;
        if (!slice_needs_dump(&sel)) continue;

        {
            attrs_t a0; attrs_init(&a0);
            attrs_str(&a0, "name", rel);
            attrs_str(&a0, "kind", "framework");
            attrs_uint(&a0, "size", sel.selected.crypt.cryptsize);
            emit(LOG_DEBUG, "image.begin", &a0,
                 "decrypting %s (%s)", rel,
                 human_bytes(sel.selected.crypt.cryptsize));
        }
        dump_result_t dr = dump_image(rel_src, rel_dst, task, base, &sel);
        attrs_t a; attrs_init(&a);
        attrs_str(&a, "name", rel);
        attrs_str(&a, "kind", "framework");
        attrs_str(&a, "source", "vm_read");
        if (dr == DUMP_OK) {
            attrs_uint(&a, "size", sel.selected.crypt.cryptsize);
            emit(LOG_INFO, "image.done", &a,
                 "decrypted %s (%s)", rel,
                 human_bytes(sel.selected.crypt.cryptsize));
            extra++;
        } else {
            attrs_str(&a, "reason", dump_reason(dr));
            emit(LOG_WARN, "image.failed", &a,
                 "failed to dump %s (%s)", rel, dump_reason(dr));
        }
    }

    // 4) Inject dlopen for any encrypted bundle Mach-O dyld didn't map.
    if (have_bundle_rt) {
        extra += inject_missing_frameworks(task, exc, bundle_src,
            bundle_dst, imgs, img_count, &bundle_rt);
    }

    free(paths); free(imgs);

    mach_port_mod_refs(mach_task_self(), exc, MACH_PORT_RIGHT_RECEIVE, -1);
    task_terminate(task);
    kill(pid, SIGKILL);
    int reaped;
    pid_t rw;
    do { rw = waitpid(pid, &reaped, 0); } while (rw < 0 && errno == EINTR);

    attrs_t a; attrs_init(&a);
    attrs_str(&a, "src", bundle_src);
    attrs_int(&a, "extras", extra);
    emit(LOG_INFO, "bundle.done", &a,
         "bundle done: %d framework(s) decrypted", extra);
    return 0;
}

void decrypt_appexes(const char *bundle_src, const char *bundle_dst) {
    const char *subdirs[] = { "PlugIns", "Extensions", NULL };
    for (int si = 0; subdirs[si]; si++) {
        char dir_src[4096], dir_dst[4096];
        snprintf(dir_src, sizeof(dir_src), "%s/%s", bundle_src, subdirs[si]);
        snprintf(dir_dst, sizeof(dir_dst), "%s/%s", bundle_dst, subdirs[si]);
        DIR *d = opendir(dir_src);
        if (!d) continue;
        struct dirent *e;
        while ((e = readdir(d))) {
            if (strcmp(e->d_name, ".") == 0 || strcmp(e->d_name, "..") == 0) continue;
            const char *dot = strrchr(e->d_name, '.');
            if (!dot || strcmp(dot, ".appex") != 0) continue;
            char s[4096], t[4096];
            snprintf(s, sizeof(s), "%s/%s", dir_src, e->d_name);
            snprintf(t, sizeof(t), "%s/%s", dir_dst, e->d_name);
            decrypt_bundle(s, t, NULL);
        }
        closedir(d);
    }
}

int run_zip(const char *staging, const char *ipa_path) {
    char cwd_save[4096];
    if (!getcwd(cwd_save, sizeof(cwd_save))) cwd_save[0] = '\0';
    if (chdir(staging) != 0) {
        er("chdir %s: %s", staging, strerror(errno));
        return -1;
    }

    posix_spawn_file_actions_t fa;
    posix_spawn_file_actions_init(&fa);
    posix_spawn_file_actions_addopen(&fa, 1, "/dev/null", O_WRONLY, 0);
    posix_spawn_file_actions_addopen(&fa, 2, "/dev/null", O_WRONLY, 0);
    char *argv[] = { "zip", "-qr", "-1", (char *)ipa_path, "Payload",
        "-x",
        "Payload/*/Watch/*", "Payload/*/Watch",
        "Payload/*/WatchKitSupport2/*", "Payload/*/WatchKitSupport2",
        "Payload/*/SC_Info/*", "Payload/*/SC_Info",
        "*/SC_Info/*", "*/SC_Info",
        "Payload/*/*.dSYM/*", "Payload/*/*.dSYM",
        "Payload/*/BCSymbolMaps/*", "Payload/*/BCSymbolMaps",
        "Payload/*/Symbols/*", "Payload/*/Symbols",
        "Payload/META-INF/*", "Payload/META-INF",
        "Payload/iTunesMetadata.plist", "Payload/iTunesArtwork",
        NULL };
    pid_t pid = 0;
    const char *zip_paths[] = { "/var/jb/usr/bin/zip", "/usr/bin/zip", NULL };
    int rc = -1;
    for (int i = 0; zip_paths[i]; i++) {
        if (access(zip_paths[i], X_OK) != 0) continue;
        rc = posix_spawn(&pid, zip_paths[i], &fa, NULL, argv, environ);
        if (rc == 0) break;
    }
    posix_spawn_file_actions_destroy(&fa);
    if (cwd_save[0]) chdir(cwd_save);
    if (rc != 0 || pid == 0) {
        er("zip not available");
        return -1;
    }
    int st;
    waitpid(pid, &st, 0);
    return (WIFEXITED(st) && WEXITSTATUS(st) == 0) ? 0 : -1;
}
