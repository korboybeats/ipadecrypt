#import <Foundation/Foundation.h>

@interface IDAutoalertArmer : NSObject
+ (NSString *)armForBundleID:(NSString *)bundleID trackID:(NSInteger)trackID;
+ (void)disarm;
@end
