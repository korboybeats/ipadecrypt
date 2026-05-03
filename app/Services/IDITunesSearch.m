#import "IDITunesSearch.h"
#import "Logging.h"

@implementation IDITunesSearch

+ (NSArray<IDSearchResult *> *)resultsFromJSONData:(NSData *)data error:(NSError **)error {
    NSDictionary *json = [NSJSONSerialization JSONObjectWithData:data options:0 error:error];
    if (*error || ![json isKindOfClass:[NSDictionary class]]) {
        return @[];
    }

    NSArray *raw = json[@"results"];
    NSMutableArray *out = [NSMutableArray array];
    for (NSDictionary *r in raw) {
        if (![r isKindOfClass:[NSDictionary class]]) continue;
        IDSearchResult *s = [[IDSearchResult alloc] init];
        s.bundleID = r[@"bundleId"];
        s.trackName = r[@"trackName"];
        s.artistName = r[@"artistName"];
        s.version = r[@"version"];
        s.minimumOSVersion = r[@"minimumOsVersion"];
        id tid = r[@"trackId"];
        if ([tid isKindOfClass:[NSNumber class]]) s.trackID = [tid integerValue];
        if (s.bundleID) [out addObject:s];
    }
    return out;
}

+ (void)search:(NSString *)term
       country:(NSString *)country
         limit:(NSInteger)limit
    completion:(void (^)(NSArray<IDSearchResult *> *, NSError *))completion {
    if (limit <= 0 || limit > 200) limit = 10;
    if (country.length == 0) country = @"US";

    NSURLComponents *u = [NSURLComponents componentsWithString:@"https://itunes.apple.com/search"];
    u.queryItems = @[
        [NSURLQueryItem queryItemWithName:@"term" value:term],
        [NSURLQueryItem queryItemWithName:@"entity" value:@"software,iPadSoftware"],
        [NSURLQueryItem queryItemWithName:@"media" value:@"software"],
        [NSURLQueryItem queryItemWithName:@"country" value:country],
        [NSURLQueryItem queryItemWithName:@"limit"
                                    value:[NSString stringWithFormat:@"%ld", (long)limit]],
    ];

    NSURLSessionDataTask *task = [[NSURLSession sharedSession]
        dataTaskWithURL:u.URL
      completionHandler:^(NSData *data, NSURLResponse *resp, NSError *err) {
          dispatch_async(dispatch_get_main_queue(), ^{
              if (err) { completion(@[], err); return; }
              NSError *jerr = nil;
              NSArray *out = [self resultsFromJSONData:data error:&jerr];
              if (jerr) {
                  completion(@[], jerr ?: [NSError errorWithDomain:@"id" code:1 userInfo:nil]);
                  return;
              }
              IDLOG(@"search %@: %lu results", term, (unsigned long)out.count);
              completion(out, nil);
          });
      }];
    [task resume];
}

+ (void)lookupBundleID:(NSString *)bundleID
               country:(NSString *)country
            completion:(void (^)(IDSearchResult *, NSError *))completion {
    if (bundleID.length == 0) {
        completion(nil, [NSError errorWithDomain:@"IDITunesSearch" code:1
                                        userInfo:@{NSLocalizedDescriptionKey: @"missing bundle ID"}]);
        return;
    }
    if (country.length == 0) country = @"US";

    NSURLComponents *u = [NSURLComponents componentsWithString:@"https://itunes.apple.com/lookup"];
    u.queryItems = @[
        [NSURLQueryItem queryItemWithName:@"bundleId" value:bundleID],
        [NSURLQueryItem queryItemWithName:@"country" value:country],
    ];

    NSURLSessionDataTask *task = [[NSURLSession sharedSession]
        dataTaskWithURL:u.URL
      completionHandler:^(NSData *data, NSURLResponse *resp, NSError *err) {
          dispatch_async(dispatch_get_main_queue(), ^{
              if (err) { completion(nil, err); return; }
              NSError *jerr = nil;
              NSArray<IDSearchResult *> *results = [self resultsFromJSONData:data error:&jerr];
              if (jerr) {
                  completion(nil, jerr);
                  return;
              }
              for (IDSearchResult *r in results) {
                  if ([r.bundleID isEqualToString:bundleID]) {
                      IDLOG(@"lookup %@: trackID=%ld", bundleID, (long)r.trackID);
                      completion(r, nil);
                      return;
                  }
              }
              completion(nil, nil);
          });
      }];
    [task resume];
}

@end
