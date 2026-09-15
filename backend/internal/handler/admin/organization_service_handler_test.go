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

func TestOrganizationIdentityAndUsageScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStubAdminService()
	svc.users = []service.User{{ID: 42, AccountType: service.AccountTypeOrganizationService,
		OrganizationIssuer: "wemoreai-ops", OrganizationID: "college", OrganizationEnvironment: "test"}}
	h := NewAdminAPIKeyHandler(svc, nil)
	f := service.UserListFilters{AccountType: service.AccountTypeOrganizationService,
		OrganizationIssuer: "wemoreai-ops", OrganizationID: "college", OrganizationEnvironment: "test"}
	u, err := h.organization(context.Background(), f)
	require.NoError(t, err)
	require.Equal(t, int64(42), u.ID)
	require.Equal(t, f, svc.lastListUsers.filters)
	for _, mismatch := range []service.UserListFilters{
		{AccountType: f.AccountType, OrganizationIssuer: f.OrganizationIssuer, OrganizationID: "guangxi", OrganizationEnvironment: "test"},
		{AccountType: f.AccountType, OrganizationIssuer: f.OrganizationIssuer, OrganizationID: "college", OrganizationEnvironment: "production"},
	} {
		_, err = h.organization(context.Background(), mismatch)
		require.ErrorIs(t, err, service.ErrServiceAccountIdentity)
	}
	router := gin.New()
	router.GET("/org/:issuer/:environment/:organization_id/usage", h.ScopeOrganizationUsage, func(c *gin.Context) {
		require.Equal(t, "42", c.Query("user_id"))
		require.Equal(t, "request-1", c.Query("request_id"))
		c.Status(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/org/wemoreai-ops/test/college/usage?user_id=999&request_id=request-1", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	denied := httptest.NewRecorder()
	router.ServeHTTP(denied, httptest.NewRequest("GET", "/org/wemoreai-ops/test/guangxi/usage", nil))
	require.NotEqual(t, http.StatusOK, denied.Code)
}

func TestOrganizationInvalidKeyDoesNotEchoSecret(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := NewAdminAPIKeyHandler(newStubAdminService(), nil)
	router.POST("/org/:issuer/:environment/:organization_id/keys", h.EnsureOrganizationKey)
	secret := "sensitive-secret-invalid"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/org/wemoreai-ops/test/college/keys", bytes.NewBufferString(`{"email":"invalid","key":"`+secret+`"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.NotContains(t, rec.Body.String(), secret)
}
