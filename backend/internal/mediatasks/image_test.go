package mediatasks

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func testModelImage(t *testing.T, format string) []byte {
	t.Helper()
	canvas := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	canvas.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	var out bytes.Buffer
	var err error
	if format == "jpeg" {
		err = jpeg.Encode(&out, canvas, nil)
	} else {
		err = png.Encode(&out, canvas)
	}
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func modelImageJSON(format string, data []byte) json.RawMessage {
	value, _ := json.Marshal("data:image/" + format + ";base64," + base64.StdEncoding.EncodeToString(data))
	return value
}

func TestModelImageValidationAndSingleTaskUnits(t *testing.T) {
	for _, format := range []string{"png", "jpeg"} {
		data := testModelImage(t, format)
		r, units, digest, err := Normalize(Model3D, Request{Model: TripoModel, Image: modelImageJSON(format, data)})
		if err != nil || units != 1 || len(digest) != 64 || !bytes.Equal(data, r.imageFile) || r.imageMIME != "image/"+format {
			t.Fatalf("%s validation/units: %v", format, err)
		}
		_, _, replayDigest, err := Normalize(Model3D, r)
		if err != nil || digest != replayDigest {
			t.Fatal("unstable image digest")
		}
		_, _, textDigest, err := Normalize(Model3D, Request{Model: TripoModel, Prompt: "a toy"})
		if err != nil || textDigest == digest {
			t.Fatal("text/image identities collided")
		}
	}
	// A real lossless WebP, converted before the reservation to Files-compatible PNG.
	webp, _ := base64.StdEncoding.DecodeString("UklGRh4AAABXRUJQVlA4TBEAAAAvAAAAAAfQ//73v/+BiOh/AAA=")
	r, _, _, err := Normalize(Model3D, Request{Model: TripoModel, Image: modelImageJSON("webp", webp)})
	if err != nil || r.imageMIME != "image/png" || !bytes.HasPrefix(r.imageFile, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("WebP conversion: %v", err)
	}
}

func TestModelImageRejectsInvalidInputBeforeAnyProviderOrReservation(t *testing.T) {
	pngBytes := testModelImage(t, "png")
	dimensions := func(width, height uint32) json.RawMessage {
		data := append([]byte(nil), pngBytes...)
		binary.BigEndian.PutUint32(data[16:20], width)
		binary.BigEndian.PutUint32(data[20:24], height)
		binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
		return modelImageJSON("png", data)
	}
	invalid := []json.RawMessage{
		json.RawMessage(`null`), json.RawMessage(`{}`), json.RawMessage(`""`),
		json.RawMessage(`"https://example.invalid/photo.png"`), json.RawMessage(`"file_abc123"`),
		json.RawMessage(`"data:image/png;base64,"`), json.RawMessage(`"data:image/png;base64,!!!!"`),
		json.RawMessage(`"data:image/png;base64,YQ==\n"`),
		modelImageJSON("jpeg", pngBytes), modelImageJSON("gif", pngBytes),
		modelImageJSON("png", pngBytes[:len(pngBytes)/2]),
		modelImageJSON("png", pngBytes[:len(pngBytes)-6]),
		dimensions(0, 1), dimensions(MaxModelImageDimension+1, 1), dimensions(8192, 4097),
		modelImageJSON("png", make([]byte, MaxModelImageBytes+1)),
	}
	jpegBytes := testModelImage(t, "jpeg")
	invalid = append(invalid, modelImageJSON("jpeg", jpegBytes[:len(jpegBytes)-2]))
	webp, _ := base64.StdEncoding.DecodeString("UklGRh4AAABXRUJQVlA4TBEAAAAvAAAAAAfQ//73v/+BiOh/AAA=")
	binary.LittleEndian.PutUint32(webp[4:8], uint32(len(webp))) // Declares eight bytes beyond EOF.
	invalid = append(invalid, modelImageJSON("webp", webp))
	for i, data := range invalid {
		if _, _, _, err := Normalize(Model3D, Request{Model: TripoModel, Image: data}); !errors.Is(err, ErrImageInvalid) {
			t.Fatalf("input %d accepted: %v", i, err)
		}
	}
	for _, r := range []Request{
		{Model: TripoModel}, {Model: TripoModel, Prompt: "  "},
		{Model: TripoModel, Prompt: "a toy", Image: modelImageJSON("png", pngBytes)},
		{Model: TripoModel, Image: modelImageJSON("png", pngBytes), Input: "extra"},
	} {
		if _, _, _, err := Normalize(Model3D, r); !errors.Is(err, ErrInvalid) {
			t.Fatal("accepted mixed/empty request")
		}
	}
	engine := NewEngine(nil, nil, nil, nil)
	if _, err := engine.Create(context.Background(), Owner{}, Model3D, "request", Request{Model: TripoModel, Image: invalid[0]}); !errors.Is(err, ErrImageInvalid) {
		t.Fatal("invalid input reached downstream dependencies")
	}
	var bounded modelImageBuffer
	if _, err := bounded.Write(make([]byte, MaxModelImageBytes)); err != nil {
		t.Fatal(err)
	}
	if _, err := bounded.Write([]byte{1}); err == nil {
		t.Fatal("converted image exceeded 20 MiB")
	}
}

func TestTripoImageUploadsBytesThenUsesOnlyOfficialToken(t *testing.T) {
	for _, format := range []string{"png", "jpeg", "webp"} {
		t.Run(format, func(t *testing.T) {
			data := testModelImage(t, format)
			if format == "webp" {
				data, _ = base64.StdEncoding.DecodeString("UklGRh4AAABXRUJQVlA4TBEAAAAvAAAAAAfQ//73v/+BiOh/AAA=")
			}
			r, _, _, err := Normalize(Model3D, Request{Model: TripoModel, Image: modelImageJSON(format, data)})
			if err != nil {
				t.Fatal(err)
			}
			var paths []string
			supplier := &OfficialSupplier{Client: fixtureClient(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodPost || req.URL.Host != "openapi.tripo3d.com" || req.Header.Get("Authorization") != "Bearer fixture" {
					t.Fatal("wrong upload/generation route")
				}
				paths = append(paths, req.URL.Path)
				if len(paths) == 1 {
					if req.URL.Path != "/v3/files" {
						t.Fatal(paths)
					}
					kind, params, e := mime.ParseMediaType(req.Header.Get("Content-Type"))
					if e != nil || kind != "multipart/form-data" {
						t.Fatal("multipart")
					}
					parts := multipart.NewReader(req.Body, params["boundary"])
					part, e := parts.NextPart()
					if e != nil || part.FormName() != "file" || part.Header.Get("Content-Type") != r.imageMIME {
						t.Fatal("file field/MIME")
					}
					got, _ := io.ReadAll(part)
					if !bytes.Equal(got, r.imageFile) {
						t.Fatal("wrong uploaded bytes")
					}
					if _, e = parts.NextPart(); e != io.EOF {
						t.Fatal("extra multipart field")
					}
					return response(200, `{"code":0,"data":{"file_token":"file_abc123"}}`), nil
				}
				if req.URL.Path != "/v3/generation/image-to-model" {
					t.Fatal(paths)
				}
				var body map[string]any
				if json.NewDecoder(req.Body).Decode(&body) != nil {
					t.Fatal("generation body")
				}
				if body["input"] != "file_abc123" || body["model"] != TripoNativeModel || body["pbr"] != true || body["texture"] != true || body["texture_quality"] != "standard" || body["geometry_quality"] != "standard" || body["face_limit"] != float64(50000) || len(body) != 10 {
					t.Fatal("fixed standard profile")
				}
				if _, exists := body["prompt"]; exists {
					t.Fatal("image generation mixed in prompt")
				}
				return response(200, `{"code":0,"data":{"task_id":"original-task"}}`), nil
			})}
			out := supplier.Submit(context.Background(), Provider{Kind: Model3D, Key: "fixture"}, Owner{}, "original-request", r)
			if out.Status != "running" || out.ProviderID != "original-task" || len(paths) != 2 {
				t.Fatal(out, paths)
			}
		})
	}
}

func TestTripoUploadFailuresNeverSendGeneration(t *testing.T) {
	for _, fixture := range []struct {
		name      string
		status    int
		body      string
		transport bool
	}{
		{"transport", 0, "", true}, {"server", 503, `{}`, false}, {"redirect", 302, `{}`, false},
		{"rejected", 422, `{}`, false}, {"auth", 401, `{}`, false}, {"rate", 429, `{}`, false},
		{"malformed", 200, `not-json`, false}, {"error", 200, `{"code":1}`, false},
		{"missing", 200, `{"code":0,"data":{}}`, false},
		{"url-token", 200, `{"code":0,"data":{"file_token":"https://evil.invalid/file"}}`, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			r, _, _, err := Normalize(Model3D, Request{Model: TripoModel, Image: modelImageJSON("png", testModelImage(t, "png"))})
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			supplier := &OfficialSupplier{Client: fixtureClient(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.Path != "/v3/files" || calls != 1 {
					t.Fatal("generation reached after upload failed")
				}
				if fixture.transport {
					return nil, errors.New("private upstream failure must not escape")
				}
				return response(fixture.status, fixture.body), nil
			})}
			out := supplier.Submit(context.Background(), Provider{Kind: Model3D, Key: "fixture"}, Owner{}, "original-request", r)
			if out.Status != "failed" || out.SettlementUnits == nil || *out.SettlementUnits != 0 || calls != 1 || strings.Contains(out.ErrorCode, "private") {
				t.Fatal(out, calls)
			}
		})
	}
}

type imageTaskStore struct {
	sync.Mutex
	task *Task
}

func (s *imageTaskStore) Find(_ context.Context, o Owner, kind, request string) (*Task, error) {
	s.Lock()
	defer s.Unlock()
	if s.task == nil || s.task.Owner != o || s.task.Kind != kind || s.task.RequestID != request {
		return nil, ErrNotFound
	}
	copy := *s.task
	return &copy, nil
}
func (s *imageTaskStore) Create(_ context.Context, task *Task) (*Task, bool, error) {
	s.Lock()
	defer s.Unlock()
	if s.task != nil {
		copy := *s.task
		return &copy, false, nil
	}
	copy := *task
	s.task = &copy
	return task, true, nil
}
func (s *imageTaskStore) Get(_ context.Context, o Owner, kind, id string) (*Task, error) {
	s.Lock()
	defer s.Unlock()
	if s.task == nil || s.task.Owner != o || s.task.Kind != kind || s.task.ID != id {
		return nil, ErrNotFound
	}
	copy := *s.task
	return &copy, nil
}
func (s *imageTaskStore) Start(context.Context, string) (bool, error) { return true, nil }
func (s *imageTaskStore) Update(_ context.Context, task *Task, _ []byte) error {
	s.Lock()
	defer s.Unlock()
	copy := *task
	s.task = &copy
	return nil
}
func (s *imageTaskStore) Content(context.Context, Owner, string, string) ([]byte, string, error) {
	return nil, "", ErrNotFound
}

type imageQuota struct {
	reserved int
	units    int64
}

func (q *imageQuota) Reserve(_ context.Context, _ Owner, _ string, _ string, _ string, _ string, units int64) error {
	q.reserved++
	q.units = units
	return nil
}
func (*imageQuota) Bind(context.Context, int64, string, string) error                  { return nil }
func (*imageQuota) Validate(context.Context, int64, string) error                      { return nil }
func (*imageQuota) Settle(context.Context, int64, string, string, int64, string) error { return nil }

type imageRoutes struct{}

func (imageRoutes) Resolve(context.Context, Owner, string, string) (Provider, error) {
	return Provider{Kind: Model3D, Key: "fixture"}, nil
}
func (imageRoutes) Reopen(context.Context, Owner, *Task) (Provider, error) {
	return Provider{Kind: Model3D, Key: "fixture"}, nil
}

func TestImageTaskReplayAndRestartNeverUploadOrGenerateAgain(t *testing.T) {
	for _, uploadFails := range []bool{true, false} {
		t.Run(map[bool]string{true: "upload-failed", false: "generation-unknown"}[uploadFails], func(t *testing.T) {
			store, quota := &imageTaskStore{}, &imageQuota{}
			var mu sync.Mutex
			uploads, generations := 0, 0
			supplier := &OfficialSupplier{Client: fixtureClient(func(req *http.Request) (*http.Response, error) {
				mu.Lock()
				defer mu.Unlock()
				if req.URL.Path == "/v3/files" {
					uploads++
					if uploadFails {
						return nil, errors.New("upload timeout")
					}
					return response(200, `{"code":0,"data":{"file_token":"file_original"}}`), nil
				}
				generations++
				return nil, errors.New("generation timeout")
			})}
			engine := NewEngine(store, quota, imageRoutes{}, supplier)
			r := Request{Model: TripoModel, Image: modelImageJSON("png", testModelImage(t, "png"))}
			owner := Owner{UserID: 1, APIKeyID: 2, GroupID: 3}
			first, err := engine.Create(context.Background(), owner, Model3D, "original-request", r)
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(3 * time.Second)
			var task *Task
			for time.Now().Before(deadline) {
				task, err = store.Get(context.Background(), owner, Model3D, first.ID)
				if err == nil && (task.Status == "failed" || task.Status == "indeterminate") {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if task == nil || (task.Status != "failed" && task.Status != "indeterminate") {
				t.Fatal("execution did not finish")
			}
			// A reconstructed engine has only the durable original receipt, never persisted input.
			restarted := NewEngine(store, quota, imageRoutes{}, supplier)
			for range 3 {
				replay, e := restarted.Create(context.Background(), owner, Model3D, "original-request", r)
				if e != nil || replay.ID != first.ID {
					t.Fatal("did not replay original task")
				}
				if _, e = restarted.Get(context.Background(), owner, Model3D, first.ID); e != nil {
					t.Fatal(e)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			wantGenerations := 1
			if uploadFails {
				wantGenerations = 0
			}
			if uploads != 1 || generations != wantGenerations || quota.reserved != 1 || quota.units != 1 {
				t.Fatal("duplicate upload/generation/reservation")
			}
			if uploadFails {
				if task.SettlementUnits == nil || *task.SettlementUnits != 0 {
					t.Fatal("upload failure consumed task")
				}
			} else if task.SettlementUnits != nil {
				t.Fatal("unknown generation released allowance")
			}
		})
	}
}
