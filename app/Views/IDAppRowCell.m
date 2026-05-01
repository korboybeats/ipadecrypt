#import "IDAppRowCell.h"

@implementation IDAppRowCell

+ (NSString *)reuseID { return @"IDAppRowCell"; }

- (instancetype)initWithStyle:(UITableViewCellStyle)style
              reuseIdentifier:(NSString *)reuse {
    self = [super initWithStyle:UITableViewCellStyleSubtitle reuseIdentifier:reuse];
    if (self) {
        self.accessoryType = UITableViewCellAccessoryDisclosureIndicator;
    }
    return self;
}

- (void)configureWithTitle:(NSString *)title
                  subtitle:(NSString *)subtitle
                   trailing:(NSString *)trailing {
    self.textLabel.text = title;
    self.detailTextLabel.text = subtitle;

    if (trailing.length > 0) {
        UILabel *t = [[UILabel alloc] init];
        t.text = trailing;
        t.font = [UIFont systemFontOfSize:13];
        t.textColor = [UIColor secondaryLabelColor];
        [t sizeToFit];
        self.accessoryView = t;
    } else {
        self.accessoryView = nil;
        self.accessoryType = UITableViewCellAccessoryDisclosureIndicator;
    }
}

@end
