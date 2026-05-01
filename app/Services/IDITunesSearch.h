#import <Foundation/Foundation.h>
#import "IDSearchResult.h"

@interface IDITunesSearch : NSObject
// Hits https://itunes.apple.com/search?term=&entity=software&limit=N
// Calls completion on the main queue.
+ (void)search:(NSString *)term
       country:(NSString *)country
         limit:(NSInteger)limit
    completion:(void (^)(NSArray<IDSearchResult *> *results, NSError *err))completion;
@end
