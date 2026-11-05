package mediatasks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

type Engine struct {
	Store       Store
	Quota       Quota
	Routes      Routes
	Supplier    Supplier
	VoiceAssets VoiceAssetResolver
	slots       chan struct{}
}

func NewEngine(store Store, quota Quota, routes Routes, supplier Supplier) *Engine {
	return &Engine{Store: store, Quota: quota, Routes: routes, Supplier: supplier, slots: make(chan struct{}, 8)}
}
func (e *Engine) Create(ctx context.Context, o Owner, kind, requestID string, r Request) (*Task, error) {
	r, units, digest, err := Normalize(kind, r)
	if err != nil {
		return nil, err
	}
	prior, err := e.Store.Find(ctx, o, kind, requestID)
	if err == nil {
		if prior.Digest != digest {
			return nil, ErrConflict
		}
		return prior.Public(), nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	var p Provider
	if r.VoiceAssetID != "" {
		if e.VoiceAssets == nil || o.ActorID == "" {
			return nil, ErrNotFound
		}
		r, p, err = e.VoiceAssets.ResolveVoiceAsset(ctx, o, r)
	} else {
		p, err = e.Routes.Resolve(ctx, o, kind, r.Model)
	}
	if err != nil {
		return nil, err
	}
	select {
	case e.slots <- struct{}{}:
	default:
		return nil, ErrBusy
	}
	owned := true
	defer func() {
		if owned {
			<-e.slots
		}
	}()
	idBytes := make([]byte, 16)
	if _, err = rand.Read(idBytes); err != nil {
		return nil, ErrUnavailable
	}
	quotaID := fmt.Sprintf("%s:%s", kind, requestID)
	if o.ActorID != "" {
		quotaID = fmt.Sprintf("%s:%s:%s", kind, o.ActorID, requestID)
	}
	if err = e.Quota.Reserve(ctx, o, quotaID, digest, kind, r.Model, units); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	task := &Task{ID: "media_" + hex.EncodeToString(idBytes), Owner: o, Kind: kind, Model: r.Model, Status: "queued", RequestID: requestID, Digest: digest, QuotaRequestID: quotaID, AccountID: p.AccountID, CreatedAt: now, UpdatedAt: now, LeaseUntil: now.Add(150 * time.Second), Usage: Usage{Quantity: units}}
	task, created, err := e.Store.Create(ctx, task)
	if err != nil {
		return nil, err
	}
	if !created {
		return task.Public(), nil
	}
	if err = e.Quota.Bind(ctx, o.UserID, quotaID, task.ID); err != nil {
		task.Status = "indeterminate"
		task.Error = &TaskError{Code: "media_task_binding_unavailable", Message: "任务额度正在核对，请勿重复生成。"}
		_ = e.Store.Update(context.WithoutCancel(ctx), task, nil)
		return task.Public(), nil
	}
	// Input only lives in this bounded execution. A restart leaves a durable
	// unresolved task; it never reconstructs or automatically resubmits input.
	execution := context.WithoutCancel(ctx)
	owned = false
	executionTask := *task
	go func() { defer func() { <-e.slots }(); e.execute(execution, &executionTask, p, r) }()
	return task.Public(), nil
}
func (e *Engine) execute(ctx context.Context, t *Task, p Provider, r Request) {
	ctx, cancel := context.WithTimeout(ctx, 125*time.Second)
	defer cancel()
	started, err := e.Store.Start(ctx, t.ID)
	if err != nil || !started {
		return
	}
	if err = e.Quota.Validate(ctx, t.Owner.UserID, t.QuotaRequestID); err != nil {
		out := rejected("media_quota_dispatch_rejected")
		_ = e.apply(ctx, t, out)
		return
	}
	_ = e.apply(ctx, t, e.Supplier.Submit(ctx, p, t.Owner, t.RequestID, r))
}
func (e *Engine) apply(ctx context.Context, t *Task, out Outcome) error {
	if out.Status == "succeeded" {
		valid := t.Kind == Audio && out.ContentType == "audio/mpeg" && ValidMP3(out.Content) || t.Kind == Model3D && out.ContentType == "model/gltf-binary" && ValidateGLB(out.Content) == nil
		if !valid {
			out.Status = "indeterminate"
			out.ErrorCode = "media_content_invalid"
			out.Content = nil
			out.ContentType = ""
			out.SettlementUnits = nil
		}
	}
	t.Status, t.Progress = out.Status, out.Progress
	t.ProviderID = out.ProviderID
	t.UpdatedAt = time.Now().UTC()
	t.Usage.ProviderUsage = out.ProviderUsage
	if out.ErrorCode != "" {
		t.Error = &TaskError{Code: out.ErrorCode, Message: ErrorMessage(out.ErrorCode)}
	} else {
		t.Error = nil
	}
	t.ContentType = out.ContentType
	t.SettlementUnits = out.SettlementUnits
	if out.Status == "succeeded" {
		n := t.Usage.Quantity
		t.SettlementUnits = &n
	}
	// Persist the provider result and output before settling. A failed quota
	// write can be reconciled by GET without ever resubmitting generation.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if err := e.Store.Update(saveCtx, t, out.Content); err != nil {
		return ErrUnavailable
	}
	// Settle the committed record, never a losing concurrent poll's observation.
	persisted, err := e.Store.Get(saveCtx, t.Owner, t.Kind, t.ID)
	if err != nil {
		return err
	}
	*t = *persisted
	e.settle(saveCtx, t)
	return nil
}
func (e *Engine) settle(ctx context.Context, t *Task) {
	if t.SettlementUnits != nil {
		_ = e.Quota.Settle(ctx, t.Owner.UserID, t.QuotaRequestID, t.ID, *t.SettlementUnits, "provider_receipt")
	}
}
func (e *Engine) Get(ctx context.Context, o Owner, kind, id string) (*Task, error) {
	t, err := e.Store.Get(ctx, o, kind, id)
	if err != nil {
		return nil, err
	}
	if t.Status == "succeeded" || t.Status == "failed" {
		e.settle(ctx, t)
		return t.Public(), nil
	}
	if t.Kind == Model3D && t.ProviderID != "" {
		p, err := e.Routes.Reopen(ctx, o, t)
		if err != nil {
			return nil, err
		}
		select {
		case e.slots <- struct{}{}:
			defer func() { <-e.slots }()
		default:
			return t.Public(), nil
		}
		pollCtx, cancel := context.WithTimeout(ctx, 100*time.Second)
		defer cancel()
		priorID := t.ProviderID
		out := e.Supplier.Poll(pollCtx, p, priorID)
		if out.ProviderID == "" {
			out.ProviderID = priorID
		}
		if err = e.apply(ctx, t, out); err != nil {
			return nil, err
		}
		return t.Public(), nil
	}
	if time.Now().After(t.LeaseUntil) {
		t.Status = "indeterminate"
		t.Error = &TaskError{Code: "media_submit_indeterminate", Message: "任务结果待核对，请勿重新提交。"}
		t.UpdatedAt = time.Now().UTC()
		if err = e.Store.Update(ctx, t, nil); err != nil {
			return nil, ErrUnavailable
		}
	}
	return t.Public(), nil
}
func (e *Engine) Content(ctx context.Context, o Owner, kind, id string) ([]byte, string, error) {
	select {
	case e.slots <- struct{}{}:
		defer func() { <-e.slots }()
	default:
		return nil, "", ErrBusy
	}
	return e.Store.Content(ctx, o, kind, id)
}
