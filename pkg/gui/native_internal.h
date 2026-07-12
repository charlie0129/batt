#import <Cocoa/Cocoa.h>

#include "native.h"

@interface BattMenuController : NSObject <NSMenuDelegate>
@property(nonatomic, assign) uintptr_t handle;
@property(nonatomic, retain) NSStatusItem *statusItem;
@property(nonatomic, retain) NSMenu *menu;
@property(nonatomic, retain) NSMutableDictionary<NSNumber *, NSMenuItem *> *items;
@property(nonatomic, retain) NSTimer *timer;

- (instancetype)initWithHandle:(uintptr_t)handle version:(NSString *)version;
- (NSMenuItem *)item:(BattMenuItem)item;
- (void)rememberItem:(NSMenuItem *)item as:(BattMenuItem)identifier;
- (void)menuAction:(NSMenuItem *)sender;
- (void)noop:(NSMenuItem *)sender;
- (void)setStatusIconInstalled:(BOOL)installed
                       capable:(BOOL)capable
                  needsUpgrade:(BOOL)needsUpgrade;
- (void)setPowerItem:(BattMenuItem)item label:(NSString *)label value:(double)value;
@end

void BattBuildMenu(BattMenuController *controller, NSString *version);
void BattApplyTooltips(BattMenuController *controller);
