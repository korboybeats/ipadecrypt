#include "args.h"
#include "decrypt.h"
#include "fs.h"
#include "log.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

#ifndef HELPER_VERSION
#define HELPER_VERSION "ipadecrypt-helper (dev)"
#endif

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

    unlink(a->out_ipa);
    {
        attrs_t at; attrs_init(&at);
        attrs_str(&at, "ipa", a->out_ipa);
        emit(LOG_DEBUG, "pack.begin", &at, "packaging IPA -> %s", a->out_ipa);
    }
    if (run_zip(staging, a->out_ipa) != 0) {
        attrs_t at; attrs_init(&at);
        attrs_str(&at, "ipa", a->out_ipa);
        emit(LOG_ERROR, "pack.failed", &at, "zip failed for %s", a->out_ipa);
        fs_rm_rf(staging);
        return 1;
    }
    {
        attrs_t at; attrs_init(&at);
        attrs_str(&at, "ipa", a->out_ipa);
        emit(LOG_INFO, "pack.done", &at, "packaged -> %s", a->out_ipa);
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
    global_flags_t globals;
    decrypt_args_t da;
    const char *sub = args_parse(argc, argv, &globals, &da);
    if (!sub) return 2;

    log_init(globals.verbose);

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
