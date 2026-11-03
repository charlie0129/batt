// Production ObjC bridge with all system mutations replaced by in-memory fakes.
#import <Foundation/Foundation.h>
#include <notify.h>
#include <unistd.h>
#include <assert.h>
#include <dlfcn.h>

static uint64_t overrideState;
static BOOL registerOK=YES, readStateOK=YES;
static int overrideReads, overrideCancels;
static NSInteger fakeMajor=27;
static BOOL refreshAvailable=YES;
static uint32_t refreshResult;
static int refreshes;
static uint32_t test_request_update(int kind) {
    assert(kind==2); refreshes++; return refreshResult;
}
static void *test_dlopen(const char *path, int flags) {
    assert(strcmp(path,"/System/Library/Frameworks/IOKit.framework/IOKit")==0 && flags==RTLD_NOW);
    return (void *)42;
}
static void *test_dlsym(void *handle, const char *name) {
    assert(handle==(void *)42 && strcmp(name,"IOPSRequestBatteryUpdate")==0);
    return refreshAvailable ? test_request_update : NULL;
}
static int test_dlclose(void *handle) { assert(handle==(void *)42); return 0; }
@interface FakeProcessInfo : NSObject
+ (instancetype)processInfo;
- (NSOperatingSystemVersion)operatingSystemVersion;
@end
@implementation FakeProcessInfo
+ (instancetype)processInfo { return [[[self alloc] init] autorelease]; }
- (NSOperatingSystemVersion)operatingSystemVersion {
    return (NSOperatingSystemVersion){fakeMajor, 0, 0};
}
@end
static uint32_t test_register_check(const char *name, int *token) {
    assert(strcmp(name,"com.apple.system.powersources.chargingtofulloverride")==0);
    *token=42;
    return registerOK ? NOTIFY_STATUS_OK : NOTIFY_STATUS_FAILED;
}
static uint32_t test_get_state(int token, uint64_t *state) {
    assert(token==42); overrideReads++; *state=overrideState;
    return readStateOK ? NOTIFY_STATUS_OK : NOTIFY_STATUS_FAILED;
}
static uint32_t test_cancel(int token) {
    assert(token==42); overrideCancels++; return NOTIFY_STATUS_OK;
}
#define notify_register_check test_register_check
#define notify_get_state test_get_state
#define notify_cancel test_cancel
#define NSProcessInfo FakeProcessInfo
#define dlopen test_dlopen
#define dlsym test_dlsym
#define dlclose test_dlclose
#include "../powerui.m"
#undef NSProcessInfo

@interface FakeClient : NSObject
@property BOOL fail;
@end
@implementation FakeClient
- (unsigned char)getMCLLimitWithError:(NSError **)error {
    if(self.fail) *error=[NSError errorWithDomain:@"test" code:1 userInfo:nil];
    return 55;
}
- (unsigned long long)isMCLCurrentlyEnabled:(NSError **)error { (void)error;return 1; }
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
    FakeClient *fake=[FakeClient new]; gClient=fake; gClientProbed=YES;
    char *error=NULL; int target=0,enabled=0;
    int active=-1;
    for(uint64_t state=0;state<=1;state++) {
        overrideState=state;
        assert(batt_powerui_full_charge_override(&active,&error)==0 && active==(int)state);
    }
    readStateOK=NO;
    assert(batt_powerui_full_charge_override(&active,&error)==-1);
    free(error); error=NULL;
    assert(overrideReads==3 && overrideCancels==3);
    registerOK=NO;
    assert(batt_powerui_full_charge_override(&active,&error)==-1);
    free(error); error=NULL;
    assert(overrideReads==3 && overrideCancels==3);
    for(NSNumber *major in @[@26,@27,@28]) {
        fakeMajor=major.integerValue;
        assert(batt_powerui_custom_limits()==(fakeMajor==27));
    }
    fakeMajor=27;
    id unsupported=[NSObject new];gClient=unsupported;
    assert(!batt_powerui_custom_limits());gClient=fake;[unsupported release];
    assert(batt_powerui_get_limit(&target,&enabled,&error)==0 && target==55 && enabled);
    fake.fail=YES;
    assert(batt_powerui_get_limit(&target,&enabled,&error)==-1); free(error); error=NULL;
    assert(batt_powerui_disable(&error)==-1); free(error); error=NULL;
    assert(batt_powerui_set_limit(80,&error)==-1); free(error); error=NULL;
    assert(batt_powerui_refresh_battery(&error)==0 && refreshes==1);
    refreshResult=0xe00002c2;
    assert(batt_powerui_refresh_battery(&error)==-1 && refreshes==2); free(error); error=NULL;
    refreshAvailable=NO;
    assert(batt_powerui_refresh_battery(&error)==-1 && refreshes==2); free(error); error=NULL;
    [fake release];
    puts("PASS: override reads, macOS capability gate, PowerUI reads, and XPC errors");
} return 0; }
