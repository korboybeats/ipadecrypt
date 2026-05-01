#import <Foundation/Foundation.h>

// Spawns the bundled helper.arm64 and parses each "@evt key=value ..." line
// emitted on stdout. Non-event stderr lines are surfaced as event=stderr.
// eventBlock and completion both fire on the main queue.
@interface IDHelperRunner : NSObject

+ (void)runWithBundleID:(NSString *)bundleID
             bundlePath:(NSString *)bundlePath
                outIPA:(NSString *)outIPA
                onEvent:(void (^)(NSDictionary *event))eventBlock
             completion:(void (^)(int exitCode, NSError *err))completion;

// Resolves the on-disk path of helper.arm64 inside the app bundle.
+ (NSString *)bundledHelperPath;

@end
