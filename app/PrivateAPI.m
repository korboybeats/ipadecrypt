#import "PrivateAPI.h"
#import "Logging.h"
#import <dlfcn.h>

BOOL IDLoadStoreKitUI(void) {
    static BOOL loaded = NO;
    if (loaded) return YES;
    void *h = dlopen("/System/Library/PrivateFrameworks/StoreKitUI.framework/StoreKitUI", RTLD_NOW);
    if (!h) {
        IDLOG(@"dlopen StoreKitUI failed: %s", dlerror());
        return NO;
    }
    loaded = YES;
    IDLOG(@"StoreKitUI loaded");
    return YES;
}
