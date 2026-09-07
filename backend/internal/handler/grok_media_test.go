package handler

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type grokMediaEligibilityProberStub struct {
	eligible bool
	reason   string
	err      error
	calls    int
}

func (s *grokMediaEligibilityProberStub) ProbeMediaEligibility(context.Context, int64) (bool, string, error) {
	s.calls++
	return s.eligible, s.reason, s.err
}

func TestGrokImageUploadRejectsInvalidFilesBeforeGatewayDependencies(t *testing.T) {
	newMultipart := func(t *testing.T, field, filename string, content []byte) ([]byte, string) {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile(field, filename)
		require.NoError(t, err)
		_, err = part.Write(content)
		require.NoError(t, err)
		require.NoError(t, writer.Close())
		return body.Bytes(), writer.FormDataContentType()
	}

	unsupportedBody, unsupportedContentType := newMultipart(t, "file", "reference.txt", []byte("not an image"))
	tooLargeBody, tooLargeContentType := newMultipart(
		t,
		"file",
		"reference.png",
		append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, bytes.Repeat([]byte{0}, int(service.GrokMediaImageUploadMaxBytes))...),
	)

	tests := []struct {
		name        string
		contentType string
		body        []byte
		wantStatus  int
	}{
		{
			name:        "non multipart body",
			contentType: "application/json",
			body:        []byte(`{"file":"image"}`),
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "unsupported image type",
			contentType: unsupportedContentType,
			body:        unsupportedBody,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "image exceeds ten megabytes",
			contentType: tooLargeContentType,
			body:        tooLargeBody,
			wantStatus:  http.StatusRequestEntityTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/uploads/images", bytes.NewReader(tt.body))
			c.Request.Header.Set("Content-Type", tt.contentType)
			groupID := int64(1)
			c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
				ID:      1,
				GroupID: &groupID,
				Group:   &service.Group{ID: groupID, Platform: service.PlatformGrok},
			})
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 1})

			(&OpenAIGatewayHandler{}).GrokImageUpload(c)

			require.Equal(t, tt.wantStatus, recorder.Code)
		})
	}
}

func TestShouldRecordGrokMediaUsage(t *testing.T) {
	tests := []struct {
		name     string
		endpoint service.GrokMediaEndpoint
		model    string
		want     bool
	}{
		{
			name:     "image generation records usage",
			endpoint: service.GrokMediaEndpointImagesGenerations,
			model:    "grok-imagine",
			want:     true,
		},
		{
			name:     "image edit records usage",
			endpoint: service.GrokMediaEndpointImagesEdits,
			model:    "grok-imagine-edit",
			want:     true,
		},
		{
			name:     "video generation records usage",
			endpoint: service.GrokMediaEndpointVideosGenerations,
			model:    "grok-imagine-video-1.5",
			want:     true,
		},
		{
			name:     "video status skips empty model usage",
			endpoint: service.GrokMediaEndpointVideoStatus,
			model:    "",
			want:     false,
		},
		{
			name:     "video content skips usage",
			endpoint: service.GrokMediaEndpointVideoContent,
			model:    "",
			want:     false,
		},
		{
			name:     "generation skips usage without model",
			endpoint: service.GrokMediaEndpointImagesGenerations,
			model:    " ",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, shouldRecordGrokMediaUsage(tt.endpoint, tt.model))
		})
	}
}

func TestGrokMediaRequiredCapability(t *testing.T) {
	tests := []struct {
		name     string
		endpoint service.GrokMediaEndpoint
		want     service.OpenAIEndpointCapability
	}{
		{name: "image generation", endpoint: service.GrokMediaEndpointImagesGenerations, want: service.OpenAIEndpointCapabilityGrokMediaGeneration},
		{name: "image edit", endpoint: service.GrokMediaEndpointImagesEdits, want: service.OpenAIEndpointCapabilityGrokMediaGeneration},
		{name: "image upload", endpoint: service.GrokMediaEndpointUploadsImages, want: service.OpenAIEndpointCapabilityGrokMediaGeneration},
		{name: "video generation", endpoint: service.GrokMediaEndpointVideosGenerations, want: service.OpenAIEndpointCapabilityGrokMediaGeneration},
		{name: "video edit", endpoint: service.GrokMediaEndpointVideosEdits, want: service.OpenAIEndpointCapabilityGrokMediaGeneration},
		{name: "video extension", endpoint: service.GrokMediaEndpointVideosExtensions, want: service.OpenAIEndpointCapabilityGrokMediaGeneration},
		{name: "video status preserves lookup", endpoint: service.GrokMediaEndpointVideoStatus, want: ""},
		{name: "video content preserves lookup", endpoint: service.GrokMediaEndpointVideoContent, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, grokMediaRequiredCapability(tt.endpoint))
		})
	}
}

func TestGrokMediaScheduleModelUsesNormalizedMappedUpstream(t *testing.T) {
	account := &service.Account{
		Platform: service.PlatformGrok,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"grok-imagine-video-1.5": "wrong-raw-model",
				"grok-imagine-video":     "mapped-video-model",
			},
		},
	}

	require.Equal(t, "mapped-video-model", grokMediaScheduleModel(account, "grok-imagine-video", nil))
	require.Equal(t, "actual-upstream-model", grokMediaScheduleModel(account, "grok-imagine-video", &service.OpenAIForwardResult{
		UpstreamModel: "actual-upstream-model",
	}))
	require.Equal(t, "mapped-video-model", grokMediaScheduleModel(account, "grok-imagine-video", &service.OpenAIForwardResult{}))
	require.Equal(t, "grok-imagine-video", grokMediaScheduleModel(nil, " grok-imagine-video ", nil))
}

func TestEnsureGrokMediaAccountEligibility(t *testing.T) {
	t.Run("non oauth account does not probe", func(t *testing.T) {
		prober := &grokMediaEligibilityProberStub{}
		h := &OpenAIGatewayHandler{grokMediaEligibilityProber: prober}
		account := &service.Account{Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey}

		eligible, reason, err := h.ensureGrokMediaAccountEligibility(context.Background(), account)

		require.NoError(t, err)
		require.True(t, eligible)
		require.Equal(t, "non_oauth", reason)
		require.Zero(t, prober.calls)
	})

	t.Run("unobserved oauth is probed before forwarding", func(t *testing.T) {
		prober := &grokMediaEligibilityProberStub{eligible: true, reason: "eligible"}
		h := &OpenAIGatewayHandler{grokMediaEligibilityProber: prober}
		account := &service.Account{ID: 7, Platform: service.PlatformGrok, Type: service.AccountTypeOAuth}

		eligible, reason, err := h.ensureGrokMediaAccountEligibility(context.Background(), account)

		require.NoError(t, err)
		require.True(t, eligible)
		require.Equal(t, "eligible", reason)
		require.Equal(t, 1, prober.calls)
	})

	t.Run("missing prober fails closed", func(t *testing.T) {
		h := &OpenAIGatewayHandler{}
		account := &service.Account{ID: 8, Platform: service.PlatformGrok, Type: service.AccountTypeOAuth}

		eligible, reason, err := h.ensureGrokMediaAccountEligibility(context.Background(), account)

		require.Error(t, err)
		require.False(t, eligible)
		require.Equal(t, "billing_probe_unavailable", reason)
	})

	t.Run("probe failure fails closed", func(t *testing.T) {
		probeErr := errors.New("probe failed")
		prober := &grokMediaEligibilityProberStub{reason: "billing_unobserved", err: probeErr}
		h := &OpenAIGatewayHandler{grokMediaEligibilityProber: prober}
		account := &service.Account{ID: 9, Platform: service.PlatformGrok, Type: service.AccountTypeOAuth}

		eligible, reason, err := h.ensureGrokMediaAccountEligibility(context.Background(), account)

		require.ErrorIs(t, err, probeErr)
		require.False(t, eligible)
		require.Equal(t, "billing_unobserved", reason)
	})
}
