// Production policy code, with every privileged operation replaced by a fake.
#import <Foundation/Foundation.h>
#include <sys/sysctl.h>
#include <libproc.h>
#include <signal.h>
#include <unistd.h>
#include <assert.h>
#include "../powerui.h"

static NSMutableDictionary *store;
static NSDictionary *concurrentPreferences;
static int copies, changeAtCopy;
static BOOL custom = YES, manualEnabled, syncOK = YES, disableOK = YES;
static BOOL signalOK = YES, restartOK = YES, listingOK = YES;
static int restartDelay, sleeps;
static pid_t currentPID = 41;
static uid_t uid;
static int writes, reloads, disables;
static void checkScope(CFStringRef domain, CFStringRef user, CFStringRef host) {
    assert(CFEqual(domain, CFSTR("com.apple.powerd.charging")));
    assert(CFEqual(user, kCFPreferencesAnyUser));
    assert(CFEqual(host, kCFPreferencesCurrentHost));
}
static Boolean fakeSync(CFStringRef domain, CFStringRef user, CFStringRef host) {
    checkScope(domain,user,host); return syncOK;
}
static CFDictionaryRef fakeCopy(CFArrayRef keys, CFStringRef domain, CFStringRef user, CFStringRef host) {
    if (++copies == changeAtCopy) [store addEntriesFromDictionary:concurrentPreferences];
    assert(keys == NULL); checkScope(domain,user,host); return (CFDictionaryRef)[store copy];
}
static void fakeSet(CFDictionaryRef values, CFArrayRef removed, CFStringRef domain, CFStringRef user, CFStringRef host) {
    assert(removed == NULL); checkScope(domain,user,host); writes++;
    [store addEntriesFromDictionary:(NSDictionary *)values];
}
static uid_t fakeUID(void) { return uid; }
static int fakeSysctl(const char *name, void *value, size_t *size, void *newValue, size_t newSize) {
    assert(strcmp(name,"kern.bootsessionuuid") == 0 && newValue == NULL && newSize == 0);
    assert(*size >= sizeof("boot1")); memcpy(value,"boot1",sizeof("boot1")); *size=sizeof("boot1"); return 0;
}
static int fakeList(void *buffer, int size) {
    if (!listingOK) return -1;
    if (!buffer) return 1;
    if (currentPID == 0) return 0;
    assert(size >= (int)sizeof(pid_t)); ((pid_t *)buffer)[0] = currentPID; return 1;
}
static int fakePath(int pid, void *buffer, uint32_t size) {
    assert(pid == currentPID && size >= 100);
    strcpy(buffer, "/System/Library/CoreServices/powerd.bundle/powerd"); return 57;
}
static int fakeKill(pid_t pid, int sig) {
    assert(pid == currentPID && sig == SIGHUP); reloads++;
    if (!signalOK) { errno = EPERM; return -1; }
    return 0;
}
static int fakeSleep(useconds_t delay) {
    assert(delay == 100000); sleeps++;
    if (restartOK && sleeps > restartDelay) currentPID++; return 0;
}
#define CFPreferencesSynchronize fakeSync
#define CFPreferencesCopyMultiple fakeCopy
#define CFPreferencesSetMultiple fakeSet
#define geteuid fakeUID
#define sysctlbyname fakeSysctl
#define proc_listallpids fakeList
#define proc_pidpath fakePath
#define kill fakeKill
#define usleep fakeSleep
#include "../policy.m"

int batt_powerui_custom_limits(void) { return custom; }
int batt_powerui_disable(char **err) {
    disables++; if (!disableOK) return policyError(err,@"release failed");
    manualEnabled=NO; return 0;
}
static BattChargePolicy *makePolicy(NSString *reason, int target, BOOL drain, BOOL suppress) {
    BattChargePolicy *p=[[[BattChargePolicy alloc] init] autorelease];
    p.reason=reason; p.limit=target; p.owner=123; p.token=[NSUUID UUID];
    p.drain=drain; p.noChargeToFull=suppress; p.endOfCharge=YES; return p;
}
static void reset(NSArray *policies) {
    store=[NSMutableDictionary dictionaryWithDictionary:@{@"bootSessionUUID":@"boot1",@"policies":encodePolicies([NSMutableArray arrayWithArray:policies])}];
    uid=0; custom=syncOK=disableOK=signalOK=restartOK=listingOK=YES; manualEnabled=NO;
    writes=reloads=disables=sleeps=restartDelay=0; currentPID=41;
    copies=changeAtCopy=0; concurrentPreferences=nil;
}
static NSMutableArray *readPolicies(void) {
    char *error=NULL; NSMutableArray *result=decodePolicies(store,@"boot1",&error);
    assert(result != nil && error == NULL); return result;
}
static void checkMutableArchive(void) {
    NSDictionary *archive=[NSPropertyListSerialization propertyListWithData:store[@"policies"] options:0 format:NULL error:NULL];
    BOOL mutable=NO;
    for(id o in archive[@"$objects"]) if([o isKindOfClass:NSDictionary.class] && [o[@"$classname"] isEqual:@"NSMutableArray"]) mutable=YES;
    assert(mutable);
}
int main(void) { @autoreleasepool {
    char *error=NULL; int limit=0,enabled=0;
    BattChargePolicy *foreign=makePolicy(@"otherOSPolicy",90,YES,YES);
    NSUUID *foreignToken=[[foreign.token copy] autorelease];
    reset(@[foreign]);
    assert(batt_powerui_get_policy(&limit,&enabled,&error) == 0 && !enabled && limit == 100);
    for (NSNumber *value in @[@10, @55, @99]) {
        int target=value.intValue;
        assert(batt_powerui_set_policy(target,&error) == 0);
        checkMutableArchive();
        NSArray *policies=readPolicies(); assert(policies.count == 2);
        BattChargePolicy *other=policies[0], *ours=policies[1];
        assert([other.reason isEqual:@"otherOSPolicy"] && other.limit == 90 && other.drain && other.noChargeToFull && [other.token isEqual:foreignToken]);
        assert([ours.reason isEqual:@"battChargeLimit"] && ours.limit == target && !ours.drain && !ours.noChargeToFull && ours.endOfCharge && !ours.terminated);
        assert(batt_powerui_get_policy(&limit,&enabled,&error) == 0 && enabled && limit == target);
    }
    assert(writes == 3 && reloads == 3 && !manualEnabled);
    assert(batt_powerui_set_policy(100,&error) == 0 && readPolicies().count == 1);
    assert(batt_powerui_get_policy(&limit,&enabled,&error) == 0 && !enabled && limit == 100);
    // Releasing batt never removes another controller's ordinary Apple limit.
    reset(@[makePolicy(@"battChargeLimit",55,NO,NO),makePolicy(@"manualChargeLimit",80,YES,NO)]);
    manualEnabled=YES;
    assert(batt_powerui_set_policy(100,&error) == 0 && readPolicies().count == 1 && manualEnabled);
    assert([((BattChargePolicy *)readPolicies()[0]).reason isEqual:@"manualChargeLimit"]);
    // An interleaved Apple update must survive and prevent a stale reload.
    for (int stage=3;stage<=4;stage++) {
        reset(@[]);
        NSData *changed=encodePolicies([NSMutableArray arrayWithObject:foreign]);
        concurrentPreferences=@{@"policies":changed}; changeAtCopy=stage;
        assert(batt_powerui_set_policy(55,&error) == -1 && error != NULL);
        assert([store[@"policies"] isEqual:changed] && reloads == 0);
        assert(writes == (stage == 3 ? 0 : 1));
        free(error);error=NULL;
        // The normal next pass reads the new snapshot and preserves it.
        changeAtCopy=0;
        assert(batt_powerui_set_policy(55,&error) == 0 && readPolicies().count == 2);
    }
    for (int invalid=0;invalid<=101;invalid+=101) {
        int before=writes; assert(batt_powerui_set_policy(invalid,&error) == -1 && writes == before); free(error);error=NULL;
    }
    // Duplicates, bad flags, and terminated policies cannot confirm a ceiling.
    for (int mode=0;mode<4;mode++) {
        BattChargePolicy *p=makePolicy(@"battChargeLimit",55,mode == 0,mode == 1);
        p.terminated=mode == 2;
        reset(mode == 3 ? @[p,p] : @[p]);
        assert(batt_powerui_get_policy(&limit,&enabled,&error) == 0 && enabled && limit == 0);
        assert(batt_powerui_set_policy(55,&error) == 0 && readPolicies().count == 1);
    }
    // Never replay an earlier boot's low hold or foreign policies.
    reset(@[makePolicy(@"battChargeLimit",8,NO,NO)]);store[@"bootSessionUUID"]=@"old-boot";
    assert(batt_powerui_get_policy(&limit,&enabled,&error) == 0 && !enabled);
    assert(batt_powerui_set_policy(55,&error) == 0 && readPolicies().count == 1);
    // Reject a schema we cannot preserve before any system mutation.
    reset(@[foreign]);
    NSMutableDictionary *archive=[NSPropertyListSerialization propertyListWithData:store[@"policies"] options:NSPropertyListMutableContainers format:NULL error:NULL];
    for(id object in archive[@"$objects"]) if([object isKindOfClass:NSMutableDictionary.class] && object[@"soclimit"]) object[@"futureField"]=@1;
    store[@"policies"]=[NSPropertyListSerialization dataWithPropertyList:archive format:NSPropertyListBinaryFormat_v1_0 options:0 error:NULL];
    assert(batt_powerui_set_policy(55,&error) == -1 && writes == 0 && disables == 0);free(error);error=NULL;
    reset(@[foreign]);
    archive=[NSPropertyListSerialization propertyListWithData:store[@"policies"] options:NSPropertyListMutableContainers format:NULL error:NULL];
    for(id object in archive[@"$objects"]) if([object isKindOfClass:NSMutableDictionary.class] && object[@"soclimit"]) object[@"soclimit"]=@55.5;
    store[@"policies"]=[NSPropertyListSerialization dataWithPropertyList:archive format:NSPropertyListBinaryFormat_v1_0 options:0 error:NULL];
    assert(batt_powerui_set_policy(55,&error) == -1 && writes == 0 && disables == 0);free(error);error=NULL;
    // Do not accept the old process while launchd restarts powerd.
    reset(@[]); restartDelay=20;
    assert(batt_powerui_set_policy(55,&error) == 0 && currentPID != 41);
    // Another service may already have stopped powerd before our reload.
    reset(@[]); currentPID=0;
    assert(batt_powerui_set_policy(55,&error) == 0 && reloads == 0);
    for(int failure=0;failure<7;failure++) {
        reset(@[]);
        if(failure == 0) uid=501;
        if(failure == 1) custom=NO;
        if(failure == 2) syncOK=NO;
        if(failure == 3) disableOK=NO;
        if(failure == 4) signalOK=NO;
        if(failure == 5) restartOK=NO;
        if(failure == 6) listingOK=NO;
        assert(batt_powerui_set_policy(55,&error) == -1 && error != NULL);free(error);error=NULL;
        if(failure < 4) assert(writes == 0 && reloads == 0);
    }
    puts("PASS: ceiling boundaries, no drain, no calibration suppression, mutable archives, foreign policies, reboot, release, schema and write errors");
} return 0; }
