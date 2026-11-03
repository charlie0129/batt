#import <Foundation/Foundation.h>
#include <sys/sysctl.h>
#include <libproc.h>
#include <signal.h>
#include <unistd.h>
#include "powerui.h"

static CFStringRef const policyDomain = CFSTR("com.apple.powerd.charging");
static NSString *const policyReason = @"battChargeLimit";

// powerd's archive schema, checked on macOS 27. Unknown fields are rejected
// before rewriting so another policy cannot silently lose new settings.
@interface BattChargePolicy : NSObject <NSSecureCoding>
@property NSInteger limit, owner;
@property BOOL drain, noChargeToFull, endOfCharge, terminated;
@property(copy) NSString *reason;
@property(retain) NSUUID *token;
@end

@implementation BattChargePolicy
+ (BOOL)supportsSecureCoding { return YES; }
- (instancetype)initWithCoder:(NSCoder *)coder {
    if ((self = [super init])) {
        self.limit = [coder decodeIntegerForKey:@"soclimit"];
        self.owner = [coder decodeIntegerForKey:@"owner"];
        self.reason = [coder decodeObjectOfClass:NSString.class forKey:@"reason"];
        self.token = [coder decodeObjectOfClass:NSUUID.class forKey:@"token"];
        self.drain = [coder decodeBoolForKey:@"drain"];
        self.noChargeToFull = [coder decodeBoolForKey:@"noChargeToFull"];
        self.endOfCharge = [coder decodeBoolForKey:@"isEndOfCharge"];
        self.terminated = [coder decodeBoolForKey:@"terminated"];
    }
    return self;
}
- (void)encodeWithCoder:(NSCoder *)coder {
    [coder encodeInteger:self.limit forKey:@"soclimit"];
    [coder encodeInteger:self.owner forKey:@"owner"];
    [coder encodeObject:self.reason forKey:@"reason"];
    [coder encodeObject:self.token forKey:@"token"];
    [coder encodeBool:self.drain forKey:@"drain"];
    [coder encodeBool:self.noChargeToFull forKey:@"noChargeToFull"];
    [coder encodeBool:self.endOfCharge forKey:@"isEndOfCharge"];
    [coder encodeBool:self.terminated forKey:@"terminated"];
}
- (void)dealloc { [_reason release]; [_token release]; [super dealloc]; }
@end

static int policyError(char **err, NSString *message) {
    if (err) *err = strdup((message ?: @"unknown native charge-policy error").UTF8String);
    return -1;
}

static NSString *bootSession(char **err) {
    char value[128]; size_t size = sizeof(value);
    if (sysctlbyname("kern.bootsessionuuid", value, &size, NULL, 0) != 0 || size == 0 || size > sizeof(value) || value[size-1] != 0) {
        policyError(err, @"cannot read the boot session for the native charge policy");
        return nil;
    }
    return [NSString stringWithUTF8String:value];
}

static NSDictionary *policyPreferences(char **err) {
    if (!batt_powerui_custom_limits() || geteuid() != 0) {
        policyError(err, @"no-drain native charging requires the root batt daemon on macOS 27");
        return nil;
    }
    if (!CFPreferencesSynchronize(policyDomain, kCFPreferencesAnyUser, kCFPreferencesCurrentHost)) {
        policyError(err, @"cannot synchronize native charge policies"); return nil;
    }
    return [(NSDictionary *)CFPreferencesCopyMultiple(NULL, policyDomain, kCFPreferencesAnyUser, kCFPreferencesCurrentHost) autorelease] ?: @{};
}

static NSMutableArray *decodePolicies(NSDictionary *preferences, NSString *boot, char **err) {
    id storedBoot = preferences[@"bootSessionUUID"];
    if (storedBoot && ![storedBoot isKindOfClass:NSString.class]) {
        policyError(err, @"unrecognized native charge-policy boot session"); return nil;
    }
    // powerd discards registrations from an earlier boot. Never resurrect them.
    if (![storedBoot isEqual:boot] || !preferences[@"policies"]) return [NSMutableArray array];
    NSData *data = preferences[@"policies"];
    if (![data isKindOfClass:NSData.class] || data.length > 1024 * 1024) {
        policyError(err, @"unrecognized native charge-policy archive"); return nil;
    }
    NSError *error = nil;
    NSDictionary *archive = [NSPropertyListSerialization propertyListWithData:data options:0 format:NULL error:&error];
    NSSet *fields = [NSSet setWithArray:@[@"$class", @"soclimit", @"owner", @"reason", @"token", @"drain", @"noChargeToFull", @"isEndOfCharge", @"terminated"]];
    if (![archive isKindOfClass:NSDictionary.class] || ![archive[@"$objects"] isKindOfClass:NSArray.class]) {
        policyError(err, @"unrecognized native charge-policy archive"); return nil;
    }
    for (id object in archive[@"$objects"]) {
        if ([object isKindOfClass:NSDictionary.class] && object[@"soclimit"]) {
            if (![fields isEqual:[NSSet setWithArray:[object allKeys]]]) {
                policyError(err, @"unrecognized charge-policy fields; refusing to rewrite other policies"); return nil;
            }
            for (NSString *key in @[@"soclimit", @"owner"]) {
                id value = object[key];
                if (![value isKindOfClass:NSNumber.class] || [value doubleValue] != [value integerValue]) {
                    policyError(err, @"invalid charge-policy number; refusing to rewrite it"); return nil;
                }
            }
            for (NSString *key in @[@"drain", @"noChargeToFull", @"isEndOfCharge", @"terminated"]) {
                if (![object[key] isEqual:@NO] && ![object[key] isEqual:@YES]) {
                    policyError(err, @"invalid charge-policy flag; refusing to rewrite it"); return nil;
                }
            }
        }
    }
    NSKeyedUnarchiver *decoder = [[[NSKeyedUnarchiver alloc] initForReadingFromData:data error:&error] autorelease];
    if (!decoder) { policyError(err, @"cannot decode native charge policies"); return nil; }
    decoder.requiresSecureCoding = YES;
    decoder.decodingFailurePolicy = NSDecodingFailurePolicySetErrorAndReturn;
    [decoder setClass:BattChargePolicy.class forClassName:@"ChargeCtrlPolicy"];
    NSSet *classes = [NSSet setWithArray:@[NSArray.class, NSMutableArray.class, BattChargePolicy.class, NSString.class, NSUUID.class]];
    NSArray *policies = [decoder decodeObjectOfClasses:classes forKey:NSKeyedArchiveRootObjectKey];
    [decoder finishDecoding];
    if (decoder.error || ![policies isKindOfClass:NSArray.class]) {
        policyError(err, @"cannot decode native charge policies"); return nil;
    }
    for (BattChargePolicy *policy in policies) {
        if (![policy isKindOfClass:BattChargePolicy.class] || policy.limit < 1 || policy.limit > 100 ||
            !policy.reason.length || !policy.token) {
            policyError(err, @"invalid native charge policy; refusing to rewrite it"); return nil;
        }
    }
    return [[policies mutableCopy] autorelease];
}

static NSData *encodePolicies(NSMutableArray *policies) {
    NSKeyedArchiver *encoder = [[[NSKeyedArchiver alloc] initRequiringSecureCoding:YES] autorelease];
    [encoder setClassName:@"ChargeCtrlPolicy" forClass:BattChargePolicy.class];
    // powerd appends policies later; an immutable NSArray crashes that path.
    [encoder encodeObject:policies forKey:NSKeyedArchiveRootObjectKey];
    [encoder finishEncoding];
    return encoder.encodedData;
}

// Wait for a different process: SIGHUP restarts powerd, and launchd can delay
// its replacement by ten seconds after rapid target changes.
static pid_t powerdPID(void) {
    int capacity = proc_listallpids(NULL, 0) + 32;
    if (capacity <= 32) return -1;
    pid_t *pids = calloc((size_t)capacity, sizeof(pid_t));
    if (!pids) return -1;
    int count = proc_listallpids(pids, capacity * (int)sizeof(pid_t));
    pid_t found = count < 0 || count >= capacity ? -1 : 0;
    for (int i = 0; i < count && found == 0; i++) {
        char path[PROC_PIDPATHINFO_MAXSIZE];
        if (proc_pidpath(pids[i], path, sizeof(path)) > 0 &&
            strcmp(path, "/System/Library/CoreServices/powerd.bundle/powerd") == 0) found = pids[i];
    }
    free(pids);
    return found;
}

static int reloadPolicies(char **err) {
    pid_t previous = powerdPID();
    if (previous < 0) return policyError(err, @"cannot identify powerd to reload the native charge policy");
    if (previous > 0 && kill(previous, SIGHUP) != 0 && errno != ESRCH) {
        return policyError(err, @"could not signal powerd to reload the native charge policy");
    }
    for (int attempt = 0; attempt < 150; attempt++) {
        usleep(100000);
        pid_t current = powerdPID();
        if (current > 0 && current != previous) return 0;
    }
    return policyError(err, @"powerd has not restarted after the charge-policy reload; batt will retry");
}

int batt_powerui_get_policy(int *limit, int *enabled, char **err) {
    @autoreleasepool { @try {
        NSDictionary *preferences = policyPreferences(err);
        NSString *boot = preferences ? bootSession(err) : nil;
        NSMutableArray *policies = boot ? decodePolicies(preferences, boot, err) : nil;
        if (!policies) return -1;
        *limit = 100; *enabled = 0;
        for (BattChargePolicy *policy in policies) {
            if ([policy.reason isEqual:policyReason]) {
                if (*enabled || policy.terminated || policy.drain || policy.noChargeToFull || !policy.endOfCharge) {
                    *limit = 0; *enabled = 1; break;
                }
                *limit = (int)policy.limit; *enabled = 1;
            }
        }
        return 0;
    } @catch (NSException *exception) { return policyError(err, exception.reason); } }
}

int batt_powerui_set_policy(int limit, char **err) {
    @autoreleasepool { @try {
        if (limit < 10 || limit > 100) return policyError(err, @"native charge ceiling must be 10 through 100");
        NSDictionary *preferences = policyPreferences(err);
        NSString *boot = preferences ? bootSession(err) : nil;
        NSMutableArray *policies = boot ? decodePolicies(preferences, boot, err) : nil;
        if (!policies) return -1;
        // Release the draining PowerUI policy before installing our own ceiling.
        // This does not clear Apple's full-charge override or touch the adapter.
        if (limit < 100) {
            if (batt_powerui_disable(err) != 0) return -1;
            // The agent may have updated this domain during its release.
            preferences = policyPreferences(err);
            policies = preferences ? decodePolicies(preferences, boot, err) : nil;
            if (!policies) return -1;
        }
        NSIndexSet *ours = [policies indexesOfObjectsPassingTest:^BOOL(BattChargePolicy *policy, NSUInteger index, BOOL *stop) {
            (void)index; (void)stop;
            return [policy.reason isEqual:policyReason];
        }];
        [policies removeObjectsAtIndexes:ours];
        if (limit < 100) {
            BattChargePolicy *policy = [[[BattChargePolicy alloc] init] autorelease];
            policy.limit = limit; policy.owner = getpid(); policy.reason = policyReason;
            policy.token = [NSUUID UUID]; policy.endOfCharge = YES;
            policy.drain = NO; policy.noChargeToFull = NO; policy.terminated = NO;
            [policies addObject:policy];
        }
        NSData *data = encodePolicies(policies);
        // shortcut: another writer can race after this check; replace this with
        // powerd IPC when available, since CFPreferences has no compare-and-swap.
        NSDictionary *current = policyPreferences(err);
        if (!current) return -1;
        if (![current isEqual:preferences]) {
            return policyError(err, @"native charge policies changed before saving; batt will retry without overwriting them");
        }
        CFPreferencesSetMultiple((CFDictionaryRef)@{@"bootSessionUUID":boot, @"policies":data}, NULL,
                                 policyDomain, kCFPreferencesAnyUser, kCFPreferencesCurrentHost);
        if (!CFPreferencesSynchronize(policyDomain, kCFPreferencesAnyUser, kCFPreferencesCurrentHost)) {
            return policyError(err, @"could not save the native no-drain policy; batt will retry");
        }
        current = policyPreferences(err);
        if (!current) return -1;
        if (![current[@"bootSessionUUID"] isEqual:boot] || ![current[@"policies"] isEqual:data]) {
            return policyError(err, @"native charge policies changed while saving; batt will retry without reloading the stale policy");
        }
        return reloadPolicies(err);
    } @catch (NSException *exception) { return policyError(err, exception.reason); } }
}
