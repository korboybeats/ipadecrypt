#import <Foundation/Foundation.h>

@interface IDInstalledApp : NSObject
@property (nonatomic, copy) NSString *bundleID;       // CFBundleIdentifier
@property (nonatomic, copy) NSString *displayName;    // DisplayName ?? Name
@property (nonatomic, copy) NSString *version;        // CFBundleShortVersionString
@property (nonatomic, copy) NSString *appBundlePath;  // /var/containers/.../X.app
@end
