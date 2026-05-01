#import "IDDecryptOptionsViewController.h"

@implementation IDDecryptOptionsViewController

+ (void)presentFrom:(UIViewController *)parent
              title:(NSString *)title
   installedDisplay:(NSString *)installedDisplay
            trackID:(NSInteger)trackID
         completion:(void (^)(IDDecryptOption, BOOL))completion {
    UIAlertController *sheet = [UIAlertController
        alertControllerWithTitle:title
                         message:nil
                  preferredStyle:UIAlertControllerStyleActionSheet];

    if (installedDisplay.length > 0) {
        NSString *t = [NSString stringWithFormat:@"Decrypt installed build (%@)", installedDisplay];
        [sheet addAction:[UIAlertAction actionWithTitle:t
                                                  style:UIAlertActionStyleDefault
                                                handler:^(UIAlertAction *a) {
            completion(IDDecryptOptionInstalled, NO);
        }]];
    }

    if (trackID > 0) {
        [sheet addAction:[UIAlertAction
            actionWithTitle:@"Latest iOS-compatible (via StoreKit)"
                      style:UIAlertActionStyleDefault
                    handler:^(UIAlertAction *a) {
            completion(IDDecryptOptionLatestStoreKit, NO);
        }]];
    }

    [sheet addAction:[UIAlertAction actionWithTitle:@"Cancel"
                                              style:UIAlertActionStyleCancel
                                            handler:^(UIAlertAction *a) {
        completion(IDDecryptOptionInstalled, YES);
    }]];

    [parent presentViewController:sheet animated:YES completion:nil];
}

@end
