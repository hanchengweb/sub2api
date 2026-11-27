package voiceassets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"github.com/google/uuid"
)

type Service struct {
	Store    Store
	Routes   Routes
	Supplier Supplier
	Decode   func(context.Context, []byte, string) ([]byte, error)
	Reclaim  *Reclaimer
}

func validName(s string) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) < 1 || utf8.RuneCountInString(s) > 80 {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func validOwner(o mediatasks.Owner) bool {
	id, e := uuid.Parse(o.ActorID)
	return e == nil && id != uuid.Nil && id.String() == o.ActorID
}
func OfficialVoice(id string) bool {
	return mediatasks.ValidVoice(id)
}
func (s *Service) List(ctx context.Context, o mediatasks.Owner) ([]*Asset, error) {
	if !validOwner(o) {
		return nil, mediatasks.ErrNotFound
	}
	values, err := s.Store.List(ctx, o)
	for i, a := range values {
		values[i] = a.Public()
	}
	return values, err
}
func (s *Service) Get(ctx context.Context, o mediatasks.Owner, id string) (*Asset, error) {
	if !validOwner(o) {
		return nil, mediatasks.ErrNotFound
	}
	a, e := s.Store.Get(ctx, o, id)
	if e != nil {
		return nil, e
	}
	return a.Public(), nil
}
func (s *Service) Official(ctx context.Context, o mediatasks.Owner, name, voice string) (*Asset, error) {
	name = strings.TrimSpace(name)
	if !validOwner(o) || !validName(name) || !OfficialVoice(voice) {
		return nil, mediatasks.ErrInvalid
	}
	p, e := s.Routes.Resolve(ctx, o, mediatasks.Audio, mediatasks.SpeechModel)
	if e != nil {
		return nil, e
	}
	now := time.Now().UTC()
	a := &Asset{ID: uuid.NewString(), Kind: "official", Name: name, Model: mediatasks.SpeechModel, SpeakerID: voice, AccountID: p.AccountID, Owner: o, Enabled: true, Status: "ready", CreatedAt: now, UpdatedAt: now}
	if e = s.Store.CreateOfficial(ctx, a); e != nil {
		return nil, e
	}
	return a.Public(), nil
}
func (s *Service) Clone(ctx context.Context, o mediatasks.Owner, requestID, name, format, demo string, audio []byte, consent bool) (*Asset, error) {
	if !consent {
		return nil, ErrConsent
	}
	name = strings.TrimSpace(name)
	demo = strings.TrimSpace(demo)
	rid, e := uuid.Parse(requestID)
	if !validOwner(o) || e != nil || rid == uuid.Nil || !validName(name) || !ValidSample(audio, format) || (demo != "" && (utf8.RuneCountInString(demo) < 4 || utf8.RuneCountInString(demo) > 300)) {
		return nil, mediatasks.ErrInvalid
	}
	for _, r := range demo {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return nil, mediatasks.ErrInvalid
		}
	}
	slots, e := s.Routes.CloneSlots(ctx, o)
	if e != nil {
		return nil, e
	}
	h := sha256.New()
	for _, v := range []string{name, format, demo} {
		h.Write([]byte(v))
		h.Write([]byte{0})
	}
	h.Write(audio)
	now := time.Now().UTC()
	a := &Asset{ID: uuid.NewString(), Kind: "clone", Name: name, Model: mediatasks.CloneModel, Owner: o, Enabled: true, Status: "indeterminate", RequestID: rid.String(), Digest: hex.EncodeToString(h.Sum(nil)), CreatedAt: now, UpdatedAt: now}
	decode := s.Decode
	if decode == nil {
		decode = DecodeSample
	}
	canonical, e := decode(ctx, audio, format)
	if e != nil {
		return nil, e
	}
	a, created, e := s.Store.ClaimClone(ctx, a, slots)
	if e != nil {
		return nil, e
	}
	if !created {
		return a.Public(), nil
	}
	// The durable attempt and exact prepaid speaker already exist. Neither a
	// canceled request nor a process restart may issue another training POST.
	p, e := s.Routes.Reopen(ctx, o, &mediatasks.Task{Owner: o, Kind: mediatasks.Audio, Model: a.Model, AccountID: a.AccountID})
	if e != nil {
		a.Error = &mediatasks.TaskError{Code: "voice_clone_verification_pending", Message: "原声音渠道暂不可用，请联系管理员核对原资产。"}
		_ = s.Store.UpdateTraining(context.WithoutCancel(ctx), a)
		return a.Public(), nil
	}
	call, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer cancel()
	// Never overwrite a voice unless the speaker is empty, or this platform itself
	// trained it and released it when its asset was deleted.
	// A crash during this check remains blocked by the persisted pending marker.
	preflight := s.Supplier.Query(call, p, a.RequestID, a.SpeakerID)
	a.Remaining = preflight.Remaining
	recycled := a.Recycled && preflight.Status != "untrained" && preflight.Reusable()
	if preflight.Status != "untrained" && !recycled {
		a.Status = "failed"
		a.Error = &mediatasks.TaskError{Code: "voice_clone_slot_not_verified_empty", Message: "克隆名额尚未确认可用，请联系管理员核对原名额。"}
		// A definitive supplier answer retires the speaker; network doubt does not.
		a.RetireSlot = preflight.Unusable() || (!a.Recycled && preflight.Status == "ready")
		if e = s.Store.UpdateTraining(context.WithoutCancel(ctx), a); e != nil {
			return nil, e
		}
		return a.Public(), nil
	}
	if recycled {
		baseline := *preflight.Remaining
		a.TrainingBaseline = &baseline
	}
	a.Error = nil
	if e = s.Store.UpdateTraining(context.WithoutCancel(ctx), a); e != nil {
		return nil, e
	}
	result := s.Supplier.Train(call, p, a.RequestID, a.SpeakerID, "wav", canonical, demo)
	s.apply(a, result)
	if e = s.Store.UpdateTraining(context.WithoutCancel(ctx), a); e != nil {
		return nil, e
	}
	return a.Public(), nil
}
func (s *Service) apply(a *Asset, r TrainingResult) {
	a.Status = r.Status
	if a.Status != "ready" && a.Status != "training" && a.Status != "failed" {
		a.Status = "indeterminate"
	}
	a.UpdatedAt = time.Now().UTC()
	a.Error = nil
	if r.ErrorCode != "" {
		a.Error = &mediatasks.TaskError{Code: r.ErrorCode, Message: "克隆结果需要核对，请查询原声音资产，勿重复上传。"}
	}
	a.Remaining = r.Remaining
	// On a recycled speaker the previous owner's voice also reports "ready" or
	// "failed"; only a consumed training attempt proves the result is this one.
	// A rejected request (4xx) is this request's own definitive answer.
	if a.TrainingBaseline != nil && r.ErrorCode != "voice_clone_rejected" && (a.Status == "ready" || a.Status == "failed") && (r.Remaining == nil || *r.Remaining >= *a.TrainingBaseline) {
		a.Status = "training"
		a.Error = nil
	}
}
func (s *Service) Refresh(ctx context.Context, o mediatasks.Owner, id string) (*Asset, error) {
	if !validOwner(o) {
		return nil, mediatasks.ErrNotFound
	}
	a, e := s.Store.Get(ctx, o, id)
	if e != nil {
		return nil, e
	}
	if a.Kind != "clone" {
		return a.Public(), nil
	}
	if a.Error != nil && (a.Error.Code == "voice_clone_verification_pending" || a.Error.Code == "voice_clone_slot_not_verified_empty") {
		return a.Public(), nil
	}
	p, e := s.Routes.Reopen(ctx, o, &mediatasks.Task{Owner: o, Kind: mediatasks.Audio, Model: a.Model, AccountID: a.AccountID})
	if e != nil {
		return nil, e
	}
	s.apply(a, s.Supplier.Query(ctx, p, a.RequestID, a.SpeakerID))
	if e = s.Store.UpdateTraining(ctx, a); e != nil {
		return nil, e
	}
	return a.Public(), nil
}
func (s *Service) Patch(ctx context.Context, o mediatasks.Owner, id string, name *string, enabled *bool) (*Asset, error) {
	if !validOwner(o) {
		return nil, mediatasks.ErrNotFound
	}
	if name != nil {
		*name = strings.TrimSpace(*name)
		if !validName(*name) {
			return nil, mediatasks.ErrInvalid
		}
	}
	if name == nil && enabled == nil {
		return nil, mediatasks.ErrInvalid
	}
	a, e := s.Store.Patch(ctx, o, id, name, enabled)
	if e != nil {
		return nil, e
	}
	return a.Public(), nil
}
func (s *Service) Delete(ctx context.Context, o mediatasks.Owner, id string) error {
	if !validOwner(o) {
		return mediatasks.ErrNotFound
	}
	return s.Store.Delete(ctx, o, id)
}
func (s *Service) Cloning(ctx context.Context, o mediatasks.Owner) (bool, error) {
	slots, e := s.Routes.CloneSlots(ctx, o)
	if e != nil {
		return false, e
	}
	return s.Store.HasFreeSlot(ctx, o, slots)
}
func (s *Service) ResolveVoiceAsset(ctx context.Context, o mediatasks.Owner, r mediatasks.Request) (mediatasks.Request, mediatasks.Provider, error) {
	if !validOwner(o) {
		return r, mediatasks.Provider{}, mediatasks.ErrNotFound
	}
	a, e := s.Store.Get(ctx, o, r.VoiceAssetID)
	if e != nil {
		return r, mediatasks.Provider{}, e
	}
	if !a.Enabled || a.Status != "ready" || a.Deleted || a.Model != r.Model {
		return r, mediatasks.Provider{}, mediatasks.ErrRoute
	}
	// A fresh generation needs the currently published model permission as well
	// as the asset's pinned account; a historical slot is not a model grant.
	if _, e = s.Routes.Resolve(ctx, o, mediatasks.Audio, a.Model); e != nil {
		return r, mediatasks.Provider{}, e
	}
	p, e := s.Routes.Reopen(ctx, o, &mediatasks.Task{Owner: o, Kind: mediatasks.Audio, Model: a.Model, AccountID: a.AccountID})
	if e != nil {
		return r, p, e
	}
	if a.Kind == "clone" && s.Reclaim != nil && s.Reclaim.Config.ExpiryEnabled {
		if s.Reclaim.Store == nil {
			return r, p, mediatasks.ErrUnavailable
		}
		if e = s.Reclaim.Store.TouchVoiceUse(ctx, o, a.ID); e != nil {
			return r, p, e
		}
	}
	r.ResolvedVoice, r.ResourceID = a.SpeakerID, a.Model
	return r, p, nil
}
