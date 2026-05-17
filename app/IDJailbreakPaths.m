#import "IDJailbreakPaths.h"

#if defined(THEOS_PACKAGE_SCHEME_ROOTHIDE)
#if __has_include(<roothide/roothide.h>)
#import <roothide/roothide.h>
#elif __has_include(<roothide.h>)
#import <roothide.h>
#endif
#endif

NSString *IDJailbreakPath(NSString *path) {
#if defined(THEOS_PACKAGE_SCHEME_ROOTHIDE)
    return jbroot(path);
#else
    return path;
#endif
}

NSString *IDUserDocumentsPath(NSString *path) {
#if defined(THEOS_PACKAGE_SCHEME_ROOTHIDE)
    NSString *rootfsPath = [@"/rootfs" stringByAppendingString:(path ?: @"")];
    if ([[NSFileManager defaultManager] fileExistsAtPath:@"/rootfs/var/mobile"]) {
        return rootfsPath;
    }
#endif
    return path;
}
