#import <UIKit/UIKit.h>

@interface IDDecryptProgressViewController : UIViewController
- (instancetype)initWithTitle:(NSString *)title;

// Append a status line. Called from the main queue.
- (void)appendStatus:(NSString *)line;

// Mark complete; show a dismiss button. outIPA is shown for sharing.
- (void)markCompleteWithOutputIPA:(NSString *)outIPA error:(NSError *)err;
@end
