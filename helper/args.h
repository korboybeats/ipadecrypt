#ifndef HELPER_ARGS_H
#define HELPER_ARGS_H

#include <stdio.h>

// Subcommand dispatch + flag parsing.
//
// Usage:
//   helper [-v] decrypt [--skip-appex] <bundle-id> <bundle-src> <out-ipa>
//   helper version
//   helper -h
//
// Globals (accepted before or after the subcommand):
//   -v, --verbose          emit LOG_DEBUG events too (extra detail)
//   -h, --help             print usage and exit 0
//
// decrypt flags:
//   --skip-appex           don't decrypt Payload/<App>.app/PlugIns/*.appex
//   --execs-only           stream framed Mach-O records on stdout instead of
//                          packaging an IPA on-device. Events go to stderr.
//                          out-ipa is ignored.

typedef struct {
    const char *bundle_id;   // CFBundleIdentifier for SBS, or "" for ptrace-only
    const char *bundle_src;  // installed .app path on disk
    const char *out_ipa;     // output IPA path; ignored when execs_only is set
    int skip_appex;          // skip the appex pass; extensions stay encrypted
    int execs_only;          // emit framed Mach-O records on stdout; no zip
} decrypt_args_t;

typedef struct {
    int verbose;
} global_flags_t;

// Parse argv. Returns subcommand name on success ("decrypt" | "version").
// On --help / version subcommand handled inline, returns the name.
// On parse error, prints usage to stderr and returns NULL.
const char *args_parse(int argc, char **argv,
                       global_flags_t *out_globals,
                       decrypt_args_t *out_decrypt);

void args_usage(FILE *out, const char *progname);

#endif // HELPER_ARGS_H
