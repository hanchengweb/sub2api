package service

import "testing"

func TestVideoQuotaAuthorizedAliasUsesCanonicalBounds(t *testing.T) {
	canonical, known := videoUpstreamLimitForModel("grok-video-1.5")
	if !known {
		t.Fatal("canonical model must be recognized")
	}
	for _, model := range []string{"t-grok-video-1.5", " T-GROK-VIDEO-1.5 "} {
		alias, ok := videoUpstreamLimitForModel(model)
		if !ok || alias != canonical || alias.MinSeconds != 6 || alias.MaxSeconds != 30 {
			t.Fatalf("alias has different duration bounds: %+v", alias)
		}
		if duration := videoDefaultDurationForModel(model); duration != 8 {
			t.Fatalf("unexpected default: %d", duration)
		}
	}
	if _, known := videoUpstreamLimitForModel("t-unknown-video"); known {
		t.Fatal("unknown aliases must remain rejected")
	}
}
