#import "IDAutoalertArmer.h"
#import "Logging.h"

static NSString *const kSentinel = @"/var/mobile/.ipadecryptautoalert-arm";

@implementation IDAutoalertArmer

+ (void)arm {
    [@"" writeToFile:kSentinel atomically:YES encoding:NSUTF8StringEncoding error:nil];
    NSDate *now = [NSDate date];
    [[NSFileManager defaultManager] setAttributes:@{NSFileModificationDate: now}
                                     ofItemAtPath:kSentinel error:nil];
    IDLOG(@"autoalert armed");
}

+ (void)disarm {
    [[NSFileManager defaultManager] removeItemAtPath:kSentinel error:nil];
    IDLOG(@"autoalert disarmed");
}

@end
