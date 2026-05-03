#import <Foundation/Foundation.h>

// Triggers an App Store download via SKUIItem (port of helper/appdl.m).
// Uses STDRDL pricing so iOS skips the Face ID purchase confirmation.
// Apple's CDN auto-serves the latest version compatible with the device's iOS.
@interface IDStoreKitDownloader : NSObject

// trackID is the numeric App Store ID (e.g. from IDSearchResult.trackID).
// Calls completion on main queue. err is nil on accepted-by-appstored.
+ (void)downloadTrackID:(NSInteger)trackID
                  nonce:(NSString *)nonce
             completion:(void (^)(NSError *err))completion;

@end
