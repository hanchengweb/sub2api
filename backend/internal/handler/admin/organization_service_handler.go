package admin

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func organizationFilters(c *gin.Context) (service.UserListFilters, error) {
	f := service.UserListFilters{AccountType: service.AccountTypeOrganizationService, OrganizationIssuer: strings.TrimSpace(c.Param("issuer")), OrganizationID: strings.TrimSpace(c.Param("organization_id")), OrganizationEnvironment: c.Param("environment")}
	if f.OrganizationIssuer == "" || len(f.OrganizationIssuer) > 80 || f.OrganizationID == "" || len(f.OrganizationID) > 160 || (f.OrganizationEnvironment != "test" && f.OrganizationEnvironment != "production") {
		return f, service.ErrServiceAccountIdentity
	}
	return f, nil
}

func (h *AdminAPIKeyHandler) organization(ctx context.Context, f service.UserListFilters) (*service.User, error) {
	users, total, err := h.adminService.ListUsers(ctx, 1, 2, f, "id", "asc")
	if err != nil {
		return nil, err
	}
	if total == 0 {
		return nil, service.ErrUserNotFound
	}
	if total != 1 || len(users) != 1 {
		return nil, service.ErrServiceAccountIdentity
	}
	u := users[0]
	if u.AccountType != f.AccountType || u.OrganizationIssuer != f.OrganizationIssuer || u.OrganizationID != f.OrganizationID || u.OrganizationEnvironment != f.OrganizationEnvironment {
		return nil, service.ErrServiceAccountIdentity
	}
	return &u, nil
}

func (h *AdminAPIKeyHandler) GetOrganization(c *gin.Context) {
	f, err := organizationFilters(c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	u, err := h.organization(c.Request.Context(), f)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.UserFromServiceAdmin(u))
}

// The operations server supplies a cryptographically random key retained in its
// encrypted pending operation. No plaintext key is returned or stored in receipts.
func (h *AdminAPIKeyHandler) EnsureOrganizationKey(c *gin.Context) {
	f, err := organizationFilters(c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var req struct {
		Email   string `json:"email" binding:"required,email"`
		Name    string `json:"name" binding:"required,max=100"`
		GroupID int64  `json:"group_id" binding:"required,gt=0"`
		Key     string `json:"key" binding:"required,min=32,max=128"`
	}
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Invalid organization key request")
		return
	}
	if strings.TrimSpace(c.GetHeader("Idempotency-Key")) == "" {
		response.BadRequest(c, "Idempotency-Key is required")
		return
	}
	if service.DefaultIdempotencyCoordinator() == nil {
		response.ErrorFrom(c, service.ErrIdempotencyStoreUnavail)
		return
	}
	// Include actual identity values; FullPath only contains parameter placeholders.
	payload := struct {
		Identity service.UserListFilters
		Request  any
	}{f, req}
	executeAdminIdempotentJSON(c, "admin.organization.key", payload, service.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		u, err := h.organization(ctx, f)
		if errors.Is(err, service.ErrUserNotFound) {
			u, err = h.adminService.CreateUser(ctx, &service.CreateUserInput{AccountType: f.AccountType, OrganizationIssuer: f.OrganizationIssuer, OrganizationID: f.OrganizationID, OrganizationEnvironment: f.OrganizationEnvironment, Email: req.Email, Username: req.Name, Concurrency: 1, AllowedGroups: []int64{req.GroupID}})
			if err != nil { // Recover an identity created by a concurrent/delayed request.
				existing, lookupErr := h.organization(ctx, f)
				if lookupErr == nil {
					u = existing
					err = nil
				}
			}
		}
		if err != nil {
			return nil, err
		}
		if !u.IsActive() {
			return nil, service.ErrUserNotActive
		}
		if u.Email != req.Email {
			return nil, service.ErrServiceAccountIdentity
		}
		granted := false
		for _, id := range u.AllowedGroups {
			if id == req.GroupID {
				granted = true
			}
		}
		if !granted {
			groups := append(append([]int64{}, u.AllowedGroups...), req.GroupID)
			if _, err = h.adminService.UpdateUser(ctx, u.ID, &service.UpdateUserInput{AllowedGroups: &groups}); err != nil {
				return nil, err
			}
		}
		key, err := h.apiKeyService.GetByKey(ctx, req.Key)
		if errors.Is(err, service.ErrAPIKeyNotFound) {
			key, err = h.apiKeyService.Create(ctx, u.ID, service.CreateAPIKeyRequest{Name: req.Name, GroupID: &req.GroupID, CustomKey: &req.Key})
		}
		if err != nil {
			return nil, err
		}
		if key.UserID != u.ID || key.GroupID == nil || *key.GroupID != req.GroupID || !key.IsActive() {
			return nil, infraerrors.Conflict("ORGANIZATION_KEY_CONFLICT", "key does not match organization operation")
		}
		return gin.H{"user_id": u.ID, "key_id": key.ID, "group_id": req.GroupID, "status": u.Status, "organization_issuer": f.OrganizationIssuer, "organization_id": f.OrganizationID, "organization_environment": f.OrganizationEnvironment}, nil
	})
}

func (h *AdminAPIKeyHandler) SetOrganizationStatus(c *gin.Context) {
	f, err := organizationFilters(c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var req struct {
		Status string `json:"status" binding:"required,oneof=active disabled"`
	}
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Invalid status")
		return
	}
	u, err := h.organization(c.Request.Context(), f)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	u, err = h.adminService.UpdateUser(c.Request.Context(), u.ID, &service.UpdateUserInput{Status: req.Status})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"user_id": u.ID, "status": u.Status})
}

func (h *AdminAPIKeyHandler) RevokeOrganizationKey(c *gin.Context) {
	f, err := organizationFilters(c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	id, err := strconv.ParseInt(c.Param("key_id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid key ID")
		return
	}
	u, err := h.organization(c.Request.Context(), f)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err = h.apiKeyService.Delete(c.Request.Context(), id, u.ID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"revoked": true})
}

func (h *AdminAPIKeyHandler) ScopeOrganizationUsage(c *gin.Context) {
	f, err := organizationFilters(c)
	if err != nil {
		response.ErrorFrom(c, err)
		c.Abort()
		return
	}
	u, err := h.organization(c.Request.Context(), f)
	if err != nil {
		response.ErrorFrom(c, err)
		c.Abort()
		return
	}
	c.Set("organization_usage", true)
	query := c.Request.URL.Query()
	query.Set("user_id", strconv.FormatInt(u.ID, 10))
	c.Request.URL.RawQuery = query.Encode()
	c.Next()
}
