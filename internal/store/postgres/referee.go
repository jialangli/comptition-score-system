package postgres

import (
	"context"
	"time"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// RefereeStore 裁判码仓储。
//
// 所有查询都带 contest_id —— 裁判码是**赛事级凭证**，换赛事必须重新建档发码，
// 旧码在新赛事里就是「查无此码」，这正是分区要保证的效果。
type RefereeStore struct{ q querier }

const refereeColumns = `id, code, name, role, event_id, group_code, seat_id,
	status, activated_at, created_at`

func scanReferee(row interface{ Scan(...any) error }) (*model.RefereeCode, error) {
	var c model.RefereeCode
	var role, status string
	var activated *time.Time
	if err := row.Scan(
		&c.ID, &c.Code, &c.Name, &role, &c.EventID, &c.GroupCode, &c.SeatID,
		&status, &activated, &c.CreatedAt,
	); err != nil {
		return nil, notFoundIfNoRows(err)
	}
	c.Role = model.RefereeRole(role)
	c.Status = model.RefereeStatus(status)
	c.ActivatedAt = activated
	return &c, nil
}

// Create 建档一条裁判码。同赛事内码重复由 ux_referee_code 挡下 → ErrDuplicate。
func (s *RefereeStore) Create(ctx context.Context, c *model.RefereeCode) error {
	return mapError(s.q.QueryRow(ctx, `
		INSERT INTO referee_codes (contest_id, code, name, role, event_id, group_code, seat_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING id, created_at`,
		store.CurrentContest(ctx), c.Code, c.Name, c.Role,
		c.EventID, c.GroupCode, c.SeatID,
	).Scan(&c.ID, &c.CreatedAt))
}

// GetByCode 按码查档（仅当前赛事）；不存在返回 ErrNotFound。
//
// 刻意只按码查、不连带姓名：service 层要靠「码查不到」与「码查到了但姓名不符」
// 这两种不同结果，才能把失败区分成前端的两个独立页面（P1.5b / P1.5c）。
func (s *RefereeStore) GetByCode(ctx context.Context, code string) (*model.RefereeCode, error) {
	return scanReferee(s.q.QueryRow(ctx, `
		SELECT `+refereeColumns+` FROM referee_codes
		WHERE contest_id=$1 AND code=$2`, store.CurrentContest(ctx), code))
}

// ListByContest 本赛事全部裁判码，按建档时间倒序。
func (s *RefereeStore) ListByContest(ctx context.Context) ([]model.RefereeCode, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+refereeColumns+` FROM referee_codes
		WHERE contest_id=$1
		ORDER BY created_at DESC, id DESC`, store.CurrentContest(ctx))
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	out := make([]model.RefereeCode, 0)
	for rows.Next() {
		c, err := scanReferee(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, mapError(rows.Err())
}

// MarkActivated 标记已激活（首登联网激活）。
//
// 带 `AND status='unused'`：已激活的码重复激活会命中 0 行。
// 这与「断网后凭本机缓存登录」不冲突 —— 后者由平板端判定，不再回后端。
func (s *RefereeStore) MarkActivated(ctx context.Context, id int64) error {
	tag, err := s.q.Exec(ctx, `
		UPDATE referee_codes
		SET status='activated', activated_at=now()
		WHERE id=$1 AND contest_id=$2 AND status='unused'`,
		id, store.CurrentContest(ctx))
	return oneRow(tag, err)
}
