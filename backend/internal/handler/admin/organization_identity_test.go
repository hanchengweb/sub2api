package admin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type organizationAdminStub struct {
	service.AdminService
	calls int
}

func (s *organizationAdminStub) CreateUser(_ context.Context, input *service.CreateUserInput) (*service.User, error) {
	s.calls++
	return &service.User{ID: 42, Email: input.Email, AccountType: input.AccountType,
		OrganizationID: input.OrganizationID, OrganizationIssuer: input.OrganizationIssuer,
		OrganizationEnvironment: input.OrganizationEnvironment, Role: service.RoleUser}, nil
}

func TestOrganizationAccountCreateIdempotency(t *testing.T) {
	previous := service.DefaultIdempotencyCoordinator()
	t.Cleanup(func() { service.SetDefaultIdempotencyCoordinator(previous) })
	service.SetDefaultIdempotencyCoordinator(nil)
	stub := &organizationAdminStub{}
	h := &UserHandler{adminService: stub}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/users", h.Create)
	body := `{"email":"service@example.test","account_type":"organization_service","organization_id":"org-a","organization_issuer":"wemoreai-ops","organization_environment":"test"}`
	request := func(body, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/users", bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	require.Equal(t, 400, request(body, "").Code)
	require.Equal(t, 503, request(body, "organization-create-a").Code)
	require.Equal(t, 400, request(`{"email":"human@example.test"}`, "").Code)
	require.Zero(t, stub.calls)
	service.SetDefaultIdempotencyCoordinator(service.NewIdempotencyCoordinator(newMemoryIdempotencyRepoStub(), service.DefaultIdempotencyConfig()))
	first := request(body, "organization-create-a")
	second := request(body, "organization-create-a")
	require.Equal(t, 200, first.Code)
	require.Equal(t, 200, second.Code)
	require.JSONEq(t, first.Body.String(), second.Body.String())
	require.Equal(t, "true", second.Header().Get("X-Idempotency-Replayed"))
	require.Equal(t, 1, stub.calls)
	require.Equal(t, 409, request(body[:len(body)-1]+`,"username":"changed"}`, "organization-create-a").Code)
	require.Equal(t, 1, stub.calls)
}
