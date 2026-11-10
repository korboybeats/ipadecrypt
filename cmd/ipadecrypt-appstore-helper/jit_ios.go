//go:build ios

package main

/*
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <spawn.h>
#include <sys/wait.h>
#include <errno.h>

extern char **environ;
extern int csops(pid_t pid, unsigned int ops, void *useraddr, size_t usersize);

#define CS_OPS_STATUS 0
#define CS_DEBUGGED 0x10000000

static int ipd_helper_is_debugged(void) {
    unsigned int flags = 0;
    if (csops(getpid(), CS_OPS_STATUS, &flags, sizeof(flags)) != 0) return 0;
    return (flags & CS_DEBUGGED) != 0;
}

static int ipd_helper_run_jbctl(const char *path) {
    if (access(path, X_OK) != 0) return -1;
    char pid[16];
    snprintf(pid, sizeof(pid), "%d", getpid());
    char *argv[] = {(char *)path, "proc_set_debugged", pid, NULL};
    posix_spawn_file_actions_t fa;
    posix_spawn_file_actions_init(&fa);
    posix_spawn_file_actions_addopen(&fa, STDOUT_FILENO, "/dev/null", O_WRONLY, 0);
    posix_spawn_file_actions_addopen(&fa, STDERR_FILENO, "/dev/null", O_WRONLY, 0);
    pid_t child;
    int rc = posix_spawn(&child, path, &fa, NULL, argv, environ);
    posix_spawn_file_actions_destroy(&fa);
    if (rc != 0) return -1;
    int st = 0;
    while (waitpid(child, &st, 0) < 0 && errno == EINTR) {}
    return (WIFEXITED(st) && WEXITSTATUS(st) == 0) ? 0 : -1;
}

static int ipd_helper_ensure_debugged(void) {
    if (ipd_helper_is_debugged()) return 1;
    const char *env = getenv("IPADECRYPT_JBCTL");
    if (env && *env && ipd_helper_run_jbctl(env) == 0 && ipd_helper_is_debugged()) return 1;
    const char *fixed[] = {"/var/jb/basebin/jbctl", "/var/jb/usr/bin/jbctl"};
    for (int i = 0; i < 2; i++) {
        if (ipd_helper_run_jbctl(fixed[i]) == 0 && ipd_helper_is_debugged()) return 1;
    }
    return ipd_helper_is_debugged();
}
*/
import "C"

import (
	"errors"
	"fmt"
)

func ensureUnicornJITReady() error {
	if C.ipd_helper_ensure_debugged() != 0 {
		emit("phase", "step", "name", "jit-ready")
		return nil
	}
	msg := "could not mark process CS_DEBUGGED via jbctl; Unicorn JIT will fault. Is Dopamine jbctl available?"
	emit("phase", "failed", "reason", "jit-not-ready", "message", msg)
	return fmt.Errorf("%w: %s", errJITNotReady, msg)
}

var errJITNotReady = errors.New("unicorn JIT precondition failed")
