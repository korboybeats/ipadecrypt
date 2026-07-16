#import <UIKit/UIKit.h>

typedef NS_ENUM(NSInteger, IDAppStoreVersionAction) {
    IDAppStoreVersionActionInstall = 0,
    IDAppStoreVersionActionDecrypt,
};

@interface IDAppStoreVersionPickerViewController : UITableViewController

- (instancetype)initWithTitle:(NSString *)title
                      bundleID:(NSString *)bundleID
                       trackID:(NSInteger)trackID
                    completion:(void (^)(NSString *externalVersionID,
                                          IDAppStoreVersionAction action))completion;

@end
