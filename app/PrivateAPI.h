#import <Foundation/Foundation.h>

// SKUIItem and SKUIItemStateCenter live in StoreKitUI; we dlopen it at
// startup since it's a private framework. Declarations mirror what
// helper/appdl.m uses; only what's needed for the purchase flow.
@interface SKUIItem : NSObject
- (instancetype)initWithLookupDictionary:(NSDictionary *)dict;
@end

@interface SKUIItemStateCenter : NSObject
+ (instancetype)defaultCenter;
- (NSArray *)_newPurchasesWithItems:(NSArray *)items;
- (void)_performPurchases:(NSArray *)purchases
         hasBundlePurchase:(BOOL)hasBundle
         withClientContext:(id)ctx
            completionBlock:(void (^)(NSArray *responses, int flags))completion;
@end

// Each entry in the responses array exposes -error and -purchase.
@protocol SKUIPurchaseResponse <NSObject>
- (NSError *)error;
- (id)purchase;
@end

// Loads StoreKitUI; safe to call multiple times. Returns YES on success.
BOOL IDLoadStoreKitUI(void);
