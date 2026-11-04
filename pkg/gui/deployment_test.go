package gui

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMacOSDeploymentTargets(t *testing.T) {
	for _, target := range []string{"12.0", "13.0"} {
		t.Run(target, func(t *testing.T) {
			t.Setenv("CGO_ENABLED", "1")
			t.Setenv("MACOSX_DEPLOYMENT_TARGET", target)
			binary := filepath.Join(t.TempDir(), "batt")
			// Go's build cache does not track MACOSX_DEPLOYMENT_TARGET.
			cmd := exec.Command("go", "build", "-a", "-o", binary, "./cmd/batt")
			cmd.Dir = "../.."
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("build for macOS %s: %v\n%s", target, err, output)
			}

			output, err := exec.Command("/usr/bin/xcrun", "otool", "-l", binary).CombinedOutput()
			if err != nil {
				t.Fatalf("read Mach-O load commands: %v\n%s", err, output)
			}
			minimum := ""
			versionCommand := false
			for _, line := range strings.Split(string(output), "\n") {
				fields := strings.Fields(line)
				if len(fields) != 2 {
					continue
				}
				if fields[0] == "cmd" {
					versionCommand = fields[1] == "LC_BUILD_VERSION" || fields[1] == "LC_VERSION_MIN_MACOSX"
				}
				if versionCommand && (fields[0] == "minos" || fields[0] == "version") {
					minimum = fields[1]
					break
				}
			}
			var major, minor, patch int
			if n, _ := fmt.Sscanf(minimum, "%d.%d.%d", &major, &minor, &patch); n < 2 {
				t.Fatalf("missing or invalid macOS minimum version %q\n%s", minimum, output)
			}
			if target == "12.0" && (major > 12 || major == 12 && (minor > 0 || patch > 0)) {
				t.Errorf("minimum macOS version = %s, want at most 12.0", minimum)
			}
			if target == "13.0" && (major != 13 || minor != 0 || patch != 0) {
				t.Errorf("minimum macOS version = %s, want 13.0", minimum)
			}

			output, err = exec.Command("/usr/bin/xcrun", "nm", "-u", binary).CombinedOutput()
			if err != nil {
				t.Fatalf("read undefined symbols: %v\n%s", err, output)
			}
			hasSMAppService := strings.Contains(string(output), "_OBJC_CLASS_$_SMAppService")
			if want := target == "13.0"; hasSMAppService != want {
				t.Errorf("SMAppService reference = %v, want %v for target %s", hasSMAppService, want, target)
			}
		})
	}
}
