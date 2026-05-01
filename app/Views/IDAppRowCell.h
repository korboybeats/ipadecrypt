#import <UIKit/UIKit.h>

@interface IDAppRowCell : UITableViewCell
+ (NSString *)reuseID;
- (void)configureWithTitle:(NSString *)title
                  subtitle:(NSString *)subtitle
                   trailing:(NSString *)trailing;
@end
