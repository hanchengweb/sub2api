package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeConvRepo 只记录调用，不关心存储细节——这组用例验证的是
// 「什么时候该去回收转存文件」，不是会话本身怎么存。
type fakeConvRepo struct {
	deleted  []string
	trimmed  int
	trimErr  error
	upserted int
}

func (r *fakeConvRepo) List(context.Context, int64, int) ([]PlaygroundConversation, error) {
	return nil, nil
}

func (r *fakeConvRepo) Upsert(context.Context, int64, *PlaygroundConversation) error {
	r.upserted++
	return nil
}

func (r *fakeConvRepo) Delete(_ context.Context, _ int64, id string) error {
	r.deleted = append(r.deleted, id)
	return nil
}

func (r *fakeConvRepo) TrimOldest(context.Context, int64, int) (int, error) {
	return r.trimmed, r.trimErr
}

type countingReclaimer struct{ calls int }

func (c *countingReclaimer) ReclaimUnreferenced(context.Context, int64) (int, error) {
	c.calls++
	return 0, nil
}

func TestDeleteConversationReclaimsMedia(t *testing.T) {
	repo := &fakeConvRepo{}
	rec := &countingReclaimer{}
	svc := NewPlaygroundConversationService(repo, rec)

	require.NoError(t, svc.Delete(context.Background(), 9, "conv-1"))
	require.Equal(t, []string{"conv-1"}, repo.deleted)
	require.Equal(t, 1, rec.calls, "会话删掉之后它引用的图片视频再也看不到，必须回收")
}

// 保存是高频动作（流式对话每秒好几次）。没裁掉任何会话就不该去扫转存表。
func TestSaveReclaimsOnlyWhenConversationsWereTrimmed(t *testing.T) {
	conv := &PlaygroundConversation{ID: "c1", Mode: "chat", Title: "t"}

	repo := &fakeConvRepo{trimmed: 0}
	rec := &countingReclaimer{}
	svc := NewPlaygroundConversationService(repo, rec)
	require.NoError(t, svc.Save(context.Background(), 9, conv))
	require.Zero(t, rec.calls)

	repo2 := &fakeConvRepo{trimmed: 3}
	rec2 := &countingReclaimer{}
	svc2 := NewPlaygroundConversationService(repo2, rec2)
	require.NoError(t, svc2.Save(context.Background(), 9, conv))
	require.Equal(t, 1, rec2.calls)
}

// 媒体转存没配起来时（reclaimer 为 nil），会话该照常能存能删。
func TestConversationServiceWorksWithoutReclaimer(t *testing.T) {
	repo := &fakeConvRepo{trimmed: 2}
	svc := NewPlaygroundConversationService(repo, nil)

	require.NoError(t, svc.Save(context.Background(), 9, &PlaygroundConversation{ID: "c1", Mode: "chat"}))
	require.NoError(t, svc.Delete(context.Background(), 9, "c1"))
}
