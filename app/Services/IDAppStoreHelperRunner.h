#import <Foundation/Foundation.h>
#import "IDAppleAuthState.h"

@interface IDAppStoreHelperRunner : NSObject

+ (void)runWithBundleID:(NSString *)bundleID
                trackID:(NSInteger)trackID
                  email:(NSString *)email
               password:(NSString *)password
               authCode:(NSString *)authCode
                onEvent:(void (^)(NSDictionary *event))eventBlock
             completion:(void (^)(int exitCode, NSError *err))completion;

+ (void)runWithBundleID:(NSString *)bundleID
                trackID:(NSInteger)trackID
      externalVersionID:(NSString *)externalVersionID
                  email:(NSString *)email
               password:(NSString *)password
               authCode:(NSString *)authCode
                onEvent:(void (^)(NSDictionary *event))eventBlock
             completion:(void (^)(int exitCode, NSError *err))completion;

+ (void)listVersionsWithBundleID:(NSString *)bundleID
                          trackID:(NSInteger)trackID
                            email:(NSString *)email
                         password:(NSString *)password
                         authCode:(NSString *)authCode
                          onEvent:(void (^)(NSDictionary *event))eventBlock
                       completion:(void (^)(int exitCode, NSError *err))completion;

+ (void)fetchVersionMetadataWithBundleID:(NSString *)bundleID
                                  trackID:(NSInteger)trackID
                        externalVersionID:(NSString *)externalVersionID
                                    email:(NSString *)email
                                 password:(NSString *)password
                                 authCode:(NSString *)authCode
                                  onEvent:(void (^)(NSDictionary *event))eventBlock
                               completion:(void (^)(int exitCode, NSError *err))completion;

+ (void)refreshAuthWithEmail:(NSString *)email
                    password:(NSString *)password
                    authCode:(NSString *)authCode
                     onEvent:(void (^)(NSDictionary *event))eventBlock
                  completion:(void (^)(int exitCode, NSError *err))completion;

+ (void)checkSavedAuthWithCompletion:(void (^)(int exitCode, NSError *err))completion;

+ (void)verifyIPA:(NSString *)ipaPath
          onEvent:(void (^)(NSDictionary *event))eventBlock
       completion:(void (^)(int exitCode, NSError *err))completion;

+ (NSString *)bundledHelperPath;
+ (BOOL)hasActiveOperation;
+ (IDAppleAuthCheckResult)lastAuthResult;

@end
