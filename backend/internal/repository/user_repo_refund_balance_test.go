package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func newRefundBalanceRepoMock(t *testing.T) (*userRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	driver := entsql.OpenDB(dialect.Postgres, db)
	client := dbent.NewClient(dbent.Driver(driver))
	t.Cleanup(func() { _ = client.Close() })
	return newUserRepositoryWithSQL(client, db), mock
}

// 退款只调余额，SQL 里不得出现 total_recharged。
//
// UpdateBalance 在 amount > 0 时会 AddTotalRecharged，把退款当成一笔新充值；
// total_recharged 驱动百分比制的低余额提醒阈值（resolveBalanceThreshold），
// 被退款抬高后会提前告警。退款只是把错扣的钱放回去，不是新充值。
func TestRefundBalance_AdjustsBalanceWithoutCountingAsRecharge(t *testing.T) {
	repo, mock := newRefundBalanceRepoMock(t)
	mock.ExpectExec(`UPDATE users SET balance = GREATEST\(balance \+ \$1, 0\), updated_at = NOW\(\) WHERE id = \$2 AND deleted_at IS NULL`).
		WithArgs(39.5, int64(6)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, repo.RefundBalance(context.Background(), 6, 39.5))
	require.NoError(t, mock.ExpectationsWereMet())
}

// 用户不存在（或已软删）时要报 ErrUserNotFound，不能静默当成退款成功。
func TestRefundBalance_MissingUser(t *testing.T) {
	repo, mock := newRefundBalanceRepoMock(t)
	mock.ExpectExec(`UPDATE users SET balance = GREATEST\(balance \+ \$1, 0\), updated_at = NOW\(\) WHERE id = \$2 AND deleted_at IS NULL`).
		WithArgs(1.0, int64(404)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.RefundBalance(context.Background(), 404, 1)
	require.ErrorIs(t, err, service.ErrUserNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}
