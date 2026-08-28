package install

import (
	"context"
	"strings"
	"testing"
)

func TestAptSessionRefreshesMetadataOnce(t *testing.T) {
	runner := &commandFixture{available: map[string]bool{"sudo": true}}
	session := &AptSession{}
	if err := refreshAptMetadata(context.Background(), runner, nil, session, "host"); err != nil {
		t.Fatal(err)
	}
	if err := refreshAptMetadata(context.Background(), runner, nil, session, "docker"); err != nil {
		t.Fatal(err)
	}
	updates := 0
	for _, call := range runner.calls {
		if strings.Contains(call, "apt-get update") {
			updates++
		}
	}
	if updates != 1 {
		t.Fatalf("apt metadata refreshes=%d, want one: %#v", updates, runner.calls)
	}
}
