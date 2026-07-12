package main

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/charlie0129/batt/pkg/compatibility"
)

func TestRequiredCapabilityInheritedFromParentCommand(t *testing.T) {
	root := &cobra.Command{Use: "batt"}
	parent := annotateCapability(&cobra.Command{Use: "adapter"}, compatibility.FeatureAdapterControl)
	child := &cobra.Command{Use: "disable"}
	parent.AddCommand(child)
	root.AddCommand(parent)

	got, ok := requiredCapability(child)
	if !ok || got != compatibility.FeatureAdapterControl {
		t.Fatalf("requiredCapability() = %q, %t", got, ok)
	}
}
