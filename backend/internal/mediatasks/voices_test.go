package mediatasks

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestOfficialPresetsAreUniqueAndAcceptedWithoutPrivateCloneSlots(t *testing.T) {
	if len(Voices) != 473 || Voices[0].ID != "zh_female_vv_uranus_bigtts" {
		t.Fatal("unexpected official catalog or changed default")
	}
	seen := map[string]bool{}
	pattern := regexp.MustCompile(`^[A-Za-z0-9_-]{1,160}$`)
	native, icl := 0, 0
	for _, voice := range Voices {
		if seen[voice.ID] || !pattern.MatchString(voice.ID) || voice.Name == "" {
			t.Fatalf("invalid or duplicate public preset: %q", voice.ID)
		}
		seen[voice.ID] = true
		sample, err := url.Parse(voice.PreviewURL)
		if err != nil || sample.Scheme != "https" || sample.Host != "lf3-static.bytednsdoc.com" || sample.User != nil || sample.RawQuery != "" || sample.Fragment != "" || !strings.HasPrefix(sample.Path, "/obj/eden-cn/lm_hz_ihsph/ljhwZthlaukjlkulzlp/portal/bigtts/") {
			t.Fatalf("unexpected official sample for %q", voice.ID)
		}
		if strings.HasPrefix(voice.ID, "ICL_") {
			icl++
		} else if strings.HasSuffix(voice.ID, "_uranus_bigtts") {
			native++
		} else {
			t.Fatalf("unexpected preset resource: %q", voice.ID)
		}
		if _, _, _, err := Normalize(Audio, Request{Model: SpeechModel, Input: "目录验证", Voice: voice.ID, Speed: 1}); err != nil {
			t.Fatalf("documented preset rejected: %q: %v", voice.ID, err)
		}
	}
	if native != 266 || icl != 207 {
		t.Fatalf("unexpected preset split: %d/%d", native, icl)
	}
	for _, id := range []string{"S_private-slot", "ICL_arbitrary", "not-a-voice"} {
		if ValidVoice(id) {
			t.Fatalf("unreviewed speaker accepted: %q", id)
		}
	}
}
