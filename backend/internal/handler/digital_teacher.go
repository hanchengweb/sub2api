package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/digitalteacher"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type DigitalTeacherHandler struct {
	gateway *digitalteacher.Gateway
	cancel  context.CancelFunc
	done    sync.WaitGroup
}

func NewDigitalTeacherHandler(db *sql.DB, cfg *config.Config, billing *service.BillingCacheService, keys *service.APIKeyService) (*DigitalTeacherHandler, error) {
	key := os.Getenv("LIVEAVATAR_API_KEY")
	if file := os.Getenv("LIVEAVATAR_API_KEY_FILE"); file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, errors.New("cannot read configured LiveAvatar secret file")
		}
		key = strings.TrimSpace(string(data))
	}
	simliKey := os.Getenv("SIMLI_API_KEY")
	if file := os.Getenv("SIMLI_API_KEY_FILE"); file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, errors.New("cannot read configured Simli secret file")
		}
		simliKey = strings.TrimSpace(string(data))
	}
	g, err := digitalteacher.New(db, digitalteacher.Options{APIKey: key, SimliAPIKey: simliKey, Provider: os.Getenv("DIGITAL_TEACHER_PROVIDER"), CryptoSecret: cfg.JWT.Secret, Invalidate: func(ctx context.Context, userID, keyID int64) {
		_ = billing.InvalidateUserBalance(ctx, userID)
		_ = billing.InvalidateAPIKeyRateLimit(ctx, keyID)
		keys.InvalidateAuthCacheByUserID(ctx, userID)
	}})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &DigitalTeacherHandler{gateway: g, cancel: cancel}
	h.done.Add(1)
	go func() { defer h.done.Done(); g.Run(ctx) }()
	return h, nil
}
func (h *DigitalTeacherHandler) SimliStream(c *gin.Context) {
	token := c.Query("session_token")
	c.Request.URL.RawQuery = "" // Access/audit middleware must not retain capabilities.
	c.Request.RequestURI = c.Request.URL.Path
	s, err := h.gateway.ResolveCapability(c.Request.Context(), token)
	if teacherError(c, err) {
		return
	}
	if err = h.gateway.ServeSimli(c.Writer, c.Request, s); err != nil && !c.Writer.Written() {
		teacherError(c, err)
	}
}
func (h *DigitalTeacherHandler) Close() {
	if h != nil {
		h.cancel()
		h.done.Wait()
	}
}

func (h *DigitalTeacherHandler) CreateAvatar(c *gin.Context) {
	o, ok := teacherOwner(c)
	if !ok {
		return
	}
	if err := c.Request.ParseMultipartForm(6 << 20); err != nil {
		response.BadRequest(c, "照片上传未完成，请选择小于 5 MB 的照片")
		return
	}
	defer c.Request.MultipartForm.RemoveAll()
	file, _, err := c.Request.FormFile("image")
	if err != nil {
		response.BadRequest(c, "请选择照片")
		return
	}
	defer file.Close()
	photo, err := io.ReadAll(io.LimitReader(file, 5<<20))
	if err != nil {
		response.BadRequest(c, "照片读取失败")
		return
	}
	v, e := h.gateway.CreateAvatar(c.Request.Context(), o, c.PostForm("request_id"), c.PostForm("name"), c.PostForm("consent") == "true", photo)
	if !teacherError(c, e) {
		response.Success(c, v)
	}
}
func (h *DigitalTeacherHandler) AvatarJobs(c *gin.Context) {
	o, ok := teacherOwner(c)
	if !ok {
		return
	}
	v, e := h.gateway.AvatarJobs(c.Request.Context(), o)
	if !teacherError(c, e) {
		response.Success(c, v)
	}
}
func (h *DigitalTeacherHandler) RefreshAvatar(c *gin.Context) {
	o, ok := teacherOwner(c)
	if !ok {
		return
	}
	v, e := h.gateway.RefreshAvatar(c.Request.Context(), o, c.Param("id"))
	if !teacherError(c, e) {
		response.Success(c, v)
	}
}
func teacherError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	var e *digitalteacher.Error
	if errors.As(err, &e) {
		response.ErrorWithDetails(c, e.Status, e.Message, e.Code, nil)
	} else {
		response.ErrorWithDetails(c, 503, "数字老师服务暂不可用，请稍后查看原会话", "digital_teacher_unavailable", nil)
	}
	return true
}
func teacherOwner(c *gin.Context) (digitalteacher.Owner, bool) {
	c.Header("Cache-Control", "no-store")
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key.GroupID == nil {
		response.Error(c, 403, "请先选择已开通数字老师的算力包")
		return digitalteacher.Owner{}, false
	}
	if (c.Request.Method == "POST" && c.FullPath() != "/v1/digital-teachers/sessions/:id/stop" || c.Request.Method == "PUT") && (key.IsExpired() || key.Status == service.StatusAPIKeyExpired) {
		response.Error(c, 403, "当前连接已过期，请重新连接")
		return digitalteacher.Owner{}, false
	}
	return digitalteacher.Owner{UserID: key.UserID, KeyID: key.ID, GroupID: *key.GroupID}, true
}
func (h *DigitalTeacherHandler) Settings(c *gin.Context) {
	o, ok := teacherOwner(c)
	if !ok {
		return
	}
	v, e := h.gateway.Settings(c.Request.Context(), o)
	if !teacherError(c, e) {
		response.Success(c, v)
	}
}
func (h *DigitalTeacherHandler) Catalog(c *gin.Context) {
	o, ok := teacherOwner(c)
	if !ok {
		return
	}
	v, e := h.gateway.Catalog(c.Request.Context(), o)
	if !teacherError(c, e) {
		response.Success(c, v)
	}
}
func (h *DigitalTeacherHandler) Save(c *gin.Context) {
	o, ok := teacherOwner(c)
	if !ok {
		return
	}
	var t digitalteacher.Teacher
	if c.ShouldBindJSON(&t) != nil {
		response.BadRequest(c, "老师资料格式无效")
		return
	}
	v, e := h.gateway.SaveTeacher(c.Request.Context(), o, t)
	if !teacherError(c, e) {
		response.Success(c, v)
	}
}
func (h *DigitalTeacherHandler) Create(c *gin.Context) {
	o, ok := teacherOwner(c)
	if !ok {
		return
	}
	var r digitalteacher.StartRequest
	if c.ShouldBindJSON(&r) != nil {
		response.BadRequest(c, "会话参数无效")
		return
	}
	v, e := h.gateway.Create(c.Request.Context(), o, r)
	if !teacherError(c, e) {
		response.Success(c, v)
	}
}
func (h *DigitalTeacherHandler) List(c *gin.Context) {
	o, ok := teacherOwner(c)
	if !ok {
		return
	}
	v, e := h.gateway.List(c.Request.Context(), o)
	if !teacherError(c, e) {
		response.Success(c, v)
	}
}
func (h *DigitalTeacherHandler) Get(c *gin.Context) {
	o, ok := teacherOwner(c)
	if !ok {
		return
	}
	v, e := h.gateway.Get(c.Request.Context(), o, c.Param("id"))
	if !teacherError(c, e) {
		response.Success(c, v)
	}
}
func (h *DigitalTeacherHandler) Stop(c *gin.Context) {
	o, ok := teacherOwner(c)
	if !ok {
		return
	}
	v, e := h.gateway.Stop(c.Request.Context(), o, c.Param("id"))
	if !teacherError(c, e) {
		response.Success(c, v)
	}
}

// The SDK receives a short-lived WindHub capability, never a provider JWT.
// It has no cookie authentication and exposes only these three lifecycle calls.
func (h *DigitalTeacherHandler) SDK(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	auth := c.GetHeader("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") || len(auth) > 1024 {
		response.Unauthorized(c, "会话凭证无效")
		return
	}
	s, err := h.gateway.ResolveCapability(c.Request.Context(), strings.TrimPrefix(auth, "Bearer "))
	if teacherError(c, err) {
		return
	}
	var v any
	switch c.FullPath() {
	case "/v1/digital-teachers/sdk/v1/sessions/start":
		var raw json.RawMessage
		raw, err = h.gateway.SDKStart(c.Request.Context(), s)
		v = raw
	case "/v1/digital-teachers/sdk/v1/sessions/keep-alive":
		err = h.gateway.SDKKeepAlive(c.Request.Context(), s)
		v = gin.H{}
	case "/v1/digital-teachers/sdk/v1/sessions/stop":
		v, err = h.gateway.Stop(c.Request.Context(), digitalteacher.Owner{UserID: s.UserID, KeyID: s.KeyID, GroupID: s.GroupID}, s.ID)
	default:
		response.NotFound(c, "会话接口不存在")
		return
	}
	if !teacherError(c, err) {
		c.JSON(http.StatusOK, gin.H{"code": 1000, "data": v})
	}
}
func (h *DigitalTeacherHandler) AdminStatus(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	v, e := h.gateway.AdminStatus(c.Request.Context())
	if !teacherError(c, e) {
		response.Success(c, v)
	}
}
func (h *DigitalTeacherHandler) SetPlan(c *gin.Context) {
	var p digitalteacher.Plan
	if c.ShouldBindJSON(&p) != nil {
		response.BadRequest(c, "算力包配置无效")
		return
	}
	v, e := h.gateway.SetPlan(c.Request.Context(), p)
	if !teacherError(c, e) {
		response.Success(c, v)
	}
}
func (h *DigitalTeacherHandler) Grant(c *gin.Context) {
	var r struct {
		UserID    int64   `json:"user_id"`
		RequestID string  `json:"request_id"`
		Minutes   float64 `json:"minutes"`
		Reason    string  `json:"reason"`
	}
	if c.ShouldBindJSON(&r) != nil {
		response.BadRequest(c, "分钟额度参数无效")
		return
	}
	if !teacherError(c, h.gateway.Grant(c.Request.Context(), r.UserID, r.RequestID, r.Minutes, r.Reason)) {
		response.Success(c, gin.H{"granted": true})
	}
}
func (h *DigitalTeacherHandler) Minutes(c *gin.Context) {
	id, e := strconv.ParseInt(c.Param("id"), 10, 64)
	if e != nil || id <= 0 {
		response.BadRequest(c, "账户编号无效")
		return
	}
	v, e := h.gateway.Minutes(c.Request.Context(), id)
	if !teacherError(c, e) {
		response.Success(c, v)
	}
}
func (h *DigitalTeacherHandler) BindAsset(c *gin.Context) {
	var r struct {
		UserID int64  `json:"user_id"`
		Kind   string `json:"kind"`
		ID     string `json:"asset_id"`
	}
	if c.ShouldBindJSON(&r) != nil {
		response.BadRequest(c, "素材参数无效")
		return
	}
	if !teacherError(c, h.gateway.BindAsset(c.Request.Context(), r.UserID, r.Kind, r.ID)) {
		response.Success(c, gin.H{"assigned": true})
	}
}
func (h *DigitalTeacherHandler) AdminSessions(c *gin.Context) {
	v, e := h.gateway.AdminSessions(c.Request.Context())
	if teacherError(c, e) {
		return
	}
	out := make([]any, 0, len(v))
	for _, s := range v {
		out = append(out, struct {
			*digitalteacher.Session
			UserID int64 `json:"user_id"`
			KeyID  int64 `json:"key_id"`
		}{s, s.UserID, s.KeyID})
	}
	response.Success(c, out)
}
func (h *DigitalTeacherHandler) Reconcile(c *gin.Context) {
	v, e := h.gateway.AdminReconcile(c.Request.Context(), c.Param("id"))
	if !teacherError(c, e) {
		response.Success(c, v)
	}
}
