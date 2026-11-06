//go:build unit

package repository

import (
	"context"
	"database/sql/driver"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestImageCacheUsageInsertPreservesUnknownAndZero(t *testing.T) {
	log := &service.UsageLog{}
	p := prepareUsageLogInsert(log)
	require.Len(t, p.args, len(usageLogInsertArgTypes))
	require.Nil(t, p.args[56]) // unknown is NULL
	log.CacheReadImageTokensKnown = true
	log.CacheReadImageCost = .123
	p = prepareUsageLogInsert(log)
	require.Equal(t, 0, p.args[56]) // known zero remains zero
	require.Equal(t, .123, p.args[57])
	key := usageLogBatchKey("image-cache", 1)
	q, args := buildUsageLogBatchInsertQuery([]string{key}, map[string]usageLogInsertPrepared{key: p})
	require.GreaterOrEqual(t, strings.Count(q, "cache_read_image_tokens"), 3)
	require.Len(t, args, len(p.args)+1)
	q, args = buildUsageLogBestEffortInsertQuery([]usageLogInsertPrepared{p})
	require.GreaterOrEqual(t, strings.Count(q, "cache_read_image_cost"), 3)
	require.Len(t, args, len(p.args))
}

func TestImageCacheChannelPricingRoundTrip(t *testing.T) {
	db, mock := newSQLMock(t)
	price := .0016
	p := &service.ChannelModelPricing{ChannelID: 1, Platform: "openai", Models: []string{"gpt-image-2.6-sunburst-official"}, BillingMode: service.BillingModeToken, ImageCacheReadPrice: &price}
	now := time.Now()
	mock.ExpectQuery("INSERT INTO channel_model_pricing .*image_cache_read_price").
		WithArgs(int64(1), "openai", sqlmock.AnyArg(), service.BillingModeToken, nil, nil, nil, nil, nil, price, nil, nil).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(8, now, now))
	require.NoError(t, createModelPricingExec(context.Background(), db, p))
	mock.ExpectQuery("SELECT .*image_cache_read_price.*FROM channel_model_pricing").WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "channel_id", "platform", "models", "billing_mode", "input_price", "output_price", "cache_write_price", "cache_read_price", "image_input_price", "image_cache_read_price", "image_output_price", "per_request_price", "time_pricing", "created_at", "updated_at"}).AddRow(8, 1, "openai", `["gpt-image-2.6-sunburst-official"]`, "token", nil, nil, nil, nil, nil, price, nil, nil, nil, now, now))
	mock.ExpectQuery("SELECT .*FROM channel_pricing_intervals").WithArgs(sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"id", "pricing_id", "min_tokens", "max_tokens", "tier_label", "input_price", "output_price", "cache_write_price", "cache_read_price", "per_request_price", "sort_order", "created_at", "updated_at"}))
	rows, err := (&channelRepository{db: db}).ListModelPricing(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, price, *rows[0].ImageCacheReadPrice)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestImageCacheUsageReadRestoresPresence(t *testing.T) {
	for _, known := range []bool{false, true} {
		db, mock := newSQLMock(t)
		cols := strings.Split(usageLogSelectColumns, ", ")
		values := make([]driver.Value, len(cols))
		for i, c := range cols {
			switch c {
			case "model":
				values[i] = "gpt-image-2.6-sunburst-official"
			case "created_at":
				values[i] = time.Now()
			case "stream", "openai_ws_mode", "cache_ttl_overridden", "long_context_billing_applied":
				values[i] = false
			case "request_id", "requested_model", "upstream_model", "group_id", "subscription_id", "account_rate_multiplier", "duration_ms", "first_token_ms", "user_agent", "ip_address", "image_size", "image_input_size", "image_output_size", "image_size_source", "image_size_breakdown", "video_resolution", "video_duration_seconds", "service_tier", "reasoning_effort", "inbound_endpoint", "upstream_endpoint", "channel_id", "model_mapping_chain", "billing_tier", "billing_mode", "account_stats_cost", "session_id", "refunded_at", "refunded_credits":
				values[i] = nil
			default:
				values[i] = int64(0)
			}
			if c == "cache_read_image_tokens" {
				if known {
					values[i] = int64(30)
				} else {
					values[i] = nil
				}
			}
			if c == "cache_read_image_cost" {
				values[i] = .048
			}
		}
		mock.ExpectQuery(regexp.QuoteMeta("SELECT " + usageLogSelectColumns + " FROM usage_logs WHERE id = $1")).WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows(cols).AddRow(values...))
		log, err := (&usageLogRepository{sql: db}).GetByID(context.Background(), 1)
		require.NoError(t, err)
		require.Equal(t, known, log.CacheReadImageTokensKnown)
		if known {
			require.Equal(t, 30, log.CacheReadImageTokens)
		}
		require.Equal(t, .048, log.CacheReadImageCost)
		require.NoError(t, mock.ExpectationsWereMet())
	}
}

func TestImageCacheAccountCostPricingRoundTrip(t *testing.T) {
	db, mock := newSQLMock(t)
	imageInput, imageCache := .00448, .00112
	p := &service.ChannelModelPricing{Platform: "openai", Models: []string{"gpt-image-2.6-sunburst-official"}, BillingMode: service.BillingModeToken, ImageInputPrice: &imageInput, ImageCacheReadPrice: &imageCache}
	now := time.Now()
	mock.ExpectBegin()
	tx, err := db.Begin()
	require.NoError(t, err)
	mock.ExpectQuery("INSERT INTO channel_account_stats_model_pricing .*image_input_price, image_cache_read_price").WithArgs(int64(4), "openai", sqlmock.AnyArg(), service.BillingModeToken, nil, nil, nil, nil, imageInput, imageCache, nil, nil).WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(9, now, now))
	require.NoError(t, createAccountStatsModelPricingTx(context.Background(), tx, 4, p))
	mock.ExpectCommit()
	require.NoError(t, tx.Commit())
	mock.ExpectQuery("SELECT .*image_input_price, image_cache_read_price.*FROM channel_account_stats_model_pricing").WithArgs(sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"id", "rule_id", "platform", "models", "billing_mode", "input_price", "output_price", "cache_write_price", "cache_read_price", "image_input_price", "image_cache_read_price", "image_output_price", "per_request_price", "time_pricing", "created_at", "updated_at"}).AddRow(9, 4, "openai", `["gpt-image-2.6-sunburst-official"]`, "token", nil, nil, nil, nil, imageInput, imageCache, nil, nil, nil, now, now))
	mock.ExpectQuery("SELECT .*FROM channel_account_stats_pricing_intervals").WithArgs(sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"id", "pricing_id", "min_tokens", "max_tokens", "tier_label", "input_price", "output_price", "cache_write_price", "cache_read_price", "per_request_price", "sort_order", "created_at", "updated_at"}))
	loaded, err := (&channelRepository{db: db}).batchLoadAccountStatsModelPricing(context.Background(), []int64{4})
	require.NoError(t, err)
	require.Len(t, loaded[4], 1)
	require.Equal(t, imageInput, *loaded[4][0].ImageInputPrice)
	require.Equal(t, imageCache, *loaded[4][0].ImageCacheReadPrice)
	require.NoError(t, mock.ExpectationsWereMet())
}
