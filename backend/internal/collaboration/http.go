package collaboration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type Handler struct {
	Store   *Store
	Gateway Gateway
	Enabled bool
}

func Register(r *gin.Engine, h *Handler, auth middleware.APIKeyAuthMiddleware) {
	group := r.Group("/v1/desktop/collaboration", middleware.RequestBodyLimit(11<<20), func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if !h.Enabled {
			writeError(c, unavailable)
			c.Abort()
			return
		}
		c.Next()
	}, gin.HandlerFunc(auth))
	group.GET("/status", h.status)
	group.GET("/rooms", h.read("rooms"))
	group.POST("/rooms", h.write("create"))
	group.POST("/join", h.write("join"))
	group.GET("/rooms/:room_id", h.read("room"))
	group.POST("/rooms/:room_id/invites", h.write("invite"))
	group.DELETE("/rooms/:room_id/invites/:item_id", h.write("revokeInvite"))
	group.DELETE("/rooms/:room_id/members/:item_id", h.write("removeMember"))
	group.POST("/rooms/:room_id/agents", h.write("addAgent"))
	group.DELETE("/rooms/:room_id/agents/:item_id", h.write("removeAgent"))
	group.GET("/rooms/:room_id/messages", h.read("messages"))
	group.POST("/rooms/:room_id/messages", h.write("message"))
	group.GET("/rooms/:room_id/runs", h.read("runs"))
	group.GET("/rooms/:room_id/tasks", h.read("tasks"))
	group.POST("/rooms/:room_id/tasks", h.write("task"))
	group.PATCH("/rooms/:room_id/tasks/:item_id", h.write("updateTask"))
	group.GET("/rooms/:room_id/assets", h.assets)
	group.POST("/rooms/:room_id/assets", h.upload)
	group.GET("/rooms/:room_id/assets/:item_id/content", h.assets)
}
func writeError(c *gin.Context, err error) {
	var e *Error
	if errors.As(err, &e) {
		response.ErrorWithDetails(c, e.Status, e.Message, e.Code, nil)
		return
	}
	response.ErrorFrom(c, err)
}
func requestKey(c *gin.Context) *service.APIKey {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok {
		response.Unauthorized(c, "个人连接不可用")
		return nil
	}
	if err := ValidateKey(key); err != nil {
		writeError(c, err)
		return nil
	}
	return key
}
func (h *Handler) status(c *gin.Context) {
	key := requestKey(c)
	if key == nil {
		return
	}
	response.Success(c, Status(c.Request.Context(), key, h.Gateway))
}
func (h *Handler) read(op string) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := requestKey(c)
		if key == nil {
			return
		}
		after := int64(0)
		limit := 100
		var err error
		if v := c.Query("after"); v != "" {
			after, err = strconv.ParseInt(v, 10, 64)
			if err != nil {
				writeError(c, invalid("消息游标无效"))
				return
			}
		}
		if v := c.Query("limit"); v != "" {
			limit, err = strconv.Atoi(v)
			if err != nil {
				writeError(c, invalid("分页无效"))
				return
			}
		}
		ctx, cancel := contextTimeout(c)
		defer cancel()
		result, err := h.Store.Read(ctx, key.UserID, op, c.Param("room_id"), after, limit)
		if err != nil {
			writeError(c, err)
			return
		}
		response.Success(c, result)
	}
}
func (h *Handler) write(op string) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := requestKey(c)
		if key == nil {
			return
		}
		var in Input
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		err := decoder.Decode(&in)
		if err != nil && !(errors.Is(err, io.EOF) && c.Request.Method == http.MethodDelete) {
			writeError(c, invalid("请求格式无效"))
			return
		}
		if err == nil {
			var trailing any
			if decoder.Decode(&trailing) != io.EOF {
				writeError(c, invalid("请求只能包含一个对象"))
				return
			}
		}
		if in.ClientRequestID == "" {
			in.ClientRequestID = c.GetHeader("Idempotency-Key")
		}
		ctx, cancel := contextTimeout(c)
		defer cancel()
		result, err := h.Store.Write(ctx, key, op, c.Param("room_id"), c.Param("item_id"), in)
		if err != nil {
			writeError(c, err)
			return
		}
		response.Success(c, result)
	}
}

func contextTimeout(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), 15*time.Second)
}
