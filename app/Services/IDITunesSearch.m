#import "IDITunesSearch.h"
#import "Logging.h"

@implementation IDITunesSearch

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
              NSDictionary *json = [NSJSONSerialization JSONObjectWithData:data options:0 error:&jerr];
              if (jerr || ![json isKindOfClass:[NSDictionary class]]) {
                  completion(@[], jerr ?: [NSError errorWithDomain:@"id" code:1 userInfo:nil]);
                  return;
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
              IDLOG(@"search %@: %lu results", term, (unsigned long)out.count);
              completion(out, nil);
          });
      }];
    [task resume];
}

@end
