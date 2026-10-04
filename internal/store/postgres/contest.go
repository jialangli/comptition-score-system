package postgres

import (
	"context"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// ContestStore 赛事仓储（0004 多赛事维度）。
//
// 赛事是其余 13 张表的分区根：它们的外键都指向 contests(id) 且 ON DELETE
// RESTRICT。所以**建赛项之前必须先有赛事记录** —— 这条外键是刻意的护栏，
// 避免出现「成绩挂在一条不存在的赛事上」这种无法追溯的孤儿数据。
type ContestStore struct{ q querier }

const contestColumns = `id, name, season, start_date, end_date, venue, host,
	status, created_at, updated_at, archived_at`

func scanContest(row interface{ Scan(...any) error }) (*model.Contest, error) {
	var c model.Contest
	if err := row.Scan(
		&c.ID, &c.Name, &c.Season, &c.StartDate, &c.EndDate, &c.Venue, &c.Host,
		&c.Status, &c.CreatedAt, &c.UpdatedAt, &c.ArchivedAt,
	); err != nil {
		return nil, notFoundIfNoRows(err)
	}
	return &c, nil
}

// Create 新建赛事，回填时间戳。
func (s *ContestStore) Create(ctx context.Context, c *model.Contest) error {
	if c.Status == "" {
		c.Status = model.ContestPrep
	}
	return mapError(s.q.QueryRow(ctx, `
		INSERT INTO contests (id, name, season, start_date, end_date, venue, host, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (id) DO NOTHING
		RETURNING created_at, updated_at`,
		c.ID, c.Name, c.Season, c.StartDate, c.EndDate, c.Venue, c.Host, string(c.Status),
	).Scan(&c.CreatedAt, &c.UpdatedAt))
}

// Get 读取赛事。
func (s *ContestStore) Get(ctx context.Context, id string) (*model.Contest, error) {
	return scanContest(s.q.QueryRow(ctx,
		`SELECT `+contestColumns+` FROM contests WHERE id=$1`, id))
}

// List 列出全部赛事，按创建时间倒序。
func (s *ContestStore) List(ctx context.Context) ([]model.Contest, error) {
	rows, err := s.q.Query(ctx,
		`SELECT `+contestColumns+` FROM contests ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var out []model.Contest
	for rows.Next() {
		c, err := scanContest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, mapError(rows.Err())
}

// SetStatus 变更赛事状态（筹备 / 进行中 / 已结束 / 已归档）。
func (s *ContestStore) SetStatus(ctx context.Context, id string, status model.ContestStatus) error {
	tag, err := s.q.Exec(ctx, `
		UPDATE contests
		SET status=$2,
		    archived_at = CASE WHEN $2 = 'archived' THEN now() ELSE NULL END,
		    updated_at  = now()
		WHERE id=$1`, id, string(status))
	return affected(tag, err)
}

// Delete 删除赛事。仍被数据引用时会被外键 RESTRICT 拦下（ErrInUse）——
// 想删就得先清干净该赛事的数据，不能靠级联静默删掉一整场赛事的成绩。
func (s *ContestStore) Delete(ctx context.Context, id string) error {
	tag, err := s.q.Exec(ctx, `DELETE FROM contests WHERE id=$1`, id)
	return affected(tag, err)
}

// EnsureDefault 确保默认赛事存在。
//
// 0004 迁移已经插入了 ct_default，但测试会 TRUNCATE 掉它 —— 各表的外键又都
// 指向 contests，于是「清空后直接建赛项」会撞 fk_events_contest。
// 默认赛事是「未指定赛事」的兜底，必须始终存在。
func (s *ContestStore) EnsureDefault(ctx context.Context) error {
	_, err := s.q.Exec(ctx, `
		INSERT INTO contests (id, name, status)
		VALUES ($1, $2, $3)
		ON CONFLICT (id) DO NOTHING`,
		store.DefaultContestID, "默认赛事", string(model.ContestPrep))
	return mapError(err)
}
