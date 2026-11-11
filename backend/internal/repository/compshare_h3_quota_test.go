//go:build unit

package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type h3QuotaFixture struct {
	db    *sql.DB
	repo  *usageBillingRepository
	quota *organizationMediaQuotaRepository
}

func newH3QuotaFixture(t *testing.T, seconds int64) h3QuotaFixture {
	t.Helper()
	db := compShareH3PostgresTestDB(t)
	f := h3QuotaFixture{db: db, repo: &usageBillingRepository{db: db}, quota: &organizationMediaQuotaRepository{db: db}}
	_, err := db.Exec(`UPDATE users SET account_type='organization_service' WHERE id=1`)
	require.NoError(t, err)
	_, err = f.quota.Grant(context.Background(), 1, service.MediaQuotaGrant{
		RequestID: "h3-fixture-grant", ContractRef: "fixture-contract", Reason: "fixture", VideoSeconds: seconds,
		EffectiveAt: time.Now().UTC().Add(-time.Hour), ExpiresAt: time.Now().UTC().Add(time.Hour),
	})
	require.NoError(t, err)
	return f
}

func h3QuotaCommand(id string, duration int) *service.UsageBillingCommand {
	snap := &service.CompShareH3Snapshot{
		RequestID: id, UserID: 1, APIKeyID: 7, GroupID: 4, AccountID: 18,
		PayloadHash: "fixture-payload", Resolution: "4k", Duration: duration,
		Prices:     map[string]float64{"480p": 1, "768p": 2, "1080p": 3, "2k": 4, "4k": 5},
		Multiplier: 2, BaseCost: float64(duration) * 5, Credits: float64(duration) * 10,
	}
	return &service.UsageBillingCommand{
		RequestID: id, APIKeyID: 7, UserID: 1, AccountID: 18, AccountType: service.AccountTypeAPIKey,
		Model: service.CompShareH3Model, BalanceCost: snap.Credits, APIKeyQuotaCost: snap.Credits,
		APIKeyRateLimitCost: snap.Credits, AccountQuotaCost: snap.BaseCost, CompShareH3: snap,
	}
}

func (f h3QuotaFixture) checkQuota(t *testing.T, reserved, consumed int64) {
	t.Helper()
	var gotReserved, gotConsumed int64
	require.NoError(t, f.db.QueryRow(`SELECT COALESCE(sum(reserved),0),COALESCE(sum(consumed),0) FROM organization_media_lots WHERE kind='video'`).Scan(&gotReserved, &gotConsumed))
	require.Equal(t, reserved, gotReserved)
	require.Equal(t, consumed, gotConsumed)
}

func (f h3QuotaFixture) balance(t *testing.T) float64 {
	t.Helper()
	var amount float64
	require.NoError(t, f.db.QueryRow(`SELECT balance FROM users WHERE id=1`).Scan(&amount))
	return amount
}

func (f h3QuotaFixture) apply(t *testing.T, cmd *service.UsageBillingCommand) {
	t.Helper()
	result, err := f.repo.Apply(context.Background(), cmd)
	require.NoError(t, err)
	require.True(t, result.Applied)
	_, err = f.db.Exec(`INSERT INTO usage_logs(request_id,api_key_id,total_cost,actual_cost,output_cost,account_stats_cost,video_resolution,video_duration_seconds)
 VALUES($1,7,$2,$3,$2,$2,'4k',$4)`, cmd.RequestID, cmd.CompShareH3.BaseCost, cmd.CompShareH3.Credits, cmd.CompShareH3.Duration)
	require.NoError(t, err)
}

func TestCompShareH3QuotaReservesAndSettlesOutput(t *testing.T) {
	f := newH3QuotaFixture(t, 10)
	ctx := context.Background()
	cmd := h3QuotaCommand("csh3-quota-four-seconds", 4)
	f.apply(t, cmd)
	require.Equal(t, cmd.RequestID, cmd.CompShareH3.MediaQuotaRequestID)
	require.Equal(t, 960.0, f.balance(t))
	f.checkQuota(t, 4, 0)
	claimed, err := f.repo.ClaimCompShareH3(ctx, cmd.RequestID)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, f.repo.BindCompShareH3(ctx, cmd.RequestID, "provider-four-seconds"))
	snap, err := f.repo.GetCompShareH3(ctx, "provider-four-seconds", 1, 7)
	require.NoError(t, err)
	require.Equal(t, cmd.RequestID, snap.MediaQuotaRequestID)
	var reservationTask, bindingTask string
	require.NoError(t, f.db.QueryRow(`SELECT r.task_id,b.task_id FROM organization_media_reservations r
 JOIN organization_media_task_bindings b ON b.reservation_id=r.id WHERE r.request_id=$1`, cmd.RequestID).Scan(&reservationTask, &bindingTask))
	require.Equal(t, "provider-four-seconds", reservationTask)
	require.Equal(t, reservationTask, bindingTask)
	refund, err := f.repo.SettleCompShareH3(ctx, cmd.RequestID, 1, 7, "succeeded", "4K", 4)
	require.NoError(t, err)
	require.Zero(t, refund)
	f.checkQuota(t, 0, 4)
	refund, err = f.repo.SettleCompShareH3(ctx, cmd.RequestID, 1, 7, "succeeded", "4K", 4)
	require.NoError(t, err)
	require.Zero(t, refund)
	f.checkQuota(t, 0, 4)
	var actual int64
	var measurement, status string
	require.NoError(t, f.db.QueryRow(`SELECT actual_units,measurement,status FROM organization_media_reservations WHERE request_id=$1`, cmd.RequestID).Scan(&actual, &measurement, &status))
	require.Equal(t, int64(4), actual)
	require.Equal(t, "provider_output", measurement)
	require.Equal(t, "completed", status)
}

func TestCompShareH3QuotaPartialOutputAndResolutionRefund(t *testing.T) {
	f := newH3QuotaFixture(t, 10)
	cmd := h3QuotaCommand("csh3-quota-downgrade", 5)
	f.apply(t, cmd)
	f.checkQuota(t, 5, 0)
	refund, err := f.repo.SettleCompShareH3(context.Background(), cmd.RequestID, 1, 7, "succeeded", "768P", 3.2)
	require.NoError(t, err)
	require.InDelta(t, 37.2, refund, 0.000001)
	require.InDelta(t, 987.2, f.balance(t), 0.000001)
	f.checkQuota(t, 0, 4) // whole seconds are rounded up once from the measured output.
}

func TestCompShareH3QuotaFailureReleasesBothReservations(t *testing.T) {
	for _, status := range []string{"failed", "blocked", "cancelled", "submission_rejected"} {
		t.Run(status, func(t *testing.T) {
			f := newH3QuotaFixture(t, 10)
			cmd := h3QuotaCommand("csh3-quota-failure", 4)
			f.apply(t, cmd)
			f.checkQuota(t, 4, 0)
			refund, err := f.repo.SettleCompShareH3(context.Background(), cmd.RequestID, 1, 7, status, "", 0)
			require.NoError(t, err)
			require.Equal(t, 40.0, refund)
			require.Equal(t, 1000.0, f.balance(t))
			f.checkQuota(t, 0, 0)
			refund, err = f.repo.SettleCompShareH3(context.Background(), cmd.RequestID, 1, 7, status, "", 0)
			require.NoError(t, err)
			require.Zero(t, refund)
		})
	}
}

func TestCompShareH3QuotaUnknownAndUnverifiedUsageStayReserved(t *testing.T) {
	f := newH3QuotaFixture(t, 10)
	cmd := h3QuotaCommand("csh3-quota-unknown", 4)
	f.apply(t, cmd)
	for _, input := range []struct {
		user, key int64
		status    string
		seconds   float64
	}{{1, 7, "unknown", 0}, {1, 7, "succeeded", 0}, {1, 8, "failed", 0}, {2, 7, "failed", 0}, {1, 7, "succeeded", 5}} {
		_, err := f.repo.SettleCompShareH3(context.Background(), cmd.RequestID, input.user, input.key, input.status, "768P", input.seconds)
		require.Error(t, err)
		f.checkQuota(t, 4, 0)
		require.Equal(t, 960.0, f.balance(t))
	}
}

func TestCompShareH3QuotaConcurrentLastSecondsAndSettlement(t *testing.T) {
	f := newH3QuotaFixture(t, 4)
	ctx := context.Background()
	var wg sync.WaitGroup
	start := make(chan struct{})
	type applied struct {
		id  string
		err error
	}
	results := make(chan applied, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			id := fmt.Sprintf("csh3-quota-last-%d", i)
			_, err := f.repo.Apply(ctx, h3QuotaCommand(id, 4))
			results <- applied{id, err}
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	winner := ""
	for result := range results {
		if result.err == nil {
			require.Empty(t, winner)
			winner = result.id
		} else {
			require.ErrorIs(t, result.err, service.ErrMediaQuotaExceeded)
		}
	}
	require.NotEmpty(t, winner)
	f.checkQuota(t, 4, 0)
	require.Equal(t, 960.0, f.balance(t))
	for _, table := range []string{"usage_billing_dedup", "media_task_charges", "organization_media_reservations"} {
		var count int
		require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM `+table).Scan(&count))
		require.Equal(t, 1, count, table)
	}
	type settled struct {
		refund float64
		err    error
	}
	settlements := make(chan settled, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			refund, err := f.repo.SettleCompShareH3(ctx, winner, 1, 7, "succeeded", "768P", 4)
			settlements <- settled{refund, err}
		}()
	}
	wg.Wait()
	close(settlements)
	refund := 0.0
	for result := range settlements {
		require.NoError(t, result.err)
		refund += result.refund
	}
	require.Equal(t, 24.0, refund)
	require.Equal(t, 984.0, f.balance(t))
	f.checkQuota(t, 0, 4)
}

func TestCompShareH3QuotaInsufficientOrBlockedRollsBackDebit(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(fmt.Sprintf("blocked=%t", blocked), func(t *testing.T) {
			f := newH3QuotaFixture(t, 3)
			_, err := f.db.Exec(`UPDATE organization_media_accounts SET blocked=$1 WHERE user_id=1`, blocked)
			require.NoError(t, err)
			_, err = f.repo.Apply(context.Background(), h3QuotaCommand("csh3-quota-rejected", 4))
			if blocked {
				require.ErrorIs(t, err, service.ErrMediaQuotaReview)
			} else {
				require.ErrorIs(t, err, service.ErrMediaQuotaExceeded)
			}
			require.Equal(t, 1000.0, f.balance(t))
			f.checkQuota(t, 0, 0)
			for _, table := range []string{"usage_billing_dedup", "media_task_charges", "organization_media_reservations"} {
				var count int
				require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM `+table).Scan(&count))
				require.Zero(t, count, table)
			}
		})
	}
}

func TestCompShareH3QuotaTransactionFailuresLeaveBothLedgersRecoverable(t *testing.T) {
	f := newH3QuotaFixture(t, 10)
	ctx := context.Background()
	_, err := f.db.Exec(`ALTER TABLE media_task_charges ADD CONSTRAINT reject_fixture_receipt CHECK(request_id<>'csh3-quota-insert-fails')`)
	require.NoError(t, err)
	_, err = f.repo.Apply(ctx, h3QuotaCommand("csh3-quota-insert-fails", 4))
	require.Error(t, err)
	f.checkQuota(t, 0, 0)
	require.Equal(t, 1000.0, f.balance(t))
	cmd := h3QuotaCommand("csh3-quota-settle-retry", 4)
	f.apply(t, cmd)
	_, err = f.db.Exec(`ALTER TABLE usage_logs ADD CONSTRAINT reject_fixture_settlement CHECK(actual_cost=40)`)
	require.NoError(t, err)
	_, err = f.repo.SettleCompShareH3(ctx, cmd.RequestID, 1, 7, "succeeded", "768P", 4)
	require.Error(t, err)
	f.checkQuota(t, 4, 0)
	require.Equal(t, 960.0, f.balance(t))
	var settled bool
	require.NoError(t, f.db.QueryRow(`SELECT settled_at IS NOT NULL FROM media_task_charges WHERE request_id=$1`, cmd.RequestID).Scan(&settled))
	require.False(t, settled)
	_, err = f.db.Exec(`ALTER TABLE usage_logs DROP CONSTRAINT reject_fixture_settlement`)
	require.NoError(t, err)
	_, err = f.repo.SettleCompShareH3(ctx, cmd.RequestID, 1, 7, "succeeded", "768P", 4)
	require.NoError(t, err)
	f.checkQuota(t, 0, 4)
	require.Equal(t, 984.0, f.balance(t))
}

func TestCompShareH3QuotaDoesNotRetroactivelyChargeLegacyReceipt(t *testing.T) {
	db := compShareH3PostgresTestDB(t)
	f := h3QuotaFixture{db: db, repo: &usageBillingRepository{db: db}, quota: &organizationMediaQuotaRepository{db: db}}
	cmd := h3QuotaCommand("csh3-quota-legacy", 4)
	f.apply(t, cmd)
	require.Empty(t, cmd.CompShareH3.MediaQuotaRequestID)
	_, err := db.Exec(`UPDATE users SET account_type='organization_service' WHERE id=1`)
	require.NoError(t, err)
	_, err = f.quota.Grant(context.Background(), 1, service.MediaQuotaGrant{
		RequestID: "h3-fixture-grant", ContractRef: "fixture-contract", Reason: "fixture", VideoSeconds: 10,
		EffectiveAt: time.Now().UTC().Add(-time.Hour), ExpiresAt: time.Now().UTC().Add(time.Hour),
	})
	require.NoError(t, err)
	replayed, err := f.repo.Apply(context.Background(), h3QuotaCommand(cmd.RequestID, 4))
	require.NoError(t, err)
	require.False(t, replayed.Applied)
	snap, err := f.repo.GetCompShareH3(context.Background(), cmd.RequestID, 1, 7)
	require.NoError(t, err)
	require.Empty(t, snap.MediaQuotaRequestID)
	_, err = f.repo.SettleCompShareH3(context.Background(), cmd.RequestID, 1, 7, "succeeded", "4K", 4)
	require.NoError(t, err)
	f.checkQuota(t, 0, 0)
	require.Equal(t, 960.0, f.balance(t))
}

func TestCompShareH3QuotaUnconfiguredAccountKeepsBalanceBilling(t *testing.T) {
	for _, accountType := range []string{"personal", "organization_service"} {
		t.Run(accountType, func(t *testing.T) {
			db := compShareH3PostgresTestDB(t)
			_, err := db.Exec(`UPDATE users SET account_type=$1 WHERE id=1`, accountType)
			require.NoError(t, err)
			repo := &usageBillingRepository{db: db}
			cmd := h3QuotaCommand("csh3-quota-unconfigured", 4)
			_, err = repo.Apply(context.Background(), cmd)
			require.NoError(t, err)
			require.Empty(t, cmd.CompShareH3.MediaQuotaRequestID)
			var amount float64
			require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=1`).Scan(&amount))
			require.Equal(t, 960.0, amount)
		})
	}
}

func TestCompShareH3QuotaExpiredGrantStopsDispatch(t *testing.T) {
	f := newH3QuotaFixture(t, 10)
	cmd := h3QuotaCommand("csh3-quota-expired", 4)
	f.apply(t, cmd)
	_, err := f.db.Exec(`UPDATE organization_media_grants SET expires_at=NOW()-INTERVAL '1 second' WHERE user_id=1`)
	require.NoError(t, err)
	claimed, err := f.repo.ClaimCompShareH3(context.Background(), cmd.RequestID)
	require.False(t, claimed)
	require.ErrorIs(t, err, service.ErrMediaQuotaExceeded)
	var dispatched bool
	require.NoError(t, f.db.QueryRow(`SELECT dispatched_at IS NOT NULL FROM media_task_charges WHERE request_id=$1`, cmd.RequestID).Scan(&dispatched))
	require.False(t, dispatched)
	_, err = f.repo.SettleCompShareH3(context.Background(), cmd.RequestID, 1, 7, "submission_rejected", "", 0)
	require.NoError(t, err)
	f.checkQuota(t, 0, 0)
	require.Equal(t, 1000.0, f.balance(t))
}

func TestCompShareH3QuotaConcurrentSameRequestIsReservedOnce(t *testing.T) {
	f := newH3QuotaFixture(t, 10)
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan *service.UsageBillingApplyResult, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := f.repo.Apply(ctx, h3QuotaCommand("csh3-quota-identical", 4))
			results <- result
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.False(t, errors.Is(err, service.ErrMediaQuotaExceeded))
		require.NoError(t, err)
	}
	applied := 0
	for result := range results {
		if result.Applied {
			applied++
		}
	}
	require.Equal(t, 1, applied)
	f.checkQuota(t, 4, 0)
	require.Equal(t, 960.0, f.balance(t))
}

func TestCompShareH3QuotaMissingOrMismatchedReservationFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name   string
		damage string
	}{
		{"missing", `DELETE FROM organization_media_allocations; DELETE FROM organization_media_reservations`},
		{"wrong_key", `UPDATE organization_media_reservations SET api_key_id=999`},
		{"wrong_owner", `INSERT INTO users(id,balance,account_type) VALUES(2,1000,'organization_service'); INSERT INTO organization_media_accounts(user_id) VALUES(2); UPDATE organization_media_reservations SET user_id=2`},
		{"wrong_kind", `UPDATE organization_media_reservations SET kind='image'`},
		{"wrong_units", `UPDATE organization_media_reservations SET units=3`},
		{"wrong_model", `UPDATE organization_media_reservations SET model='other-video'`},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newH3QuotaFixture(t, 10)
			ctx := context.Background()
			cmd := h3QuotaCommand("csh3-quota-corrupt-fixture", 4)
			f.apply(t, cmd)
			_, err := f.db.Exec(test.damage)
			require.NoError(t, err)
			claimed, err := f.repo.ClaimCompShareH3(ctx, cmd.RequestID)
			require.False(t, claimed)
			require.ErrorIs(t, err, service.ErrMediaQuotaConflict)
			require.ErrorIs(t, f.repo.BindCompShareH3(ctx, cmd.RequestID, "provider-corrupt-fixture"), service.ErrMediaQuotaConflict)
			_, err = f.repo.SettleCompShareH3(ctx, cmd.RequestID, 1, 7, "failed", "", 0)
			require.ErrorIs(t, err, service.ErrMediaQuotaConflict)
			var dispatched, settled bool
			require.NoError(t, f.db.QueryRow(`SELECT dispatched_at IS NOT NULL,settled_at IS NOT NULL FROM media_task_charges WHERE request_id=$1`, cmd.RequestID).Scan(&dispatched, &settled))
			require.False(t, dispatched)
			require.False(t, settled)
			require.Equal(t, 960.0, f.balance(t))
			f.checkQuota(t, 4, 0)
		})
	}
	// A terminal reservation cannot start a new supplier request even if its
	// immutable owner/quantity still match the marked H3 receipt.
	f := newH3QuotaFixture(t, 10)
	cmd := h3QuotaCommand("csh3-quota-terminal-fixture", 4)
	f.apply(t, cmd)
	_, err := f.db.Exec(`UPDATE organization_media_reservations SET status='failed'`)
	require.NoError(t, err)
	claimed, err := f.repo.ClaimCompShareH3(context.Background(), cmd.RequestID)
	require.False(t, claimed)
	require.ErrorIs(t, err, service.ErrMediaQuotaConflict)
}
