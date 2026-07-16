#import "IDAppStoreVersionPickerViewController.h"
#import "IDAppStoreHelperRunner.h"
#import "IDAppStoreVersion.h"
#import "Logging.h"

@interface IDAppStoreVersionPickerViewController ()
@property (nonatomic, copy) NSString *displayTitle;
@property (nonatomic, copy) NSString *bundleID;
@property (nonatomic) NSInteger trackID;
@property (nonatomic, copy) void (^completion)(NSString *externalVersionID);
@property (nonatomic, strong) NSMutableArray<IDAppStoreVersion *> *versions;
@property (nonatomic, strong) NSMutableDictionary<NSString *, IDAppStoreVersion *> *versionsByID;
@property (nonatomic, copy) NSString *email;
@property (nonatomic, copy) NSString *password;
@property (nonatomic, copy) NSString *statusText;
@property (nonatomic) BOOL loading;
@property (nonatomic) BOOL loaded;
@property (nonatomic, strong) NSMutableSet<NSString *> *fetchingMetadata;
@end

@implementation IDAppStoreVersionPickerViewController

- (instancetype)initWithTitle:(NSString *)title
                      bundleID:(NSString *)bundleID
                       trackID:(NSInteger)trackID
                    completion:(void (^)(NSString *externalVersionID))completion {
    self = [super initWithStyle:UITableViewStyleInsetGrouped];
    if (self) {
        _displayTitle = [title copy];
        _bundleID = [bundleID copy];
        _trackID = trackID;
        _completion = [completion copy];
        _versions = [NSMutableArray array];
        _versionsByID = [NSMutableDictionary dictionary];
        _fetchingMetadata = [NSMutableSet set];
        _statusText = @"Loading versions...";
        self.title = @"App Store Versions";
    }
    return self;
}

- (void)viewDidLoad {
    [super viewDidLoad];
    self.navigationItem.largeTitleDisplayMode = UINavigationItemLargeTitleDisplayModeNever;
    self.navigationItem.rightBarButtonItem = [[UIBarButtonItem alloc]
        initWithBarButtonSystemItem:UIBarButtonSystemItemRefresh
                             target:self
                             action:@selector(refreshVersions)];
    [self loadVersionsWithEmail:nil password:nil authCode:nil];
}

- (void)refreshVersions {
    [self loadVersionsWithEmail:self.email password:self.password authCode:nil];
}

- (void)loadVersionsWithEmail:(NSString *)email
                      password:(NSString *)password
                      authCode:(NSString *)authCode {
    if (self.loading) return;
    self.loading = YES;
    self.loaded = NO;
    self.statusText = @"Loading versions...";
    [self.versions removeAllObjects];
    [self.versionsByID removeAllObjects];
    [self.tableView reloadData];

    __weak typeof(self) weakSelf = self;
    [IDAppStoreHelperRunner listVersionsWithBundleID:self.bundleID
                                             trackID:self.trackID
                                               email:email
                                            password:password
                                            authCode:authCode
                                             onEvent:^(NSDictionary *ev) {
        [weakSelf handleEvent:ev];
    }
                                          completion:^(int code, NSError *err) {
        __strong typeof(weakSelf) self = weakSelf;
        if (!self) return;
        self.loading = NO;

        BOOL needsCredentials = code == 20;
        BOOL needsCode = code == 21;
        if (needsCredentials || needsCode) {
            [self promptAppleAuthCodeOnly:needsCode];
            return;
        }

        if (err) {
            self.statusText = err.localizedDescription ?: @"Failed to load versions";
        } else {
            self.loaded = YES;
            self.statusText = self.versions.count ? @"Tap a version to decrypt." : @"No versions found.";
        }
        [self.tableView reloadData];
    }];
}

- (void)handleEvent:(NSDictionary *)ev {
    NSString *phase = ev[@"phase"] ?: @"";
    if ([phase isEqualToString:@"step"]) {
        NSString *name = ev[@"name"] ?: @"";
        if ([name isEqualToString:@"lookup-track"] || [name isEqualToString:@"lookup-bundle"]) {
            self.statusText = @"Finding app...";
        } else if ([name isEqualToString:@"list-versions"]) {
            self.statusText = @"Loading version list...";
        }
        [self.tableView reloadData];
    } else if ([phase isEqualToString:@"auth-required"]) {
        self.statusText = @"Apple ID sign-in required.";
        [self.tableView reloadData];
    } else if ([phase isEqualToString:@"auth"]) {
        self.statusText = @"Refreshing Apple ID session...";
        [self.tableView reloadData];
    } else if ([phase isEqualToString:@"version-list"]) {
        self.statusText = [NSString stringWithFormat:@"%@ versions", ev[@"count"] ?: @"0"];
        [self.tableView reloadData];
    } else if ([phase isEqualToString:@"version-row"]) {
        [self upsertVersionFromEvent:ev];
    } else if ([phase isEqualToString:@"version-list-done"]) {
        self.statusText = self.versions.count ? @"Tap a version to decrypt." : @"No versions found.";
        [self.tableView reloadData];
    } else if ([phase isEqualToString:@"failed"]) {
        self.statusText = ev[@"message"] ?: @"Failed to load versions";
        [self.tableView reloadData];
    }
}

- (void)upsertVersionFromEvent:(NSDictionary *)ev {
    NSString *external = ev[@"external"] ?: @"";
    if (!external.length) return;

    IDAppStoreVersion *v = self.versionsByID[external];
    if (!v) {
        v = [[IDAppStoreVersion alloc] init];
        v.externalVersionID = external;
        v.index = [ev[@"index"] integerValue];
        self.versionsByID[external] = v;
        [self.versions addObject:v];
        [self.versions sortUsingComparator:^NSComparisonResult(IDAppStoreVersion *a, IDAppStoreVersion *b) {
            if (a.index < b.index) return NSOrderedAscending;
            if (a.index > b.index) return NSOrderedDescending;
            return NSOrderedSame;
        }];
    }

    if (ev[@"latest"]) {
        v.latest = [ev[@"latest"] isEqualToString:@"1"];
    }
    v.status = ev[@"status"] ?: v.status;
    v.displayVersion = ev[@"version"] ?: v.displayVersion;
    v.bundleVersion = ev[@"build"] ?: v.bundleVersion;
    v.devices = ev[@"devices"] ?: v.devices;
    v.message = ev[@"message"] ?: v.message;

    if (![v.status isEqualToString:@"unfetched"] || self.versions.count == 1 || self.versions.count % 50 == 0) {
        [self.tableView reloadData];
    }
}

- (void)fetchMetadataForVersion:(IDAppStoreVersion *)version {
    NSString *external = version.externalVersionID;
    if (!external.length || [self.fetchingMetadata containsObject:external]) return;

    [self.fetchingMetadata addObject:external];
    version.status = @"pending";
    self.statusText = [NSString stringWithFormat:@"Fetching %@...", external];
    [self.tableView reloadData];

    __weak typeof(self) weakSelf = self;
    [IDAppStoreHelperRunner fetchVersionMetadataWithBundleID:self.bundleID
                                                     trackID:self.trackID
                                           externalVersionID:external
                                                       email:self.email
                                                    password:self.password
                                                    authCode:nil
                                                     onEvent:^(NSDictionary *ev) {
        [weakSelf handleEvent:ev];
    }
                                                  completion:^(int code, NSError *err) {
        __strong typeof(weakSelf) self = weakSelf;
        if (!self) return;
        [self.fetchingMetadata removeObject:external];

        BOOL needsCredentials = code == 20;
        BOOL needsCode = code == 21;
        if (needsCredentials || needsCode) {
            [self promptAppleAuthCodeOnly:needsCode];
            return;
        }

        IDAppStoreVersion *current = self.versionsByID[external];
        if (err) {
            current.status = @"error";
            current.message = err.localizedDescription ?: @"Metadata fetch failed";
            self.statusText = current.message;
        } else {
            current.status = @"fetched";
            self.statusText = @"Tap the version again to decrypt.";
        }
        [self.tableView reloadData];
    }];
}

- (NSInteger)numberOfSectionsInTableView:(UITableView *)tableView {
    return 1;
}

- (NSInteger)tableView:(UITableView *)tableView numberOfRowsInSection:(NSInteger)section {
    return self.versions.count;
}

- (NSString *)tableView:(UITableView *)tableView titleForHeaderInSection:(NSInteger)section {
    return self.displayTitle.length ? self.displayTitle : self.bundleID;
}

- (NSString *)tableView:(UITableView *)tableView titleForFooterInSection:(NSInteger)section {
    return self.statusText;
}

- (UITableViewCell *)tableView:(UITableView *)tableView cellForRowAtIndexPath:(NSIndexPath *)indexPath {
    UITableViewCell *cell = [tableView dequeueReusableCellWithIdentifier:@"VersionCell"];
    if (!cell) {
        cell = [[UITableViewCell alloc] initWithStyle:UITableViewCellStyleSubtitle reuseIdentifier:@"VersionCell"];
    }
    IDAppStoreVersion *v = self.versions[indexPath.row];

    NSString *title = v.displayVersion.length ? v.displayVersion : v.externalVersionID;
    if (v.latest) {
        title = [title stringByAppendingString:@" (latest)"];
    }
    cell.textLabel.text = title;

    NSMutableArray<NSString *> *parts = [NSMutableArray array];
    [parts addObject:[NSString stringWithFormat:@"external ID %@", v.externalVersionID ?: @""]];
    if ([v.status isEqualToString:@"pending"]) {
        [parts addObject:@"fetching details"];
    } else if ([v.status isEqualToString:@"unfetched"]) {
        [parts addObject:@"tap to fetch details"];
    }
    if (v.bundleVersion.length) {
        [parts addObject:[NSString stringWithFormat:@"build %@", v.bundleVersion]];
    }
    if (v.devices.length) {
        [parts addObject:[NSString stringWithFormat:@"devices %@", v.devices]];
    }
    if ([v.status isEqualToString:@"error"] && v.message.length) {
        [parts addObject:v.message];
    }
    cell.detailTextLabel.text = [parts componentsJoinedByString:@" - "];
    cell.accessoryType = UITableViewCellAccessoryDisclosureIndicator;
    return cell;
}

- (void)tableView:(UITableView *)tableView didSelectRowAtIndexPath:(NSIndexPath *)indexPath {
    [tableView deselectRowAtIndexPath:indexPath animated:YES];
    IDAppStoreVersion *v = self.versions[indexPath.row];
    if (!v.externalVersionID.length) return;
    if (![v.status isEqualToString:@"fetched"]) {
        [self fetchMetadataForVersion:v];
        return;
    }

    UIAlertController *alert = [UIAlertController
        alertControllerWithTitle:(v.displayVersion.length ? v.displayVersion : v.externalVersionID)
                         message:v.externalVersionID
                  preferredStyle:UIAlertControllerStyleActionSheet];
    [alert addAction:[UIAlertAction actionWithTitle:@"Decrypt this version"
                                              style:UIAlertActionStyleDefault
                                            handler:^(UIAlertAction *a) {
        if (self.completion) {
            NSString *external = v.externalVersionID;
            [self.navigationController popViewControllerAnimated:NO];
            self.completion(external);
        }
    }]];
    [alert addAction:[UIAlertAction actionWithTitle:@"Cancel"
                                              style:UIAlertActionStyleCancel
                                            handler:nil]];
    [self presentViewController:alert animated:YES completion:nil];
}

- (void)promptAppleAuthCodeOnly:(BOOL)codeOnly {
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
            tf.text = self.email ?: @"";
            tf.keyboardType = UIKeyboardTypeEmailAddress;
            tf.autocapitalizationType = UITextAutocapitalizationTypeNone;
        }];
        [alert addTextFieldWithConfigurationHandler:^(UITextField *tf) {
            tf.placeholder = @"Password";
            tf.secureTextEntry = YES;
            tf.text = self.password ?: @"";
        }];
    }

    [alert addAction:[UIAlertAction actionWithTitle:@"Cancel"
                                              style:UIAlertActionStyleCancel
                                            handler:^(UIAlertAction *a) {
        self.statusText = @"Apple ID sign-in cancelled.";
        [self.tableView reloadData];
    }]];
    [alert addAction:[UIAlertAction actionWithTitle:@"Continue"
                                              style:UIAlertActionStyleDefault
                                            handler:^(UIAlertAction *a) {
        NSString *nextEmail = self.email;
        NSString *nextPassword = self.password;
        NSString *nextCode = nil;
        if (codeOnly) {
            nextCode = alert.textFields.firstObject.text ?: @"";
        } else {
            nextEmail = alert.textFields.count > 0 ? alert.textFields[0].text : @"";
            nextPassword = alert.textFields.count > 1 ? alert.textFields[1].text : @"";
            self.email = nextEmail;
            self.password = nextPassword;
        }
        [self loadVersionsWithEmail:nextEmail password:nextPassword authCode:nextCode];
    }]];

    [self presentViewController:alert animated:YES completion:nil];
}

@end
