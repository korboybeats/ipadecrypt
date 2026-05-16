#import <UIKit/UIKit.h>

@interface IDAppStoreVersionPickerViewController : UITableViewController

- (instancetype)initWithTitle:(NSString *)title
                      bundleID:(NSString *)bundleID
                       trackID:(NSInteger)trackID
                    completion:(void (^)(NSString *externalVersionID))completion;

@end
