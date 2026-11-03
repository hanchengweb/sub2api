package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type OrganizationMediaTaskHandler struct {
	engine  *mediatasks.Engine
	enabled bool
}

func NewOrganizationMediaTaskHandler(store mediatasks.Store, accounts service.AccountRepository, quota service.OrganizationMediaQuotaRepository, groups service.GroupRepository, composite *service.CompositeRouteResolver) *OrganizationMediaTaskHandler {
	return &OrganizationMediaTaskHandler{engine: mediatasks.NewEngine(store, mediaTaskQuota{quota}, mediaTaskRoutes{accounts: accounts, groups: groups, composite: composite}, mediatasks.NewOfficialSupplier()), enabled: os.Getenv("WXM_OFFICIAL_MEDIA_ENABLED") == "true"}
}
func mediaTaskError(c *gin.Context, err error) {
	status, code, message := 503, "media_task_unavailable", "媒体任务暂时不可用，请稍后查询。"
	switch {
	case errors.Is(err, mediatasks.ErrNotFound):
		status, code, message = 404, "media_task_not_found", "未找到当前账号的任务。"
	case errors.Is(err, mediatasks.ErrConflict), errors.Is(err, service.ErrMediaQuotaConflict):
		status, code, message = 409, "media_task_conflict", "请求编号已使用且参数已变化，请查询原任务。"
	case errors.Is(err, mediatasks.ErrInvalid):
		status, code, message = 400, "media_request_invalid", "媒体生成参数无效。"
	case errors.Is(err, mediatasks.ErrRoute):
		code, message = "media_route_unavailable", "当前组织尚未配置对应的官方模型渠道。"
	case errors.Is(err, mediatasks.ErrCredentials):
		code, message = "media_credentials_missing", "官方模型渠道的凭据尚未配置。"
	case errors.Is(err, mediatasks.ErrQuotaRequired):
		status, code, message = 403, "media_quota_required", "请先为组织分配语音或3D任务额度。"
	case errors.Is(err, service.ErrMediaQuotaExceeded):
		status, code, message = 429, "media_quota_exceeded", "当前媒体额度不足或已到期，请联系管理员追加。"
	case errors.Is(err, service.ErrMediaQuotaReview):
		status, code, message = 409, "media_quota_review_required", "媒体用量待核对，已暂停新生成。"
	case errors.Is(err, mediatasks.ErrBusy):
		status, code, message = 429, "media_provider_busy", "媒体服务繁忙，请稍后使用同一请求编号重试。"
	case errors.Is(err, mediatasks.ErrImageUnavailable):
		status, code, message = 422, "model3d_image_unavailable", "图片生成3D尚未开放，请使用文字描述。"
	}
	c.Header("Cache-Control", "no-store")
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message, "type": "media_error"}})
}
func mediaTaskOwner(c *gin.Context) (mediatasks.Owner, bool) {
	c.Header("Cache-Control", "no-store")
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key.User == nil {
		mediaTaskError(c, mediatasks.ErrNotFound)
		return mediatasks.Owner{}, false
	}
	if key.User.AccountType != "organization_service" || key.GroupID == nil || *key.GroupID <= 0 {
		c.AbortWithStatusJSON(403, gin.H{"error": gin.H{"code": "media_organization_required", "message": "此媒体能力需要组织连接。"}})
		return mediatasks.Owner{}, false
	}
	if key.IsExpired() || key.Status == service.StatusAPIKeyExpired {
		c.AbortWithStatusJSON(403, gin.H{"error": gin.H{"code": "API_KEY_EXPIRED", "message": "当前连接已过期，请重新连接。"}})
		return mediatasks.Owner{}, false
	}
	return mediatasks.Owner{UserID: key.UserID, APIKeyID: key.ID, GroupID: *key.GroupID}, true
}
func (h *OrganizationMediaTaskHandler) Create(kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		o, ok := mediaTaskOwner(c)
		if !ok {
			return
		}
		if !h.enabled {
			mediaTaskError(c, mediatasks.ErrRoute)
			return
		}
		id, err := uuid.Parse(strings.TrimSpace(c.GetHeader("X-Client-Request-ID")))
		if err != nil {
			mediaTaskError(c, mediatasks.ErrInvalid)
			return
		}
		r := mediatasks.Request{}
		if kind == mediatasks.Audio {
			r.Speed = 1
		}
		d := json.NewDecoder(io.LimitReader(c.Request.Body, 32<<10))
		d.DisallowUnknownFields()
		if d.Decode(&r) != nil || d.Decode(&struct{}{}) != io.EOF {
			mediaTaskError(c, mediatasks.ErrInvalid)
			return
		}
		task, err := h.engine.Create(c.Request.Context(), o, kind, id.String(), r)
		if err != nil {
			mediaTaskError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, task)
	}
}
func (h *OrganizationMediaTaskHandler) Get(kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		o, ok := mediaTaskOwner(c)
		if !ok {
			return
		}
		task, err := h.engine.Get(c.Request.Context(), o, kind, c.Param("id"))
		if err != nil {
			mediaTaskError(c, err)
			return
		}
		c.JSON(200, task)
	}
}
func (h *OrganizationMediaTaskHandler) Content(kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		o, ok := mediaTaskOwner(c)
		if !ok {
			return
		}
		b, typ, err := h.engine.Content(c.Request.Context(), o, kind, c.Param("id"))
		if err != nil {
			mediaTaskError(c, err)
			return
		}
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Content-Security-Policy", "default-src 'none'; sandbox")
		c.Data(200, typ, b)
	}
}
func (h *OrganizationMediaTaskHandler) Models(c *gin.Context) {
	o, ok := mediaTaskOwner(c)
	if !ok {
		return
	}
	entries := make([]gin.H, 0, 2)
	for _, row := range []struct{ kind, model, provider string }{{mediatasks.Audio, mediatasks.SpeechModel, "volcengine_speech"}, {mediatasks.Model3D, mediatasks.TripoModel, "tripo"}} {
		_, err := h.engine.Routes.Resolve(c.Request.Context(), o, row.kind, row.model)
		available := h.enabled && err == nil
		entry := gin.H{"id": row.model, "kind": row.kind, "provider": row.provider, "available": available}
		if !available {
			entry["error_code"] = "media_route_unavailable"
			if errors.Is(err, mediatasks.ErrCredentials) {
				entry["error_code"] = "media_credentials_missing"
			}
			if !h.enabled {
				entry["error_code"] = "media_feature_disabled"
			}
		}
		if row.kind == mediatasks.Audio {
			entry["voices"] = mediatasks.Voices
			entry["scenes"] = mediatasks.Scenes
			entry["emotions"] = mediatasks.Emotions
			entry["max_input_characters"] = 2000
			entry["unit"] = "characters"
		} else {
			entry["modes"] = []string{"text"}
			entry["image_available"] = false
			entry["unit"] = "tasks"
		}
		entries = append(entries, entry)
	}
	c.JSON(200, gin.H{"object": "list", "data": entries})
}

type mediaTaskQuota struct {
	repo service.OrganizationMediaQuotaRepository
}

func (q mediaTaskQuota) Reserve(ctx context.Context, o mediatasks.Owner, id, digest, kind, model string, units int64) error {
	if q.repo == nil {
		return mediatasks.ErrQuotaRequired
	}
	v, err := q.repo.Reserve(ctx, service.MediaQuotaClaim{UserID: o.UserID, APIKeyID: o.APIKeyID, RequestID: id, Digest: digest, Kind: kind, Model: model, Units: units})
	if err != nil {
		return err
	}
	if v == nil {
		return mediatasks.ErrQuotaRequired
	}
	return nil
}
func (q mediaTaskQuota) Bind(ctx context.Context, user int64, requestID, taskID string) error {
	return q.repo.Bind(ctx, user, requestID, taskID)
}
func (q mediaTaskQuota) Validate(ctx context.Context, user int64, id string) error {
	return q.repo.ValidateDispatch(ctx, user, id)
}
func (q mediaTaskQuota) Settle(ctx context.Context, user int64, requestID, taskID string, units int64, measurement string) error {
	return q.repo.Settle(ctx, user, requestID, taskID, units, measurement)
}

type mediaTaskRoutes struct {
	accounts  service.AccountRepository
	groups    service.GroupRepository
	composite *service.CompositeRouteResolver
}

func (r mediaTaskRoutes) allowed(ctx context.Context, o mediatasks.Owner, kind, model string) bool {
	if r.groups == nil {
		return false
	}
	g, err := r.groups.GetByID(ctx, o.GroupID)
	if err != nil || g == nil || !g.IsActive() || !g.ModelsListConfig.Enabled {
		return false
	}
	published := false
	for _, m := range g.ModelsListConfig.Models {
		if m == model {
			published = true
			break
		}
	}
	if !published {
		return false
	}
	if g.Platform == service.PlatformOpenAI {
		return true
	}
	if g.Platform != service.PlatformComposite || r.composite == nil {
		return false
	}
	d, err := r.composite.Resolve(ctx, o.GroupID, model, kind)
	if err != nil || !d.Matched || d.Source != service.CompositeRouteSourceExplicit || d.Route == nil {
		return false
	}
	native := mediatasks.SpeechModel
	if kind == mediatasks.Model3D {
		native = mediatasks.TripoNativeModel
	}
	return d.Route.Endpoint == kind && d.Route.MatchType == service.CompositeRouteMatchExact && d.Route.PublicModel == model && d.TargetPlatform == service.PlatformOpenAI && d.UpstreamModel == native
}

func mediaTaskAccount(a service.Account, kind, model string) bool {
	expectedBase, provider, native := "https://openspeech.bytedance.com", "volcengine_speech", mediatasks.SpeechModel
	if kind == mediatasks.Model3D {
		expectedBase, provider, native = "https://openapi.tripo3d.com/v3", "tripo", mediatasks.TripoNativeModel
	}
	mapped, explicit := a.GetModelMapping()[model]
	return a.Platform == service.PlatformOpenAI && a.Type == service.AccountTypeAPIKey && a.IsSchedulable() && a.GetExtraString("media_provider") == provider && strings.TrimRight(a.GetCredential("base_url"), "/") == expectedBase && explicit && mapped == native
}
func (r mediaTaskRoutes) Resolve(ctx context.Context, o mediatasks.Owner, kind, model string) (mediatasks.Provider, error) {
	if r.accounts == nil {
		return mediatasks.Provider{}, mediatasks.ErrRoute
	}
	if !r.allowed(ctx, o, kind, model) {
		return mediatasks.Provider{}, mediatasks.ErrRoute
	}
	accounts, err := r.accounts.ListSchedulableByGroupID(ctx, o.GroupID)
	if err != nil {
		return mediatasks.Provider{}, mediatasks.ErrUnavailable
	}
	sort.SliceStable(accounts, func(i, j int) bool {
		if accounts[i].Priority != accounts[j].Priority {
			return accounts[i].Priority < accounts[j].Priority
		}
		return accounts[i].ID < accounts[j].ID
	})
	missing := false
	for _, a := range accounts {
		if !mediaTaskAccount(a, kind, model) {
			continue
		}
		key := strings.TrimSpace(a.GetCredential("api_key"))
		if key == "" {
			missing = true
			continue
		}
		return mediatasks.Provider{AccountID: a.ID, Kind: kind, Key: key}, nil
	}
	if missing {
		return mediatasks.Provider{}, mediatasks.ErrCredentials
	}
	return mediatasks.Provider{}, mediatasks.ErrRoute
}
func (r mediaTaskRoutes) Reopen(ctx context.Context, o mediatasks.Owner, t *mediatasks.Task) (mediatasks.Provider, error) {
	if o.UserID != t.Owner.UserID {
		return mediatasks.Provider{}, mediatasks.ErrNotFound
	}
	accounts, err := r.accounts.ListByGroup(ctx, t.Owner.GroupID)
	if err != nil {
		return mediatasks.Provider{}, mediatasks.ErrUnavailable
	}
	for _, a := range accounts {
		if a.ID == t.AccountID && mediaTaskAccount(a, t.Kind, t.Model) {
			key := strings.TrimSpace(a.GetCredential("api_key"))
			if key == "" {
				return mediatasks.Provider{}, mediatasks.ErrCredentials
			}
			return mediatasks.Provider{AccountID: a.ID, Kind: t.Kind, Key: key}, nil
		}
	}
	return mediatasks.Provider{}, mediatasks.ErrRoute
}
