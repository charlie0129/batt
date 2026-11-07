#pragma once

// Shared by the Go assertion reader and the native powerd reload path.
typedef struct {
	int pid;
	char assertion_type[64];
	char name[256];
} battSleepAssertion;

int battCopySleepAssertions(battSleepAssertion **out, int *outCount, char **error);
void battFreeSleepAssertions(battSleepAssertion *list);
