package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// --- 模型定价 ---

func (r *channelRepository) ListModelPricing(ctx context.Context, channelID int64) ([]service.ChannelModelPricing, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, channel_id, platform, models, billing_mode, input_price, output_price, cache_write_price, cache_read_price, image_input_price, image_output_price, per_request_price, time_pricing, created_at, updated_at
		 FROM channel_model_pricing WHERE channel_id = $1 ORDER BY id`, channelID,
	)
	if err != nil {
		return nil, fmt.Errorf("list model pricing: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result, pricingIDs, err := scanModelPricingRows(rows)
	if err != nil {
		return nil, err
	}

	if len(pricingIDs) > 0 {
		intervalMap, err := r.batchLoadIntervals(ctx, pricingIDs)
		if err != nil {
			return nil, err
		}
		for i := range result {
			result[i].Intervals = intervalMap[result[i].ID]
		}
	}

	return result, nil
}

func (r *channelRepository) CreateModelPricing(ctx context.Context, pricing *service.ChannelModelPricing) error {
	return createModelPricingExec(ctx, r.db, pricing)
}

func (r *channelRepository) UpdateModelPricing(ctx context.Context, pricing *service.ChannelModelPricing) error {
	modelsJSON, err := json.Marshal(pricing.Models)
	if err != nil {
		return fmt.Errorf("marshal models: %w", err)
	}
	billingMode := pricing.BillingMode
	if billingMode == "" {
		billingMode = service.BillingModeToken
	}
	result, err := r.db.ExecContext(ctx,
		`UPDATE channel_model_pricing
		 SET models = $1, billing_mode = $2, input_price = $3, output_price = $4, cache_write_price = $5, cache_read_price = $6, image_input_price = $7, image_output_price = $8, per_request_price = $9, platform = $10, updated_at = NOW()
		 WHERE id = $11`,
		modelsJSON, billingMode, pricing.InputPrice, pricing.OutputPrice, pricing.CacheWritePrice, pricing.CacheReadPrice,
		pricing.ImageInputPrice, pricing.ImageOutputPrice, pricing.PerRequestPrice, pricing.Platform, pricing.ID,
	)
	if err != nil {
		return fmt.Errorf("update model pricing: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("pricing entry not found: %d", pricing.ID)
	}
	return nil
}

func (r *channelRepository) DeleteModelPricing(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM channel_model_pricing WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete model pricing: %w", err)
	}
	return nil
}

func (r *channelRepository) ReplaceModelPricing(ctx context.Context, channelID int64, pricingList []service.ChannelModelPricing) error {
	return r.runInTx(ctx, func(tx *sql.Tx) error {
		return replaceModelPricingTx(ctx, tx, channelID, pricingList)
	})
}

// --- 批量加载辅助方法 ---

// batchLoadModelPricing 批量加载多个渠道的模型定价（含区间）
func (r *channelRepository) batchLoadModelPricing(ctx context.Context, channelIDs []int64) (map[int64][]service.ChannelModelPricing, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, channel_id, platform, models, billing_mode, input_price, output_price, cache_write_price, cache_read_price, image_input_price, image_output_price, per_request_price, time_pricing, created_at, updated_at
		 FROM channel_model_pricing WHERE channel_id = ANY($1) ORDER BY channel_id, id`,
		pq.Array(channelIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("batch load model pricing: %w", err)
	}
	defer func() { _ = rows.Close() }()

	allPricing, allPricingIDs, err := scanModelPricingRows(rows)
	if err != nil {
		return nil, err
	}

	// 按 channelID 分组
	pricingMap := make(map[int64][]service.ChannelModelPricing, len(channelIDs))
	for _, p := range allPricing {
		pricingMap[p.ChannelID] = append(pricingMap[p.ChannelID], p)
	}

	// 批量加载所有区间
	if len(allPricingIDs) > 0 {
		intervalMap, err := r.batchLoadIntervals(ctx, allPricingIDs)
		if err != nil {
			return nil, err
		}
		for chID := range pricingMap {
			for i := range pricingMap[chID] {
				pricingMap[chID][i].Intervals = intervalMap[pricingMap[chID][i].ID]
			}
		}
	}

	return pricingMap, nil
}

// batchLoadIntervals 批量加载多个定价条目的区间
func (r *channelRepository) batchLoadIntervals(ctx context.Context, pricingIDs []int64) (map[int64][]service.PricingInterval, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, pricing_id, min_tokens, max_tokens, tier_label,
		        input_price, output_price, cache_write_price, cache_read_price,
		        per_request_price, sort_order, created_at, updated_at
		 FROM channel_pricing_intervals
		 WHERE pricing_id = ANY($1) ORDER BY pricing_id, sort_order, id`,
		pq.Array(pricingIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("batch load intervals: %w", err)
	}
	defer func() { _ = rows.Close() }()

	intervalMap := make(map[int64][]service.PricingInterval, len(pricingIDs))
	for rows.Next() {
		var iv service.PricingInterval
		if err := rows.Scan(
			&iv.ID, &iv.PricingID, &iv.MinTokens, &iv.MaxTokens, &iv.TierLabel,
			&iv.InputPrice, &iv.OutputPrice, &iv.CacheWritePrice, &iv.CacheReadPrice,
			&iv.PerRequestPrice, &iv.SortOrder, &iv.CreatedAt, &iv.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan interval: %w", err)
		}
		intervalMap[iv.PricingID] = append(intervalMap[iv.PricingID], iv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate intervals: %w", err)
	}
	return intervalMap, nil
}

// --- 共享 scan 辅助 ---

// scanModelPricingRows 扫描 model pricing 行，返回结果列表和 ID 列表
func scanModelPricingRows(rows *sql.Rows) ([]service.ChannelModelPricing, []int64, error) {
	var result []service.ChannelModelPricing
	var pricingIDs []int64
	for rows.Next() {
		var p service.ChannelModelPricing
		var modelsJSON []byte
		var timePricingJSON sql.NullString
		if err := rows.Scan(
			&p.ID, &p.ChannelID, &p.Platform, &modelsJSON, &p.BillingMode,
			&p.InputPrice, &p.OutputPrice, &p.CacheWritePrice, &p.CacheReadPrice,
			&p.ImageInputPrice, &p.ImageOutputPrice, &p.PerRequestPrice, &timePricingJSON,
			&p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, nil, fmt.Errorf("scan model pricing: %w", err)
		}
		if err := json.Unmarshal(modelsJSON, &p.Models); err != nil {
			p.Models = []string{}
		}
		// 解析失败按未配置处理（ParseTimePricing 内部已容错）：定价在热路径上，
		// 一条脏配置不该让所有请求失败。
		if timePricingJSON.Valid {
			p.TimePricing = service.ParseTimePricing(timePricingJSON.String)
		}
		pricingIDs = append(pricingIDs, p.ID)
		result = append(result, p)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate model pricing: %w", err)
	}
	return result, pricingIDs, nil
}

// --- 事务内辅助方法 ---

// dbExec 是 *sql.DB 和 *sql.Tx 共享的最小 SQL 执行接口
type dbExec interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func setGroupIDsTx(ctx context.Context, exec dbExec, channelID int64, groupIDs []int64) error {
	if _, err := exec.ExecContext(ctx, `DELETE FROM channel_groups WHERE channel_id = $1`, channelID); err != nil {
		return fmt.Errorf("delete old group associations: %w", err)
	}
	if len(groupIDs) == 0 {
		return nil
	}
	_, err := exec.ExecContext(ctx,
		`INSERT INTO channel_groups (channel_id, group_id)
		 SELECT $1, unnest($2::bigint[])`,
		channelID, pq.Array(groupIDs),
	)
	if err != nil {
		return fmt.Errorf("insert group associations: %w", err)
	}
	return nil
}

func createModelPricingExec(ctx context.Context, exec dbExec, pricing *service.ChannelModelPricing) error {
	modelsJSON, err := json.Marshal(pricing.Models)
	if err != nil {
		return fmt.Errorf("marshal models: %w", err)
	}
	billingMode := pricing.BillingMode
	if billingMode == "" {
		billingMode = service.BillingModeToken
	}
	platform := pricing.Platform
	if platform == "" {
		platform = "anthropic"
	}
	err = exec.QueryRowContext(ctx,
		`INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, input_price, output_price, cache_write_price, cache_read_price, image_input_price, image_output_price, per_request_price)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING id, created_at, updated_at`,
		pricing.ChannelID, platform, modelsJSON, billingMode,
		pricing.InputPrice, pricing.OutputPrice, pricing.CacheWritePrice, pricing.CacheReadPrice,
		pricing.ImageInputPrice, pricing.ImageOutputPrice, pricing.PerRequestPrice,
	).Scan(&pricing.ID, &pricing.CreatedAt, &pricing.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert model pricing: %w", err)
	}

	for i := range pricing.Intervals {
		pricing.Intervals[i].PricingID = pricing.ID
		if err := createIntervalExec(ctx, exec, &pricing.Intervals[i]); err != nil {
			return err
		}
	}

	return nil
}

func createIntervalExec(ctx context.Context, exec dbExec, iv *service.PricingInterval) error {
	return exec.QueryRowContext(ctx,
		`INSERT INTO channel_pricing_intervals
		 (pricing_id, min_tokens, max_tokens, tier_label, input_price, output_price, cache_write_price, cache_read_price, per_request_price, sort_order)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id, created_at, updated_at`,
		iv.PricingID, iv.MinTokens, iv.MaxTokens, iv.TierLabel,
		iv.InputPrice, iv.OutputPrice, iv.CacheWritePrice, iv.CacheReadPrice,
		iv.PerRequestPrice, iv.SortOrder,
	).Scan(&iv.ID, &iv.CreatedAt, &iv.UpdatedAt)
}

// preservedPricingColumns 是「后台表单不编辑、但保存时不能丢」的那几列。
//
// 保存走的是整表删除 + 重新插入，而 createModelPricingExec 的 INSERT 里
// 没有这几列——于是每次在后台点一下「更新」，它们就被写成 NULL。
// 2026-09-11 线上就这么丢过一次：DeepSeek 的峰谷定价（time_pricing）被清空，
// 用户在空闲时段按峰值被多收一倍，而界面上根本看不出发生了什么。
//
// 按「模型集合」而不是行 id 做快照：id 在删除后就没了，而模型集合是这条
// 定价的实际身份。同一个渠道里两条定价不会有完全相同的模型集合。
const preservedPricingColumns = `time_pricing, fast_multiplier, flex_multiplier, cache_write_1h_price`

type preservedPricing struct {
	timePricing      []byte
	fastMultiplier   sql.NullFloat64
	flexMultiplier   sql.NullFloat64
	cacheWrite1hPric sql.NullFloat64
}

func snapshotPreservedPricing(ctx context.Context, exec dbExec, channelID int64) (map[string]preservedPricing, error) {
	rows, err := exec.QueryContext(ctx,
		`SELECT models::text, `+preservedPricingColumns+
			` FROM channel_model_pricing WHERE channel_id = $1`, channelID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[string]preservedPricing{}
	for rows.Next() {
		var key string
		var p preservedPricing
		if err := rows.Scan(&key, &p.timePricing, &p.fastMultiplier,
			&p.flexMultiplier, &p.cacheWrite1hPric); err != nil {
			return nil, err
		}
		// 全空的行不必记：还原时会白跑一次 UPDATE。
		if p.timePricing == nil && !p.fastMultiplier.Valid &&
			!p.flexMultiplier.Valid && !p.cacheWrite1hPric.Valid {
			continue
		}
		out[normalizeModelsKey(key)] = p
	}
	return out, rows.Err()
}

// normalizeModelsKey 把 models 的 JSON 文本归一成与顺序无关的键。
// 后台保存时模型顺序可能变（["a","b"] → ["b","a"]），按原文比会对不上。
func normalizeModelsKey(raw string) string {
	var models []string
	if err := json.Unmarshal([]byte(raw), &models); err != nil {
		return raw
	}
	sort.Strings(models)
	return strings.Join(models, ",")
}

func restorePreservedPricing(ctx context.Context, exec dbExec, pricing *service.ChannelModelPricing, saved map[string]preservedPricing) error {
	if len(saved) == 0 {
		return nil
	}
	modelsJSON, err := json.Marshal(pricing.Models)
	if err != nil {
		return nil
	}
	p, ok := saved[normalizeModelsKey(string(modelsJSON))]
	if !ok {
		return nil
	}
	var timePricing any
	if p.timePricing != nil {
		timePricing = string(p.timePricing)
	}
	_, err = exec.ExecContext(ctx,
		`UPDATE channel_model_pricing
		 SET time_pricing = $2::jsonb, fast_multiplier = $3,
		     flex_multiplier = $4, cache_write_1h_price = $5
		 WHERE id = $1`,
		pricing.ID, timePricing, p.fastMultiplier, p.flexMultiplier, p.cacheWrite1hPric)
	return err
}

func replaceModelPricingTx(ctx context.Context, exec dbExec, channelID int64, pricingList []service.ChannelModelPricing) error {
	// 先快照那几列，删除后按模型集合还原——否则后台每保存一次就丢一次。
	saved, err := snapshotPreservedPricing(ctx, exec, channelID)
	if err != nil {
		return fmt.Errorf("snapshot preserved pricing: %w", err)
	}
	if _, err := exec.ExecContext(ctx, `DELETE FROM channel_model_pricing WHERE channel_id = $1`, channelID); err != nil {
		return fmt.Errorf("delete old model pricing: %w", err)
	}
	for i := range pricingList {
		pricingList[i].ChannelID = channelID
		if err := createModelPricingExec(ctx, exec, &pricingList[i]); err != nil {
			return fmt.Errorf("insert model pricing: %w", err)
		}
		if err := restorePreservedPricing(ctx, exec, &pricingList[i], saved); err != nil {
			return fmt.Errorf("restore preserved pricing: %w", err)
		}
	}
	return nil
}

// isUniqueViolation 检查 pq 唯一约束违反错误
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr != nil {
		return pqErr.Code == "23505"
	}
	return false
}

// escapeLike 转义 LIKE/ILIKE 模式中的特殊字符
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
