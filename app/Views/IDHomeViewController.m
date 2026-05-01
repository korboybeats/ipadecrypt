#import "IDHomeViewController.h"
#import "IDAppRowCell.h"
#import "IDInstalledApp.h"
#import "IDSearchResult.h"
#import "IDAppEnumerator.h"
#import "IDITunesSearch.h"
#import "IDDecryptOptionsViewController.h"
#import "IDDecryptProgressViewController.h"
#import "IDHelperRunner.h"
#import "IDStoreKitDownloader.h"
#import "IDAutoalertArmer.h"
#import "Logging.h"

static NSString *const IDOpenFilzaAfterDecryptKey = @"OpenFilzaAfterDecrypt";

@interface IDHomeViewController ()
@property (nonatomic, strong) NSArray<IDInstalledApp *> *installed;
@property (nonatomic, strong) NSArray<IDInstalledApp *> *installedFiltered;
@property (nonatomic, strong) NSArray<IDSearchResult *> *appStoreHits;
@property (nonatomic, strong) UISearchController *searchController;
@property (nonatomic, copy) NSString *currentTerm;
@end

@implementation IDHomeViewController

- (void)viewDidLoad {
    [super viewDidLoad];
    self.title = @"ipadecrypt";
    [self.tableView registerClass:[IDAppRowCell class] forCellReuseIdentifier:[IDAppRowCell reuseID]];

    self.searchController = [[UISearchController alloc] initWithSearchResultsController:nil];
    self.searchController.searchResultsUpdater = self;
    self.searchController.obscuresBackgroundDuringPresentation = NO;
    self.searchController.searchBar.placeholder = @"Search installed or App Store";
    self.navigationItem.searchController = self.searchController;
    self.navigationItem.hidesSearchBarWhenScrolling = NO;
    UIImage *gear = [UIImage systemImageNamed:@"gearshape"];
    UIBarButtonItem *settings = [[UIBarButtonItem alloc] initWithImage:gear
                                                                 style:UIBarButtonItemStylePlain
                                                                target:self
                                                                action:@selector(showSettings)];
    settings.accessibilityLabel = @"Settings";
    self.navigationItem.rightBarButtonItem = settings;

    UIRefreshControl *rc = [[UIRefreshControl alloc] init];
    [rc addTarget:self action:@selector(reload) forControlEvents:UIControlEventValueChanged];
    self.refreshControl = rc;

    [self reload];
}

- (void)showSettings {
    BOOL enabled = [[NSUserDefaults standardUserDefaults] boolForKey:IDOpenFilzaAfterDecryptKey];
    UIAlertController *sheet = [UIAlertController alertControllerWithTitle:@"Settings"
                                                                   message:nil
                                                            preferredStyle:UIAlertControllerStyleActionSheet];
    NSString *toggleTitle = enabled ? @"Open Filza after decrypt: On" : @"Open Filza after decrypt: Off";
    [sheet addAction:[UIAlertAction actionWithTitle:toggleTitle
                                              style:UIAlertActionStyleDefault
                                            handler:^(UIAlertAction *a) {
        [[NSUserDefaults standardUserDefaults] setBool:!enabled forKey:IDOpenFilzaAfterDecryptKey];
    }]];
    [sheet addAction:[UIAlertAction actionWithTitle:@"Cancel"
                                              style:UIAlertActionStyleCancel
                                            handler:nil]];
    [self presentViewController:sheet animated:YES completion:nil];
}

- (void)reload {
    self.installed = [IDAppEnumerator installedApps];
    self.installedFiltered = self.installed;
    [self.tableView reloadData];
    [self.refreshControl endRefreshing];
}

#pragma mark - UISearchResultsUpdating

- (void)updateSearchResultsForSearchController:(UISearchController *)sc {
    NSString *term = sc.searchBar.text ?: @"";
    self.currentTerm = term;

    if (term.length == 0) {
        self.installedFiltered = self.installed;
        self.appStoreHits = @[];
        [self.tableView reloadData];
        return;
    }

    NSString *lc = term.lowercaseString;
    NSMutableArray *m = [NSMutableArray array];
    for (IDInstalledApp *a in self.installed) {
        if ([a.bundleID.lowercaseString containsString:lc] ||
            [a.displayName.lowercaseString containsString:lc]) {
            [m addObject:a];
        }
    }
    self.installedFiltered = m;
    [self.tableView reloadData];

    [NSObject cancelPreviousPerformRequestsWithTarget:self selector:@selector(performStoreSearch:)
                                               object:nil];
    [self performSelector:@selector(performStoreSearch:) withObject:term afterDelay:0.4];
}

- (void)performStoreSearch:(NSString *)term {
    if (![term isEqualToString:self.currentTerm]) return;
    [IDITunesSearch search:term country:@"US" limit:10
                completion:^(NSArray<IDSearchResult *> *r, NSError *err) {
        if (![term isEqualToString:self.currentTerm]) return;
        self.appStoreHits = r ?: @[];
        [self.tableView reloadData];
    }];
}

#pragma mark - UITableView

- (NSInteger)numberOfSectionsInTableView:(UITableView *)tv { return 2; }

- (NSString *)tableView:(UITableView *)tv titleForHeaderInSection:(NSInteger)s {
    if (s == 0) return @"Installed";
    return @"App Store";
}

- (NSInteger)tableView:(UITableView *)tv numberOfRowsInSection:(NSInteger)s {
    if (s == 0) return self.installedFiltered.count;
    return self.appStoreHits.count;
}

- (UITableViewCell *)tableView:(UITableView *)tv cellForRowAtIndexPath:(NSIndexPath *)ip {
    IDAppRowCell *cell = [tv dequeueReusableCellWithIdentifier:[IDAppRowCell reuseID] forIndexPath:ip];
    if (ip.section == 0) {
        IDInstalledApp *a = self.installedFiltered[ip.row];
        [cell configureWithTitle:a.displayName subtitle:a.bundleID
                        trailing:[NSString stringWithFormat:@"v%@", a.version]];
    } else {
        IDSearchResult *r = self.appStoreHits[ip.row];
        [cell configureWithTitle:r.trackName
                        subtitle:[NSString stringWithFormat:@"%@ — %@", r.bundleID, r.artistName]
                        trailing:r.minimumOSVersion];
    }
    return cell;
}

- (void)tableView:(UITableView *)tv didSelectRowAtIndexPath:(NSIndexPath *)ip {
    [tv deselectRowAtIndexPath:ip animated:YES];

    NSString *title = nil;
    NSString *installedDisplay = nil;
    NSInteger trackID = 0;

    if (ip.section == 0) {
        IDInstalledApp *a = self.installedFiltered[ip.row];
        title = a.displayName;
        installedDisplay = [NSString stringWithFormat:@"v%@", a.version];

        // Try to find a matching App Store hit for the trackID. Best-effort.
        for (IDSearchResult *r in self.appStoreHits) {
            if ([r.bundleID isEqualToString:a.bundleID]) {
                trackID = r.trackID;
                break;
            }
        }
    } else {
        IDSearchResult *r = self.appStoreHits[ip.row];
        title = r.trackName;
        trackID = r.trackID;

        // If the app is already installed, surface that option too.
        for (IDInstalledApp *ia in self.installed) {
            if ([ia.bundleID isEqualToString:r.bundleID]) {
                installedDisplay = [NSString stringWithFormat:@"v%@", ia.version];
                break;
            }
        }
    }

    [IDDecryptOptionsViewController presentFrom:self
                                          title:title
                               installedDisplay:installedDisplay
                                        trackID:trackID
                                     completion:^(IDDecryptOption opt, BOOL cancelled) {
        if (cancelled) return;
        IDLOG(@"picked option=%ld for %@", (long)opt, title);
        if (opt == IDDecryptOptionInstalled) {
            IDInstalledApp *target = nil;
            NSString *bid = ip.section == 1 ? self.appStoreHits[ip.row].bundleID : nil;
            for (IDInstalledApp *a in self.installed) {
                if (bid && [a.bundleID isEqualToString:bid]) { target = a; break; }
                if (!bid && [a.displayName isEqualToString:title]) { target = a; break; }
            }
            if (target) [self decryptInstalled:target];
        } else if (opt == IDDecryptOptionLatestStoreKit) {
            NSString *bid = ip.section == 1
                ? self.appStoreHits[ip.row].bundleID
                : self.installedFiltered[ip.row].bundleID;
            [self downloadAndDecryptTrackID:trackID bundleID:bid displayName:title];
        }
    }];
}

- (NSString *)outputIPAForBundle:(NSString *)bundleID version:(NSString *)version {
    NSString *dir = @"/var/mobile/Documents/ipadecrypt";
    [[NSFileManager defaultManager] createDirectoryAtPath:dir
                              withIntermediateDirectories:YES
                                               attributes:nil error:nil];
    NSString *file = [NSString stringWithFormat:@"%@_%@.decrypted.ipa",
                      bundleID, version.length ? version : @"unknown"];
    return [dir stringByAppendingPathComponent:file];
}

- (void)decryptInstalled:(IDInstalledApp *)app {
    [self decryptInstalled:app reuseVC:nil];
}

- (void)decryptInstalled:(IDInstalledApp *)app reuseVC:(IDDecryptProgressViewController *)existing {
    NSString *out = [self outputIPAForBundle:app.bundleID version:app.version];

    IDDecryptProgressViewController *vc = existing;
    if (!vc) {
        vc = [[IDDecryptProgressViewController alloc] initWithTitle:app.displayName];
        [self.navigationController pushViewController:vc animated:YES];
    }
    [vc appendStatus:[NSString stringWithFormat:@"decrypting %@ v%@", app.bundleID, app.version]];

    [IDHelperRunner runWithBundleID:app.bundleID
                         bundlePath:app.appBundlePath
                            outIPA:out
                            onEvent:^(NSDictionary *ev) {
        NSString *type = ev[@"event"] ?: @"";
        NSString *phase = ev[@"phase"] ?: @"";
        NSString *name = ev[@"name"] ?: ev[@"src"] ?: @"";
        if ([type isEqualToString:@"stderr"]) {
            [vc appendStatus:[NSString stringWithFormat:@"  %@", ev[@"line"] ?: @""]];
        } else if ([type isEqualToString:@"image"] && [phase isEqualToString:@"done"]) {
            [vc appendStatus:[NSString stringWithFormat:@"  decrypted %@", name]];
        } else if ([type isEqualToString:@"image"] && [phase isEqualToString:@"failed"]) {
            [vc appendStatus:[NSString stringWithFormat:@"  ! failed %@ (%@)",
                              name, ev[@"reason"] ?: @"unknown"]];
        } else if ([type isEqualToString:@"bundle"] && [phase isEqualToString:@"skipped"]) {
            [vc appendStatus:[NSString stringWithFormat:@"  skipped %@ (%@)",
                              [name lastPathComponent] ?: name, ev[@"reason"] ?: @"unknown"]];
        } else if ([type isEqualToString:@"bundle"] && [phase isEqualToString:@"done"]) {
            [vc appendStatus:[NSString stringWithFormat:@"  bundle done (%@ extra)",
                              ev[@"extras"] ?: @"0"]];
        } else if ([type isEqualToString:@"pack"] && [phase isEqualToString:@"start"]) {
            [vc appendStatus:@"  packaging IPA"];
        } else if ([type isEqualToString:@"pack"] && [phase isEqualToString:@"done"]) {
            [vc appendStatus:[NSString stringWithFormat:@"  packaged %@", [ev[@"ipa"] lastPathComponent] ?: @""]];
        } else if ([type isEqualToString:@"pack"] && [phase isEqualToString:@"failed"]) {
            [vc appendStatus:@"  ! packaging failed"];
        } else if ([type isEqualToString:@"spawn_failed"]) {
            [vc appendStatus:[NSString stringWithFormat:@"  ! spawn failed: %@", [name lastPathComponent] ?: name]];
        }
    }
                         completion:^(int code, NSError *err) {
        NSError *finalErr = err;
        [vc markCompleteWithOutputIPA:out error:finalErr];
    }];
}

- (void)downloadAndDecryptTrackID:(NSInteger)trackID
                          bundleID:(NSString *)bundleID
                       displayName:(NSString *)displayName {
    IDDecryptProgressViewController *vc = [[IDDecryptProgressViewController alloc]
        initWithTitle:displayName];
    [self.navigationController pushViewController:vc animated:YES];
    [vc appendStatus:[NSString stringWithFormat:@"requesting App Store download for %@", bundleID]];

    NSString *beforePath = [self pathForInstalledBundle:bundleID];
    NSString *beforeVersion = [self versionAtPath:beforePath];

    [IDAutoalertArmer arm];

    [IDStoreKitDownloader downloadTrackID:trackID
                               completion:^(NSError *err) {
        if (err) {
            [vc markCompleteWithOutputIPA:@""
                                    error:[NSError errorWithDomain:@"IDStoreKitDownloader" code:5
                                                          userInfo:@{NSLocalizedDescriptionKey:
                                            [NSString stringWithFormat:@"App Store rejected: %@",
                                             err.localizedDescription]}]];
            [IDAutoalertArmer disarm];
            return;
        }
        [vc appendStatus:@"download accepted, waiting for install..."];
        [self pollInstallChange:vc bundleID:bundleID
                     beforePath:beforePath beforeVersion:beforeVersion
                    deadlineSec:180 completion:^(IDInstalledApp *installed) {
            [IDAutoalertArmer disarm];
            if (!installed) {
                [vc markCompleteWithOutputIPA:@""
                                        error:[NSError errorWithDomain:@"IDPoll" code:1
                                                              userInfo:@{NSLocalizedDescriptionKey:
                                                @"timed out waiting for install"}]];
                return;
            }
            [vc appendStatus:[NSString stringWithFormat:@"installed v%@", installed.version]];
            // Reload installed list so future taps see the new version.
            self.installed = [IDAppEnumerator installedApps];
            // Reuse the same progress VC for the decrypt phase — pushing a new
            // one and popping the old one is racy and just popped the new one.
            [self decryptInstalled:installed reuseVC:vc];
        }];
    }];
}

- (NSString *)pathForInstalledBundle:(NSString *)bundleID {
    for (IDInstalledApp *a in [IDAppEnumerator installedApps]) {
        if ([a.bundleID isEqualToString:bundleID]) return a.appBundlePath;
    }
    return nil;
}

- (NSString *)versionAtPath:(NSString *)appPath {
    if (!appPath) return @"";
    NSDictionary *p = [NSDictionary dictionaryWithContentsOfFile:
                       [appPath stringByAppendingPathComponent:@"Info.plist"]];
    return p[@"CFBundleShortVersionString"] ?: p[@"CFBundleVersion"] ?: @"";
}

- (void)pollInstallChange:(IDDecryptProgressViewController *)vc
                 bundleID:(NSString *)bundleID
               beforePath:(NSString *)beforePath
            beforeVersion:(NSString *)beforeVersion
              deadlineSec:(NSTimeInterval)deadlineSec
               completion:(void (^)(IDInstalledApp *installed))completion {
    NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:deadlineSec];
    __block NSDate *stableSince = nil;

    dispatch_queue_t q = dispatch_get_global_queue(QOS_CLASS_USER_INITIATED, 0);
    __block void (^tick)(void);
    void (^tickBlock)(void) = ^{
        if ([[NSDate date] compare:deadline] == NSOrderedDescending) {
            dispatch_async(dispatch_get_main_queue(), ^{ completion(nil); });
            tick = nil;
            return;
        }
        IDInstalledApp *current = nil;
        for (IDInstalledApp *a in [IDAppEnumerator installedApps]) {
            if ([a.bundleID isEqualToString:bundleID]) { current = a; break; }
        }
        BOOL changed = current && (![current.appBundlePath isEqualToString:beforePath ?: @""] ||
                                   ![current.version isEqualToString:beforeVersion ?: @""]);
        if (changed) {
            dispatch_async(dispatch_get_main_queue(), ^{ completion(current); });
            tick = nil;
            return;
        }
        if (current) {
            if (!stableSince) stableSince = [NSDate date];
            if ([[NSDate date] timeIntervalSinceDate:stableSince] > 15) {
                // Already at latest — proceed.
                dispatch_async(dispatch_get_main_queue(), ^{ completion(current); });
                tick = nil;
                return;
            }
        } else {
            stableSince = nil;
        }
        __weak void (^weakTick)(void) = tick;
        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 2 * NSEC_PER_SEC), q, ^{
            void (^strongTick)(void) = weakTick;
            if (strongTick) strongTick();
        });
    };
    tick = tickBlock;
    dispatch_async(q, tick);
}

@end
