#import "IDAppEnumerator.h"
#import "Logging.h"

@implementation IDAppEnumerator

+ (NSArray<IDInstalledApp *> *)installedApps {
    NSString *root = @"/var/containers/Bundle/Application";
    NSError *err = nil;
    NSArray *uuids = [[NSFileManager defaultManager] contentsOfDirectoryAtPath:root error:&err];
    if (err) {
        IDLOG(@"enum: %@", err.localizedDescription);
        return @[];
    }

    NSMutableArray *out = [NSMutableArray array];
    for (NSString *uuid in uuids) {
        NSString *uuidPath = [root stringByAppendingPathComponent:uuid];
        NSArray *children = [[NSFileManager defaultManager] contentsOfDirectoryAtPath:uuidPath error:nil];
        for (NSString *name in children) {
            if (![name hasSuffix:@".app"]) continue;
            NSString *appPath = [uuidPath stringByAppendingPathComponent:name];
            NSString *plistPath = [appPath stringByAppendingPathComponent:@"Info.plist"];
            NSDictionary *plist = [NSDictionary dictionaryWithContentsOfFile:plistPath];
            NSString *bid = plist[@"CFBundleIdentifier"];
            if (!bid) continue;

            IDInstalledApp *a = [[IDInstalledApp alloc] init];
            a.bundleID = bid;
            a.displayName = plist[@"CFBundleDisplayName"] ?: plist[@"CFBundleName"] ?: name;
            a.version = plist[@"CFBundleShortVersionString"] ?: plist[@"CFBundleVersion"] ?: @"";
            a.appBundlePath = appPath;
            [out addObject:a];
        }
    }

    [out sortUsingComparator:^NSComparisonResult(IDInstalledApp *x, IDInstalledApp *y) {
        return [x.displayName caseInsensitiveCompare:y.displayName];
    }];
    IDLOG(@"enum: found %lu installed apps", (unsigned long)out.count);
    return out;
}

@end
