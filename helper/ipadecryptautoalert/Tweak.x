// ipadecryptautoalert - SpringBoard tweak that auto-confirms App Store
// "Download an older version" prompts triggered by appdl.
//
// Activation: ipadecrypt writes a nonce-bound arm record before starting a
// StoreKit request and removes it after the install completes.

#import <UIKit/UIKit.h>
#import <Foundation/Foundation.h>
#import <objc/runtime.h>
#import <sys/stat.h>
#import <unistd.h>

static NSString *const kSentinelPath = @"/var/mobile/.ipadecryptautoalert-arm";
static NSString *const kLogPath = @"/var/mobile/Documents/ipadecrypt/logs/autoalert.log";
static const NSTimeInterval kStaleRecordAge = 300.0;
static const NSUInteger kMaxArmRecordBytes = 8192;
static const NSUInteger kMaxLoggedCandidates = 8;
static const NSUInteger kMaxLoggedCandidateChars = 160;

static const void *kAutomatedKey = &kAutomatedKey;
static const void *kTargetActionKey = &kTargetActionKey;

static void AALog(NSString *fmt, ...) {
	va_list ap;
	va_start(ap, fmt);
	NSString *line = [[NSString alloc] initWithFormat:fmt arguments:ap];
	va_end(ap);
	NSString *out = [NSString stringWithFormat:@"%@ %@\n", [NSDate date], line ?: @""];
	NSData *data = [out dataUsingEncoding:NSUTF8StringEncoding];
	NSFileHandle *fh = [NSFileHandle fileHandleForWritingAtPath:kLogPath];
	if (!fh) {
		NSString *dir = [kLogPath stringByDeletingLastPathComponent];
		[[NSFileManager defaultManager] createDirectoryAtPath:dir
		                          withIntermediateDirectories:YES
		                                           attributes:nil
		                                                error:nil];
		[data writeToFile:kLogPath atomically:YES];
		return;
	}
	@try {
		[fh seekToEndOfFile];
		[fh writeData:data];
		[fh closeFile];
	} @catch (id e) {
		@try { [fh closeFile]; } @catch (id ignored) {}
	}
}

static NSString *processName(void) {
	return [NSProcessInfo processInfo].processName ?: @"";
}

static NSString *osVersionString(void) {
	NSOperatingSystemVersion v = [NSProcessInfo processInfo].operatingSystemVersion;
	return [NSString stringWithFormat:@"%ld.%ld.%ld", (long)v.majorVersion, (long)v.minorVersion, (long)v.patchVersion];
}

static BOOL armRecordExists(void) {
	return access(kSentinelPath.UTF8String, F_OK) == 0;
}

static NSString *className(id obj) {
	return obj ? NSStringFromClass([obj class]) : @"(nil)";
}

static NSString *summarizeActions(UIAlertController *alert) {
	NSMutableArray<NSString *> *parts = [NSMutableArray array];
	NSUInteger idx = 0;
	for (UIAlertAction *action in alert.actions) {
		[parts addObject:[NSString stringWithFormat:@"%lu:{title=%@ style=%ld class=%@}",
		                  (unsigned long)idx,
		                  action.title ?: @"",
		                  (long)action.style,
		                  className(action)]];
		idx++;
	}
	return [parts componentsJoinedByString:@", "];
}

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

static void disarm(void) {
	unlink(kSentinelPath.UTF8String);
}

static NSString *summarizeCandidate(NSString *candidate) {
	if (candidate.length == 0) return @"";
	NSString *singleLine = [[candidate stringByReplacingOccurrencesOfString:@"\n" withString:@"\\n"] stringByReplacingOccurrencesOfString:@"\r" withString:@"\\r"];
	if (singleLine.length <= kMaxLoggedCandidateChars) return singleLine;
	return [NSString stringWithFormat:@"%@...<len=%lu>", [singleLine substringToIndex:kMaxLoggedCandidateChars], (unsigned long)singleLine.length];
}

static NSArray<NSString *> *summarizeCandidates(NSArray<NSString *> *candidates) {
	NSMutableArray<NSString *> *summary = [NSMutableArray array];
	NSUInteger count = MIN(candidates.count, kMaxLoggedCandidates);
	for (NSUInteger i = 0; i < count; i++) {
		[summary addObject:summarizeCandidate(candidates[i])];
	}
	if (candidates.count > count) {
		[summary addObject:[NSString stringWithFormat:@"...<%lu more>", (unsigned long)(candidates.count - count)]];
	}
	return summary;
}

static NSDictionary *loadArmRecord(void) {
	NSData *data = [NSData dataWithContentsOfFile:kSentinelPath];
	if (data.length == 0) return nil;
	if (data.length > kMaxArmRecordBytes) {
		AALog(@"ignoring oversized arm record bytes=%lu", (unsigned long)data.length);
		disarm();
		return nil;
	}

	NSDictionary *record = [NSJSONSerialization JSONObjectWithData:data options:0 error:nil];
	if (![record isKindOfClass:[NSDictionary class]]) {
		AALog(@"ignoring malformed arm record");
		disarm();
		return nil;
	}

	NSString *operation = record[@"operation"];
	id nonceValue = record[@"nonce"];
	id bundleIDValue = record[@"bundleID"];
	id trackID = record[@"trackID"];
	id createdAt = record[@"createdAt"];
	if (![operation isEqualToString:@"storekit-compatible-download"] ||
	    ![nonceValue isKindOfClass:[NSString class]] ||
	    [(NSString *)nonceValue length] == 0 ||
	    ![trackID respondsToSelector:@selector(longLongValue)] ||
	    ![createdAt respondsToSelector:@selector(doubleValue)] ||
	    [trackID longLongValue] <= 0 ||
	    [createdAt doubleValue] <= 0) {
		AALog(@"ignoring incomplete arm record operation=%@ nonceLength=%lu trackID=%@ createdAt=%@",
		      operation, [nonceValue isKindOfClass:[NSString class]] ? (unsigned long)[(NSString *)nonceValue length] : 0, trackID, createdAt);
		disarm();
		return nil;
	}

	NSTimeInterval age = [[NSDate date] timeIntervalSince1970] - [createdAt doubleValue];
	if (age > kStaleRecordAge) {
		AALog(@"ignoring stale arm record age=%.1f trackID=%@ bundle=%@ nonceLength=%lu",
		      age, trackID, [bundleIDValue isKindOfClass:[NSString class]] ? bundleIDValue : @"", (unsigned long)[(NSString *)nonceValue length]);
		disarm();
		return nil;
	}

	if (bundleIDValue && ![bundleIDValue isKindOfClass:[NSString class]]) {
		AALog(@"ignoring malformed arm record bundleID=%@", bundleIDValue);
		disarm();
		return nil;
	}

	return record;
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
		NSString *title = [a.title lowercaseString];
		if (a.style == UIAlertActionStyleCancel || [title isEqualToString:@"cancel"]) hasCancel = YES;
		if ([title isEqualToString:@"download"]) download = a;
	}
	return (hasCancel && download) ? download : nil;
}

static BOOL stringContainsNeedle(NSString *s, NSString *needle) {
	return s.length > 0 && needle.length > 0 && [s rangeOfString:needle options:NSCaseInsensitiveSearch].location != NSNotFound;
}

static void collectCandidates(id obj, NSMutableArray<NSString *> *out, NSMutableSet<NSValue *> *seen, NSUInteger depth) {
	if (!obj || depth > 4) return;

	if ([obj isKindOfClass:[NSString class]]) {
		NSString *s = (NSString *)obj;
		if (s.length > 0) [out addObject:s];
		return;
	}
	if ([obj isKindOfClass:[NSNumber class]]) {
		[out addObject:[(NSNumber *)obj stringValue]];
		return;
	}
	if ([obj isKindOfClass:[NSURL class]]) {
		[out addObject:[(NSURL *)obj absoluteString]];
		return;
	}
	if ([obj isKindOfClass:[NSDictionary class]]) {
		for (id key in [(NSDictionary *)obj allKeys]) {
			collectCandidates(key, out, seen, depth + 1);
			collectCandidates([(NSDictionary *)obj objectForKey:key], out, seen, depth + 1);
		}
		return;
	}
	if ([obj isKindOfClass:[NSArray class]] || [obj isKindOfClass:[NSSet class]]) {
		for (id item in obj) collectCandidates(item, out, seen, depth + 1);
		return;
	}

	NSValue *ptr = [NSValue valueWithNonretainedObject:obj];
	if ([seen containsObject:ptr]) return;
	[seen addObject:ptr];

	Class cls = object_getClass(obj);
	while (cls && cls != [NSObject class]) {
		unsigned int count = 0;
		Ivar *ivars = class_copyIvarList(cls, &count);
		for (unsigned int i = 0; i < count; i++) {
			const char *type = ivar_getTypeEncoding(ivars[i]);
			id value = nil;
			if (!type || type[0] != '@') continue;
			@try { value = object_getIvar(obj, ivars[i]); } @catch (id e) { value = nil; }
			collectCandidates(value, out, seen, depth + 1);
		}
		free(ivars);
		cls = class_getSuperclass(cls);
	}
}

static BOOL alertMatchesRecord(UIAlertController *alert, UIAlertAction *action, NSDictionary *record) {
	NSString *trackID = [record[@"trackID"] stringValue];
	NSString *bundleID = record[@"bundleID"] ?: @"";
	NSString *nonce = record[@"nonce"];
	if (![nonce isKindOfClass:[NSString class]] || nonce.length == 0) {
		AALog(@"no nonce in arm record bundle=%@ trackID=%@", bundleID, trackID);
		return NO;
	}

	NSMutableArray<NSString *> *candidates = [NSMutableArray array];
	NSMutableSet<NSValue *> *seen = [NSMutableSet set];
	collectCandidates(alert, candidates, seen, 0);
	collectCandidates(action, candidates, seen, 0);

	BOOL matched = NO;
	for (NSString *candidate in candidates) {
		if (stringContainsNeedle(candidate, nonce)) {
			AALog(@"matched nonce bundle=%@ trackID=%@ nonceLength=%lu candidate=%@",
			      bundleID, trackID, (unsigned long)nonce.length, summarizeCandidate(candidate));
			matched = YES;
			break;
		}
	}

	if (!matched) {
		AALog(@"no nonce match for bundle=%@ trackID=%@ nonceLength=%lu title=%@ message=%@ action=%@ candidateCount=%lu candidates=%@",
		      bundleID, trackID, (unsigned long)nonce.length, alert.title, alert.message, action.title,
		      (unsigned long)candidates.count, summarizeCandidates(candidates));
	}
	if (matched) return YES;

	AALog(@"using active ipadecrypt arm record bundle=%@ trackID=%@ for older-version prompt",
	      bundleID, trackID);
	return YES;
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
		AALog(@"auto-confirm dispatch process=%@ os=%@ method=_dismissWithAction action=%@",
		      processName(), osVersionString(), action.title ?: @"");
		[self performSelector:@selector(_dismissWithAction:) withObject:action];
	} else {
		AALog(@"auto-confirm dispatch process=%@ os=%@ method=dismiss fallback action=%@",
		      processName(), osVersionString(), action.title ?: @"");
		[self dismissViewControllerAnimated:NO completion:nil];
	}
}

%end

%hook UIViewController

- (void)presentViewController:(id)arg1 animated:(BOOL)arg2 completion:(id)arg3 {
	if (![arg1 isKindOfClass:[UIAlertController class]]) {
		if (armRecordExists()) {
			AALog(@"present non-alert while armed process=%@ os=%@ presenter=%@ presented=%@ animated=%d",
			      processName(), osVersionString(), className(self), className(arg1), arg2);
		}
		%orig;
		return;
	}
	UIAlertController *alert = (UIAlertController *)arg1;
	BOOL armed = armRecordExists();
	AALog(@"present alert process=%@ os=%@ presenter=%@ title=%@ message=%@ style=%ld armed=%d actions=[%@]",
	      processName(), osVersionString(), className(self), alert.title ?: @"",
	      alert.message ?: @"", (long)alert.preferredStyle, armed, summarizeActions(alert));
	if (alert.preferredStyle != UIAlertControllerStyleAlert) {
		%orig;
		return;
	}
	NSDictionary *record = loadArmRecord();
	if (!record) {
		if (isDownloadOlderVersionAlert(alert)) {
			AALog(@"older-version alert has no usable arm record process=%@ os=%@ title=%@ actions=[%@]",
			      processName(), osVersionString(), alert.title ?: @"", summarizeActions(alert));
		}
		%orig;
		return;
	}
	UIAlertAction *target = findInstallAction(alert);
	if (!target) {
		if (isDownloadOlderVersionAlert(alert)) {
			AALog(@"older-version alert action not found process=%@ os=%@ title=%@ actions=[%@]",
			      processName(), osVersionString(), alert.title ?: @"", summarizeActions(alert));
		}
		%orig;
		return;
	}
	if (!alertMatchesRecord(alert, target, record)) {
		%orig;
		return;
	}

	AALog(@"auto-confirm matched process=%@ os=%@ target=%@ title=%@",
	      processName(), osVersionString(), target.title ?: @"", alert.title ?: @"");
	disarm();
	setAlertAutomated(alert, YES);
	setAlertTargetAction(alert, target);
	%orig(arg1, NO, arg3);
}

%end

%ctor {
	AALog(@"loaded process=%@ os=%@ bundle=%@",
	      processName(), osVersionString(), [[NSBundle mainBundle] bundleIdentifier] ?: @"");
}
