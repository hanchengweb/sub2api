package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type DesktopDriveHandler struct{ drive *service.DesktopDriveService }

func NewDesktopDriveHandler(drive *service.DesktopDriveService) *DesktopDriveHandler {
	return &DesktopDriveHandler{drive: drive}
}

func desktopDriveKey(c *gin.Context) *service.APIKey {
	c.Header("Cache-Control", "no-store")
	key, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		response.Unauthorized(c, "个人连接不可用")
		return nil
	}
	if err := service.ValidateDesktopDriveKey(key); err != nil {
		response.ErrorFrom(c, err)
		return nil
	}
	return key
}

func driveQueryInt(c *gin.Context, name string, fallback int) (int, bool) {
	value := c.Query(name)
	if value == "" {
		return fallback, true
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		response.BadRequest(c, "分页参数无效")
		return 0, false
	}
	return n, true
}

func (h *DesktopDriveHandler) Catalog(c *gin.Context) {
	key := desktopDriveKey(c)
	if key == nil {
		return
	}
	result, err := h.drive.Catalog(c.Request.Context(), key)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *DesktopDriveHandler) List(c *gin.Context) {
	key := desktopDriveKey(c)
	if key == nil {
		return
	}
	offset, ok := driveQueryInt(c, "offset", 0)
	if !ok {
		return
	}
	limit, ok := driveQueryInt(c, "limit", 100)
	if !ok {
		return
	}
	result, err := h.drive.List(c.Request.Context(), key.UserID, c.Param("resource_id"), c.Query("parent_id"), offset, limit)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *DesktopDriveHandler) Item(c *gin.Context) {
	key := desktopDriveKey(c)
	if key == nil {
		return
	}
	offset, ok := driveQueryInt(c, "offset", 0)
	if !ok {
		return
	}
	limit, ok := driveQueryInt(c, "limit", 100000)
	if !ok {
		return
	}
	result, err := h.drive.Content(c.Request.Context(), key.UserID, c.Param("resource_id"), c.Param("item_id"), offset, limit)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *DesktopDriveHandler) Original(c *gin.Context) {
	key := desktopDriveKey(c)
	if key == nil {
		return
	}
	item, file, err := h.drive.Original(c.Request.Context(), key.UserID, c.Param("resource_id"), c.Param("item_id"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	defer file.Close()
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": item.FileName}))
	c.Header("Content-Type", "application/octet-stream")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "sandbox; default-src 'none'")
	http.ServeContent(c.Writer, c.Request, item.FileName, time.Time{}, file)
}

func (h *DesktopDriveHandler) Write(operation string) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := desktopDriveKey(c)
		if key == nil {
			return
		}
		requestID := c.GetHeader("Idempotency-Key")
		parsed, err := uuid.Parse(requestID)
		if err != nil || parsed == uuid.Nil || parsed.String() != requestID {
			response.ErrorFrom(c, service.ErrIdempotencyKeyInvalid)
			return
		}
		var input service.DesktopDriveInput
		var uploads []service.DesktopDriveUpload
		var releaseUpload func()
		defer func() {
			if len(uploads) > 0 {
				h.drive.DropUploads(key.UserID, uploads)
			}
			if releaseUpload != nil {
				releaseUpload()
			}
		}()
		if operation == "upload" {
			release, err := h.drive.BeginUpload(c.Request.Context(), key.UserID)
			if err != nil {
				response.ErrorFrom(c, err)
				return
			}
			releaseUpload = release
			committed, err := h.drive.UploadCommitted(c.Request.Context(), key.UserID, requestID)
			if err != nil {
				response.ErrorFrom(c, err)
				return
			}
			reader, err := c.Request.MultipartReader()
			if err != nil {
				response.BadRequest(c, "请选择上传文件")
				return
			}
			var total int64
			parentSeen := false
			for {
				part, err := reader.NextPart()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					response.BadRequest(c, "上传数据不完整或超过80MiB")
					return
				}
				if part.FormName() == "parent_id" && part.FileName() == "" && !parentSeen {
					value, err := io.ReadAll(io.LimitReader(part, 100))
					part.Close()
					if err != nil || len(value) > 36 {
						response.BadRequest(c, "父目录无效")
						return
					}
					input.ParentID = string(value)
					parentSeen = true
					continue
				}
				if part.FormName() != "files" || part.FileName() == "" || len(uploads) >= 100 {
					part.Close()
					response.BadRequest(c, "上传字段无效或文件超过100个")
					return
				}
				var file *service.DesktopDriveUpload
				if committed {
					file, err = h.drive.StageReplay(c.Request.Context(), key.UserID, part.FileName(), part)
				} else {
					file, err = h.drive.StageRetry(c.Request.Context(), key.UserID, requestID, c.Param("resource_id"), len(uploads), part.FileName(), part)
				}
				part.Close()
				if err != nil {
					response.ErrorFrom(c, err)
					return
				}
				uploads = append(uploads, *file)
				total += file.SizeBytes
				if total > service.DesktopDriveMaxBatchBytes {
					response.BadRequest(c, "单次上传不能超过80MiB")
					return
				}
			}
		} else if operation != "delete" {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
			if err := c.ShouldBindJSON(&input); err != nil {
				response.BadRequest(c, "云盘信息格式错误")
				return
			}
		}
		coordinator := service.DefaultIdempotencyCoordinator()
		if coordinator == nil {
			response.ErrorFrom(c, service.ErrIdempotencyStoreUnavail)
			return
		}
		payload := struct {
			DriveID, ItemID, Operation string
			Input                      service.DesktopDriveInput
			Files                      []service.DesktopDriveUpload
		}{c.Param("resource_id"), c.Param("item_id"), operation, input, uploads}
		_, err = coordinator.Execute(c.Request.Context(), service.IdempotencyExecuteOptions{
			Scope: fmt.Sprintf("desktop.drive.user:%d", key.UserID), ActorScope: fmt.Sprintf("user:%d", key.UserID), Method: c.Request.Method, Route: c.FullPath(), IdempotencyKey: requestID,
			Payload: payload, RequireKey: true, TTL: 24 * time.Hour,
		}, func(ctx context.Context) (any, error) {
			_, mutationErr := h.drive.Mutate(ctx, key.UserID, requestID, operation, c.Param("resource_id"), c.Param("item_id"), input, uploads)
			return map[string]bool{"committed": mutationErr == nil}, mutationErr
		})
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		result, err := h.drive.MutationResult(c.Request.Context(), key.UserID, requestID, operation, c.Param("resource_id"), c.Param("item_id"), input, uploads)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		response.Success(c, result)
	}
}
