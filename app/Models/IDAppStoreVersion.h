#import <Foundation/Foundation.h>

@interface IDAppStoreVersion : NSObject

@property (nonatomic) NSInteger index;
@property (nonatomic, copy) NSString *externalVersionID;
@property (nonatomic, copy) NSString *displayVersion;
@property (nonatomic, copy) NSString *bundleVersion;
@property (nonatomic, copy) NSString *devices;
@property (nonatomic) BOOL latest;
@property (nonatomic, copy) NSString *status;
@property (nonatomic, copy) NSString *message;

@end
