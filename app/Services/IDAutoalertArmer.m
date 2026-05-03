#import "IDAutoalertArmer.h"
#import "Logging.h"

static NSString *const kSentinel = @"/var/mobile/.ipadecryptautoalert-arm";

@implementation IDAutoalertArmer

+ (NSString *)armForBundleID:(NSString *)bundleID trackID:(NSInteger)trackID {
    if (bundleID.length == 0 || trackID <= 0) {
        [self disarm];
        IDLOG(@"autoalert not armed: missing bundleID or trackID");
        return nil;
    }

    NSString *nonce = NSUUID.UUID.UUIDString;
    NSDictionary *record = @{
        @"operation": @"storekit-compatible-download",
        @"bundleID": bundleID,
        @"trackID": @(trackID),
        @"nonce": nonce,
        @"createdAt": @([[NSDate date] timeIntervalSince1970]),
    };
    NSData *data = [NSJSONSerialization dataWithJSONObject:record options:0 error:nil];
    if (data.length == 0 || ![data writeToFile:kSentinel atomically:YES]) {
        [self disarm];
        IDLOG(@"autoalert arm write failed");
        return nil;
    }

    IDLOG(@"autoalert armed bundle=%@ trackID=%ld", bundleID, (long)trackID);
    return nonce;
}

+ (void)disarm {
    [[NSFileManager defaultManager] removeItemAtPath:kSentinel error:nil];
    IDLOG(@"autoalert disarmed");
}

@end
