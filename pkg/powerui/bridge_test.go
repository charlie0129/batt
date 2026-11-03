package powerui

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestObjectiveCBridgeWithIsolatedPreferences(t *testing.T) {
	// This executable stubs ALL preference writes, notifications and XPC calls.
	// Sanitizers exercise the production bridge without changing this Mac.
	for _, source := range []string{"bridge_test.m", "policy_test.m"} {
		t.Run(source, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "bridge-test")
			cmd := exec.Command("/usr/bin/xcrun", "clang", "-Wall", "-Wextra", "-Werror",
				"-fsanitize=address,undefined", "-fno-omit-frame-pointer", "-framework", "Foundation",
				"testdata/"+source, "-o", binary)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("compile native bridge test: %v\n%s", err, output)
			}
			if output, err := exec.Command(binary).CombinedOutput(); err != nil {
				t.Fatalf("native bridge test: %v\n%s", err, output)
			}
		})
	}
}
