// ipadecryptautoalert - SpringBoard tweak that auto-confirms App Store
// "Download an older version" prompts triggered by appdl.
//
// Activation: ipadecrypt touches /var/mobile/.ipadecryptautoalert-arm before
// running appdl and removes it after the install completes.

#import <UIKit/UIKit.h>
#import <objc/runtime.h>
#import <sys/stat.h>

static const char *kSentinel = "/var/mobile/.ipadecryptautoalert-arm";
static const NSTimeInterval kSentinelMaxAge = 60.0;

static const void *kAutomatedKey = &kAutomatedKey;
static const void *kTargetActionKey = &kTargetActionKey;

static BOOL alertIsAutomated(UIAlertController *alert) {
	NSNumber *n = objc_getAssociatedObject(alert, kAutomatedKey);
	return n.boolValue;
}

static void setAlertAutomated(UIAlertController *alert, BOOL value) {
	objc_setAssociatedObject(alert, kAutomatedKey, @(value), OBJC_ASSOCIATION_RETAIN_NONATOMIC);
}

static UIAlertAction *alertTargetAction(UIAlertController *alert) {
	return objc_getAssociatedObject(alert, kTargetActionKey);
}

static void setAlertTargetAction(UIAlertController *alert, UIAlertAction *action) {
	objc_setAssociatedObject(alert, kTargetActionKey, action, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
}

@interface UIAlertController (IPADecryptPrivate)
@property (readonly) UIView *_dimmingView;
- (void)_dismissWithAction:(id)action;
@end

static BOOL armedNow(void) {
	struct stat st;
	if (stat(kSentinel, &st) != 0) return NO;
	NSTimeInterval age = [[NSDate date] timeIntervalSince1970] - (NSTimeInterval)st.st_mtimespec.tv_sec;
	return age <= kSentinelMaxAge;
}

static BOOL isDownloadOlderVersionAlert(UIAlertController *alert) {
	if (!alert.title) return NO;
	NSString *title = [alert.title lowercaseString];
	return [title rangeOfString:@"older version"].location != NSNotFound;
}

static UIAlertAction *findInstallAction(UIAlertController *alert) {
	if (!isDownloadOlderVersionAlert(alert)) return nil;
	if (!alert.actions || alert.actions.count < 2) return nil;
	BOOL hasCancel = NO;
	UIAlertAction *download = nil;
	for (UIAlertAction *a in alert.actions) {
		if (a.style == UIAlertActionStyleCancel) hasCancel = YES;
		if (a.title && [[a.title lowercaseString] isEqualToString:@"download"]) download = a;
	}
	return (hasCancel && download) ? download : nil;
}

%hook UIAlertController

- (void)viewDidLoad {
	%orig;
	if (!alertIsAutomated(self)) return;
	self.view.hidden = YES;
	@try { self._dimmingView.hidden = YES; } @catch (id e) {}
}

- (void)viewWillAppear:(BOOL)animated {
	%orig;
	if (!alertIsAutomated(self)) return;
	self.view.hidden = YES;
	@try { self._dimmingView.hidden = YES; } @catch (id e) {}
}

- (void)viewDidAppear:(BOOL)animated {
	%orig;
	if (!alertIsAutomated(self)) return;
	self.view.hidden = YES;
	@try { self._dimmingView.hidden = YES; } @catch (id e) {}

	UIAlertAction *action = alertTargetAction(self);
	if (!action) return;

	if ([self respondsToSelector:@selector(_dismissWithAction:)]) {
		[self performSelector:@selector(_dismissWithAction:) withObject:action];
	} else {
		[self dismissViewControllerAnimated:NO completion:nil];
	}
}

%end

%hook UIViewController

- (void)presentViewController:(id)arg1 animated:(BOOL)arg2 completion:(id)arg3 {
	if (![arg1 isKindOfClass:[UIAlertController class]]) {
		%orig;
		return;
	}
	UIAlertController *alert = (UIAlertController *)arg1;
	if (alert.preferredStyle != UIAlertControllerStyleAlert) {
		%orig;
		return;
	}
	if (!armedNow()) {
		%orig;
		return;
	}
	UIAlertAction *target = findInstallAction(alert);
	if (!target) {
		%orig;
		return;
	}

	setAlertAutomated(alert, YES);
	setAlertTargetAction(alert, target);
	%orig(arg1, NO, arg3);
}

%end
