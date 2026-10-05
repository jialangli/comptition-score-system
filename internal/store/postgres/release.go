package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// ReleaseStore 发布单元仓储。
type ReleaseStore struct{ q querier }

const releaseColumns = `id, event_id, group_code, seat_id, status,
	handed_by, handed_at, received_by, received_at,
	published_by, published_at, republish_required, republish_reason, created_at`

func scanRelease(row interface{ Scan(...any) error }) (*model.ReleaseUnit, error) {
	var u model.ReleaseUnit
	var status string
	var handed, received, published *time.Time
	if err := row.Scan(
		&u.ID, &u.EventID, &u.GroupCode, &u.SeatID, &status,
		&u.HandedBy, &handed, &u.ReceivedBy, &received,
		&u.PublishedBy, &published, &u.RepublishRequired, &u.RepublishReason, &u.CreatedAt,
	); err != nil {
		return nil, notFoundIfNoRows(err)
	}
	u.Status = model.ReleaseStatus(status)
	u.HandedAt, u.ReceivedAt, u.PublishedAt = handed, received, published
	u.DeriveLabels()
	return &u, nil
}

// releaseWhere 发布单元的唯一键条件。
//
// seat_id 可能为 NULL（未指定赛台的单元），用 COALESCE 归一 ——
// SQL 里 NULL = NULL 是 NULL（不成立），直接比较会永远查不到。
const releaseWhere = `WHERE contest_id=$1 AND event_id=$2 AND group_code=$3
	AND COALESCE(seat_id,0)=COALESCE($4,0)`

// Ensure 取（或建）一个发布单元，幂等。
//
// 为什么是「取或建」而不是分成两个接口：运营不该先「建单元」再「移交」两步走 ——
// 发布单元是赛项×组别×赛台的天然产物，用到时自然存在即可。
//
// 并发下两个请求同时 Ensure 同一单元时，INSERT 会因 ux_release_unit 冲突而
// 返回 0 行（ON CONFLICT DO NOTHING），此时回读即可 —— 不向调用方抛错。
func (s *ReleaseStore) Ensure(ctx context.Context, eventID, groupCode string,
	seatID *int64) (*model.ReleaseUnit, error) {
	cid := store.CurrentContest(ctx)

	u, err := scanRelease(s.q.QueryRow(ctx,
		`SELECT `+releaseColumns+` FROM release_units `+releaseWhere,
		cid, eventID, groupCode, seatID))
	if err == nil {
		return u, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	var id int64
	err = s.q.QueryRow(ctx, `
		INSERT INTO release_units (contest_id, event_id, group_code, seat_id)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT DO NOTHING
		RETURNING id`, cid, eventID, groupCode, seatID).Scan(&id)
	if err != nil {
		if !isNoRows(err) {
			return nil, mapError(err)
		}
		// 被并发请求抢先建好了 —— 回读，不视为错误
		return scanRelease(s.q.QueryRow(ctx,
			`SELECT `+releaseColumns+` FROM release_units `+releaseWhere,
			cid, eventID, groupCode, seatID))
	}
	return scanRelease(s.q.QueryRow(ctx,
		`SELECT `+releaseColumns+` FROM release_units WHERE id=$1`, id))
}

// Get 按 ID 读取。
func (s *ReleaseStore) Get(ctx context.Context, id int64) (*model.ReleaseUnit, error) {
	return scanRelease(s.q.QueryRow(ctx, `
		SELECT `+releaseColumns+` FROM release_units
		WHERE id=$1 AND contest_id=$2`, id, store.CurrentContest(ctx)))
}

// ListByContest 本赛事全部发布单元（P13 看板数据源）。
func (s *ReleaseStore) ListByContest(ctx context.Context) ([]model.ReleaseUnit, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+releaseColumns+` FROM release_units
		WHERE contest_id=$1
		ORDER BY event_id, group_code, id`, store.CurrentContest(ctx))
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	out := make([]model.ReleaseUnit, 0)
	for rows.Next() {
		u, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, mapError(rows.Err())
}

// HandOver 移交：未移交 / 待发布（重发后）→ 已移交·处理中。
//
// 带 `AND status IN (...)` 前置条件：重复移交、或对已发布的单元移交都会命中 0 行，
// 由 affected 转成 ErrNotFound —— 状态机由数据库把关，不给并发留窗口。
func (s *ReleaseStore) HandOver(ctx context.Context, id int64, operator string) error {
	tag, err := s.q.Exec(ctx, `
		UPDATE release_units
		SET status='handed', handed_by=$3, handed_at=now(),
		    republish_required=false, republish_reason='', updated_at=now()
		WHERE id=$1 AND contest_id=$2 AND status IN ('not_handed','pending')`,
		id, store.CurrentContest(ctx), operator)
	return oneRow(tag, err)
}

// Receive 接收：已移交·处理中 → ⏳ 待发布。
func (s *ReleaseStore) Receive(ctx context.Context, id int64, operator string) error {
	tag, err := s.q.Exec(ctx, `
		UPDATE release_units
		SET status='pending', received_by=$3, received_at=now(), updated_at=now()
		WHERE id=$1 AND contest_id=$2 AND status='handed'`,
		id, store.CurrentContest(ctx), operator)
	return oneRow(tag, err)
}

// Publish 发布：已移交 / 待发布 → ✓ 运营已发布，并清空重发标记。
func (s *ReleaseStore) Publish(ctx context.Context, id int64, operator string) error {
	tag, err := s.q.Exec(ctx, `
		UPDATE release_units
		SET status='published', published_by=$3, published_at=now(),
		    republish_required=false, republish_reason='', updated_at=now()
		WHERE id=$1 AND contest_id=$2 AND status IN ('handed','pending')`,
		id, store.CurrentContest(ctx), operator)
	return oneRow(tag, err)
}

// MarkRepublish 标记重发：已发布 → 待发布 + 重发标记。
//
// 刻意**不**清 published_by / published_at：那是「上一次发布」的事实，
// 重发期间前端既要提示「待重发」，也要能看到上一版是谁发的。
func (s *ReleaseStore) MarkRepublish(ctx context.Context, id int64, reason string) error {
	tag, err := s.q.Exec(ctx, `
		UPDATE release_units
		SET status='pending', republish_required=true, republish_reason=$3, updated_at=now()
		WHERE id=$1 AND contest_id=$2 AND status='published'`,
		id, store.CurrentContest(ctx), reason)
	return oneRow(tag, err)
}

// oneRow 把「更新命中 0 行」翻译成 ErrNotFound。
func oneRow(tag interface{ RowsAffected() int64 }, err error) error {
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}
