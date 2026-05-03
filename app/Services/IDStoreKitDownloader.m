#import "IDStoreKitDownloader.h"
#import "PrivateAPI.h"
#import "Logging.h"
#import <objc/runtime.h>
#import <objc/message.h>

@implementation IDStoreKitDownloader

+ (void)downloadTrackID:(NSInteger)trackID
                  nonce:(NSString *)nonce
             completion:(void (^)(NSError *))completion {
    if (!IDLoadStoreKitUI()) {
        completion([NSError errorWithDomain:@"IDStoreKitDownloader" code:1
                                   userInfo:@{NSLocalizedDescriptionKey: @"StoreKitUI unavailable"}]);
        return;
    }

    NSString *buyParams = [NSString stringWithFormat:
        @"productType=C&price=0&salableAdamId=%ld&pricingParameters=STDRDL", (long)trackID];
    NSString *requestNonce = nonce ?: @"";

    NSDictionary *lookup = @{
        @"kind": @"iosSoftware",
        @"ipadecryptAutoalertNonce": requestNonce,
        @"offers": @[@{
            @"buyParams": buyParams,
            @"ipadecryptAutoalertNonce": requestNonce,
        }],
    };

    Class itemCls = NSClassFromString(@"SKUIItem");
    Class centerCls = NSClassFromString(@"SKUIItemStateCenter");
    if (!itemCls || !centerCls) {
        completion([NSError errorWithDomain:@"IDStoreKitDownloader" code:2
                                   userInfo:@{NSLocalizedDescriptionKey: @"private classes missing"}]);
        return;
    }

    id item = ((id (*)(id, SEL, NSDictionary *))objc_msgSend)(
        ((id (*)(Class, SEL))objc_msgSend)(itemCls, sel_registerName("alloc")),
        sel_registerName("initWithLookupDictionary:"),
        lookup);
    if (!item) {
        completion([NSError errorWithDomain:@"IDStoreKitDownloader" code:3
                                   userInfo:@{NSLocalizedDescriptionKey: @"SKUIItem init failed"}]);
        return;
    }

    id center = ((id (*)(Class, SEL))objc_msgSend)(centerCls, sel_registerName("defaultCenter"));
    NSArray *purchases = ((NSArray * (*)(id, SEL, NSArray *))objc_msgSend)(
        center, sel_registerName("_newPurchasesWithItems:"), @[item]);
    if (purchases.count == 0) {
        completion([NSError errorWithDomain:@"IDStoreKitDownloader" code:4
                                   userInfo:@{NSLocalizedDescriptionKey: @"no purchases produced"}]);
        return;
    }

    IDLOG(@"storekit: initiating download for trackID=%ld", (long)trackID);

    ((void (*)(id, SEL, NSArray *, BOOL, id,
        void (^)(NSArray *, int)))objc_msgSend)(center,
        sel_registerName("_performPurchases:hasBundlePurchase:withClientContext:completionBlock:"),
        purchases, NO, nil, ^(NSArray *responses, int flags) {
            __block NSError *outErr = nil;
            for (id resp in responses) {
                NSError *e = ((NSError * (*)(id, SEL))objc_msgSend)(
                    resp, sel_registerName("error"));
                if (e) { outErr = e; break; }
            }
            dispatch_async(dispatch_get_main_queue(), ^{
                if (outErr) {
                    IDLOG(@"storekit error: %@", outErr.localizedDescription);
                } else {
                    IDLOG(@"storekit: accepted");
                }
                completion(outErr);
            });
        });
}

@end
