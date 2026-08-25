package version

import "testing"

func TestDefaultVersionIsInitialBaronNexusRelease(t *testing.T) {
	if Value != "0.1.1" {
		t.Fatalf("default version=%q, want 0.1.1", Value)
	}
}
