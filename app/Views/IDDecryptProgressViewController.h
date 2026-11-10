#import <UIKit/UIKit.h>

@interface IDDecryptProgressViewController : UIViewController
- (instancetype)initWithTitle:(NSString *)title;

// Append a status line. Called from the main queue.
- (void)appendStatus:(NSString *)line;

// Mark complete; show a dismiss button. outIPA is shown for sharing.
- (void)markCompleteWithOutputIPA:(NSString *)outIPA error:(NSError *)err;

// Mark complete without an output IPA/share action.
- (void)markCompleteWithMessage:(NSString *)message error:(NSError *)err;

// Invoked when the user taps Cancel while work is in flight. Cleared on completion.
@property (nonatomic, copy) void (^cancelHandler)(void);
@end
