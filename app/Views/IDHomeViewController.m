#import "IDHomeViewController.h"
#import "IDAppRowCell.h"
#import "IDInstalledApp.h"
#import "IDSearchResult.h"
#import "IDAppEnumerator.h"
#import "IDITunesSearch.h"
#import "IDDecryptOptionsViewController.h"
#import "IDAppStoreVersionPickerViewController.h"
#import "IDDecryptProgressViewController.h"
#import "IDHelperRunner.h"
#import "IDAppStoreHelperRunner.h"
#import "IDStoreKitDownloader.h"
#import "IDAutoalertArmer.h"
#import "IDJailbreakPaths.h"
#import "Logging.h"
#import <objc/message.h>
#import <objc/runtime.h>

static NSString *const IDOpenFilzaAfterDecryptKey = @"OpenFilzaAfterDecrypt";
static NSString *IDDecryptedOutputDirectory(void) {
    return IDUserDocumentsPath(@"/var/mobile/Documents/ipadecrypt/decrypted");
}

static NSString *IDAppStoreStepTitle(NSString *name) {
    if ([name isEqualToString:@"loading-config"]) return @"Preparing App Store session";
    if ([name isEqualToString:@"authenticating"]) return @"Signing in to Apple ID";
    if ([name isEqualToString:@"lookup-track"] || [name isEqualToString:@"lookup-bundle"]) return @"Checking latest App Store version";
    if ([name isEqualToString:@"prepare-download"]) return @"Authorizing download";
    if ([name isEqualToString:@"patch"]) return @"Preparing IPA for install";
    if ([name isEqualToString:@"install"]) return @"Installing IPA";
    return name.length ? name : @"Working";
}

static BOOL IDShouldShowAppinstLine(NSString *line) {
    if (line.length == 0) return NO;
    if ([line containsString:@"Successfully installed"]) return YES;
    if ([line.lowercaseString containsString:@"error"] || [line.lowercaseString containsString:@"failed"]) return YES;
    return NO;
}

static NSString *IDAppinstDisplayLine(NSString *line) {
    if ([line containsString:@"Successfully installed"]) return @"Install completed";
    return [NSString stringWithFormat:@"appinst: %@", line];
}

static BOOL IDIsExpectedAuthStderr(NSString *line) {
    return [line containsString:@"Apple ID sign-in required"] ||
           [line containsString:@"auth code required"];
}

static BOOL IDIsExpectedDecryptStderr(NSString *line) {
    return [line containsString:@"child died during exec of "] ||
           [line containsString:@"spawn failed for "];
}

static NSString *IDHumanBytes(long long n) {
    double v = (double)n;
    if (n >= (1LL << 40)) return [NSString stringWithFormat:@"%.2f TB", v / (double)(1LL << 40)];
    if (n >= (1LL << 30)) return [NSString stringWithFormat:@"%.2f GB", v / (double)(1LL << 30)];
    if (n >= (1LL << 20)) return [NSString stringWithFormat:@"%.1f MB", v / (double)(1LL << 20)];
    if (n >= (1LL << 10)) return [NSString stringWithFormat:@"%.1f KB", v / (double)(1LL << 10)];
    return [NSString stringWithFormat:@"%lld B", n];
}

static NSString *IDPluralize(NSString *count, NSString *noun) {
    return [count isEqualToString:@"1"] ? [NSString stringWithFormat:@"%@ %@", count, noun]
                                        : [NSString stringWithFormat:@"%@ %@s", count, noun];
}

static NSString *IDPrettyImageName(NSString *name) {
    NSRange fw = [name rangeOfString:@".framework/"];
    if (fw.location != NSNotFound) {
        NSString *prefix = [name substringToIndex:fw.location];
        NSString *base = prefix.lastPathComponent;
        return [base stringByAppendingString:@".framework"];
    }

    NSRange appex = [name rangeOfString:@".appex/"];
    if (appex.location != NSNotFound) {
        NSString *prefix = [name substringToIndex:appex.location];
        NSString *base = prefix.lastPathComponent;
        return [base stringByAppendingString:@".appex"];
    }

    return name;
}

@interface IDHomeViewController ()
@property (nonatomic, strong) NSArray<IDInstalledApp *> *installed;
@property (nonatomic, strong) NSArray<IDInstalledApp *> *installedFiltered;
@property (nonatomic, strong) NSArray<IDSearchResult *> *appStoreHits;
@property (nonatomic, strong) UISearchController *searchController;
@property (nonatomic, copy) NSString *currentTerm;
@property (nonatomic) NSInteger decryptTotal;
@property (nonatomic) NSInteger decryptMain;
@property (nonatomic) NSInteger decryptFrameworks;
@property (nonatomic) NSInteger decryptOther;
@property (nonatomic, strong) UIBarButtonItem *authItem;
@property (nonatomic) BOOL authRefreshing;
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
    UIImage *authImage = [UIImage systemImageNamed:@"arrow.clockwise.circle.fill"];
    self.authItem = [[UIBarButtonItem alloc] initWithImage:authImage
                                                     style:UIBarButtonItemStylePlain
                                                    target:self
                                                    action:@selector(refreshAppleAuth)];
    self.navigationItem.leftBarButtonItem = self.authItem;
    [self setAppleAuthVerified:NO refreshing:NO];
    UIImage *gear = [UIImage systemImageNamed:@"gearshape"];
    UIBarButtonItem *settings = [[UIBarButtonItem alloc] initWithImage:gear
                                                                 style:UIBarButtonItemStylePlain
                                                                target:self
                                                                action:@selector(showSettings)];
    settings.accessibilityLabel = @"Settings";
    UIImage *folder = [UIImage systemImageNamed:@"folder"];
    UIBarButtonItem *openDecrypted = [[UIBarButtonItem alloc] initWithImage:folder
                                                                      style:UIBarButtonItemStylePlain
                                                                     target:self
                                                                     action:@selector(openDecryptedFolder)];
    openDecrypted.accessibilityLabel = @"Open decrypted IPAs in Filza";
    self.navigationItem.rightBarButtonItems = @[settings, openDecrypted];

    UIRefreshControl *rc = [[UIRefreshControl alloc] init];
    [rc addTarget:self action:@selector(reload) forControlEvents:UIControlEventValueChanged];
    self.refreshControl = rc;

    [self reload];
}

- (void)setAppleAuthVerified:(BOOL)verified refreshing:(BOOL)refreshing {
    self.authRefreshing = refreshing;
    self.authItem.enabled = !refreshing;
    self.authItem.tintColor = refreshing
        ? [UIColor systemGrayColor]
        : (verified ? [UIColor systemGreenColor] : [UIColor systemRedColor]);
    self.authItem.accessibilityLabel = verified ? @"Apple ID auth verified" : @"Apple ID auth needed";
}

- (void)refreshAppleAuth {
    if (self.authRefreshing) return;

    IDDecryptProgressViewController *vc = [[IDDecryptProgressViewController alloc]
        initWithTitle:@"Apple ID Auth"];
    [self.navigationController pushViewController:vc animated:YES];
    [vc appendStatus:@"Refreshing Apple ID auth"];
    [self runAppleAuthRefreshWithEmail:nil password:nil authCode:nil vc:vc];
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

- (void)openDecryptedFolder {
    [[NSFileManager defaultManager] createDirectoryAtPath:IDDecryptedOutputDirectory()
                              withIntermediateDirectories:YES
                                               attributes:nil
                                                    error:nil];
    [self openFilzaForPath:IDDecryptedOutputDirectory()];
}

- (void)openFilzaForPath:(NSString *)path {
    if (path.length == 0) return;

    NSString *encoded = [path stringByAddingPercentEncodingWithAllowedCharacters:
        [NSCharacterSet URLPathAllowedCharacterSet]];
    if (![encoded hasSuffix:@"/."]) {
        encoded = [[encoded stringByAppendingString:@"/"] stringByAppendingString:@"."];
    }
    NSURL *url = [NSURL URLWithString:[@"filza://view" stringByAppendingString:encoded ?: path]];
    [UIApplication.sharedApplication openURL:url options:@{} completionHandler:^(BOOL ok) {
        if (ok) return;
        if (![self openFilzaByBundleID:@"com.tigisoftware.FilzaTS"] &&
            ![self openFilzaByBundleID:@"com.tigisoftware.Filza"]) {
            UIAlertController *alert = [UIAlertController alertControllerWithTitle:@"Filza unavailable"
                                                                           message:nil
                                                                    preferredStyle:UIAlertControllerStyleAlert];
            [alert addAction:[UIAlertAction actionWithTitle:@"OK"
                                                      style:UIAlertActionStyleCancel
                                                    handler:nil]];
            [self presentViewController:alert animated:YES completion:nil];
        }
    }];
}

- (BOOL)openFilzaByBundleID:(NSString *)bundleID {
    Class cls = NSClassFromString(@"LSApplicationWorkspace");
    if (!cls) return NO;
    id workspace = ((id (*)(Class, SEL))objc_msgSend)(cls, sel_registerName("defaultWorkspace"));
    if (!workspace) return NO;
    SEL sel = sel_registerName("openApplicationWithBundleID:");
    if (![workspace respondsToSelector:sel]) return NO;
    return ((BOOL (*)(id, SEL, NSString *))objc_msgSend)(workspace, sel, bundleID);
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

- (NSInteger)numberOfSectionsInTableView:(UITableView *)tv {
    return self.currentTerm.length > 0 ? 2 : 1;
}

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
    NSString *bundleID = nil;
    NSInteger trackID = 0;

    if (ip.section == 0) {
        IDInstalledApp *a = self.installedFiltered[ip.row];
        title = a.displayName;
        bundleID = a.bundleID;
        installedDisplay = [NSString stringWithFormat:@"v%@", a.version];

        // Try to find a matching App Store hit for the trackID. Best-effort.
        for (IDSearchResult *r in self.appStoreHits) {
            if ([r.bundleID isEqualToString:a.bundleID]) {
                trackID = r.trackID;
                break;
            }
        }
        if (trackID == 0) {
            [IDITunesSearch lookupBundleID:bundleID country:@"US"
                                completion:^(IDSearchResult *result, NSError *err) {
                [self presentDecryptOptionsForTitle:title
                                   installedDisplay:installedDisplay
                                           bundleID:bundleID
                                            trackID:result.trackID
                                          indexPath:ip];
            }];
            return;
        }
    } else {
        IDSearchResult *r = self.appStoreHits[ip.row];
        title = r.trackName;
        bundleID = r.bundleID;
        trackID = r.trackID;

        // If the app is already installed, surface that option too.
        for (IDInstalledApp *ia in self.installed) {
            if ([ia.bundleID isEqualToString:r.bundleID]) {
                installedDisplay = [NSString stringWithFormat:@"v%@", ia.version];
                break;
            }
        }
    }

    [self presentDecryptOptionsForTitle:title
                       installedDisplay:installedDisplay
                               bundleID:bundleID
                                trackID:trackID
                              indexPath:ip];
}

- (void)presentDecryptOptionsForTitle:(NSString *)title
                      installedDisplay:(NSString *)installedDisplay
                              bundleID:(NSString *)bundleID
                               trackID:(NSInteger)trackID
                             indexPath:(NSIndexPath *)ip {
    [IDDecryptOptionsViewController presentFrom:self
                                          title:title
                               installedDisplay:installedDisplay
                                        trackID:trackID
                              appStoreAvailable:(bundleID.length > 0)
                                     completion:^(IDDecryptOption opt, BOOL cancelled) {
        if (cancelled) return;
        IDLOG(@"picked option=%ld for %@", (long)opt, title);
        if (opt == IDDecryptOptionInstalled) {
            IDInstalledApp *target = nil;
            for (IDInstalledApp *a in self.installed) {
                if ([a.bundleID isEqualToString:bundleID]) { target = a; break; }
            }
            if (target) [self decryptInstalled:target];
        } else if (opt == IDDecryptOptionLatestAppStore) {
            [self latestFromAppStoreBundleID:bundleID
                                     trackID:trackID
                                displayName:title
                           externalVersionID:nil
                                      email:nil
                                   password:nil
                                   authCode:nil];
        } else if (opt == IDDecryptOptionLatestStoreKit) {
            [self downloadAndDecryptTrackID:trackID bundleID:bundleID displayName:title];
        } else if (opt == IDDecryptOptionSelectAppStore) {
            [self presentAppStoreVersionPickerForTitle:title bundleID:bundleID trackID:trackID];
        }
    }];
}

- (void)presentAppStoreVersionPickerForTitle:(NSString *)title
                                    bundleID:(NSString *)bundleID
                                     trackID:(NSInteger)trackID {
    IDAppStoreVersionPickerViewController *vc = [[IDAppStoreVersionPickerViewController alloc]
        initWithTitle:title
             bundleID:bundleID
              trackID:trackID
           completion:^(NSString *externalVersionID) {
        [self latestFromAppStoreBundleID:bundleID
                                 trackID:trackID
                             displayName:title
                       externalVersionID:externalVersionID
                                   email:nil
                                password:nil
                                authCode:nil];
    }];
    [self.navigationController pushViewController:vc animated:YES];
}

- (NSString *)outputIPAForBundle:(NSString *)bundleID version:(NSString *)version {
    NSString *dir = IDDecryptedOutputDirectory();
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
    self.decryptTotal = 0;
    self.decryptMain = 0;
    self.decryptFrameworks = 0;
    self.decryptOther = 0;

    IDDecryptProgressViewController *vc = existing;
    if (!vc) {
        vc = [[IDDecryptProgressViewController alloc] initWithTitle:app.displayName];
        [self.navigationController pushViewController:vc animated:YES];
    }
    [vc appendStatus:[NSString stringWithFormat:@"Decrypting %@ v%@", app.bundleID, app.version]];

    [IDHelperRunner runWithBundleID:app.bundleID
                         bundlePath:app.appBundlePath
                            outIPA:out
                            onEvent:^(NSDictionary *ev) {
        NSString *type = ev[@"event"] ?: @"";
        NSString *phase = ev[@"phase"] ?: @"";
        NSString *name = ev[@"name"] ?: ev[@"src"] ?: @"";
        NSString *pretty = IDPrettyImageName(name);
        if ([type isEqualToString:@"stderr"]) {
            NSString *line = ev[@"line"] ?: @"";
            if (!IDIsExpectedDecryptStderr(line)) {
                [vc appendStatus:[NSString stringWithFormat:@"  %@", line]];
            }
        } else if ([type isEqualToString:@"spawn_chmod"]) {
            return;
        } else if ([type isEqualToString:@"spawn_path"]) {
            return;
        } else if ([type isEqualToString:@"spawn_path_fallback"]) {
            [vc appendStatus:[NSString stringWithFormat:@"  SBS failed on %@, falling back to ptrace",
                              [ev[@"exec"] lastPathComponent] ?: @""]];
        } else if ([type isEqualToString:@"dyld"] && [phase isEqualToString:@"settled"]) {
            return;
        } else if ([type isEqualToString:@"dyld"] && [phase isEqualToString:@"trapped"]) {
            return;
        } else if ([type isEqualToString:@"image"] && [phase isEqualToString:@"done"]) {
            self.decryptTotal++;
            NSString *kind = ev[@"kind"] ?: @"";
            if ([kind isEqualToString:@"main"]) self.decryptMain++;
            else if ([kind isEqualToString:@"framework"]) self.decryptFrameworks++;
            else self.decryptOther++;
            [vc appendStatus:[NSString stringWithFormat:@"  decrypted %@ (%@)",
                              pretty, IDHumanBytes([ev[@"size"] longLongValue])]];
        } else if ([type isEqualToString:@"image"] && [phase isEqualToString:@"failed"]) {
            [vc appendStatus:[NSString stringWithFormat:@"  failed to decrypt %@ (%@)",
                              pretty, ev[@"reason"] ?: @"unknown"]];
        } else if ([type isEqualToString:@"bundle"] && [phase isEqualToString:@"skipped"]) {
            [vc appendStatus:[NSString stringWithFormat:@"  bundle skipped: %@ (%@)",
                              [name lastPathComponent] ?: name, ev[@"reason"] ?: @"unknown"]];
        } else if ([type isEqualToString:@"bundle"] && [phase isEqualToString:@"done"]) {
            NSString *extras = ev[@"extras"] ?: @"0";
            if (![extras isEqualToString:@"0"]) {
                [vc appendStatus:[NSString stringWithFormat:@"  bundle done (%@)",
                                  IDPluralize(extras, @"framework")]];
            }
        } else if ([type isEqualToString:@"pack"] && [phase isEqualToString:@"start"]) {
            [vc appendStatus:[NSString stringWithFormat:@"  packaging IPA -> %@",
                              [ev[@"ipa"] lastPathComponent] ?: @""]];
        } else if ([type isEqualToString:@"pack"] && [phase isEqualToString:@"progress"]) {
            [vc appendStatus:[NSString stringWithFormat:@"  packaging IPA %@%%",
                              ev[@"percent"] ?: @"0"]];
        } else if ([type isEqualToString:@"pack"] && [phase isEqualToString:@"done"]) {
            [vc appendStatus:[NSString stringWithFormat:@"  packaged -> %@", [ev[@"ipa"] lastPathComponent] ?: @""]];
        } else if ([type isEqualToString:@"pack"] && [phase isEqualToString:@"failed"]) {
            [vc appendStatus:[NSString stringWithFormat:@"  pack failed -> %@", [ev[@"ipa"] lastPathComponent] ?: @""]];
        } else if ([type isEqualToString:@"spawn_failed"]) {
            [vc appendStatus:[NSString stringWithFormat:@"  could not spawn %@ (skipped)", [name lastPathComponent] ?: name]];
        }
    }
                         completion:^(int code, NSError *err) {
        NSError *finalErr = err;
        if (finalErr) {
            [vc markCompleteWithOutputIPA:out error:finalErr];
            return;
        }
        NSString *summary = [NSString stringWithFormat:@"decrypted %ld image(s): %ld main, %ld framework",
                             (long)self.decryptTotal, (long)self.decryptMain, (long)self.decryptFrameworks];
        if (self.decryptOther > 0) {
            summary = [summary stringByAppendingFormat:@", %ld other", (long)self.decryptOther];
        }
        [vc appendStatus:[NSString stringWithFormat:@"  %@", summary]];
        [self verifyDecryptedIPA:out progressVC:vc];
    }];
}

- (void)verifyDecryptedIPA:(NSString *)out progressVC:(IDDecryptProgressViewController *)vc {
    __block NSInteger encryptedCount = 0;
    __block NSError *verifyError = nil;

    [IDAppStoreHelperRunner verifyIPA:out
                              onEvent:^(NSDictionary *ev) {
        NSString *type = ev[@"event"] ?: @"";
        NSString *phase = ev[@"phase"] ?: @"";
        if (![type isEqualToString:@"verify"]) {
            return;
        }

        if ([phase isEqualToString:@"begin"]) {
            [vc appendStatus:@"  verify: begin"];
        } else if ([phase isEqualToString:@"scanned"]) {
            encryptedCount = [ev[@"encrypted"] integerValue];
            [vc appendStatus:[NSString stringWithFormat:@"  verify: scanned=%@ encrypted=%@ skipped=%@",
                              ev[@"scanned"] ?: @"0", ev[@"encrypted"] ?: @"0", ev[@"skipped"] ?: @"0"]];
            if (encryptedCount > 0) {
                verifyError = [NSError errorWithDomain:@"IDCryptidVerify" code:1
                                              userInfo:@{NSLocalizedDescriptionKey:
                                                  [NSString stringWithFormat:@"%ld binary(ies) still have cryptid != 0",
                                                   (long)encryptedCount]}];
                [vc appendStatus:[NSString stringWithFormat:@"  %ld binary(ies) still have cryptid != 0",
                                  (long)encryptedCount]];
            }
        } else if ([phase isEqualToString:@"encrypted"]) {
            [vc appendStatus:[NSString stringWithFormat:@"    %@", ev[@"name"] ?: @""]];
        } else if ([phase isEqualToString:@"skipped"]) {
            [vc appendStatus:[NSString stringWithFormat:@"    skipped: %@", ev[@"name"] ?: @""]];
        } else if ([phase isEqualToString:@"done"]) {
            [vc appendStatus:@"  verified cryptid=0"];
        }
    }
                           completion:^(int code, NSError *err) {
        if (verifyError) {
            [vc markCompleteWithOutputIPA:out error:verifyError];
        } else if (err) {
            [vc markCompleteWithOutputIPA:out error:err];
        } else {
            [vc markCompleteWithOutputIPA:out error:nil];
        }
    }];
}

- (void)latestFromAppStoreBundleID:(NSString *)bundleID
                            trackID:(NSInteger)trackID
                       displayName:(NSString *)displayName
                  externalVersionID:(NSString *)externalVersionID
                              email:(NSString *)email
                           password:(NSString *)password
                           authCode:(NSString *)authCode {
    IDDecryptProgressViewController *vc = [[IDDecryptProgressViewController alloc]
        initWithTitle:displayName];
    [self.navigationController pushViewController:vc animated:YES];
    if (externalVersionID.length) {
        [vc appendStatus:[NSString stringWithFormat:@"Installing App Store version %@ for %@", externalVersionID, bundleID]];
    } else {
        [vc appendStatus:[NSString stringWithFormat:@"Installing latest App Store build for %@", bundleID]];
    }
    [self runAppStoreHelperForBundleID:bundleID
                               trackID:trackID
                           displayName:displayName
                      externalVersionID:externalVersionID
                                 email:email
                              password:password
                              authCode:authCode
                                    vc:vc];
}

- (void)runAppStoreHelperForBundleID:(NSString *)bundleID
                             trackID:(NSInteger)trackID
                         displayName:(NSString *)displayName
                    externalVersionID:(NSString *)externalVersionID
                               email:(NSString *)email
                            password:(NSString *)password
                            authCode:(NSString *)authCode
                                  vc:(IDDecryptProgressViewController *)vc {
    __block NSString *installedBundle = nil;
    __block NSString *installedVersion = nil;
    __block NSString *installedPath = nil;
    __block NSString *failureReason = nil;
    __block NSString *failureMessage = nil;
    __block NSString *lastHelperStderr = nil;
    __block NSInteger lastDownloadPercent = -1;

    [IDAppStoreHelperRunner runWithBundleID:bundleID
                                    trackID:trackID
                          externalVersionID:externalVersionID
                                      email:email
                                   password:password
                                   authCode:authCode
                                    onEvent:^(NSDictionary *ev) {
        NSString *phase = ev[@"phase"] ?: @"";
        if ([phase isEqualToString:@"step"]) {
            [vc appendStatus:[NSString stringWithFormat:@"  %@", IDAppStoreStepTitle(ev[@"name"])]];
        } else if ([phase isEqualToString:@"auth-required"]) {
            [vc appendStatus:@"  Apple ID sign-in required"];
        } else if ([phase isEqualToString:@"done"] && [ev[@"name"] isEqualToString:@"authenticated"]) {
            [self setAppleAuthVerified:YES refreshing:NO];
        } else if ([phase isEqualToString:@"auth"]) {
            NSString *name = ev[@"name"] ?: @"";
            if ([name isEqualToString:@"reauth"]) {
                [vc appendStatus:@"  Refreshing Apple ID session"];
            } else if ([name isEqualToString:@"license"]) {
                [vc appendStatus:@"  Acquiring App Store license"];
            } else if ([name isEqualToString:@"retry"]) {
                [vc appendStatus:@"  Retrying download authorization"];
            }
        } else if ([phase isEqualToString:@"app"]) {
            if (externalVersionID.length) {
                [vc appendStatus:[NSString stringWithFormat:@"  Selected external ID: %@", externalVersionID]];
            } else {
                [vc appendStatus:[NSString stringWithFormat:@"  Latest version: %@", ev[@"version"] ?: @"unknown"]];
            }
        } else if ([phase isEqualToString:@"cached"]) {
            [vc appendStatus:[NSString stringWithFormat:@"  Using cached IPA: %@", [ev[@"path"] lastPathComponent] ?: @"IPA"]];
        } else if ([phase isEqualToString:@"download"]) {
            [vc appendStatus:[NSString stringWithFormat:@"  Downloading version %@", ev[@"version"] ?: @""]];
        } else if ([phase isEqualToString:@"download-progress"]) {
            long long cur = [ev[@"current"] longLongValue];
            long long total = [ev[@"total"] longLongValue];
            if (total > 0) {
                NSInteger pct = (NSInteger)((cur * 100) / total);
                if (pct == 100 || pct >= lastDownloadPercent + 10) {
                    lastDownloadPercent = pct;
                    if (pct >= 100) {
                        [vc appendStatus:@"  Download complete"];
                    } else {
                        [vc appendStatus:[NSString stringWithFormat:@"  Download %ld%%", (long)pct]];
                    }
                }
            }
        } else if ([phase isEqualToString:@"downloaded"]) {
            [vc appendStatus:[NSString stringWithFormat:@"  Downloaded %@", [ev[@"path"] lastPathComponent] ?: @"IPA"]];
        } else if ([phase isEqualToString:@"patch"]) {
            if ([ev[@"changed"] isEqualToString:@"1"]) {
                [vc appendStatus:@"  IPA patched for install"];
            } else {
                [vc appendStatus:@"  No install patch needed"];
            }
        } else if ([phase isEqualToString:@"appinst"]) {
            NSString *line = ev[@"line"] ?: @"";
            if (IDShouldShowAppinstLine(line)) {
                [vc appendStatus:[NSString stringWithFormat:@"  %@", IDAppinstDisplayLine(line)]];
            }
        } else if ([phase isEqualToString:@"installed"]) {
            installedBundle = ev[@"bundle"] ?: bundleID;
            installedVersion = ev[@"version"] ?: @"";
            installedPath = ev[@"path"] ?: @"";
            [vc appendStatus:[NSString stringWithFormat:@"  Installed version %@", installedVersion.length ? installedVersion : @"unknown"]];
        } else if ([phase isEqualToString:@"failed"]) {
            failureReason = ev[@"reason"];
            failureMessage = ev[@"message"];
        } else if ([phase isEqualToString:@"stderr"]) {
            NSString *line = ev[@"line"] ?: @"";
            if (!IDIsExpectedAuthStderr(line)) {
                lastHelperStderr = line;
                [vc appendStatus:[NSString stringWithFormat:@"  %@", line]];
            }
        }
    }
                                 completion:^(int code, NSError *err) {
        BOOL needsCredentials = code == 20 ||
            [failureReason isEqualToString:@"auth-required"] ||
            [failureMessage containsString:@"Apple ID sign-in required"];
        BOOL needsCode = code == 21 || [failureReason isEqualToString:@"auth-code-required"];

        if (needsCredentials || needsCode) {
            if (needsCode) {
                [vc appendStatus:@"  Apple ID verification code required"];
            }
            [self promptAppleAuthForBundleID:bundleID
                                     trackID:trackID
                                 displayName:displayName
                           externalVersionID:externalVersionID
                                         code:(needsCode && email.length && password.length)
                                           vc:vc
                                        email:email
                                     password:password];
            return;
        }

        if (err) {
            [self setAppleAuthVerified:NO refreshing:NO];
            if (lastHelperStderr.length && [err.localizedDescription containsString:lastHelperStderr]) {
                err = [NSError errorWithDomain:err.domain code:err.code
                                      userInfo:@{NSLocalizedDescriptionKey:
                                          [NSString stringWithFormat:@"appstore helper exit %d", code]}];
            }
            [vc markCompleteWithOutputIPA:@"" error:err];
            return;
        }

        [self setAppleAuthVerified:YES refreshing:NO];
        if (installedPath.length == 0) {
            NSError *missing = [NSError errorWithDomain:@"IDAppStoreHelperRunner" code:4
                                               userInfo:@{NSLocalizedDescriptionKey:
                                                   @"helper finished without installed bundle path"}];
            [vc markCompleteWithOutputIPA:@"" error:missing];
            return;
        }

        IDInstalledApp *installed = [[IDInstalledApp alloc] init];
        installed.bundleID = installedBundle ?: bundleID;
        installed.displayName = displayName;
        installed.version = installedVersion ?: @"";
        installed.appBundlePath = installedPath;
        self.installed = [IDAppEnumerator installedApps];
        self.installedFiltered = self.installed;
        [self.tableView reloadData];
        [self decryptInstalled:installed reuseVC:vc];
    }];
}

- (void)promptAppleAuthForBundleID:(NSString *)bundleID
                            trackID:(NSInteger)trackID
                        displayName:(NSString *)displayName
                  externalVersionID:(NSString *)externalVersionID
                                code:(BOOL)codeOnly
                                  vc:(IDDecryptProgressViewController *)vc
                               email:(NSString *)email
                            password:(NSString *)password {
    UIAlertController *alert = [UIAlertController
        alertControllerWithTitle:(codeOnly ? @"Apple 2FA Code" : @"Apple ID Sign In")
                         message:nil
                  preferredStyle:UIAlertControllerStyleAlert];

    if (codeOnly) {
        [alert addTextFieldWithConfigurationHandler:^(UITextField *tf) {
            tf.placeholder = @"Code";
            tf.keyboardType = UIKeyboardTypeNumberPad;
        }];
    } else {
        [alert addTextFieldWithConfigurationHandler:^(UITextField *tf) {
            tf.placeholder = @"Email";
            tf.text = email ?: @"";
            tf.keyboardType = UIKeyboardTypeEmailAddress;
            tf.autocapitalizationType = UITextAutocapitalizationTypeNone;
        }];
        [alert addTextFieldWithConfigurationHandler:^(UITextField *tf) {
            tf.placeholder = @"Password";
            tf.secureTextEntry = YES;
            tf.text = password ?: @"";
        }];
    }

    [alert addAction:[UIAlertAction actionWithTitle:@"Cancel"
                                              style:UIAlertActionStyleCancel
                                            handler:^(UIAlertAction *a) {
        NSError *err = [NSError errorWithDomain:@"IDAppStoreAuth" code:1
                                       userInfo:@{NSLocalizedDescriptionKey: @"Apple ID sign-in cancelled"}];
        [vc markCompleteWithOutputIPA:@"" error:err];
    }]];
    [alert addAction:[UIAlertAction actionWithTitle:@"Continue"
                                              style:UIAlertActionStyleDefault
                                            handler:^(UIAlertAction *a) {
        NSString *nextEmail = email;
        NSString *nextPassword = password;
        NSString *nextCode = nil;
        if (codeOnly) {
            nextCode = alert.textFields.firstObject.text ?: @"";
        } else {
            nextEmail = alert.textFields.count > 0 ? alert.textFields[0].text : @"";
            nextPassword = alert.textFields.count > 1 ? alert.textFields[1].text : @"";
        }
        [self runAppStoreHelperForBundleID:bundleID
                                   trackID:trackID
                               displayName:displayName
                          externalVersionID:externalVersionID
                                     email:nextEmail
                                  password:nextPassword
                                  authCode:nextCode
                                        vc:vc];
    }]];

    [self presentViewController:alert animated:YES completion:nil];
}

- (void)runAppleAuthRefreshWithEmail:(NSString *)email
                             password:(NSString *)password
                             authCode:(NSString *)authCode
                                   vc:(IDDecryptProgressViewController *)vc {
    __block NSString *failureReason = nil;
    __block NSString *failureMessage = nil;
    __block NSString *lastHelperStderr = nil;
    [self setAppleAuthVerified:NO refreshing:YES];

    [IDAppStoreHelperRunner refreshAuthWithEmail:email
                                        password:password
                                        authCode:authCode
                                         onEvent:^(NSDictionary *ev) {
        NSString *phase = ev[@"phase"] ?: @"";
        if ([phase isEqualToString:@"step"]) {
            [vc appendStatus:[NSString stringWithFormat:@"  %@", IDAppStoreStepTitle(ev[@"name"])]];
        } else if ([phase isEqualToString:@"auth-required"]) {
            [vc appendStatus:@"  Apple ID sign-in required"];
        } else if ([phase isEqualToString:@"done"] && [ev[@"name"] isEqualToString:@"authenticated"]) {
            [vc appendStatus:@"  Apple ID auth verified"];
        } else if ([phase isEqualToString:@"failed"]) {
            failureReason = ev[@"reason"];
            failureMessage = ev[@"message"];
        } else if ([phase isEqualToString:@"stderr"]) {
            NSString *line = ev[@"line"] ?: @"";
            if (!IDIsExpectedAuthStderr(line)) {
                lastHelperStderr = line;
                [vc appendStatus:[NSString stringWithFormat:@"  %@", line]];
            }
        }
    }
                                      completion:^(int code, NSError *err) {
        BOOL needsCredentials = code == 20 ||
            [failureReason isEqualToString:@"auth-required"] ||
            [failureMessage containsString:@"Apple ID sign-in required"];
        BOOL needsCode = code == 21 || [failureReason isEqualToString:@"auth-code-required"];

        if (needsCredentials || needsCode) {
            if (needsCode) {
                [vc appendStatus:@"  Apple ID verification code required"];
            }
            [self setAppleAuthVerified:NO refreshing:NO];
            [self promptAppleAuthForRefreshCode:(needsCode && email.length && password.length)
                                             vc:vc
                                          email:email
                                       password:password];
            return;
        }

        if (err) {
            if (lastHelperStderr.length && [err.localizedDescription containsString:lastHelperStderr]) {
                err = [NSError errorWithDomain:err.domain code:err.code
                                      userInfo:@{NSLocalizedDescriptionKey:
                                          [NSString stringWithFormat:@"appstore helper exit %d", code]}];
            }
            [self setAppleAuthVerified:NO refreshing:NO];
            [vc markCompleteWithMessage:nil error:err];
            return;
        }

        [self setAppleAuthVerified:YES refreshing:NO];
        [vc markCompleteWithMessage:@"Apple ID auth verified" error:nil];
    }];
}

- (void)promptAppleAuthForRefreshCode:(BOOL)codeOnly
                                   vc:(IDDecryptProgressViewController *)vc
                                email:(NSString *)email
                             password:(NSString *)password {
    UIAlertController *alert = [UIAlertController
        alertControllerWithTitle:(codeOnly ? @"Apple 2FA Code" : @"Apple ID Sign In")
                         message:nil
                  preferredStyle:UIAlertControllerStyleAlert];

    if (codeOnly) {
        [alert addTextFieldWithConfigurationHandler:^(UITextField *tf) {
            tf.placeholder = @"Code";
            tf.keyboardType = UIKeyboardTypeNumberPad;
        }];
    } else {
        [alert addTextFieldWithConfigurationHandler:^(UITextField *tf) {
            tf.placeholder = @"Email";
            tf.text = email ?: @"";
            tf.keyboardType = UIKeyboardTypeEmailAddress;
            tf.autocapitalizationType = UITextAutocapitalizationTypeNone;
        }];
        [alert addTextFieldWithConfigurationHandler:^(UITextField *tf) {
            tf.placeholder = @"Password";
            tf.secureTextEntry = YES;
            tf.text = password ?: @"";
        }];
    }

    [alert addAction:[UIAlertAction actionWithTitle:@"Cancel"
                                              style:UIAlertActionStyleCancel
                                            handler:^(UIAlertAction *a) {
        [self setAppleAuthVerified:NO refreshing:NO];
        NSError *err = [NSError errorWithDomain:@"IDAppStoreAuth" code:1
                                       userInfo:@{NSLocalizedDescriptionKey: @"Apple ID sign-in cancelled"}];
        [vc markCompleteWithMessage:nil error:err];
    }]];
    [alert addAction:[UIAlertAction actionWithTitle:@"Continue"
                                              style:UIAlertActionStyleDefault
                                            handler:^(UIAlertAction *a) {
        NSString *nextEmail = email;
        NSString *nextPassword = password;
        NSString *nextCode = nil;
        if (codeOnly) {
            nextCode = alert.textFields.firstObject.text ?: @"";
        } else {
            nextEmail = alert.textFields.count > 0 ? alert.textFields[0].text : @"";
            nextPassword = alert.textFields.count > 1 ? alert.textFields[1].text : @"";
        }
        [self runAppleAuthRefreshWithEmail:nextEmail
                                  password:nextPassword
                                  authCode:nextCode
                                        vc:vc];
    }]];

    [self presentViewController:alert animated:YES completion:nil];
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

    NSString *nonce = [IDAutoalertArmer armForBundleID:bundleID trackID:trackID];

    [vc appendStatus:@"waiting for App Store confirmation..."];
    [IDStoreKitDownloader downloadTrackID:trackID
                                    nonce:nonce
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

- (BOOL)installedAppReadyForDecrypt:(IDInstalledApp *)app {
    if (!app || app.bundleID.length == 0 || app.version.length == 0 || app.appBundlePath.length == 0) {
        return NO;
    }

    NSDictionary *plist = [NSDictionary dictionaryWithContentsOfFile:
                           [app.appBundlePath stringByAppendingPathComponent:@"Info.plist"]];
    NSString *executable = plist[@"CFBundleExecutable"];
    if (executable.length == 0) {
        return NO;
    }

    NSString *binary = [app.appBundlePath stringByAppendingPathComponent:executable];
    BOOL isDir = NO;
    if (![[NSFileManager defaultManager] fileExistsAtPath:binary isDirectory:&isDir] || isDir) {
        return NO;
    }

    NSDictionary *attrs = [[NSFileManager defaultManager] attributesOfItemAtPath:binary error:nil];
    return [attrs fileSize] > 0;
}

- (void)pollInstallChange:(IDDecryptProgressViewController *)vc
                 bundleID:(NSString *)bundleID
               beforePath:(NSString *)beforePath
            beforeVersion:(NSString *)beforeVersion
              deadlineSec:(NSTimeInterval)deadlineSec
               completion:(void (^)(IDInstalledApp *installed))completion {
    NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:deadlineSec];
    __block NSString *readyPath = nil;
    __block NSString *readyVersion = nil;
    __block NSDate *readySince = nil;
    __block NSDate *unchangedSince = nil;

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
        BOOL ready = [self installedAppReadyForDecrypt:current];
        if (ready) {
            BOOL sameReady = [current.appBundlePath isEqualToString:readyPath ?: @""] &&
                             [current.version isEqualToString:readyVersion ?: @""];
            if (!sameReady) {
                readyPath = current.appBundlePath;
                readyVersion = current.version;
                readySince = [NSDate date];
            }
            BOOL changed = ![current.appBundlePath isEqualToString:beforePath ?: @""] ||
                           ![current.version isEqualToString:beforeVersion ?: @""];
            if (changed && [[NSDate date] timeIntervalSinceDate:readySince] > 3) {
                dispatch_async(dispatch_get_main_queue(), ^{ completion(current); });
                tick = nil;
                return;
            }
        } else {
            readyPath = nil;
            readyVersion = nil;
            readySince = nil;
        }

        if (current && ready) {
            if (!unchangedSince) unchangedSince = [NSDate date];
            if ([[NSDate date] timeIntervalSinceDate:unchangedSince] > 15) {
                // Already at latest.
                dispatch_async(dispatch_get_main_queue(), ^{ completion(current); });
                tick = nil;
                return;
            }
        } else {
            unchangedSince = nil;
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
