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
