#import <Foundation/Foundation.h>
#import <CoreFoundation/CoreFoundation.h>
#import <IOKit/pwr_mgt/IOPMLib.h>
#include <libproc.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include "assertions.h"

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

// Return sleep assertions owned by any process except powerd. batt's own
// assertions are included: restarting powerd drops those too, so a calibration
// hold must delay the restart rather than be silently lost.
int battCopySleepAssertions(battSleepAssertion **out, int *outCount, char **error) {
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
			pid == 0 || battIsPowerd(pid)) continue;
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

void battFreeSleepAssertions(battSleepAssertion *list) { free(list); }
