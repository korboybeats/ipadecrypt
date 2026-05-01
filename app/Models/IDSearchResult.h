#import <Foundation/Foundation.h>

@interface IDSearchResult : NSObject
@property (nonatomic, copy) NSString *bundleID;
@property (nonatomic, copy) NSString *trackName;       // app display name
@property (nonatomic, copy) NSString *artistName;      // developer
@property (nonatomic, copy) NSString *version;
@property (nonatomic, copy) NSString *minimumOSVersion;
@property (nonatomic) NSInteger trackID;
@end
