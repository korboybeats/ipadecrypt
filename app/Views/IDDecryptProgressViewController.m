#import "IDDecryptProgressViewController.h"
#import <objc/message.h>
#import <objc/runtime.h>

static NSString *const IDOpenFilzaAfterDecryptKey = @"OpenFilzaAfterDecrypt";

@interface IDDecryptProgressViewController ()
@property (nonatomic, strong) UITextView *textView;
@property (nonatomic, strong) UIActivityIndicatorView *spinner;
@property (nonatomic, copy) NSString *outIPA;
@property (nonatomic, strong) UIBarButtonItem *shareItem;
@property (nonatomic, strong) UIBarButtonItem *logsItem;
@property (nonatomic, strong) NSMutableArray<NSString *> *pendingStatusLines;
@end

@implementation IDDecryptProgressViewController

- (instancetype)initWithTitle:(NSString *)title {
    self = [super init];
    if (self) {
        self.title = title;
        self.pendingStatusLines = [NSMutableArray array];
    }
    return self;
}

- (void)viewDidLoad {
    [super viewDidLoad];
    self.view.backgroundColor = [UIColor systemBackgroundColor];

    self.textView = [[UITextView alloc] initWithFrame:self.view.bounds];
    self.textView.editable = NO;
    self.textView.font = [UIFont monospacedSystemFontOfSize:13 weight:UIFontWeightRegular];
    self.textView.autoresizingMask = UIViewAutoresizingFlexibleWidth | UIViewAutoresizingFlexibleHeight;
    self.textView.contentInset = UIEdgeInsetsMake(8, 8, 8, 8);
    [self.view addSubview:self.textView];

    NSArray<NSString *> *pending = [self.pendingStatusLines copy];
    [self.pendingStatusLines removeAllObjects];
    for (NSString *line in pending) {
        [self appendStatus:line];
    }

    self.spinner = [[UIActivityIndicatorView alloc]
        initWithActivityIndicatorStyle:UIActivityIndicatorViewStyleMedium];
    [self.spinner startAnimating];
    self.navigationItem.rightBarButtonItem =
        [[UIBarButtonItem alloc] initWithCustomView:self.spinner];
}

- (void)appendStatus:(NSString *)line {
    if (line.length == 0) return;
    if (!self.textView) {
        if (!self.pendingStatusLines) {
            self.pendingStatusLines = [NSMutableArray array];
        }
        [self.pendingStatusLines addObject:line];
        return;
    }
    NSString *cur = self.textView.text ?: @"";
    self.textView.text = [cur stringByAppendingFormat:@"%@\n", line];
    NSRange end = NSMakeRange(self.textView.text.length, 0);
    [self.textView scrollRangeToVisible:end];
}

- (void)markCompleteWithOutputIPA:(NSString *)outIPA error:(NSError *)err {
    [self.spinner stopAnimating];
    self.outIPA = outIPA;
    UIImage *copyImage = [UIImage systemImageNamed:@"doc.on.doc"];
    self.logsItem = [[UIBarButtonItem alloc] initWithImage:copyImage
                                                     style:UIBarButtonItemStylePlain
                                                    target:self
                                                    action:@selector(copyLogs)];
    self.logsItem.accessibilityLabel = @"Copy logs";
    if (err) {
        [self appendStatus:[NSString stringWithFormat:@"\nFAILED: %@", err.localizedDescription]];
        UIBarButtonItem *doneItem =
            [[UIBarButtonItem alloc] initWithBarButtonSystemItem:UIBarButtonSystemItemDone
                                                          target:self
                                                          action:@selector(done)];
        self.navigationItem.rightBarButtonItems = @[doneItem, self.logsItem];
    } else {
        [self appendStatus:[NSString stringWithFormat:@"\n→ %@", outIPA]];
        self.shareItem = [[UIBarButtonItem alloc]
            initWithBarButtonSystemItem:UIBarButtonSystemItemAction
                                 target:self
                                 action:@selector(share)];
        self.navigationItem.rightBarButtonItems = @[self.shareItem, self.logsItem];
        if ([[NSUserDefaults standardUserDefaults] boolForKey:IDOpenFilzaAfterDecryptKey]) {
            [self openFilzaForPath:[outIPA stringByDeletingLastPathComponent]];
        }
    }
}

- (void)markCompleteWithMessage:(NSString *)message error:(NSError *)err {
    [self.spinner stopAnimating];
    UIImage *copyImage = [UIImage systemImageNamed:@"doc.on.doc"];
    self.logsItem = [[UIBarButtonItem alloc] initWithImage:copyImage
                                                     style:UIBarButtonItemStylePlain
                                                    target:self
                                                    action:@selector(copyLogs)];
    self.logsItem.accessibilityLabel = @"Copy logs";

    UIBarButtonItem *doneItem =
        [[UIBarButtonItem alloc] initWithBarButtonSystemItem:UIBarButtonSystemItemDone
                                                      target:self
                                                      action:@selector(done)];
    self.navigationItem.rightBarButtonItems = @[doneItem, self.logsItem];

    if (err) {
        [self appendStatus:[NSString stringWithFormat:@"\nFAILED: %@", err.localizedDescription]];
    } else if (message.length) {
        [self appendStatus:[NSString stringWithFormat:@"\n%@", message]];
    }
}

- (void)done {
    [self.navigationController popViewControllerAnimated:YES];
}

- (void)share {
    if (self.outIPA.length == 0) return;
    NSURL *u = [NSURL fileURLWithPath:self.outIPA];
    UIActivityViewController *avc = [[UIActivityViewController alloc]
        initWithActivityItems:@[u] applicationActivities:nil];
    avc.popoverPresentationController.barButtonItem = self.shareItem;
    [self presentViewController:avc animated:YES completion:nil];
}

- (void)copyLogs {
    NSString *logs = self.textView.text ?: @"";
    if (logs.length == 0) return;
    UIPasteboard.generalPasteboard.string = logs;
    [self appendStatus:@"logs copied"];
}

- (void)openFilzaForPath:(NSString *)path {
    if (path.length == 0) return;

    NSString *encoded = [path stringByAddingPercentEncodingWithAllowedCharacters:
        [NSCharacterSet URLPathAllowedCharacterSet]];
    if (![encoded hasSuffix:@"/."]) {
        encoded = [[encoded stringByAppendingString:@"/"] stringByAppendingString:@"."];
    }
    NSURL *url = [NSURL URLWithString:[@"filza://view" stringByAppendingString:encoded ?: path]];
    UIApplication *app = UIApplication.sharedApplication;

    void (^fallback)(BOOL) = ^(BOOL ok) {
        if (ok) return;
        if ([self openFilzaByBundleID:@"com.tigisoftware.FilzaTS"] ||
            [self openFilzaByBundleID:@"com.tigisoftware.Filza"]) {
            [self appendStatus:@"opened Filza"];
        } else {
            [self appendStatus:@"Filza URL scheme unavailable"];
        }
    };

    [app openURL:url options:@{} completionHandler:fallback];
}

- (BOOL)openFilzaByBundleID:(NSString *)bundleID {
    Class cls = NSClassFromString(@"LSApplicationWorkspace");
    if (!cls) return NO;
    id workspace = ((id (*)(Class, SEL))objc_msgSend)(cls, sel_registerName("defaultWorkspace"));
    if (!workspace) return NO;
    SEL sel = sel_registerName("openApplicationWithBundleID:");
    if (![workspace respondsToSelector:sel]) return NO;
    return ((BOOL (*)(id, SEL, NSString *))objc_msgSend)(workspace, sel, bundleID);
}

@end
