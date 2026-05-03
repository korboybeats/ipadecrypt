#import <Foundation/Foundation.h>
#import "IDSearchResult.h"

@interface IDITunesSearch : NSObject
// Hits https://itunes.apple.com/search?term=&entity=software&limit=N
// Calls completion on the main queue.
+ (void)search:(NSString *)term
       country:(NSString *)country
         limit:(NSInteger)limit
    completion:(void (^)(NSArray<IDSearchResult *> *results, NSError *err))completion;

// Exact lookup by CFBundleIdentifier. Used to resolve trackID for installed apps.
+ (void)lookupBundleID:(NSString *)bundleID
               country:(NSString *)country
            completion:(void (^)(IDSearchResult *result, NSError *err))completion;
@end
