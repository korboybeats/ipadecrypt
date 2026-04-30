// ipadecrypt-appdl-arm64 - on-device App Store download via StoreKitUI.
//
// Triggers the iOS StoreKit purchase flow for a given bundle-id / app-id.
// Apple's CDN auto-serves the latest version compatible with the device's
// iOS, so this avoids the MinimumOSVersion patching path on the computer.
//
// Strategy: dlopen StoreKitUI, build a SKUIItem from a synthetic lookup
// dictionary (productType=C, salableAdamId=<id>, pricingParameters=STDQ),
// hand it to SKUIItemStateCenter._performPurchases. The active iCloud
// account on the device authorizes the purchase. Pre-built ipadecrypt
// downloads via the Configurator endpoint from the computer - that path
// has no clue what iOS the device is on, so we only fall back to it.
//
// CLI: ipadecrypt-appdl-arm64 <bundle-id|app-id>
//   bundle-id - looked up via itunes.apple.com/lookup?bundleId=
//   app-id    - all-digits, used directly as salableAdamId.
//
// Output (stderr): @evt event=... key=value\n  one line per phase change.
// Exit:  0 = purchase request accepted, 1 = lookup/setup error,
//        2 = appstored returned an error response.

#import <Foundation/Foundation.h>
#import <objc/runtime.h>
#import <objc/message.h>
#import <dlfcn.h>

#define EVT(fmt, ...) do { fprintf(stderr, "@evt " fmt "\n", ##__VA_ARGS__); fflush(stderr); } while (0)
#define ERR(fmt, ...) do { fprintf(stderr, "[appdl] ERROR: " fmt "\n", ##__VA_ARGS__); fflush(stderr); } while (0)

// Look up a numeric trackId from a CFBundleIdentifier via the public iTunes
// lookup endpoint. Returns nil if not found.
static NSString *lookupAppID(NSString *bundleID) {
    NSString *urlStr = [NSString stringWithFormat:
        @"https://itunes.apple.com/lookup?bundleId=%@&limit=1", bundleID];
    NSURL *url = [NSURL URLWithString:urlStr];
    NSData *data = [NSData dataWithContentsOfURL:url];
    if (!data) return nil;

    NSError *err = nil;
    NSDictionary *json = [NSJSONSerialization JSONObjectWithData:data options:0 error:&err];
    if (err || ![json isKindOfClass:[NSDictionary class]]) return nil;

    NSArray *results = json[@"results"];
    if (![results isKindOfClass:[NSArray class]] || results.count == 0) return nil;

    id trackId = results[0][@"trackId"];
    if ([trackId isKindOfClass:[NSNumber class]]) {
        return [(NSNumber *)trackId stringValue];
    }
    if ([trackId isKindOfClass:[NSString class]]) {
        return (NSString *)trackId;
    }
    return nil;
}

static BOOL isAllDigits(NSString *s) {
    if (s.length == 0) return NO;
    for (NSUInteger i = 0; i < s.length; i++) {
        unichar c = [s characterAtIndex:i];
        if (c < '0' || c > '9') return NO;
    }
    return YES;
}

// Initiate the purchase. Returns 0 on accepted-by-appstored, 2 on error
// reported in the completion block.
static int performAppDownload(NSString *appID) {
    // STDRDL = redownload from library: no purchase transaction, no Face ID
    // prompt, no install confirmation. Apps must already be in the user's
    // App Store library (free apps you've previously gotten count). For
    // never-acquired apps appstored returns an error.
    NSString *buyParams = [NSString stringWithFormat:
        @"productType=C&price=0&salableAdamId=%@&pricingParameters=STDRDL", appID];

    NSDictionary *lookupDict = @{
        @"kind": @"iosSoftware",
        @"offers": @[@{@"buyParams": buyParams}],
    };

    Class itemCls = objc_getClass("SKUIItem");
    if (!itemCls) {
        ERR("SKUIItem class not found - StoreKitUI not loaded?");
        return 1;
    }

    id allocatedItem = ((id (*)(Class, SEL))objc_msgSend)(itemCls, sel_registerName("alloc"));
    id item = ((id (*)(id, SEL, NSDictionary *))objc_msgSend)(
        allocatedItem, sel_registerName("initWithLookupDictionary:"), lookupDict);
    if (!item) {
        ERR("could not create SKUIItem for app id %s", appID.UTF8String);
        return 1;
    }

    Class centerCls = objc_getClass("SKUIItemStateCenter");
    if (!centerCls) {
        ERR("SKUIItemStateCenter class not found");
        return 1;
    }

    id center = ((id (*)(Class, SEL))objc_msgSend)(centerCls, sel_registerName("defaultCenter"));
    NSArray *purchases = ((NSArray * (*)(id, SEL, NSArray *))objc_msgSend)(
        center, sel_registerName("_newPurchasesWithItems:"), @[item]);
    if (!purchases || purchases.count == 0) {
        ERR("could not create SSPurchase batch");
        return 1;
    }

    EVT("event=initiating app_id=%s", appID.UTF8String);

    __block int rc = 0;
    dispatch_semaphore_t sem = dispatch_semaphore_create(0);

    ((void (*)(id, SEL, NSArray *, BOOL, id,
        void (^)(NSArray *, int)))objc_msgSend)(center,
        sel_registerName("_performPurchases:hasBundlePurchase:withClientContext:completionBlock:"),
        purchases, NO, nil, ^(NSArray *responses, int flags) {

        if (responses.count == 0) {
            ERR("no responses from appstored");
            EVT("event=error reason=\"no responses from appstored\"");
            rc = 2;
            dispatch_semaphore_signal(sem);
            return;
        }

        for (id response in responses) {
            NSError *responseError = ((NSError * (*)(id, SEL))objc_msgSend)(
                response, sel_registerName("error"));
            if (responseError) {
                NSString *desc = [responseError localizedDescription] ?: @"(unknown)";
                NSString *escaped = [desc stringByReplacingOccurrencesOfString:@"\"" withString:@"'"];
                ERR("appstored: %s", desc.UTF8String);
                EVT("event=error reason=\"%s\" code=%ld",
                    escaped.UTF8String, (long)responseError.code);
                rc = 2;
            }
        }
        dispatch_semaphore_signal(sem);
    });

    dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, 30 * NSEC_PER_SEC));

    if (rc == 0) {
        EVT("event=accepted app_id=%s", appID.UTF8String);
    }
    return rc;
}

int main(int argc, char **argv) {
    @autoreleasepool {
        if (argc != 2) {
            fprintf(stderr, "usage: %s <bundle-id|app-id>\n", argv[0]);
            return 1;
        }

        if (!dlopen("/System/Library/PrivateFrameworks/StoreKitUI.framework/StoreKitUI", RTLD_NOW)) {
            ERR("dlopen StoreKitUI: %s", dlerror());
            return 1;
        }

        NSString *target = [NSString stringWithUTF8String:argv[1]];
        NSString *appID = nil;

        if (isAllDigits(target)) {
            appID = target;
        } else {
            EVT("event=lookup bundle_id=%s", target.UTF8String);
            appID = lookupAppID(target);
            if (!appID) {
                ERR("itunes lookup failed for %s", target.UTF8String);
                return 1;
            }
            EVT("event=resolved bundle_id=%s app_id=%s", target.UTF8String, appID.UTF8String);
        }

        return performAppDownload(appID);
    }
}
