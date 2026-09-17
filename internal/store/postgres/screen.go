package postgres

import (
	"context"
	"encoding/json"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ScreenStore 大屏配置仓储。
type ScreenStore struct{ q querier }

// Get 读取大屏配置。
//
// 与其它 Get 不同：**未配置过时不返回 ErrNotFound，而是返回一份带默认值的配置**。
// 理由是大屏页面必须永远能渲染 —— 运营没配过不等于「大屏不存在」，
// 让它因为一个配置行缺失而白屏是最没有意义的一类故障。
func (s *ScreenStore) Get(ctx context.Context, eventID string) (*model.ScreenConfig, error) {
	cfg := &model.ScreenConfig{
		EventID:     eventID,
		PageSize:    model.DefaultPageSize,
		IntervalSec: model.DefaultIntervalSec,
	}
	var pinned *string
	err := s.q.QueryRow(ctx, `
		SELECT page_size, interval_sec, pinned FROM screen_config WHERE event_id=$1`,
		eventID).Scan(&cfg.PageSize, &cfg.IntervalSec, &pinned)
	if err != nil {
		if !isNoRows(err) {
			return nil, mapError(err)
		}
		cfg.Normalize()
		return cfg, nil
	}
	if pinned != nil {
		cfg.Pinned = *pinned
	}
	cfg.Normalize()
	return cfg, nil
}

// Upsert 写入大屏配置。
func (s *ScreenStore) Upsert(ctx context.Context, c *model.ScreenConfig) error {
	c.Normalize()
	var pinned any
	if c.Pinned != "" {
		pinned = c.Pinned
	}
	_, err := s.q.Exec(ctx, `
		INSERT INTO screen_config (event_id, page_size, interval_sec, pinned)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (event_id) DO UPDATE SET
			page_size=EXCLUDED.page_size,
			interval_sec=EXCLUDED.interval_sec,
			pinned=EXCLUDED.pinned,
			updated_at=now()`,
		c.EventID, c.PageSize, c.IntervalSec, pinned)
	return mapError(err)
}

// ---------------------------------------------------------------------------

// ConfigSnapshotStore 配置快照仓储。
//
// 用途：改配置前留档。运营改错了能回滚，也能回答「上周这一版权重是多少」。
type ConfigSnapshotStore struct{ q querier }

// Create 写入一份配置快照，返回其 ID。
func (s *ConfigSnapshotStore) Create(ctx context.Context, note string, payload any) (int64, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, mapError(err)
	}
	var id int64
	err = s.q.QueryRow(ctx, `
		INSERT INTO config_snapshots (note, payload) VALUES ($1,$2)
		RETURNING id`, note, raw).Scan(&id)
	return id, mapError(err)
}

// List 按时间倒序返回快照元信息（payload 也一并返回，量级很小）。
func (s *ConfigSnapshotStore) List(ctx context.Context, limit int) ([]model.ConfigSnapshot, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.q.Query(ctx, `
		SELECT id, note, payload, created_at
		FROM config_snapshots ORDER BY created_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var out []model.ConfigSnapshot
	for rows.Next() {
		var c model.ConfigSnapshot
		var raw []byte
		if err := rows.Scan(&c.ID, &c.Note, &raw, &c.CreatedAt); err != nil {
			return nil, mapError(err)
		}
		if err := scanJSONB(raw, &c.Payload); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, mapError(rows.Err())
}

// Get 读取单份快照。
func (s *ConfigSnapshotStore) Get(ctx context.Context, id int64) (*model.ConfigSnapshot, error) {
	var c model.ConfigSnapshot
	var raw []byte
	err := s.q.QueryRow(ctx, `
		SELECT id, note, payload, created_at FROM config_snapshots WHERE id=$1`, id).
		Scan(&c.ID, &c.Note, &raw, &c.CreatedAt)
	if err != nil {
		return nil, notFoundIfNoRows(err)
	}
	if err := scanJSONB(raw, &c.Payload); err != nil {
		return nil, err
	}
	return &c, nil
}
