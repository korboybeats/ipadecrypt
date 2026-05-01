#import <UIKit/UIKit.h>

typedef NS_ENUM(NSInteger, IDDecryptOption) {
    IDDecryptOptionInstalled,           // decrypt the build already on disk
    IDDecryptOptionLatestStoreKit,      // trigger StoreKit download then decrypt
};

@interface IDDecryptOptionsViewController : UIViewController

// installedDisplay is non-nil when the app is already on disk (allows
// the "Installed" option). trackID is non-zero when StoreKit download
// is possible. completion fires with the picked option, or with cancelled=YES.
+ (void)presentFrom:(UIViewController *)parent
              title:(NSString *)title
   installedDisplay:(NSString *)installedDisplay
            trackID:(NSInteger)trackID
         completion:(void (^)(IDDecryptOption picked, BOOL cancelled))completion;

@end
