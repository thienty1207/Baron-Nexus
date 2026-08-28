package install

import "testing"

func BenchmarkNormalizeVersion(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := NormalizeVersion("uv 0.12.6 (abcdef)"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkComponentState(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := componentState("uv", "uv 0.12.6", "v0.12.6", true); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkInstallStateScenarios(b *testing.B) {
	scenarios := []struct {
		name   string
		local  string
		latest string
	}{
		{name: "repeat-all-latest", local: "0.12.6", latest: "0.12.6"},
		{name: "one-component-update", local: "0.12.5", latest: "0.12.6"},
	}
	for _, scenario := range scenarios {
		scenario := scenario
		b.Run(scenario.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := componentState("uv", scenario.local, scenario.latest, true); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
