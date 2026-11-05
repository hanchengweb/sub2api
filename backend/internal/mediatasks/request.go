package mediatasks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

type Choice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

var Voices = []Choice{{"zh_female_vv_uranus_bigtts", "Vivi2.0"}, {"zh_female_xiaohe_uranus_bigtts", "小何2.0"}, {"zh_male_m191_uranus_bigtts", "云舟2.0"}, {"zh_male_taocheng_uranus_bigtts", "小天2.0"}}
var Scenes = []Choice{{"general", "通用"}, {"teaching", "教学讲解"}, {"narration", "旁白"}, {"encouragement", "鼓励"}, {"storytelling", "讲故事"}, {"customer_service", "客服"}}
var Emotions = []Choice{{"neutral", "自然"}, {"happy", "愉快"}, {"sad", "悲伤"}, {"angry", "生气"}, {"tender", "温柔"}, {"excited", "兴奋"}, {"serious", "严肃"}}

func inChoices(value string, choices []Choice) bool {
	for _, v := range choices {
		if value == v.ID {
			return true
		}
	}
	return false
}
func plain(s string) bool {
	if !utf8.ValidString(s) || strings.ContainsAny(s, "<>") {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
	}
	return true
}

// Normalize materializes all defaults before hashing so equivalent retries replay.
func Normalize(kind string, r Request) (Request, int64, string, error) {
	var units int64
	switch kind {
	case Audio:
		r.Input, r.Instruction = strings.TrimSpace(r.Input), strings.TrimSpace(r.Instruction)
		if (r.Model != SpeechModel && !(r.Model == CloneModel && r.VoiceAssetID != "")) || r.Prompt != "" || len(r.Image) > 0 || !plain(r.Input) || !plain(r.Instruction) || utf8.RuneCountInString(r.Input) < 1 || utf8.RuneCountInString(r.Input) > 2000 || utf8.RuneCountInString(r.Instruction) > 300 {
			return r, 0, "", ErrInvalid
		}
		if r.VoiceAssetID != "" {
			id, err := uuid.Parse(r.VoiceAssetID)
			if err != nil || id == uuid.Nil || r.Voice != "" {
				return r, 0, "", ErrInvalid
			}
			r.VoiceAssetID = id.String()
		} else if r.Voice == "" {
			r.Voice = Voices[0].ID
		}
		if r.Scene == "" {
			r.Scene = "general"
		}
		if r.Emotion == "" {
			r.Emotion = "neutral"
		}
		if r.ResponseFormat == "" {
			r.ResponseFormat = "mp3"
		}
		if (r.VoiceAssetID == "" && !inChoices(r.Voice, Voices)) || !inChoices(r.Scene, Scenes) || !inChoices(r.Emotion, Emotions) || math.IsNaN(r.Speed) || math.IsInf(r.Speed, 0) || r.Speed < 0.5 || r.Speed > 2 || r.ResponseFormat != "mp3" {
			return r, 0, "", ErrInvalid
		}
		units = int64(utf8.RuneCountInString(r.Input))
	case Model3D:
		if len(r.Image) > 0 {
			return r, 0, "", ErrImageUnavailable
		}
		r.Prompt = strings.TrimSpace(r.Prompt)
		if r.Model != TripoModel || !plain(r.Prompt) || utf8.RuneCountInString(r.Prompt) < 1 || utf8.RuneCountInString(r.Prompt) > 1024 || r.Input != "" || r.Voice != "" || r.VoiceAssetID != "" || r.Scene != "" || r.Emotion != "" || r.Instruction != "" || r.Speed != 0 || r.ResponseFormat != "" {
			return r, 0, "", ErrInvalid
		}
		units = 1
	default:
		return r, 0, "", ErrInvalid
	}
	body, _ := json.Marshal(r)
	h := sha256.Sum256(append([]byte(kind+":"), body...))
	return r, units, hex.EncodeToString(h[:]), nil
}

// ContextTexts is one documented TTS2 instruction, not TTS1 emotion_scale.
func ContextTexts(r Request) string {
	scene := map[string]string{"general": "请自然清晰地朗读。", "teaching": "请像耐心的老师一样讲解，重点清楚，停顿适当。", "narration": "请以连贯、清晰的旁白语气朗读。", "encouragement": "请以真诚、积极、鼓励的口吻朗读。", "storytelling": "请以富有画面感、节奏生动的讲故事语气朗读。", "customer_service": "请以礼貌、耐心、专业的客服语气朗读。"}
	emotion := map[string]string{"neutral": "语气自然平和。", "happy": "请表达愉快的情绪。", "sad": "请表达克制而真实的悲伤。", "angry": "请表达生气的情绪，但保持语言清晰。", "tender": "语气温柔、亲切。", "excited": "请表达兴奋、充满活力的情绪。", "serious": "语气严肃、认真。"}
	return strings.TrimSpace(scene[r.Scene] + emotion[r.Emotion] + r.Instruction)
}
