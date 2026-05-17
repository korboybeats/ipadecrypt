#import "IDHelperRunner.h"
#import "Logging.h"
#import "IDJailbreakPaths.h"
#import <errno.h>
#import <sys/socket.h>
#import <sys/un.h>
#import <unistd.h>

static NSString *IDDecryptDaemonSocket(void) {
    return IDJailbreakPath(@"/var/jb/var/run/ipadecryptd.sock");
}

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

    IDLOG(@"helper: daemon %@ %@ %@", helper, bundleID, bundlePath);

    int fd = socket(AF_UNIX, SOCK_STREAM, 0);
    if (fd < 0) {
        completion(-1, [NSError errorWithDomain:@"IDHelperRunner" code:3
                                       userInfo:@{NSLocalizedDescriptionKey:
                                                       [NSString stringWithFormat:@"socket errno=%d", errno]}]);
        return;
    }

    struct sockaddr_un addr;
    memset(&addr, 0, sizeof(addr));
    addr.sun_family = AF_UNIX;
    NSString *socketPath = IDDecryptDaemonSocket();
    snprintf(addr.sun_path, sizeof(addr.sun_path), "%s", socketPath.UTF8String);
    if (connect(fd, (struct sockaddr *)&addr, sizeof(addr)) != 0) {
        int e = errno;
        close(fd);
        completion(-1, [NSError errorWithDomain:@"IDHelperRunner" code:4
                                       userInfo:@{NSLocalizedDescriptionKey:
                                                       [NSString stringWithFormat:@"connect %@ errno=%d", socketPath, e]}]);
        return;
    }

    NSString *request = [NSString stringWithFormat:@"%@\n%@\n%@\n%@\n",
                                                   helper,
                                                   bundleID ?: @"",
                                                   bundlePath ?: @"",
                                                   outIPA ?: @""];
    NSData *data = [request dataUsingEncoding:NSUTF8StringEncoding];
    const uint8_t *bytes = data.bytes;
    NSUInteger left = data.length;
    while (left > 0) {
        ssize_t n = write(fd, bytes, left);
        if (n <= 0) {
            int e = errno;
            close(fd);
            completion(-1, [NSError errorWithDomain:@"IDHelperRunner" code:5
                                           userInfo:@{NSLocalizedDescriptionKey:
                                                           [NSString stringWithFormat:@"write daemon request errno=%d", e]}]);
            return;
        }
        bytes += n;
        left -= (NSUInteger)n;
    }

    dispatch_queue_t q = dispatch_queue_create("ipadecrypt.helper", DISPATCH_QUEUE_SERIAL);
    NSMutableArray<NSString *> *stderrLines = [NSMutableArray array];

    dispatch_async(q, ^{
        FILE *stream = fdopen(fd, "r");
        int exitCode = -1;
        char *buf = NULL; size_t cap = 0; ssize_t n;
        while (stream && (n = getline(&buf, &cap, stream)) > 0) {
            NSString *line = [[NSString alloc] initWithBytes:buf length:(NSUInteger)n - 1
                                                    encoding:NSUTF8StringEncoding];
            if ([line hasPrefix:@"__ipadecryptd_exit "]) {
                exitCode = [[line substringFromIndex:19] intValue];
                continue;
            }
            NSDictionary *ev = parseEvent(line);
            if (ev) {
                if ([ev[@"event"] isEqualToString:@"stderr"] && [ev[@"line"] length] > 0) {
                    @synchronized (stderrLines) {
                        [stderrLines addObject:ev[@"line"]];
                        if (stderrLines.count > 8) {
                            [stderrLines removeObjectAtIndex:0];
                        }
                    }
                }
                dispatch_async(dispatch_get_main_queue(), ^{ if (eventBlock) eventBlock(ev); });
            } else {
                IDLOG(@"helper-stdout: %@", line);
            }
        }
        free(buf);
        if (stream) {
            fclose(stream);
        } else {
            close(fd);
        }

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
