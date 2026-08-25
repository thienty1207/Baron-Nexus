package version

import "testing"

func TestDefaultVersionIsInitialBaronNexusRelease(t *testing.T) {
	if Value != "0.1.0" {
		t.Fatalf("default version=%q", Value)
	}
}
