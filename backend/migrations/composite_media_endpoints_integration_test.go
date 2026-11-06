//go:build integration

package migrations

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestCompositeMediaEndpointsMigration(t *testing.T) {
	dsn := os.Getenv("MEDIA_QUOTA_TEST_DSN")
	if dsn == "" {
		t.Skip("MEDIA_QUOTA_TEST_DSN is required for PostgreSQL constraint validation")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	// PostgreSQL resolves unqualified migration names to this temporary table.
	// The test never changes an existing application table or migration ledger.
	_, err = tx.ExecContext(ctx, `CREATE TEMP TABLE composite_model_routes (
		endpoint varchar(32) NOT NULL,
		CONSTRAINT composite_model_routes_endpoint_check CHECK
		(endpoint IN ('any','messages','count_tokens','responses','chat_completions','embeddings','images','gemini'))
	) ON COMMIT DROP`)
	require.NoError(t, err)
	reject := func(endpoint string) {
		t.Helper()
		_, err = tx.ExecContext(ctx, "SAVEPOINT invalid_endpoint")
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, "INSERT INTO composite_model_routes(endpoint) VALUES ($1)", endpoint)
		require.ErrorContains(t, err, "composite_model_routes_endpoint_check")
		_, err = tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT invalid_endpoint")
		require.NoError(t, err)
	}
	reject("audio")
	reject("model3d")
	content, err := FS.ReadFile("250_composite_media_endpoints.sql")
	require.NoError(t, err)
	for range 2 {
		_, err = tx.ExecContext(ctx, string(content))
		require.NoError(t, err)
	}
	for _, endpoint := range []string{"any", "messages", "count_tokens", "responses", "chat_completions", "embeddings", "images", "gemini", "audio", "model3d"} {
		_, err = tx.ExecContext(ctx, "INSERT INTO composite_model_routes(endpoint) VALUES ($1)", endpoint)
		require.NoError(t, err, endpoint)
	}
	reject("unknown_endpoint")
	var count int
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT count(*) FROM composite_model_routes").Scan(&count))
	require.Equal(t, 10, count)
}
