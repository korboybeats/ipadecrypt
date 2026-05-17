#import "Logging.h"

NSString *const IDLogPath = @"/var/mobile/Documents/ipadecrypt/logs/app.log";

void IDLogv(const char *file, int line, NSString *fmt, va_list args) {
    NSString *msg = [[NSString alloc] initWithFormat:fmt arguments:args];
    NSDateFormatter *df = [[NSDateFormatter alloc] init];
    df.dateFormat = @"HH:mm:ss.SSS";
    NSString *base = [[NSString stringWithUTF8String:file] lastPathComponent];
    NSString *line_ = [NSString stringWithFormat:@"[%@] %@:%d  %@\n",
                       [df stringFromDate:[NSDate date]], base, line, msg];

    NSFileHandle *fh = [NSFileHandle fileHandleForWritingAtPath:IDLogPath];
    if (!fh) {
        NSString *dir = [IDLogPath stringByDeletingLastPathComponent];
        [[NSFileManager defaultManager] createDirectoryAtPath:dir
                                  withIntermediateDirectories:YES
                                                   attributes:nil
                                                        error:nil];
        [[NSFileManager defaultManager] createFileAtPath:IDLogPath
                                                contents:nil attributes:nil];
        fh = [NSFileHandle fileHandleForWritingAtPath:IDLogPath];
    }
    [fh seekToEndOfFile];
    [fh writeData:[line_ dataUsingEncoding:NSUTF8StringEncoding]];
    [fh closeFile];
}

void IDLog(const char *file, int line, NSString *fmt, ...) {
    va_list args;
    va_start(args, fmt);
    IDLogv(file, line, fmt, args);
    va_end(args);
}
