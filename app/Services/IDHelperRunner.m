#import "IDHelperRunner.h"
#import "Logging.h"
#import <spawn.h>
#import <sys/wait.h>

extern char **environ;

#define POSIX_SPAWN_PERSONA_FLAGS_OVERRIDE 1
extern int posix_spawnattr_set_persona_np(const posix_spawnattr_t *attr, uid_t persona_id, uint32_t flags);
extern int posix_spawnattr_set_persona_uid_np(const posix_spawnattr_t *attr, uid_t uid);
extern int posix_spawnattr_set_persona_gid_np(const posix_spawnattr_t *attr, uid_t gid);

static NSDictionary *parseEvent(NSString *line) {
    if (![line hasPrefix:@"@evt "]) return nil;
    NSString *body = [line substringFromIndex:5];
    NSMutableDictionary *out = [NSMutableDictionary dictionary];
    NSScanner *s = [NSScanner scannerWithString:body];
    s.charactersToBeSkipped = nil;
    while (!s.isAtEnd) {
        while (!s.isAtEnd && [s.string characterAtIndex:s.scanLocation] == ' ') s.scanLocation++;
        NSString *key = nil;
        if (![s scanUpToString:@"=" intoString:&key]) break;
        if (s.isAtEnd) break;
        s.scanLocation++;
        if (s.isAtEnd) break;
        NSString *value = nil;
        if ([s.string characterAtIndex:s.scanLocation] == '"') {
            s.scanLocation++;
            [s scanUpToString:@"\"" intoString:&value];
            if (!s.isAtEnd) s.scanLocation++;
        } else {
            [s scanUpToString:@" " intoString:&value];
        }
        if (key && value) out[key] = value;
    }
    return out;
}

@implementation IDHelperRunner

+ (NSString *)bundledHelperPath {
    return [[NSBundle mainBundle] pathForResource:@"helper" ofType:@"arm64"];
}

+ (void)runWithBundleID:(NSString *)bundleID
             bundlePath:(NSString *)bundlePath
                outIPA:(NSString *)outIPA
                onEvent:(void (^)(NSDictionary *))eventBlock
             completion:(void (^)(int, NSError *))completion {
    NSString *helper = [self bundledHelperPath];
    if (!helper || ![[NSFileManager defaultManager] isExecutableFileAtPath:helper]) {
        completion(-1, [NSError errorWithDomain:@"IDHelperRunner" code:1
                                       userInfo:@{NSLocalizedDescriptionKey:
                                                       @"helper.arm64 not bundled or not executable"}]);
        return;
    }

    IDLOG(@"helper: spawn %@ %@ %@ %@", helper, bundleID, bundlePath, outIPA);

    int outFD[2], errFD[2];
    if (pipe(outFD) != 0 || pipe(errFD) != 0) {
        completion(-1, [NSError errorWithDomain:@"IDHelperRunner" code:2
                                       userInfo:@{NSLocalizedDescriptionKey: @"pipe() failed"}]);
        return;
    }

    posix_spawn_file_actions_t fa;
    posix_spawn_file_actions_init(&fa);
    posix_spawn_file_actions_addclose(&fa, outFD[0]);
    posix_spawn_file_actions_addclose(&fa, errFD[0]);
    posix_spawn_file_actions_adddup2(&fa, outFD[1], STDOUT_FILENO);
    posix_spawn_file_actions_adddup2(&fa, errFD[1], STDERR_FILENO);
    posix_spawn_file_actions_addclose(&fa, outFD[1]);
    posix_spawn_file_actions_addclose(&fa, errFD[1]);

    char *argv[] = {
        (char *)helper.UTF8String,
        (char *)(bundleID.UTF8String ?: ""),
        (char *)bundlePath.UTF8String,
        (char *)outIPA.UTF8String,
        NULL,
    };

    posix_spawnattr_t attr;
    posix_spawnattr_init(&attr);
    posix_spawnattr_set_persona_np(&attr, 99, POSIX_SPAWN_PERSONA_FLAGS_OVERRIDE);
    posix_spawnattr_set_persona_uid_np(&attr, 0);
    posix_spawnattr_set_persona_gid_np(&attr, 0);

    pid_t pid = 0;
    int rc = posix_spawn(&pid, helper.UTF8String, &fa, &attr, argv, environ);
    posix_spawnattr_destroy(&attr);
    posix_spawn_file_actions_destroy(&fa);
    close(outFD[1]);
    close(errFD[1]);
    if (rc != 0) {
        close(outFD[0]); close(errFD[0]);
        completion(-1, [NSError errorWithDomain:@"IDHelperRunner" code:3
                                       userInfo:@{NSLocalizedDescriptionKey:
                                                       [NSString stringWithFormat:@"spawn rc=%d", rc]}]);
        return;
    }

    int outRead = outFD[0];
    int errRead = errFD[0];
    dispatch_queue_t q = dispatch_queue_create("ipadecrypt.helper", DISPATCH_QUEUE_CONCURRENT);
    NSMutableArray<NSString *> *stderrLines = [NSMutableArray array];

    // helper.c emits @evt lines to STDOUT; ERR()s and progress text to STDERR.
    // Read both concurrently so neither blocks the other on a full pipe.
    dispatch_group_t g = dispatch_group_create();

    dispatch_group_async(g, q, ^{
        FILE *outStream = fdopen(outRead, "r");
        char *buf = NULL; size_t cap = 0; ssize_t n;
        while ((n = getline(&buf, &cap, outStream)) > 0) {
            NSString *line = [[NSString alloc] initWithBytes:buf length:(NSUInteger)n - 1
                                                    encoding:NSUTF8StringEncoding];
            NSDictionary *ev = parseEvent(line);
            if (ev) {
                dispatch_async(dispatch_get_main_queue(), ^{ if (eventBlock) eventBlock(ev); });
            } else {
                IDLOG(@"helper-stdout: %@", line);
            }
        }
        free(buf);
        fclose(outStream);
    });

    dispatch_group_async(g, q, ^{
        FILE *errStream = fdopen(errRead, "r");
        char *buf = NULL; size_t cap = 0; ssize_t n;
        while ((n = getline(&buf, &cap, errStream)) > 0) {
            NSString *line = [[NSString alloc] initWithBytes:buf length:(NSUInteger)n - 1
                                                    encoding:NSUTF8StringEncoding];
            IDLOG(@"helper-stderr: %@", line);
            if (line.length > 0) {
                @synchronized (stderrLines) {
                    [stderrLines addObject:line];
                    if (stderrLines.count > 8) {
                        [stderrLines removeObjectAtIndex:0];
                    }
                }
                dispatch_async(dispatch_get_main_queue(), ^{
                    if (eventBlock) eventBlock(@{@"event": @"stderr", @"line": line});
                });
            }
        }
        free(buf);
        fclose(errStream);
    });

    dispatch_group_notify(g, q, ^{
        int status = 0;
        waitpid(pid, &status, 0);
        int exitCode = WIFEXITED(status) ? WEXITSTATUS(status) : -1;
        IDLOG(@"helper: exit %d", exitCode);
        NSError *err = nil;
        if (exitCode != 0) {
            NSString *detail = nil;
            @synchronized (stderrLines) {
                detail = [stderrLines componentsJoinedByString:@"\n"];
            }
            NSString *msg = detail.length
                ? [NSString stringWithFormat:@"helper exit %d\n%@", exitCode, detail]
                : [NSString stringWithFormat:@"helper exit %d", exitCode];
            err = [NSError errorWithDomain:@"IDHelperRunner" code:exitCode
                                  userInfo:@{NSLocalizedDescriptionKey: msg}];
        }
        dispatch_async(dispatch_get_main_queue(), ^{ completion(exitCode, err); });
    });
}

@end
