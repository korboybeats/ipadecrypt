#import "IDAppStoreHelperRunner.h"
#import "Logging.h"
#import <spawn.h>
#import <sys/wait.h>
#import <unistd.h>

extern char **environ;

#define POSIX_SPAWN_PERSONA_FLAGS_OVERRIDE 1
extern int posix_spawnattr_set_persona_np(const posix_spawnattr_t *attr, uid_t persona_id, uint32_t flags);
extern int posix_spawnattr_set_persona_uid_np(const posix_spawnattr_t *attr, uid_t uid);
extern int posix_spawnattr_set_persona_gid_np(const posix_spawnattr_t *attr, uid_t gid);

static NSInteger IDActiveAppStoreHelperCount = 0;
static BOOL IDAuthHelperRunning = NO;
static NSMutableArray *IDPendingAuthOperations = nil;
static IDAppleAuthCheckResult IDLastAuthResult = IDAppleAuthCheckResultNone;

static NSDictionary *IDParseAppStoreEvent(NSString *line) {
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

@implementation IDAppStoreHelperRunner

+ (BOOL)hasActiveOperation {
    @synchronized (self) {
        return IDActiveAppStoreHelperCount > 0;
    }
}

+ (IDAppleAuthCheckResult)lastAuthResult {
    @synchronized (self) {
        return IDLastAuthResult;
    }
}

+ (void)enqueueAuthOperation:(dispatch_block_t)operation {
    dispatch_block_t start = nil;
    @synchronized (self) {
        if (!IDPendingAuthOperations) IDPendingAuthOperations = [NSMutableArray array];
        if (IDAuthHelperRunning) {
            [IDPendingAuthOperations addObject:[operation copy]];
            return;
        }
        IDAuthHelperRunning = YES;
        start = [operation copy];
    }
    start();
}

+ (void)finishAuthOperation {
    dispatch_block_t next = nil;
    @synchronized (self) {
        if (IDPendingAuthOperations.count > 0) {
            next = IDPendingAuthOperations.firstObject;
            [IDPendingAuthOperations removeObjectAtIndex:0];
        } else {
            IDAuthHelperRunning = NO;
        }
    }
    if (next) next();
}

+ (void)spawnAuthWithArguments:(NSArray<NSString *> *)args
                       onEvent:(void (^)(NSDictionary *))eventBlock
                    completion:(void (^)(int, NSError *))completion {
    [self enqueueAuthOperation:^{
        [self spawnWithArguments:args onEvent:eventBlock completion:^(int code, NSError *err) {
            @synchronized (self) {
                if (code == 0 && !err) {
                    IDLastAuthResult = IDAppleAuthCheckResultSuccess;
                } else if (code == 20 || code == 21) {
                    IDLastAuthResult = IDAppleAuthCheckResultAuthRequired;
                } else {
                    IDLastAuthResult = IDAppleAuthCheckResultInconclusive;
                }
            }
            if (completion) completion(code, err);
            [self finishAuthOperation];
        }];
    }];
}

+ (NSString *)bundledHelperPath {
    return [[NSBundle mainBundle] pathForResource:@"appstore-helper" ofType:@"arm64"];
}

+ (void)spawnWithArguments:(NSArray<NSString *> *)args
                   onEvent:(void (^)(NSDictionary *))eventBlock
                completion:(void (^)(int, NSError *))completion {
    void (^callerCompletion)(int, NSError *) = [completion copy];
    @synchronized (self) {
        IDActiveAppStoreHelperCount++;
    }
    completion = ^(int code, NSError *err) {
        @synchronized (self) {
            IDActiveAppStoreHelperCount--;
        }
        if (callerCompletion) callerCompletion(code, err);
    };

    NSString *helper = [self bundledHelperPath];
    if (!helper || ![[NSFileManager defaultManager] isExecutableFileAtPath:helper]) {
        completion(-1, [NSError errorWithDomain:@"IDAppStoreHelperRunner" code:1
                                       userInfo:@{NSLocalizedDescriptionKey:
                                           @"appstore-helper.arm64 not bundled or not executable"}]);
        return;
    }

    NSMutableArray<NSString *> *fullArgs = [NSMutableArray arrayWithObject:helper];
    [fullArgs addObjectsFromArray:args ?: @[]];

    int outFD[2], errFD[2];
    if (pipe(outFD) != 0 || pipe(errFD) != 0) {
        completion(-1, [NSError errorWithDomain:@"IDAppStoreHelperRunner" code:2
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

    NSUInteger argc = fullArgs.count;
    char **argv = calloc(argc + 1, sizeof(char *));
    for (NSUInteger i = 0; i < argc; i++) {
        argv[i] = (char *)fullArgs[i].UTF8String;
    }
    argv[argc] = NULL;

    posix_spawnattr_t attr;
    posix_spawnattr_init(&attr);
    posix_spawnattr_set_persona_np(&attr, 99, POSIX_SPAWN_PERSONA_FLAGS_OVERRIDE);
    posix_spawnattr_set_persona_uid_np(&attr, 0);
    posix_spawnattr_set_persona_gid_np(&attr, 0);

    pid_t pid = 0;
    int rc = posix_spawn(&pid, helper.UTF8String, &fa, &attr, argv, environ);
    free(argv);
    posix_spawnattr_destroy(&attr);
    posix_spawn_file_actions_destroy(&fa);
    close(outFD[1]);
    close(errFD[1]);
    if (rc != 0) {
        close(outFD[0]); close(errFD[0]);
        completion(-1, [NSError errorWithDomain:@"IDAppStoreHelperRunner" code:3
                                       userInfo:@{NSLocalizedDescriptionKey:
                                           [NSString stringWithFormat:@"spawn rc=%d", rc]}]);
        return;
    }

    int outRead = outFD[0];
    int errRead = errFD[0];
    dispatch_queue_t q = dispatch_queue_create("ipadecrypt.appstore-helper", DISPATCH_QUEUE_CONCURRENT);
    NSMutableArray<NSString *> *stderrLines = [NSMutableArray array];
    dispatch_group_t g = dispatch_group_create();

    dispatch_group_async(g, q, ^{
        FILE *outStream = fdopen(outRead, "r");
        char *buf = NULL; size_t cap = 0; ssize_t n;
        while ((n = getline(&buf, &cap, outStream)) > 0) {
            NSString *line = [[NSString alloc] initWithBytes:buf length:(NSUInteger)n - 1
                                                    encoding:NSUTF8StringEncoding];
            NSDictionary *ev = IDParseAppStoreEvent(line);
            if (ev) {
                dispatch_async(dispatch_get_main_queue(), ^{ if (eventBlock) eventBlock(ev); });
            } else {
                IDLOG(@"appstore-helper-stdout: %@", line);
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
            IDLOG(@"appstore-helper-stderr: %@", line);
            if (line.length > 0) {
                @synchronized (stderrLines) {
                    [stderrLines addObject:line];
                    if (stderrLines.count > 8) [stderrLines removeObjectAtIndex:0];
                }
                dispatch_async(dispatch_get_main_queue(), ^{
                    if (eventBlock) eventBlock(@{@"phase": @"stderr", @"line": line});
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
        NSError *err = nil;
        if (exitCode != 0) {
            NSString *detail = nil;
            @synchronized (stderrLines) {
                detail = [stderrLines componentsJoinedByString:@"\n"];
            }
            NSString *msg = detail.length
                ? [NSString stringWithFormat:@"appstore helper exit %d\n%@", exitCode, detail]
                : [NSString stringWithFormat:@"appstore helper exit %d", exitCode];
            err = [NSError errorWithDomain:@"IDAppStoreHelperRunner" code:exitCode
                                  userInfo:@{NSLocalizedDescriptionKey: msg}];
        }
        dispatch_async(dispatch_get_main_queue(), ^{ completion(exitCode, err); });
    });
}

+ (void)runWithBundleID:(NSString *)bundleID
                trackID:(NSInteger)trackID
                  email:(NSString *)email
               password:(NSString *)password
               authCode:(NSString *)authCode
                onEvent:(void (^)(NSDictionary *))eventBlock
             completion:(void (^)(int, NSError *))completion {
    [self runWithBundleID:bundleID
                  trackID:trackID
        externalVersionID:nil
                    email:email
                 password:password
                 authCode:authCode
                  onEvent:eventBlock
               completion:completion];
}

+ (void)runWithBundleID:(NSString *)bundleID
                trackID:(NSInteger)trackID
      externalVersionID:(NSString *)externalVersionID
                  email:(NSString *)email
               password:(NSString *)password
               authCode:(NSString *)authCode
                onEvent:(void (^)(NSDictionary *))eventBlock
             completion:(void (^)(int, NSError *))completion {
    NSMutableArray<NSString *> *args = [NSMutableArray array];
    if (bundleID.length) {
        [args addObjectsFromArray:@[@"--bundle-id", bundleID]];
    }
    if (trackID > 0) {
        [args addObjectsFromArray:@[@"--track-id", [NSString stringWithFormat:@"%ld", (long)trackID]]];
    }
    if (externalVersionID.length) {
        [args addObjectsFromArray:@[@"--external-version-id", externalVersionID]];
    }
    if (email.length) {
        [args addObjectsFromArray:@[@"--email", email]];
    }
    if (password.length) {
        [args addObjectsFromArray:@[@"--password", password]];
    }
    if (authCode.length) {
        [args addObjectsFromArray:@[@"--auth-code", authCode]];
    }

    [self spawnAuthWithArguments:args onEvent:eventBlock completion:completion];
}

+ (void)listVersionsWithBundleID:(NSString *)bundleID
                          trackID:(NSInteger)trackID
                            email:(NSString *)email
                         password:(NSString *)password
                         authCode:(NSString *)authCode
                          onEvent:(void (^)(NSDictionary *))eventBlock
                       completion:(void (^)(int, NSError *))completion {
    NSMutableArray<NSString *> *args = [NSMutableArray arrayWithObject:@"--list-versions"];
    if (bundleID.length) {
        [args addObjectsFromArray:@[@"--bundle-id", bundleID]];
    }
    if (trackID > 0) {
        [args addObjectsFromArray:@[@"--track-id", [NSString stringWithFormat:@"%ld", (long)trackID]]];
    }
    if (email.length) {
        [args addObjectsFromArray:@[@"--email", email]];
    }
    if (password.length) {
        [args addObjectsFromArray:@[@"--password", password]];
    }
    if (authCode.length) {
        [args addObjectsFromArray:@[@"--auth-code", authCode]];
    }

    [self spawnAuthWithArguments:args onEvent:eventBlock completion:completion];
}

+ (void)fetchVersionMetadataWithBundleID:(NSString *)bundleID
                                  trackID:(NSInteger)trackID
                        externalVersionID:(NSString *)externalVersionID
                                    email:(NSString *)email
                                 password:(NSString *)password
                                 authCode:(NSString *)authCode
                                  onEvent:(void (^)(NSDictionary *))eventBlock
                               completion:(void (^)(int, NSError *))completion {
    NSMutableArray<NSString *> *args = [NSMutableArray arrayWithObjects:
        @"--version-metadata",
        @"--external-version-id", externalVersionID ?: @"",
        nil];
    if (bundleID.length) {
        [args addObjectsFromArray:@[@"--bundle-id", bundleID]];
    }
    if (trackID > 0) {
        [args addObjectsFromArray:@[@"--track-id", [NSString stringWithFormat:@"%ld", (long)trackID]]];
    }
    if (email.length) {
        [args addObjectsFromArray:@[@"--email", email]];
    }
    if (password.length) {
        [args addObjectsFromArray:@[@"--password", password]];
    }
    if (authCode.length) {
        [args addObjectsFromArray:@[@"--auth-code", authCode]];
    }

    [self spawnAuthWithArguments:args onEvent:eventBlock completion:completion];
}

+ (void)refreshAuthWithEmail:(NSString *)email
                    password:(NSString *)password
                    authCode:(NSString *)authCode
                     onEvent:(void (^)(NSDictionary *))eventBlock
                  completion:(void (^)(int, NSError *))completion {
    NSMutableArray<NSString *> *args = [NSMutableArray arrayWithObject:@"--auth-only"];
    if (email.length) {
        [args addObjectsFromArray:@[@"--email", email]];
    }
    if (password.length) {
        [args addObjectsFromArray:@[@"--password", password]];
    }
    if (authCode.length) {
        [args addObjectsFromArray:@[@"--auth-code", authCode]];
    }

    [self spawnAuthWithArguments:args onEvent:eventBlock completion:completion];
}

+ (void)checkSavedAuthWithCompletion:(void (^)(int, NSError *))completion {
    [self spawnWithArguments:@[@"--auth-status"] onEvent:nil completion:completion];
}

+ (void)verifyIPA:(NSString *)ipaPath
          onEvent:(void (^)(NSDictionary *))eventBlock
       completion:(void (^)(int, NSError *))completion {
    [self spawnWithArguments:@[@"--verify-ipa", ipaPath ?: @""]
                     onEvent:eventBlock
                  completion:completion];
}

@end
