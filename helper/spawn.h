#ifndef HELPER_SPAWN_H
#define HELPER_SPAWN_H

#include <mach/mach.h>
#include <sys/types.h>

// Find the main executable file name inside a bundle directory. Probes
// <basename>.app/<basename> first, then any Mach-O in the top of the
// bundle. Returns 0 on success.
int spawn_find_main_name(const char *bundle, char *out, size_t cap);

// Spawn `exec_path` suspended.
//   bundle_id non-empty: SBS launch (launchd lineage). If SBS rejects
//     (e.g. AMFI cross-SDK reject = error 12), fall back to PT_TRACE_ME.
//   bundle_id empty:     PT_TRACE_ME directly. Used for .appex which
//     SBS won't launch by id.
// *out_ptrace=1 iff the ptrace path was taken (caller should use the
// PT_ATTACHEXC-stop short-circuit: vm_read main exec, kill target).
// Returns 0 on success.
int spawn_suspended(const char *bundle_id, const char *exec_path,
                    pid_t *out_pid, task_t *out_task, int *out_ptrace);

// Resume the SBS-launched target and wait up to `ms` ms for a Mach
// exception (cross-OS bind-fail abort or dyld halt brk) or timeout,
// then suspend the task. Mach-exception delivery short-circuits the
// wait, so healthy targets are the only thing that pays the full
// timeout.
void spawn_run_and_suspend(task_t task, mach_port_t exc_port, int ms);

#endif // HELPER_SPAWN_H
