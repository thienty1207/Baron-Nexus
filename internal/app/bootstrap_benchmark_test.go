package app

import (
	"context"
	"testing"

	"github.com/baron-shared-brain/baron/internal/install"
)

func BenchmarkBoundedDiscoveryPlan(b *testing.B) {
	tasks := []discoveryTask{
		{Name: "dsh", Run: func(context.Context) (install.ComponentState, error) {
			return install.ComponentState{Name: "DSH", LocalVersion: "0.2.0", LatestVersion: "0.2.0"}, nil
		}},
		{Name: "codex", Run: func(context.Context) (install.ComponentState, error) {
			return install.ComponentState{Name: "Codex", LocalVersion: "0.150.0", LatestVersion: "0.150.0"}, nil
		}},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := runBoundedDiscovery(context.Background(), tasks); err != nil {
			b.Fatal(err)
		}
	}
}
