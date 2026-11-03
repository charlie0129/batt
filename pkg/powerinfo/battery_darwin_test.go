package powerinfo

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBatterySnapshotLayouts(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "battery-test")
	cmd := exec.Command("/usr/bin/xcrun", "clang", "-Wall", "-Wextra", "-Werror",
		"-fsanitize=address,undefined", "-framework", "Foundation", "-framework", "IOKit",
		"testdata/battery_test.m", "-o", binary)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile battery reader test: %v\n%s", err, output)
	}
	if output, err := exec.Command(binary).CombinedOutput(); err != nil {
		t.Fatalf("decode battery snapshots: %v\n%s", err, output)
	}
}
