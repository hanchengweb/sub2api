package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	entgroup "github.com/Wei-Shaw/sub2api/ent/group"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

// Uses the dedicated CI/local test cluster, with a separate database per run.
// It deliberately does not use the Docker-only integration TestMain.
func TestFreshGroupSchemaPostgres(t *testing.T) {
	dsn := os.Getenv("MEDIA_QUOTA_TEST_DSN")
	if dsn == "" {
		t.Skip("MEDIA_QUOTA_TEST_DSN not set")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") ||
		u.Path != "/media_quota_test" || !net.ParseIP(u.Hostname()).IsLoopback() {
		t.Fatal("MEDIA_QUOTA_TEST_DSN must target media_quota_test on a literal loopback address")
	}
	for _, key := range []string{"host", "hostaddr", "port", "dbname", "service"} {
		require.False(t, u.Query().Has(key), "test DSN must not override %s", key)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })
	require.NoError(t, admin.PingContext(ctx))
	name := fmt.Sprintf("fresh_group_%d", time.Now().UnixNano())
	_, err = admin.ExecContext(ctx, "CREATE DATABASE "+pq.QuoteIdentifier(name)+" TEMPLATE template0")
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, err := admin.ExecContext(cleanupCtx, "DROP DATABASE "+pq.QuoteIdentifier(name)+" WITH (FORCE)")
		require.NoError(t, err)
	})
	u.Path = "/" + name
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, ApplyMigrations(ctx, db), "all migrations must work on an empty database")
	require.NoError(t, ApplyMigrations(ctx, db), "startup migration replay must be idempotent")

	const migration = "248_group_video_model_prices.sql"
	data, err := migrations.FS.ReadFile(migration)
	require.NoError(t, err)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, string(data))
	require.NoError(t, err, "DDL replay must be safe even when the column already exists")
	require.NoError(t, tx.Commit())
	var applied int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations WHERE filename=$1", migration).Scan(&applied))
	require.Equal(t, 1, applied)

	var dataType, nullable string
	var defaultValue sql.NullString
	require.NoError(t, db.QueryRowContext(ctx, `SELECT data_type, is_nullable, column_default
		FROM information_schema.columns WHERE table_schema='public' AND table_name='groups'
		AND column_name='video_model_prices'`).Scan(&dataType, &nullable, &defaultValue))
	require.Equal(t, "jsonb", dataType)
	require.Equal(t, "YES", nullable)
	require.False(t, defaultValue.Valid, "optional JSON must retain an SQL NULL default")
	var defaultIsNull bool
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO groups(name) VALUES ('fresh-default-null')
		RETURNING video_model_prices IS NULL`).Scan(&defaultIsNull))
	require.True(t, defaultIsNull, "omitting optional JSON must store SQL NULL")
	rows, err := db.QueryContext(ctx, `SELECT column_name FROM information_schema.columns
		WHERE table_schema='public' AND table_name='groups'`)
	require.NoError(t, err)
	var columns []string
	for rows.Next() {
		var column string
		require.NoError(t, rows.Scan(&column))
		columns = append(columns, column)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Subset(t, columns, entgroup.Columns, "fresh schema must support every generated Group column")
	t.Logf("fresh schema contains all %d generated Group columns", len(entgroup.Columns))

	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	repo := NewGroupRepository(client, db)
	for _, tc := range []struct {
		name    string
		initial map[string]map[string]float64
		updated map[string]map[string]float64
	}{
		{name: "nil"},
		{
			name:    "configured",
			initial: map[string]map[string]float64{"seedance-2": {"480p": 74, "720p": 119}},
			updated: map[string]map[string]float64{"seedance-2": {"1080p": 254, "4k": 529}, "grok": {"720p": 8}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := &service.Group{
				Name:             "fresh-" + tc.name,
				Platform:         service.PlatformOpenAI,
				RateMultiplier:   1,
				Status:           service.StatusActive,
				SubscriptionType: service.SubscriptionTypeStandard,
				VideoModelPrices: tc.initial,
			}
			require.NoError(t, repo.Create(ctx, group))
			require.NotZero(t, group.ID)
			assertRead := func(prices map[string]map[string]float64) {
				t.Helper()
				got, err := repo.GetByID(ctx, group.ID)
				require.NoError(t, err)
				require.Equal(t, group.Name, got.Name)
				require.Equal(t, prices, got.VideoModelPrices)
				// The repository explicitly sets nil maps, which Ent encodes as JSON null.
				// Omitted values may instead be SQL NULL; both read back as nil.
				var isUnset bool
				require.NoError(t, db.QueryRowContext(ctx, `SELECT video_model_prices IS NULL
					OR video_model_prices='null'::jsonb FROM groups WHERE id=$1`, group.ID).Scan(&isUnset))
				require.Equal(t, prices == nil, isUnset)
			}
			assertRead(tc.initial)
			group.Name += "-updated"
			group.VideoModelPrices = tc.updated
			require.NoError(t, repo.Update(ctx, group))
			assertRead(tc.updated)
			require.NoError(t, repo.Delete(ctx, group.ID))
			_, err := repo.GetByID(ctx, group.ID)
			require.ErrorIs(t, err, service.ErrGroupNotFound)
		})
	}
}
