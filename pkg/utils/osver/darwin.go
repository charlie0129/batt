package osver

// #cgo CFLAGS: -x objective-c
// #cgo LDFLAGS: -framework Foundation
// #import <Foundation/Foundation.h>
//
// void getSystemVersion(int *major, int *minor, int *patch) {
//     NSAutoreleasePool *pool = [[NSAutoreleasePool alloc] init];
//     NSOperatingSystemVersion version = [[NSProcessInfo processInfo] operatingSystemVersion];
//     *major = (int)version.majorVersion;
//     *minor = (int)version.minorVersion;
//     *patch = (int)version.patchVersion;
//     [pool release];
// }
import "C"

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
)

var (
	cachedVersion Version
	initOnce      sync.Once
)

// Version represents a macOS version with major, minor, and patch components.
type Version struct {
	Major int
	Minor int
	Patch int
}

// String returns the string representation of a Version.
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Get returns the current macOS system version.
// The version is retrieved once and cached for subsequent calls.
func Get() Version {
	initOnce.Do(func() {
		var major, minor, patch C.int
		C.getSystemVersion(&major, &minor, &patch)
		cachedVersion = Version{
			Major: int(major),
			Minor: int(minor),
			Patch: int(patch),
		}
	})
	return cachedVersion
}

// Parse converts a version string into a Version struct.
// Format should be "major.minor.patch" or "major.minor".
func Parse(version string) (Version, error) {
	parts := strings.Split(version, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return Version{}, fmt.Errorf("invalid version format: %s", version)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return Version{}, fmt.Errorf("invalid major version: %s", parts[0])
	}

	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return Version{}, fmt.Errorf("invalid minor version: %s", parts[1])
	}

	patch := 0
	if len(parts) == 3 {
		patch, err = strconv.Atoi(parts[2])
		if err != nil {
			return Version{}, fmt.Errorf("invalid patch version: %s", parts[2])
		}
	}

	return Version{Major: major, Minor: minor, Patch: patch}, nil
}

// Compare compares two versions and returns:
// -1 if v < other
// 0 if v == other
// 1 if v > other
func (v Version) Compare(other Version) int {
	if v.Major != other.Major {
		if v.Major < other.Major {
			return -1
		}
		return 1
	}

	if v.Minor != other.Minor {
		if v.Minor < other.Minor {
			return -1
		}
		return 1
	}

	if v.Patch != other.Patch {
		if v.Patch < other.Patch {
			return -1
		}
		return 1
	}

	return 0
}

// LessThan returns true if this version is less than the other version.
func (v Version) LessThan(other Version) bool {
	return v.Compare(other) < 0
}

// GreaterThan returns true if this version is greater than the other version.
func (v Version) GreaterThan(other Version) bool {
	return v.Compare(other) > 0
}

// Equal returns true if this version is equal to the other version.
func (v Version) Equal(other Version) bool {
	return v.Compare(other) == 0
}

// AtLeast returns true if this version is greater than or equal to the specified version.
func (v Version) AtLeast(other Version) bool {
	return v.Compare(other) >= 0
}

// IsAtLeast checks if the current system version is at least the specified version.
func IsAtLeast(major, minor, patch int) bool {
	current := Get()
	required := Version{Major: major, Minor: minor, Patch: patch}
	return current.AtLeast(required)
}
