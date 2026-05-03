#import <Foundation/Foundation.h>

@interface IDAppStoreHelperRunner : NSObject

+ (void)runWithBundleID:(NSString *)bundleID
                trackID:(NSInteger)trackID
                  email:(NSString *)email
               password:(NSString *)password
               authCode:(NSString *)authCode
                onEvent:(void (^)(NSDictionary *event))eventBlock
             completion:(void (^)(int exitCode, NSError *err))completion;

+ (void)verifyIPA:(NSString *)ipaPath
          onEvent:(void (^)(NSDictionary *event))eventBlock
       completion:(void (^)(int exitCode, NSError *err))completion;

+ (NSString *)bundledHelperPath;

@end
