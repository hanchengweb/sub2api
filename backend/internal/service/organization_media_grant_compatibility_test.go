package service

import (
	"encoding/json"
	"testing"
	"time"
)

// Existing image/video grant receipts have a persisted SHA-256 of this exact
// serialization. Adding zero-value new media fields must not change it.
func TestMediaGrantLegacyDigestCompatibility(t *testing.T) {
	type legacyGrant struct {
		RequestID       string    `json:"request_id"`
		ExpectedVersion int64     `json:"expected_version"`
		ContractRef     string    `json:"contract_ref"`
		Reason          string    `json:"reason"`
		Images          int64     `json:"images"`
		VideoSeconds    int64     `json:"video_seconds"`
		EffectiveAt     time.Time `json:"effective_at"`
		ExpiresAt       time.Time `json:"expires_at"`
	}
	old := legacyGrant{RequestID: "legacy-request", ExpectedVersion: 3, ContractRef: "contract", Reason: "fixture", Images: 10, VideoSeconds: 25, EffectiveAt: time.Unix(100, 0).UTC(), ExpiresAt: time.Unix(200, 0).UTC()}
	now := MediaQuotaGrant{RequestID: old.RequestID, ExpectedVersion: old.ExpectedVersion, ContractRef: old.ContractRef, Reason: old.Reason, Images: old.Images, VideoSeconds: old.VideoSeconds, EffectiveAt: old.EffectiveAt, ExpiresAt: old.ExpiresAt}
	before, _ := json.Marshal(old)
	after, _ := json.Marshal(now)
	if string(before) != string(after) {
		t.Fatalf("legacy grant digest input changed:\n%s\n%s", before, after)
	}
}
