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
	bindMediaTaskChargeSQL = `(?s)INSERT INTO media_task_charges \(task_key, user_id, api_key_id, group_id, credits\)\s+VALUES \(\$1, \$2, \$3, \$4, \$5\)\s+ON CONFLICT \(task_key\) DO UPDATE\s+SET credits = EXCLUDED\.credits\s+WHERE media_task_charges\.refunded_at IS NULL`
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
		WithArgs("k1", int64(6), int64(9), int64(4), 39.5).
		WillReturnResult(sqlmock.NewResult(1, 1))

	require.NoError(t, repo.BindMediaTaskCharge(context.Background(), &service.MediaTaskChargeCommand{
		TaskKey: "k1", UserID: 6, APIKeyID: 9, GroupID: &gid, Credits: 39.5,
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
