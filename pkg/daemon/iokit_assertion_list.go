package daemon

/*
#cgo LDFLAGS: -framework CoreFoundation -framework IOKit
#include <CoreFoundation/CoreFoundation.h>
#include <IOKit/pwr_mgt/IOPMLib.h>
#include <libproc.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

// A small C representation avoids passing Core Foundation objects through Go.
typedef struct {
	int pid;
	char assertion_type[64];
	char name[256];
} battSleepAssertion;

static int battIsPowerd(pid_t pid) {
	char path[PROC_PIDPATHINFO_MAXSIZE];
	return proc_pidpath(pid, path, sizeof(path)) > 0 &&
		strcmp(path, "/System/Library/CoreServices/powerd.bundle/powerd") == 0;
}

static int battSleepType(CFStringRef type) {
	return type != NULL && CFGetTypeID(type) == CFStringGetTypeID() &&
		(CFStringCompare(type, kIOPMAssertionTypePreventUserIdleSystemSleep, 0) == kCFCompareEqualTo ||
		 CFStringCompare(type, kIOPMAssertionTypePreventUserIdleDisplaySleep, 0) == kCFCompareEqualTo ||
		 CFStringCompare(type, kIOPMAssertionTypeNoIdleSleep, 0) == kCFCompareEqualTo ||
		 CFStringCompare(type, kIOPMAssertionTypeNoDisplaySleep, 0) == kCFCompareEqualTo ||
		 CFStringCompare(type, CFSTR("PreventSystemSleep"), 0) == kCFCompareEqualTo);
}

// Return assertions owned by applications other than batt and powerd. powerd's
// own display assertion is recreated after its launchd restart and must not
// prevent every no-drain update while the display is on.
static int battCopySleepAssertions(battSleepAssertion **out, int *outCount, char **error) {
	*out = NULL;
	*outCount = 0;
	CFDictionaryRef byProcess = NULL;
	IOReturn result = IOPMCopyAssertionsByProcess(&byProcess);
	if (result != kIOReturnSuccess || byProcess == NULL) {
		if (error != NULL) *error = strdup("could not read system power assertions");
		return -1;
	}
	CFIndex processCount = CFDictionaryGetCount(byProcess);
	CFTypeRef *keys = calloc((size_t)processCount, sizeof(CFTypeRef));
	CFTypeRef *values = calloc((size_t)processCount, sizeof(CFTypeRef));
	if (processCount > 0 && (keys == NULL || values == NULL)) {
		free(keys); free(values); CFRelease(byProcess);
		if (error != NULL) *error = strdup("could not allocate power assertion list");
		return -1;
	}
	CFDictionaryGetKeysAndValues(byProcess, keys, values);
	battSleepAssertion *list = NULL;
	int count = 0;
	for (CFIndex i = 0; i < processCount; i++) {
		int pid = 0;
		if (keys[i] == NULL || CFGetTypeID(keys[i]) != CFNumberGetTypeID() ||
			!CFNumberGetValue((CFNumberRef)keys[i], kCFNumberIntType, &pid) ||
			pid == getpid() || pid == 0 || battIsPowerd(pid)) continue;
		if (values[i] == NULL || CFGetTypeID(values[i]) != CFArrayGetTypeID()) continue;
		CFArrayRef assertions = (CFArrayRef)values[i];
		for (CFIndex j = 0; j < CFArrayGetCount(assertions); j++) {
			CFTypeRef item = CFArrayGetValueAtIndex(assertions, j);
			if (item == NULL || CFGetTypeID(item) != CFDictionaryGetTypeID()) continue;
			CFDictionaryRef assertion = (CFDictionaryRef)item;
			CFStringRef type = (CFStringRef)CFDictionaryGetValue(assertion, kIOPMAssertionTypeKey);
			if (!battSleepType(type)) continue;
			battSleepAssertion *next = realloc(list, (size_t)(count + 1) * sizeof(*list));
			if (next == NULL) {
				free(list); free(keys); free(values); CFRelease(byProcess);
				if (error != NULL) *error = strdup("could not allocate power assertion list");
				return -1;
			}
			list = next;
			memset(&list[count], 0, sizeof(list[count]));
			list[count].pid = pid;
			if (!CFStringGetCString(type, list[count].assertion_type, sizeof(list[count].assertion_type), kCFStringEncodingUTF8))
				list[count].assertion_type[0] = 0;
			CFStringRef name = (CFStringRef)CFDictionaryGetValue(assertion, kIOPMAssertionNameKey);
			if (name != NULL && CFGetTypeID(name) == CFStringGetTypeID())
				CFStringGetCString(name, list[count].name, sizeof(list[count].name), kCFStringEncodingUTF8);
			count++;
		}
	}
	free(keys); free(values); CFRelease(byProcess);
	*out = list;
	*outCount = count;
	return 0;
}

static void battFreeSleepAssertions(battSleepAssertion *list) { free(list); }
*/
import "C"

import (
	"errors"
	"unsafe"
)

// SleepAssertion is a power assertion held by another process. Type is the
// IOKit assertion type and Name is the name supplied by its owner.
type SleepAssertion struct {
	PID  int
	Type string
	Name string
}

// ForeignSleepAssertions returns sleep and display-sleep assertions held by
// processes other than batt and powerd. Restarting powerd drops these until
// their owners create them again, which some applications never do.
func (a *iokitAssertions) ForeignSleepAssertions() ([]SleepAssertion, error) {
	var list *C.battSleepAssertion
	var count C.int
	var cerr *C.char
	if C.battCopySleepAssertions(&list, &count, &cerr) != 0 { //nolint:gocritic // cgo output parameters
		if cerr != nil {
			defer C.free(unsafe.Pointer(cerr))
			return nil, errors.New(C.GoString(cerr))
		}
		return nil, errors.New("could not read system power assertions")
	}
	defer C.battFreeSleepAssertions(list)
	if count == 0 {
		return nil, nil
	}
	items := unsafe.Slice(list, int(count))
	result := make([]SleepAssertion, 0, len(items))
	for _, item := range items {
		result = append(result, SleepAssertion{
			PID: int(item.pid), Type: C.GoString(&item.assertion_type[0]), Name: C.GoString(&item.name[0]),
		})
	}
	return result, nil
}
