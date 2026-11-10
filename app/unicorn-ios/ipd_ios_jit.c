/* Created by IPA Decryption bot */
/* W^X code-buffer switching for Unicorn on jailbroken iOS. See ipd_ios_jit.h. */
#include <errno.h>
#include <fcntl.h>
#include <mach-o/dyld.h>
#include <pthread.h>
#include <spawn.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/wait.h>
#include <unistd.h>

#define IPD_MAX_BUFFERS 32
#define CS_OPS_STATUS 0
#define CS_DEBUGGED 0x10000000

extern char **environ;
extern int csops(pid_t pid, unsigned int ops, void *useraddr, size_t usersize);

static pthread_mutex_t ipd_lock = PTHREAD_MUTEX_INITIALIZER;
static struct { void *buf; size_t size; } ipd_bufs[IPD_MAX_BUFFERS];
static int ipd_debugged;

static int ipd_is_debugged(void) {
    unsigned int flags = 0;
    if (csops(getpid(), CS_OPS_STATUS, &flags, sizeof(flags)) != 0) return 0;
    return (flags & CS_DEBUGGED) != 0;
}

static int ipd_run_jbctl(const char *path) {
    if (access(path, X_OK) != 0) return -1;
    char pid[16];
    snprintf(pid, sizeof(pid), "%d", getpid());
    char *argv[] = {(char *)path, "proc_set_debugged", pid, NULL};
    /* jbctl prints a status line; keep it off our stdout (it may be a protocol pipe). */
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

/* Marks this process CS_DEBUGGED so modified pages may become executable. */
static int ipd_ensure_debugged(void) {
    if (ipd_debugged || ipd_is_debugged()) return ipd_debugged = 1;

    const char *env = getenv("IPADECRYPT_JBCTL");
    if (env && *env && ipd_run_jbctl(env) == 0 && ipd_is_debugged()) return ipd_debugged = 1;

    static const char *fixed[] = {"/var/jb/basebin/jbctl", "/var/jb/usr/bin/jbctl"};
    for (size_t i = 0; i < sizeof(fixed) / sizeof(fixed[0]); i++)
        if (ipd_run_jbctl(fixed[i]) == 0 && ipd_is_debugged()) return ipd_debugged = 1;

    /* RootHide: derive the randomized jbroot from our own executable path. */
    char exe[4096];
    uint32_t n = sizeof(exe);
    if (_NSGetExecutablePath(exe, &n) == 0) {
        char *mark = strstr(exe, "/.jbroot-");
        if (mark) {
            char *end = strchr(mark + 1, '/');
            if (end) *end = '\0';
            static const char *rel[] = {"/basebin/jbctl", "/usr/bin/jbctl"};
            for (size_t i = 0; i < sizeof(rel) / sizeof(rel[0]); i++) {
                char cand[4200];
                snprintf(cand, sizeof(cand), "%s%s", exe, rel[i]);
                if (ipd_run_jbctl(cand) == 0 && ipd_is_debugged()) return ipd_debugged = 1;
            }
        }
    }
    fprintf(stderr, "unicorn: could not mark process CS_DEBUGGED via jbctl; JIT will fault\n");
    return 0;
}

static int ipd_used_locked(void) {
    int n = 0;
    for (int i = 0; i < IPD_MAX_BUFFERS; i++)
        if (ipd_bufs[i].buf) n++;
    return n;
}

int ipd_jit_register(void *buf, size_t size) {
    ipd_ensure_debugged();
    pthread_mutex_lock(&ipd_lock);
    for (int i = 0; i < IPD_MAX_BUFFERS; i++) {
        if (!ipd_bufs[i].buf) {
            ipd_bufs[i].buf = buf;
            ipd_bufs[i].size = size;
            int used = ipd_used_locked();
            pthread_mutex_unlock(&ipd_lock);
            fprintf(stderr, "unicorn: jit buffer %zu bytes (%d in use)\n", size, used);
            return 0;
        }
    }
    pthread_mutex_unlock(&ipd_lock);
    fprintf(stderr, "unicorn: no free JIT buffer slot (max %d)\n", IPD_MAX_BUFFERS);
    return -1;
}

void ipd_jit_unregister(void *buf) {
    pthread_mutex_lock(&ipd_lock);
    for (int i = 0; i < IPD_MAX_BUFFERS; i++)
        if (ipd_bufs[i].buf == buf) ipd_bufs[i].buf = NULL;
    int used = ipd_used_locked();
    pthread_mutex_unlock(&ipd_lock);
    fprintf(stderr, "unicorn: jit buffer released (%d in use)\n", used);
}

/* enabled != 0: code is executable (RX); enabled == 0: writable (RW). */
void ipd_jit_write_protect_np(int enabled) {
    int prot = enabled ? (PROT_READ | PROT_EXEC) : (PROT_READ | PROT_WRITE);
    pthread_mutex_lock(&ipd_lock);
    for (int i = 0; i < IPD_MAX_BUFFERS; i++) {
        if (ipd_bufs[i].buf && mprotect(ipd_bufs[i].buf, ipd_bufs[i].size, prot) != 0) {
            fprintf(stderr, "unicorn: mprotect(%s) failed: %s\n", enabled ? "rx" : "rw", strerror(errno));
            abort();
        }
    }
    pthread_mutex_unlock(&ipd_lock);
}
