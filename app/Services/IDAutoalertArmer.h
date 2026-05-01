#import <Foundation/Foundation.h>

// Touches /var/mobile/.ipadecryptautoalert-arm so the SpringBoard tweak
// auto-confirms the "Download an older version" prompt for the next 60s.
// disarm should be called when the install finishes.
@interface IDAutoalertArmer : NSObject
+ (void)arm;
+ (void)disarm;
@end
