package mediatasks

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

func testMP3() []byte { b := make([]byte, 417); copy(b, []byte{255, 251, 144, 0}); return b }
func testGLB(doc string) []byte {
	for len(doc)%4 != 0 {
		doc += " "
	}
	b := make([]byte, 20+len(doc))
	copy(b, "glTF")
	binary.LittleEndian.PutUint32(b[4:], 2)
	binary.LittleEndian.PutUint32(b[8:], uint32(len(b)))
	binary.LittleEndian.PutUint32(b[12:], uint32(len(doc)))
	binary.LittleEndian.PutUint32(b[16:], 0x4E4F534A)
	copy(b[20:], doc)
	return b
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fixtureClient(f transportFunc) *http.Client                          { return &http.Client{Transport: f} }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestSpeechV2ProtocolAndTerminalReceipt(t *testing.T) {
	s := &OfficialSupplier{Client: fixtureClient(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://openspeech.bytedance.com/api/v3/tts/unidirectional/sse" || req.Header.Get("Authorization") != "" || req.Header.Get("X-Api-Key") != "fixture" || req.Header.Get("X-Api-Resource-Id") != SpeechModel {
			t.Fatal("incorrect official speech route/auth")
		}
		var body map[string]any
		if json.NewDecoder(req.Body).Decode(&body) != nil {
			t.Fatal("body")
		}
		params, ok := body["req_params"].(map[string]any)
		if !ok {
			t.Fatal("req_params shape")
		}
		audio, ok := params["audio_params"].(map[string]any)
		if !ok {
			t.Fatal("audio_params shape")
		}
		if _, bad := audio["emotion_scale"]; bad {
			t.Fatal("TTS1 parameter leaked")
		}
		if audio["speech_rate"] != float64(25) {
			t.Fatal(audio)
		}
		var additions map[string][]string
		direction, ok := params["additions"].(string)
		if !ok {
			t.Fatal("additions must be a JSON string")
		}
		if json.Unmarshal([]byte(direction), &additions) != nil || len(additions["context_texts"]) != 1 || !strings.Contains(additions["context_texts"][0], "老师") {
			t.Fatal("instruction contract")
		}
		return response(200, "event: 352\ndata: {\"code\":0,\"data\":\""+base64.StdEncoding.EncodeToString(testMP3())+"\"}\n\nevent: 152\ndata: {\"code\":20000000,\"data\":null,\"usage\":{\"text_words\":2}}\n\n"), nil
	})}
	r, _, _, err := Normalize(Audio, Request{Model: SpeechModel, Input: "你好", Scene: "teaching", Emotion: "tender", Speed: 1.25})
	if err != nil {
		t.Fatal(err)
	}
	out := s.Submit(context.Background(), Provider{Kind: Audio, Key: "fixture"}, Owner{UserID: 7}, "123e4567-e89b-42d3-a456-426614174000", r)
	if out.Status != "succeeded" || !bytes.Equal(out.Content, testMP3()) || !strings.Contains(string(out.ProviderUsage), "text_words") {
		t.Fatal(out.Status, string(out.ProviderUsage))
	}
}
func TestCloneSynthesisPinsResourceSpeakerAndExpressiveInstructions(t *testing.T) {
	r, units, _, err := Normalize(Audio, Request{Model: CloneModel, Input: "你好", VoiceAssetID: "123e4567-e89b-42d3-a456-426614174000", Scene: "teaching", Emotion: "tender", Speed: 1})
	if err != nil || units != 2 {
		t.Fatal(err, units)
	}
	r.ResolvedVoice, r.ResourceID = "S_private", CloneModel
	s := &OfficialSupplier{Client: fixtureClient(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("X-Api-Resource-Id") != CloneModel {
			t.Fatal("wrong clone resource")
		}
		var body map[string]any
		if json.NewDecoder(req.Body).Decode(&body) != nil {
			t.Fatal("body")
		}
		p := body["req_params"].(map[string]any)
		if p["speaker"] != "S_private" || p["model"] != "seed-tts-2.0-expressive" {
			t.Fatal(p)
		}
		if _, ok := p["additions"].(string); !ok {
			t.Fatal("additions must be string")
		}
		return response(400, `{}`), nil
	})}
	s.Submit(context.Background(), Provider{Kind: Audio, Key: "fixture"}, Owner{UserID: 7}, "request", r)
	for _, bad := range []Request{{Model: CloneModel, Input: "你好", Voice: "S_private", Speed: 1}, {Model: CloneModel, Input: "你好", VoiceAssetID: r.VoiceAssetID, Voice: Voices[0].ID, Speed: 1}, {Model: SpeechModel, Input: "你好", VoiceAssetID: "bad", Speed: 1}} {
		if _, _, _, e := Normalize(Audio, bad); e == nil {
			t.Fatal("accepted unowned/raw/conflicting clone")
		}
	}
}
func TestSpeechPartialInvalidAndOversizeNeverSettle(t *testing.T) {
	for _, body := range []string{"data: {\"code\":0,\"data\":\"" + base64.StdEncoding.EncodeToString(testMP3()) + "\"}\n", "data: {\"code\":20000000}\n", "data: {\"code\":0,\"data\":\"SUQzZ2FyYmFnZQ==\"}\ndata: {\"code\":20000000}\n", "data: {\"code\":0,\"data\":\"bad%%\"}\n"} {
		out := ParseSpeechSSE(strings.NewReader(body))
		if out.Status != "indeterminate" || out.SettlementUnits != nil {
			t.Fatal(out)
		}
	}
	if ValidMP3([]byte("ID3garbage")) {
		t.Fatal("tag alone is not audio")
	}
}

func TestSubmitTimeoutHTTPRetainsAllowance(t *testing.T) {
	for _, status := range []int{408, 409, 499, 500, 503} {
		out := rejectedHTTP(status)
		if out.Status != "indeterminate" || out.SettlementUnits != nil {
			t.Fatalf("status %d released allowance", status)
		}
	}
	for _, status := range []int{400, 401, 402, 403, 422, 429} {
		out := rejectedHTTP(status)
		if out.Status != "failed" || out.SettlementUnits == nil || *out.SettlementUnits != 0 {
			t.Fatalf("status %d was not a confirmed rejection", status)
		}
	}
}
func TestTripoFixedProfileAndUnknownStatus(t *testing.T) {
	s := &OfficialSupplier{Client: fixtureClient(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Authorization") != "Bearer fixture" {
			t.Fatal("auth")
		}
		if req.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(req.Body).Decode(&body)
			if body["model"] != TripoNativeModel || body["texture_version"] != "v3.0-20250812" || body["face_limit"] != float64(50000) || body["quad"] != false || body["generate_parts"] != false || body["geometry_quality"] != "standard" {
				t.Fatal(body)
			}
			return response(200, `{"code":0,"data":{"task_id":"owned-task"}}`), nil
		}
		return response(200, `{"code":0,"data":{"task_id":"owned-task","status":"unknown","progress":10}}`), nil
	})}
	out := s.Submit(context.Background(), Provider{Kind: Model3D, Key: "fixture"}, Owner{}, "request", Request{Prompt: "a toy"})
	if out.Status != "running" || out.ProviderID != "owned-task" {
		t.Fatal(out)
	}
	out = s.Poll(context.Background(), Provider{Kind: Model3D, Key: "fixture"}, out.ProviderID)
	if out.Status != "indeterminate" || out.ProviderID != "owned-task" || out.SettlementUnits != nil {
		t.Fatal(out)
	}
}
func TestModelDownloadAndSelfContainedValidation(t *testing.T) {
	good := testGLB(`{"asset":{"version":"2.0"},"scenes":[{}]}`)
	if ValidateGLB(good) != nil {
		t.Fatal("valid GLB")
	}
	for _, b := range [][]byte{[]byte("glTF"), testGLB(`{"asset":{"version":"1.0"}}`), testGLB(`{"asset":{"version":"2.0"},"buffers":[{"uri":"https://evil.test/a.bin","byteLength":4}]}`), testGLB(`{"asset":{"version":"2.0"},"extensions":{"custom":{"uri":"https://evil.test"}}}`), append(append([]byte{}, good...), 0)} {
		if ValidateGLB(b) == nil {
			t.Fatal("accepted malformed/external GLB")
		}
	}
	for _, raw := range []string{"http://cdn.tripo3d.ai/a.glb", "https://cdn.tripo3d.ai.evil.test/a.glb", "https://user@cdn.tripo3d.ai/a.glb", "https://cdn.tripo3d.ai:8443/a.glb", "https://127.0.0.1/a.glb", "https://cdn.tripo3d.ai/a.glb#fragment"} {
		if _, err := ApprovedModelURL(raw); err == nil {
			t.Fatal(raw)
		}
	}
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "100.64.1.1", "169.254.169.254", "192.0.2.1", "::1", "::ffff:127.0.0.1", "fc00::1", "fec0::1", "2002:7f00:1::"} {
		if PublicIP(netip.MustParseAddr(ip)) {
			t.Fatal(ip)
		}
	}
	if !PublicIP(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("public address")
	}
}
func TestRequestBoundsAndProductUnits(t *testing.T) {
	r, n, digest, err := Normalize(Audio, Request{Model: SpeechModel, Input: "  你好 😀  ", Speed: 1})
	if err != nil || n != 4 || r.Input != "你好 😀" || len(digest) != 64 {
		t.Fatal(r, n, err)
	}
	for _, bad := range []Request{{Model: SpeechModel, Input: "<speak>你好</speak>", Speed: 1}, {Model: SpeechModel, Input: "你好", Voice: "arbitrary", Speed: 1}, {Model: SpeechModel, Input: "你好", Speed: 0}, {Model: SpeechModel, Input: "你好", Speed: 2.1}} {
		if _, _, _, err := Normalize(Audio, bad); err == nil {
			t.Fatal(bad)
		}
	}
	if _, _, _, err := Normalize(Model3D, Request{Model: TripoModel, Image: json.RawMessage(`{}`)}); err != ErrImageUnavailable {
		t.Fatal(err)
	}
}
