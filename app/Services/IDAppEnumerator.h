#import <Foundation/Foundation.h>
#import "IDInstalledApp.h"

@interface IDAppEnumerator : NSObject
// Scans /var/containers/Bundle/Application/* for .app bundles, reads each
// Info.plist, returns one IDInstalledApp per app sorted by display name.
+ (NSArray<IDInstalledApp *> *)installedApps;
@end
