//go:build unit

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Opt-in real PostgreSQL test. The DSN is a disposable local fixture, never a deployed DB.
func TestCompShareH3PostgresAtomicSettlement(t *testing.T) {
	dsn := os.Getenv("H3_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set H3_TEST_POSTGRES_DSN to an isolated local PostgreSQL fixture")
	}
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	require.Equal(t, "windhub-h3-test-pg", u.Hostname(), "refuse non-fixture DB")
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := fmt.Sprintf("h3_test_%d", time.Now().UnixNano())
	_, err = admin.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	defer admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(12)
	_, err = db.Exec(`
 CREATE TABLE users(id bigint PRIMARY KEY,balance numeric(20,10),updated_at timestamptz,deleted_at timestamptz);
 CREATE TABLE api_keys(id bigint PRIMARY KEY,quota numeric DEFAULT 1000,quota_used numeric DEFAULT 0,status text DEFAULT 'active',usage_5h numeric DEFAULT 0,usage_1d numeric DEFAULT 0,usage_7d numeric DEFAULT 0,window_5h_start timestamptz,window_1d_start timestamptz,window_7d_start timestamptz,updated_at timestamptz,deleted_at timestamptz);
 CREATE TABLE accounts(id bigint PRIMARY KEY,extra jsonb,updated_at timestamptz,deleted_at timestamptz);
 CREATE TABLE usage_billing_dedup(id bigserial,request_id text,api_key_id bigint,request_fingerprint text,UNIQUE(request_id,api_key_id));
 CREATE TABLE usage_billing_dedup_archive(LIKE usage_billing_dedup INCLUDING ALL);
 CREATE TABLE usage_logs(request_id text,api_key_id bigint,total_cost numeric,actual_cost numeric,output_cost numeric,account_stats_cost numeric,refunded_credits numeric DEFAULT 0,refunded_at timestamptz,video_resolution text,video_duration_seconds int,UNIQUE(request_id,api_key_id));
 INSERT INTO users VALUES(1,1000,NULL,NULL);
 INSERT INTO api_keys(id) VALUES(7);
 INSERT INTO accounts(id,extra) VALUES(18,'{"quota_used":0,"quota_total_limit":10000,"quota_daily_limit":10000,"quota_weekly_limit":10000}');
 `)
	require.NoError(t, err)
	for _, name := range []string{"191_media_task_charges.sql", "192_toapis_task_webhook.sql", "195_usage_logs_refund.sql", "251_compshare_h3_settlement.sql"} {
		raw, e := migrations.FS.ReadFile(name)
		require.NoError(t, e)
		if name == "192_toapis_task_webhook.sql" { // Only the task binding DDL is needed, not webhook fixtures.
			_, e = db.Exec(`ALTER TABLE media_task_charges ADD COLUMN task_id varchar(200); CREATE UNIQUE INDEX h3_task_id ON media_task_charges(task_id) WHERE task_id IS NOT NULL`)
		} else if name == "195_usage_logs_refund.sql" {
			_, e = db.Exec(`ALTER TABLE media_task_charges ADD COLUMN request_id varchar(64)`)
		} else {
			_, e = db.Exec(string(raw))
		}
		require.NoError(t, e, name)
	}
	repo := &usageBillingRepository{db: db}
	ctx := context.Background()
	cmd := func(id string) *service.UsageBillingCommand {
		snap := &service.CompShareH3Snapshot{RequestID: id, UserID: 1, APIKeyID: 7, GroupID: 4, AccountID: 18, PayloadHash: "fixture", Resolution: "4k", Duration: 5, Prices: map[string]float64{"480p": 1, "768p": 2, "1080p": 3, "2k": 4, "4k": 5}, Multiplier: 2, BaseCost: 25, Credits: 50}
		return &service.UsageBillingCommand{RequestID: id, APIKeyID: 7, UserID: 1, AccountID: 18, AccountType: service.AccountTypeAPIKey, Model: service.CompShareH3Model, BalanceCost: 50, APIKeyQuotaCost: 50, APIKeyRateLimitCost: 50, AccountQuotaCost: 25, CompShareH3: snap}
	}
	readBalance := func() float64 {
		var v float64
		require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=1`).Scan(&v))
		return v
	}
	insertUsage := func(id string) {
		_, e := db.Exec(`INSERT INTO usage_logs(request_id,api_key_id,total_cost,actual_cost,output_cost,account_stats_cost,video_resolution,video_duration_seconds) VALUES($1,7,25,50,25,25,'4k',5)`, id)
		require.NoError(t, e)
	}
	first := cmd("csh3-fixture-1")
	applied, err := repo.Apply(ctx, first)
	require.NoError(t, err)
	require.True(t, applied.Applied)
	require.Equal(t, 950.0, readBalance())
	snap, err := repo.GetCompShareH3(ctx, first.RequestID, 1, 7)
	require.NoError(t, err)
	require.NotNil(t, snap)
	require.Equal(t, 50.0, snap.Billing.BalanceCost)
	replay, err := repo.Apply(ctx, first)
	require.NoError(t, err)
	require.False(t, replay.Applied)
	require.Equal(t, 950.0, readBalance())
	claimed, err := repo.ClaimCompShareH3(ctx, first.RequestID)
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = repo.ClaimCompShareH3(ctx, first.RequestID)
	require.NoError(t, err)
	require.False(t, claimed)
	require.NoError(t, repo.BindCompShareH3(ctx, first.RequestID, "provider-fixture-1"))
	insertUsage(first.RequestID)
	// The generic failure-refund route cannot take an H3 receipt.
	legacy, err := repo.TakeMediaTaskChargeByTaskID(ctx, "provider-fixture-1")
	require.NoError(t, err)
	require.Nil(t, legacy)
	var wg sync.WaitGroup
	refunds := make(chan float64, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := repo.SettleCompShareH3(ctx, first.RequestID, 1, 7, "succeeded", "768P", 5)
			refunds <- v
			errs <- e
		}()
	}
	wg.Wait()
	close(refunds)
	close(errs)
	sum := 0.0
	for v := range refunds {
		sum += v
	}
	for e := range errs {
		require.NoError(t, e)
	}
	require.Equal(t, 30.0, sum)
	require.Equal(t, 980.0, readBalance())
	var quota, rate, account, total, actual, refunded float64
	require.NoError(t, db.QueryRow(`SELECT quota_used,usage_5h FROM api_keys WHERE id=7`).Scan(&quota, &rate))
	require.Equal(t, 20.0, quota)
	require.Equal(t, 20.0, rate)
	require.NoError(t, db.QueryRow(`SELECT (extra->>'quota_used')::numeric FROM accounts WHERE id=18`).Scan(&account))
	require.Equal(t, 10.0, account)
	require.NoError(t, db.QueryRow(`SELECT total_cost,actual_cost,refunded_credits FROM usage_logs WHERE request_id=$1`, first.RequestID).Scan(&total, &actual, &refunded))
	require.Equal(t, 10.0, total)
	require.Equal(t, 20.0, actual)
	require.Equal(t, 30.0, refunded)
	// A provider failure releases the full second reservation, once.
	second := cmd("csh3-fixture-2")
	_, err = repo.Apply(ctx, second)
	require.NoError(t, err)
	insertUsage(second.RequestID)
	v, err := repo.SettleCompShareH3(ctx, second.RequestID, 1, 7, "blocked", "", 0)
	require.NoError(t, err)
	require.Equal(t, 50.0, v)
	require.Equal(t, 980.0, readBalance())
	v, err = repo.SettleCompShareH3(ctx, second.RequestID, 1, 7, "failed", "", 0)
	require.NoError(t, err)
	require.Zero(t, v)
	// Missing success usage cannot release a reservation.
	third := cmd("csh3-fixture-3")
	_, err = repo.Apply(ctx, third)
	require.NoError(t, err)
	insertUsage(third.RequestID)
	_, err = repo.SettleCompShareH3(ctx, third.RequestID, 1, 7, "succeeded", "768P", 0)
	require.Error(t, err)
	require.Equal(t, 930.0, readBalance())
	_, err = repo.SettleCompShareH3(ctx, third.RequestID, 2, 7, "failed", "", 0)
	require.Error(t, err)
	require.Equal(t, 930.0, readBalance())
	// Fail the snapshot insert after the debit: both the debit and dedup claim must roll back.
	_, err = db.Exec(`ALTER TABLE media_task_charges ADD CONSTRAINT fixture_reject_snapshot CHECK(request_id<>'csh3-rollback')`)
	require.NoError(t, err)
	_, err = repo.Apply(ctx, cmd("csh3-rollback"))
	require.Error(t, err)
	require.Equal(t, 930.0, readBalance())
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM usage_billing_dedup WHERE request_id='csh3-rollback'`).Scan(&count))
	require.Zero(t, count)
	// A positive balance smaller than the reservation cannot go negative or dispatch.
	_, err = db.Exec(`UPDATE users SET balance=1 WHERE id=1`)
	require.NoError(t, err)
	_, err = repo.Apply(ctx, cmd("csh3-insufficient"))
	require.ErrorIs(t, err, service.ErrInsufficientBalance)
	require.Equal(t, 1.0, readBalance())
	// Existing non-H3 refund semantics remain available.
	require.NoError(t, repo.BindMediaTaskCharge(ctx, &service.MediaTaskChargeCommand{TaskKey: "old-task", TaskID: "old-task", UserID: 1, APIKeyID: 7, Credits: 8}))
	taken, err := repo.TakeMediaTaskChargeByTaskID(ctx, "old-task")
	require.NoError(t, err)
	require.Equal(t, 8.0, taken.Credits)
}
