#ifndef HELPER_DECRYPT_H
#define HELPER_DECRYPT_H

// Decrypt one bundle (main .app, .appex, or any .framework with
// executable). When bundle_id is non-empty, SBS is used as the launch
// method (only works for main apps); empty/NULL forces ptrace.
int decrypt_bundle(const char *bundle_src, const char *bundle_dst,
                   const char *bundle_id);

// Walk PlugIns/ and Extensions/ at the top of main_app and decrypt each
// .appex bundle via the ptrace path.
void decrypt_appexes(const char *bundle_src, const char *bundle_dst);

// Zip the staging tree's Payload/ into out_ipa ("-" streams to stdout).
int run_zip(const char *staging, const char *ipa_path);

#endif // HELPER_DECRYPT_H
