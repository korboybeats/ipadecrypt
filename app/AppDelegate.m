#import "AppDelegate.h"
#import "Logging.h"
#import <unistd.h>

@implementation AppDelegate

- (BOOL)application:(UIApplication *)application
    didFinishLaunchingWithOptions:(NSDictionary *)options {
    IDLOG(@"app launched, pid=%d", getpid());
    return YES;
}

- (UISceneConfiguration *)application:(UIApplication *)application
    configurationForConnectingSceneSession:(UISceneSession *)connecting
                                   options:(UISceneConnectionOptions *)opts {
    return [[UISceneConfiguration alloc] initWithName:@"Default Configuration"
                                          sessionRole:connecting.role];
}

@end
