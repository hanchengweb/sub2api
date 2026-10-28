package service

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMediaQuotaVideoUnitsAreBoundedBeforeSubmission(t *testing.T) {
	for _, tc := range []struct {
		model    string
		duration int
		want     int64
		invalid  bool
	}{
		{"seedance-2-fast", 0, 5, false}, {"seedance-2-fast", 15, 15, false}, {"seedance-2-fast", -1, 0, true},
		{"seedance-2-fast", 16, 0, true}, {"unknown-provider", 5, 0, true},
		// gemini_omni_flash bills only the four tiers the upstream sells.
		{"gemini_omni_flash", 0, 8, false}, {"gemini_omni_flash", 4, 4, false}, {"gemini_omni_flash", 10, 10, false},
		{"gemini_omni_flash", 5, 0, true}, {"gemini_omni_flash", 3, 0, true}, {"gemini_omni_flash", 12, 0, true},
	} {
		t.Run(tc.model+string(rune(tc.duration+100)), func(t *testing.T) {
			n, err := MediaQuotaVideoUnits(tc.model, tc.duration)
			if tc.invalid {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, n)
			}
		})
	}
}

func TestMediaQuotaVideoUnitsExplainWhatToChange(t *testing.T) {
	var detail *MediaQuotaVideoDurationError
	_, err := MediaQuotaVideoUnits("unknown-provider", 5)
	require.ErrorAs(t, err, &detail)
	require.Nil(t, detail.Rule, "A model without a contract is an organization setting, not a duration")
	require.Equal(t, "media_quota_requires_fixed_video_duration", err.Error())

	_, err = MediaQuotaVideoUnits("gemini_omni_flash", 5)
	require.ErrorAs(t, err, &detail)
	require.Equal(t, "media_quota_invalid_video_duration", err.Error())
	require.Equal(t, VideoQuotaDurationRule{MinSeconds: 4, MaxSeconds: 10, DefaultSeconds: 8, AllowedSeconds: []int{4, 6, 8, 10}}, *detail.Rule)
	require.Equal(t, "4、6、8、10 秒", detail.Rule.Describe())

	_, err = MediaQuotaVideoUnits("kling-v3", 20)
	require.ErrorAs(t, err, &detail)
	require.Equal(t, "3-15 秒", detail.Rule.Describe())
	_, err = MediaQuotaVideoUnits("seedance-2-fast", -1)
	require.ErrorAs(t, err, &detail)
	require.NotNil(t, detail.Rule)
	require.Equal(t, "media_quota_requires_fixed_video_duration", err.Error())
}

func TestGeminiOmniFlashPersonalBillingStaysWithinTheUpstreamRange(t *testing.T) {
	require.Equal(t, 8, NormalizeVideoBillingDurationForModel("gemini_omni_flash", 0))
	require.Equal(t, 10, NormalizeVideoBillingDurationForModel("gemini_omni_flash", 15))
	require.Equal(t, 4, NormalizeVideoBillingDurationForModel("gemini_omni_flash", 2))
	require.Equal(t, VideoBillingResolution720P, NormalizeVideoBillingResolutionForModel("gemini_omni_flash", ""))
}
