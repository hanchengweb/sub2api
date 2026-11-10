package mediatasks

import (
	"context"
	"errors"
	"testing"
)

type imageGateRoutes struct {
	Routes
	resolves int
}

func (r *imageGateRoutes) Resolve(context.Context, Owner, string, string) (Provider, error) {
	r.resolves++
	return Provider{}, ErrRoute
}

func TestModelImageGateDefaultsOffBeforeAnyWriteOrSupplierCall(t *testing.T) {
	for _, value := range []string{"", "false", "TRUE"} {
		t.Run("env="+value, func(t *testing.T) {
			t.Setenv("WXM_MODEL3D_IMAGE_ENABLED", value)
			store, routes := &imageTaskStore{}, &imageGateRoutes{}
			// Nil quota/supplier make any accidental reservation, upload or submit fail.
			engine := NewEngine(store, nil, routes, nil)
			request := Request{Model: TripoModel, Image: modelImageJSON("png", testModelImage(t, "png"))}
			_, err := engine.Create(context.Background(), Owner{UserID: 1}, Model3D, "new-image-request", request)
			if !errors.Is(err, ErrImageUnavailable) || routes.resolves != 0 || store.task != nil {
				t.Fatalf("disabled image task crossed side-effect boundary: %v", err)
			}
			if engine.ModelImageEnabled() {
				t.Fatal("image gate must require explicit true")
			}
			// Existing text 3D and audio continue to their normal route boundary.
			for _, tc := range []struct {
				kind    string
				request Request
			}{
				{Model3D, Request{Model: TripoModel, Prompt: "a toy"}},
				{Audio, Request{Model: SpeechModel, Input: "hello", Speed: 1}},
			} {
				_, err = engine.Create(context.Background(), Owner{UserID: 1}, tc.kind, "legacy-request", tc.request)
				if !errors.Is(err, ErrRoute) {
					t.Fatalf("image gate changed %s: %v", tc.kind, err)
				}
			}
			if routes.resolves != 2 || store.task != nil {
				t.Fatal("legacy paths or disabled image persistence changed")
			}
		})
	}
}
