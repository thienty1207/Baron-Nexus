package version

import "testing"

func TestDefaultVersionIsCurrentBaronNexusRelease(t *testing.T) {
	if Value != "0.1.16" {
		t.Fatalf("default version=%q, want 0.1.16", Value)
	}
}
