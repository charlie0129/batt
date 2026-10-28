// Production ObjC bridge with all system mutations replaced by in-memory fakes.
#import <Foundation/Foundation.h>
#include <notify.h>
#include <unistd.h>
#include <assert.h>

static NSMutableDictionary *prefs;
static int writes, notifications;
static BOOL flushOK=YES, notifyOK=YES;
static uid_t fakeUID=0;
static uid_t test_geteuid(void) { return fakeUID; }
static void checkDomain(CFStringRef domain, CFStringRef user, CFStringRef host) {
    assert(CFEqual(domain,CFSTR("com.apple.smartcharging.topoffprotection")));
    assert(CFEqual(user,kCFPreferencesCurrentUser));
    assert(CFEqual(host,kCFPreferencesAnyHost));
}
static Boolean test_sync(CFStringRef domain, CFStringRef user, CFStringRef host) {
    checkDomain(domain,user,host); return flushOK;
}
static CFPropertyListRef test_copy(CFStringRef key, CFStringRef domain, CFStringRef user, CFStringRef host) {
    checkDomain(domain,user,host); return (CFPropertyListRef)[prefs[(NSString *)key] retain];
}
static void test_set(CFStringRef key, CFPropertyListRef value, CFStringRef domain, CFStringRef user, CFStringRef host) {
    checkDomain(domain,user,host); writes++; prefs[(NSString *)key]=(id)value;
}
static uint32_t test_notify(const char *name) {
    assert(strcmp(name,"com.apple.smartcharging.defaultschanged")==0);
    notifications++; return notifyOK ? NOTIFY_STATUS_OK : NOTIFY_STATUS_FAILED;
}
#define geteuid test_geteuid
#define CFPreferencesSynchronize test_sync
#define CFPreferencesCopyValue test_copy
#define CFPreferencesSetValue test_set
#define notify_post test_notify
#include "../powerui.m"

@interface FakeClient : NSObject
@property BOOL fail;
@end
@implementation FakeClient
- (BOOL)enableMCL:(NSError **)error {
    if(self.fail) *error=[NSError errorWithDomain:@"test" code:1 userInfo:nil];
    return YES;
}
- (BOOL)disableMCL:(NSError **)error { return [self enableMCL:error]; }
- (BOOL)setMCLLimit:(unsigned char)limit error:(NSError **)error {
    (void)limit; return [self enableMCL:error];
}
@end

int main(void) { @autoreleasepool {
    prefs=[NSMutableDictionary dictionary];
    FakeClient *fake=[FakeClient new]; gClient=fake; gClientProbed=YES;
    char *error=NULL; int target=0,enabled=0;
    assert(batt_powerui_get_preferences(&target,&enabled,&error)==0 && target==100 && !enabled);
    assert(batt_powerui_write_preferences(69,&error)==0);
    assert(writes==2 && notifications==1);
    assert(batt_powerui_get_preferences(&target,&enabled,&error)==0 && target==69 && enabled);
    // Exercise the actual ObjC bridge for all advertised exact targets,
    // including 55%, while keeping every OS side effect stubbed out.
    for(int requested=1;requested<100;requested++) {
        int beforeWrites=writes, beforeNotifications=notifications;
        assert(batt_powerui_write_preferences(requested,&error)==0);
        assert(writes==beforeWrites+2 && notifications==beforeNotifications+1);
        assert(batt_powerui_get_preferences(&target,&enabled,&error)==0);
        assert(enabled && target==requested);
    }
    for(id invalid in @[@69.5,@101,@0,@"69"]) {
        prefs[@"mclLimitValue"]=invalid;
        assert(batt_powerui_get_preferences(&target,&enabled,&error)==-1); free(error); error=NULL;
    }
    prefs[@"mclLimitValue"]=@69; prefs[@"MCLFeatureState"]=@2;
    assert(batt_powerui_get_preferences(&target,&enabled,&error)==-1); free(error); error=NULL;
    prefs[@"MCLFeatureState"]=@1; [prefs removeObjectForKey:@"mclLimitValue"];
    assert(batt_powerui_get_preferences(&target,&enabled,&error)==-1); free(error); error=NULL;
    fakeUID=501; int before=writes;
    assert(batt_powerui_write_preferences(60,&error)==-1 && writes==before); free(error); error=NULL;
    assert(batt_powerui_get_preferences(&target,&enabled,&error)==-1); free(error); error=NULL;
    fakeUID=0; flushOK=NO;
    assert(batt_powerui_write_preferences(60,&error)==-1 && writes==before); free(error); error=NULL;
    flushOK=YES; notifyOK=NO;
    assert(batt_powerui_write_preferences(60,&error)==-1); free(error); error=NULL;
    for(int n=0;n<=100;n+=100) {
        before=writes;
        assert(batt_powerui_write_preferences(n,&error)==-1 && writes==before); free(error); error=NULL;
    }
    fake.fail=YES;
    assert(batt_powerui_enable(&error)==-1); free(error); error=NULL;
    assert(batt_powerui_disable(&error)==-1); free(error); error=NULL;
    assert(batt_powerui_set_limit(80,&error)==-1); free(error); error=NULL;
    [fake release];
    puts("PASS: all exact targets 10-99, root domain, invalid preferences, notification failure, and XPC errors");
} return 0; }
