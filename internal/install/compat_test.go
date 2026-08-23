package install

import "testing"

func TestUnsupportedUpstreamVersionIsActionable(t *testing.T) {
	if err := CheckCompatible("dsh", "9.9.9", []string{"0.1.0", "developer-preview"}); err == nil {
		t.Fatal("unsupported DSH version was accepted")
	}
	if err := CheckCompatible("codex", "0.149.0", []string{"0.149.0"}); err != nil {
		t.Fatal(err)
	}
}
