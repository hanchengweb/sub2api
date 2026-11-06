package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

var toAPIImageTaskID = regexp.MustCompile(`^tsk_img_[A-Za-z0-9_-]{1,140}$`)

// awaitToAPI26ImageTask only follows the task accepted by the original POST.
// It never submits again or follows a result-supplied polling URL.
func (s *OpenAIGatewayService) awaitToAPI26ImageTask(ctx context.Context, c *gin.Context, account *Account, request *http.Request, response *http.Response, proxyURL string) (*http.Response, error) {
	return awaitToAPI26ImageTask(ctx, c, account, request, response, proxyURL, s.httpUpstream, 2*time.Second, 10*time.Minute)
}

func awaitToAPI26ImageTask(ctx context.Context, c *gin.Context, account *Account, request *http.Request, response *http.Response, proxyURL string, upstream HTTPUpstream, interval, timeout time.Duration) (*http.Response, error) {
	if request == nil || request.URL == nil || response == nil || response.Body == nil || account == nil || upstream == nil {
		return nil, fmt.Errorf("image task transport unavailable")
	}
	switch strings.ToLower(request.URL.Hostname()) {
	case "toapis.cn", "api.toapis.cn", "toapis.com", "api.toapis.com":
	default:
		return nil, fmt.Errorf("GPT Image 2.6 requires its configured ToAPI endpoint")
	}
	if request.URL.Scheme != "https" || (request.URL.Port() != "" && request.URL.Port() != "443") || request.URL.User != nil || request.URL.RawQuery != "" || request.URL.Fragment != "" {
		return nil, fmt.Errorf("image task endpoint is invalid")
	}
	if !strings.HasSuffix(request.URL.Path, openAIImagesGenerationsEndpoint) {
		return nil, fmt.Errorf("GPT Image 2.6 uses the generations endpoint for reference images")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	current := response
	taskID := ""
	for {
		if current == nil || current.Body == nil {
			return nil, toAPI26TaskError(c, taskID, "upstream_task_result_unknown")
		}
		body, err := io.ReadAll(io.LimitReader(current.Body, 32*1024*1024+1))
		_ = current.Body.Close()
		if err != nil || len(body) > 32*1024*1024 {
			return nil, toAPI26TaskError(c, taskID, "upstream_task_result_unknown")
		}
		if current.StatusCode < 200 || current.StatusCode >= 300 {
			return nil, toAPI26TaskError(c, taskID, "upstream_task_result_unknown")
		}
		var envelope map[string]json.RawMessage
		if json.Unmarshal(body, &envelope) != nil {
			return nil, toAPI26TaskError(c, taskID, "upstream_task_result_unknown")
		}
		var id, status string
		_ = json.Unmarshal(envelope["id"], &id)
		_ = json.Unmarshal(envelope["status"], &status)
		if taskID == "" {
			if !toAPIImageTaskID.MatchString(id) {
				return nil, toAPI26TaskError(c, "", "upstream_task_result_unknown")
			}
			taskID = id
			setToAPI26TaskHeader(c, taskID)
		} else if id != taskID {
			return nil, toAPI26TaskError(c, taskID, "upstream_task_identity_mismatch")
		}
		switch status {
		case "completed":
			var result map[string]json.RawMessage
			if json.Unmarshal(envelope["result"], &result) != nil {
				return nil, toAPI26TaskError(c, taskID, "upstream_task_usage_unavailable")
			}
			var data []json.RawMessage
			if json.Unmarshal(result["data"], &data) != nil || len(data) == 0 || len(data) > 4 || len(envelope["usage"]) == 0 {
				return nil, toAPI26TaskError(c, taskID, "upstream_task_usage_unavailable")
			}
			// Supplier billing is procurement information, not the customer's price.
			final := map[string]json.RawMessage{"id": envelope["id"], "data": result["data"], "usage": envelope["usage"]}
			if len(envelope["model"]) > 0 {
				final["model"] = envelope["model"]
			}
			if len(envelope["created_at"]) > 0 {
				final["created"] = envelope["created_at"]
			}
			finalBody, err := json.Marshal(final)
			if err != nil {
				return nil, toAPI26TaskError(c, taskID, "upstream_task_result_unknown")
			}
			headers := response.Header.Clone()
			headers.Del("Content-Length")
			headers.Set("Content-Type", "application/json")
			return &http.Response{StatusCode: http.StatusOK, Header: headers, Body: io.NopCloser(bytes.NewReader(finalBody))}, nil
		case "failed":
			return nil, toAPI26TaskError(c, taskID, "upstream_task_failed")
		case "pending", "queued", "in_progress":
		default:
			return nil, toAPI26TaskError(c, taskID, "upstream_task_result_unknown")
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, toAPI26TaskError(c, taskID, "upstream_task_result_pending")
		case <-timer.C:
		}
		poll := request.Clone(ctx)
		poll.Method = http.MethodGet
		poll.Body, poll.GetBody, poll.ContentLength, poll.TransferEncoding = nil, nil, 0, nil
		poll.URL.Path = strings.TrimRight(request.URL.Path, "/") + "/" + taskID
		poll.URL.RawPath = ""
		poll.Header.Del("Content-Type")
		poll.Header.Del("Content-Length")
		current, err = upstream.Do(poll, proxyURL, account.ID, account.Concurrency)
		if err != nil {
			return nil, toAPI26TaskError(c, taskID, "upstream_task_result_unknown")
		}
	}
}

func toAPI26TaskError(c *gin.Context, taskID, code string) error {
	// A non-retryable conflict preserves the accepted ID. Never ask the account
	// failover loop to create a second billable task after acceptance.
	if c != nil && c.Writer != nil && !(c.Writer.Written() && OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c) >= 0) {
		status := http.StatusConflict
		if StopOpenAIImagesJSONKeepaliveCommitted(c) {
			// Heartbeats already committed HTTP 200, but no JSON result. Preserve
			// the non-retryable task error in the final JSON body.
			status = http.StatusOK
		}
		c.JSON(status, gin.H{"error": gin.H{"type": "upstream_task_error", "code": code,
			"message": "图片任务结果尚未确认，请勿重复生成；请凭原任务编号查询或联系管理员。", "task_id": taskID, "retryable": false}})
	}
	return fmt.Errorf("image task %s: %s; do not resubmit", taskID, code)
}

func setToAPI26TaskHeader(c *gin.Context, taskID string) {
	if c == nil || c.Writer == nil {
		return
	}
	if k := openAIImagesJSONKeepaliveFromContext(c); k != nil {
		// The wrapped Header method stops heartbeats. Setting an early task ID
		// must preserve them while the original upstream task is being polled.
		k.mu.Lock()
		if !k.writer.Written() {
			k.writer.Header().Set("X-WindHub-Upstream-Task-ID", taskID)
		}
		k.mu.Unlock()
		return
	}
	c.Header("X-WindHub-Upstream-Task-ID", taskID)
}
