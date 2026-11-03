#import <Foundation/Foundation.h>
#import <dlfcn.h>
#import <objc/message.h>
#import <objc/runtime.h>
#include <string.h>
#include <notify.h>
#include <unistd.h>

#include "powerui.h"

static const char *kPowerUIPath = "/System/Library/PrivateFrameworks/PowerUI.framework/Versions/A/PowerUI";

// Method type encodings observed on macOS 27.0 (26A428):
//   initWithClientName:              @24@0:8@16
//   isMCLSupported                   B16@0:8
//   getMCLLimitWithError:            C24@0:8^@16   (unsigned char)
//   setMCLLimit:error:               B28@0:8C16^@20
//   isMCLCurrentlyEnabled:           Q24@0:8^@16
//   availableChargeLimitsWithError:  @24@0:8^@16   (NSArray<NSNumber *>)
//   disableMCL:                      B24@0:8^@16
typedef id (*initWithClientNameFn)(id, SEL, id);
typedef BOOL (*boolFn)(id, SEL);
typedef unsigned char (*ucharErrFn)(id, SEL, NSError **);
typedef BOOL (*boolUcharErrFn)(id, SEL, unsigned char, NSError **);
typedef unsigned long long (*ullErrFn)(id, SEL, NSError **);
typedef id (*idErrFn)(id, SEL, NSError **);
typedef BOOL (*boolErrFn)(id, SEL, NSError **);

// The client is created once and kept for the lifetime of the process. The Go
// side serializes all calls, so no locking is needed here.
static id gClient = nil;
static BOOL gClientProbed = NO;

static void setError(char **err, NSString *message) {
    if (err == NULL) {
        return;
    }
    *err = strdup(message != nil ? [message UTF8String] : "unknown PowerUI error");
}

static void setNSError(char **err, NSError *error, NSString *fallback) {
    setError(err, error != nil ? [error description] : fallback);
}

int batt_powerui_refresh_battery(char **err) {
    @autoreleasepool {
        void *framework = dlopen("/System/Library/Frameworks/IOKit.framework/IOKit", RTLD_NOW);
        if (!framework) {
            setError(err, @"IOKit is unavailable for the battery refresh"); return -1;
        }
        uint32_t (*request)(int) = dlsym(framework, "IOPSRequestBatteryUpdate");
        if (!request) {
            dlclose(framework);
            setError(err, @"IOPSRequestBatteryUpdate is unavailable"); return -1;
        }
        // kIOPSReadAll: one sample after the policy changes, not continuous polling.
        uint32_t result = request(2);
        dlclose(framework);
        if (result != 0) {
            setError(err, [NSString stringWithFormat:@"battery refresh failed: 0x%08x", result]); return -1;
        }
        return 0;
    }
}

int batt_powerui_full_charge_override(int *active, char **err) {
    int token;
    if (notify_register_check("com.apple.system.powersources.chargingtofulloverride", &token) != NOTIFY_STATUS_OK) {
        setError(err, @"could not register for the macOS full-charge override state");
        return -1;
    }
    uint64_t state = 0;
    uint32_t status = notify_get_state(token, &state);
    uint32_t cancelStatus = notify_cancel(token);
    if (status != NOTIFY_STATUS_OK || cancelStatus != NOTIFY_STATUS_OK) {
        setError(err, @"could not read the macOS full-charge override state");
        return -1;
    }
    *active = state != 0;
    return 0;
}

static BOOL responds(id obj, const char *sel) {
    return obj != nil && [obj respondsToSelector:sel_registerName(sel)];
}

static id client(void) {
    if (gClientProbed) {
        return gClient;
    }
    gClientProbed = YES;

    if (dlopen(kPowerUIPath, RTLD_NOW) == NULL) {
        return nil;
    }
    Class cls = NSClassFromString(@"PowerUISmartChargeClient");
    if (cls == Nil) {
        return nil;
    }
    id instance = [cls alloc];
    if (!responds(instance, "initWithClientName:")) {
        [instance release];
        return nil;
    }
    instance = ((initWithClientNameFn)objc_msgSend)(instance, sel_registerName("initWithClientName:"), @"batt");
    if (!responds(instance, "isMCLSupported") ||
        !responds(instance, "getMCLLimitWithError:") ||
        !responds(instance, "setMCLLimit:error:") ||
        !responds(instance, "isMCLCurrentlyEnabled:") ||
        !responds(instance, "availableChargeLimitsWithError:") ||
        !responds(instance, "disableMCL:")) {
        [instance release];
        return nil;
    }
    gClient = instance;
    return gClient;
}

int batt_powerui_supported(void) {
    @autoreleasepool {
        id c = client();
        if (c == nil) {
            return 0;
        }
        return ((boolFn)objc_msgSend)(c, sel_registerName("isMCLSupported")) ? 1 : 0;
    }
}

int batt_powerui_available_limits(int *limits, int max, int *count, char **err) {
    @autoreleasepool {
        id c = client();
        if (c == nil) {
            setError(err, @"PowerUI smart charge client is unavailable");
            return -1;
        }
        NSError *error = nil;
        id result = ((idErrFn)objc_msgSend)(c, sel_registerName("availableChargeLimitsWithError:"), &error);
        if (result == nil || ![result isKindOfClass:[NSArray class]]) {
            setNSError(err, error, @"availableChargeLimitsWithError: returned no limits");
            return -1;
        }
        int n = 0;
        for (id value in (NSArray *)result) {
            if (n >= max) {
                break;
            }
            if ([value respondsToSelector:@selector(intValue)]) {
                limits[n++] = [value intValue];
            }
        }
        *count = n;
        return 0;
    }
}

int batt_powerui_get_limit(int *limit, int *enabled, char **err) {
    @autoreleasepool {
        id c = client();
        if (c == nil) {
            setError(err, @"PowerUI smart charge client is unavailable");
            return -1;
        }
        NSError *error = nil;
        unsigned char value = ((ucharErrFn)objc_msgSend)(c, sel_registerName("getMCLLimitWithError:"), &error);
        if (error != nil) {
            setNSError(err, error, nil);
            return -1;
        }
        error = nil;
        unsigned long long on = ((ullErrFn)objc_msgSend)(c, sel_registerName("isMCLCurrentlyEnabled:"), &error);
        if (error != nil) {
            setNSError(err, error, nil);
            return -1;
        }
        *limit = (int)value;
        *enabled = on != 0 ? 1 : 0;
        return 0;
    }
}

int batt_powerui_set_limit(int limit, char **err) {
    @autoreleasepool {
        id c = client();
        if (c == nil) {
            setError(err, @"PowerUI smart charge client is unavailable");
            return -1;
        }
        NSError *error = nil;
        BOOL ok = ((boolUcharErrFn)objc_msgSend)(c, sel_registerName("setMCLLimit:error:"), (unsigned char)limit, &error);
        if (!ok || error != nil) {
            setNSError(err, error, @"setMCLLimit:error: failed");
            return -1;
        }
        return 0;
    }
}

int batt_powerui_disable(char **err) {
    @autoreleasepool {
        id c = client();
        if (c == nil) {
            setError(err, @"PowerUI smart charge client is unavailable");
            return -1;
        }
        NSError *error = nil;
        BOOL ok = ((boolErrFn)objc_msgSend)(c, sel_registerName("disableMCL:"), &error);
        if (!ok || error != nil) {
            setNSError(err, error, @"disableMCL: failed");
            return -1;
        }
        return 0;
    }
}

// Use the no-drain archive only on the OS generation whose schema was tested.
int batt_powerui_custom_limits(void) {
    @autoreleasepool {
        return NSProcessInfo.processInfo.operatingSystemVersion.majorVersion == 27 &&
            responds(client(), "enableMCL:") ? 1 : 0;
    }
}
