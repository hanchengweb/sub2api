//go:build unit

package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	bindMediaTaskChargeSQL = `(?s)INSERT INTO media_task_charges \(task_key, task_id, user_id, api_key_id, group_id, credits\)\s+VALUES \(\$1, NULLIF\(\$2, ''\), \$3, \$4, \$5, \$6\)\s+ON CONFLICT \(task_key\) DO UPDATE\s+SET credits = EXCLUDED\.credits, task_id = EXCLUDED\.task_id\s+WHERE media_task_charges\.refunded_at IS NULL`
	takeMediaTaskChargeSQL = `(?s)UPDATE media_task_charges\s+SET refunded_at = NOW\(\)\s+WHERE task_key = \$1 AND refunded_at IS NULL\s+RETURNING credits`
)

func newMediaChargeRepo(t *testing.T) (*usageBillingRepository, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	return &usageBillingRepository{db: db}, mock, func() { _ = db.Close() }
}

// 绑定必须落库，且带上用户/密钥/分组，否则退款时无从定位该退给谁。
func TestBindMediaTaskCharge_PersistsCharge(t *testing.T) {
	repo, mock, done := newMediaChargeRepo(t)
	defer done()

	gid := int64(4)
	mock.ExpectExec(bindMediaTaskChargeSQL).
		WithArgs("k1", "tsk_img_abc", int64(6), int64(9), int64(4), 39.5).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// task_id 必须一并落库：Webhook 只带 task_id，不存就反查不到该退给谁。
	require.NoError(t, repo.BindMediaTaskCharge(context.Background(), &service.MediaTaskChargeCommand{
		TaskKey: "k1", TaskID: "tsk_img_abc", UserID: 6, APIKeyID: 9, GroupID: &gid, Credits: 39.5,
	}))
	require.NoError(t, mock.ExpectationsWereMet())
}

// 未扣费的任务没有可退金额，不该写库。
func TestBindMediaTaskCharge_SkipsZeroCredits(t *testing.T) {
	repo, mock, done := newMediaChargeRepo(t)
	defer done()

	require.NoError(t, repo.BindMediaTaskCharge(context.Background(), &service.MediaTaskChargeCommand{
		TaskKey: "k1", UserID: 6, APIKeyID: 9, Credits: 0,
	}))
	require.NoError(t, mock.ExpectationsWereMet(), "不应产生任何 SQL")
}

// 参数不全直接报错：静默跳过会让失败任务永远退不了款且无人察觉。
func TestBindMediaTaskCharge_RejectsInvalidCommand(t *testing.T) {
	repo, _, done := newMediaChargeRepo(t)
	defer done()

	ctx := context.Background()
	for _, cmd := range []*service.MediaTaskChargeCommand{
		{TaskKey: "", UserID: 6, APIKeyID: 9, Credits: 1},
		{TaskKey: "k", UserID: 0, APIKeyID: 9, Credits: 1},
		{TaskKey: "k", UserID: 6, APIKeyID: 0, Credits: 1},
	} {
		require.Error(t, repo.BindMediaTaskCharge(ctx, cmd))
	}
}

// 取款要拿到金额并由 SQL 自身完成「标记已退」。
func TestTakeMediaTaskCharge_ReturnsCredits(t *testing.T) {
	repo, mock, done := newMediaChargeRepo(t)
	defer done()

	mock.ExpectQuery(takeMediaTaskChargeSQL).
		WithArgs("k1").
		WillReturnRows(sqlmock.NewRows([]string{"credits"}).AddRow(39.5))

	credits, ok, err := repo.TakeMediaTaskCharge(context.Background(), "k1")
	require.NoError(t, err)
	require.True(t, ok)
	require.InDelta(t, 39.5, credits, 1e-9)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 幂等：已退过的行被 WHERE refunded_at IS NULL 排除，返回 0 行 → 不再退第二次。
// 客户端会反复轮询同一个失败任务，这条是防重复退款的关键。
func TestTakeMediaTaskCharge_AlreadyRefundedYieldsNothing(t *testing.T) {
	repo, mock, done := newMediaChargeRepo(t)
	defer done()

	mock.ExpectQuery(takeMediaTaskChargeSQL).
		WithArgs("k1").
		WillReturnRows(sqlmock.NewRows([]string{"credits"}))

	credits, ok, err := repo.TakeMediaTaskCharge(context.Background(), "k1")
	require.NoError(t, err)
	require.False(t, ok)
	require.Zero(t, credits)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 查库出错必须把错误往上报，调用方据此选择「不退」而不是当成「没有可退」。
func TestTakeMediaTaskCharge_PropagatesError(t *testing.T) {
	repo, mock, done := newMediaChargeRepo(t)
	defer done()

	mock.ExpectQuery(takeMediaTaskChargeSQL).
		WithArgs("k1").
		WillReturnError(errors.New("boom"))

	_, ok, err := repo.TakeMediaTaskCharge(context.Background(), "k1")
	require.Error(t, err)
	require.False(t, ok)
}

// 空 key 不查库，也不算错误——调用方传空表示这次没有可退记录。
func TestTakeMediaTaskCharge_EmptyKeyIsNoop(t *testing.T) {
	repo, mock, done := newMediaChargeRepo(t)
	defer done()

	credits, ok, err := repo.TakeMediaTaskCharge(context.Background(), "  ")
	require.NoError(t, err)
	require.False(t, ok)
	require.Zero(t, credits)
	require.NoError(t, mock.ExpectationsWereMet())
}

const (
	takeByTaskIDSQL  = `(?s)UPDATE media_task_charges\s+SET refunded_at = NOW\(\)\s+WHERE task_id = \$1 AND refunded_at IS NULL\s+RETURNING user_id, credits`
	recordWebhookSQL = `(?s)INSERT INTO webhook_events \(provider, event_id, event_type, task_id\)\s+VALUES \(\$1, \$2, \$3, NULLIF\(\$4, ''\)\)\s+ON CONFLICT \(provider, event_id\) DO NOTHING`
)

// Webhook 只带 task_id，必须能据此取到「退给谁、退多少」。
func TestTakeMediaTaskChargeByTaskID_ReturnsUserAndCredits(t *testing.T) {
	repo, mock, done := newMediaChargeRepo(t)
	defer done()

	mock.ExpectQuery(takeByTaskIDSQL).
		WithArgs("tsk_vid_abc").
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "credits"}).AddRow(int64(6), 185.0))

	taken, err := repo.TakeMediaTaskChargeByTaskID(context.Background(), "tsk_vid_abc")
	require.NoError(t, err)
	require.NotNil(t, taken)
	require.Equal(t, int64(6), taken.UserID)
	require.InDelta(t, 185.0, taken.Credits, 1e-9)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 幂等：已退过的行被 WHERE refunded_at IS NULL 排除，重投不会二次退款。
func TestTakeMediaTaskChargeByTaskID_AlreadyRefunded(t *testing.T) {
	repo, mock, done := newMediaChargeRepo(t)
	defer done()

	mock.ExpectQuery(takeByTaskIDSQL).
		WithArgs("tsk_vid_abc").
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "credits"}))

	taken, err := repo.TakeMediaTaskChargeByTaskID(context.Background(), "tsk_vid_abc")
	require.NoError(t, err)
	require.Nil(t, taken)
}

// 空 task_id 不查库也不算错误。
func TestTakeMediaTaskChargeByTaskID_EmptyIsNoop(t *testing.T) {
	repo, mock, done := newMediaChargeRepo(t)
	defer done()

	taken, err := repo.TakeMediaTaskChargeByTaskID(context.Background(), "  ")
	require.NoError(t, err)
	require.Nil(t, taken)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 首次事件返回 true，重复投递（冲突 0 行）返回 false——退款是否执行全看这个返回值。
func TestRecordWebhookEventOnce_FirstThenDuplicate(t *testing.T) {
	repo, mock, done := newMediaChargeRepo(t)
	defer done()

	mock.ExpectExec(recordWebhookSQL).
		WithArgs("toapis", "evt_1", "generation.failed", "tsk_vid_abc").
		WillReturnResult(sqlmock.NewResult(1, 1))
	first, err := repo.RecordWebhookEventOnce(context.Background(), "toapis", "evt_1", "generation.failed", "tsk_vid_abc")
	require.NoError(t, err)
	require.True(t, first)

	mock.ExpectExec(recordWebhookSQL).
		WithArgs("toapis", "evt_1", "generation.failed", "tsk_vid_abc").
		WillReturnResult(sqlmock.NewResult(0, 0))
	again, err := repo.RecordWebhookEventOnce(context.Background(), "toapis", "evt_1", "generation.failed", "tsk_vid_abc")
	require.NoError(t, err)
	require.False(t, again, "重复 event id 必须返回 false，否则会重复退款")
	require.NoError(t, mock.ExpectationsWereMet())
}

// provider / event_id 缺失要报错，不能当成「首次事件」放行退款。
func TestRecordWebhookEventOnce_RejectsMissingKeys(t *testing.T) {
	repo, _, done := newMediaChargeRepo(t)
	defer done()

	_, err := repo.RecordWebhookEventOnce(context.Background(), "", "evt_1", "t", "")
	require.Error(t, err)
	_, err = repo.RecordWebhookEventOnce(context.Background(), "toapis", "", "t", "")
	require.Error(t, err)
}
