#import <Foundation/Foundation.h>

// Always log to /var/mobile/Documents/ipadecrypt/logs/app.log so we can
// diagnose the app without a TTY. NSLog goes to oslog and is harder to
// retrieve from a sealed app.
extern NSString *const IDLogPath;

void IDLogv(const char *file, int line, NSString *fmt, va_list args);
void IDLog(const char *file, int line, NSString *fmt, ...) NS_FORMAT_FUNCTION(3, 4);

#define IDLOG(...) IDLog(__FILE__, __LINE__, __VA_ARGS__)
