package postgres

import (
	"context"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// TeamStore 队伍仓储。
type TeamStore struct{ q querier }

const teamColumns = `id, event_id, team_no, name, school, coach, group_code,
	members, status, source, created_at, updated_at`

func scanTeam(row interface{ Scan(...any) error }) (*model.Team, error) {
	var t model.Team
	var status, source string
	if err := row.Scan(
		&t.ID, &t.EventID, &t.TeamNo, &t.Name, &t.School, &t.Coach, &t.GroupCode,
		&t.Members, &status, &source, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return nil, notFoundIfNoRows(err)
	}
	t.Status = model.TeamStatus(status)
	t.Source = model.TeamSource(source)
	return &t, nil
}

// Create 新增队伍并回填自增 ID 与时间戳。
func (s *TeamStore) Create(ctx context.Context, t *model.Team) error {
	if t.Status == "" {
		t.Status = model.TeamActive
	}
	if t.Source == "" {
		t.Source = model.SourceManual
	}
	return mapError(s.q.QueryRow(ctx, `
		INSERT INTO teams (event_id, team_no, name, school, coach, group_code, members, status, source)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id, created_at, updated_at`,
		t.EventID, t.TeamNo, t.Name, t.School, t.Coach, t.GroupCode,
		t.Members, string(t.Status), string(t.Source),
	).Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt))
}

// Get 按 ID 读取队伍。
func (s *TeamStore) Get(ctx context.Context, id int64) (*model.Team, error) {
	return scanTeam(s.q.QueryRow(ctx, `SELECT `+teamColumns+` FROM teams WHERE id = $1`, id))
}

// GetByNo 按「赛项 + 编号」读取队伍 —— 导入比对的主键查找。
func (s *TeamStore) GetByNo(ctx context.Context, eventID, teamNo string) (*model.Team, error) {
	return scanTeam(s.q.QueryRow(ctx,
		`SELECT `+teamColumns+` FROM teams WHERE event_id = $1 AND team_no = $2`, eventID, teamNo))
}

// ListByEvent 按录入顺序返回赛项下的队伍。
//
// includeWithdrawn=false 时过滤掉弃赛队伍（打分 / 排名场景）；
// 队伍管理页需要看到弃赛记录，传 true。
func (s *TeamStore) ListByEvent(ctx context.Context, eventID string, includeWithdrawn bool) ([]model.Team, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+teamColumns+` FROM teams
		WHERE event_id = $1 AND ($2 OR status = 'active')
		ORDER BY id`, eventID, includeWithdrawn)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var out []model.Team
	for rows.Next() {
		t, err := scanTeam(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, mapError(rows.Err())
}

// Upsert 合并式写入：命中 (event_id, team_no) 则更新，否则插入。
//
// 这是报名导入的核心语义 —— **绝不物理删除已有队伍**。
// 文件里没出现的队伍保持原样（可能是运营手工补录的，也可能是退赛的），
// 只有明确标记退赛的才置 withdrawn。
//
// 用 `xmax = 0` 判断本次是插入还是更新：ON CONFLICT DO UPDATE 命中已存在行时
// 该行的 xmax 会被设置为当前事务号，未命中的新插入行 xmax 为 0。
func (s *TeamStore) Upsert(ctx context.Context, t *model.Team) (bool, error) {
	if t.Status == "" {
		t.Status = model.TeamActive
	}
	if t.Source == "" {
		t.Source = model.SourceExcel
	}
	var created bool
	err := s.q.QueryRow(ctx, `
		INSERT INTO teams (event_id, team_no, name, school, coach, group_code, members, status, source)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (event_id, team_no) DO UPDATE SET
			name       = EXCLUDED.name,
			school     = EXCLUDED.school,
			coach      = EXCLUDED.coach,
			group_code = EXCLUDED.group_code,
			members    = EXCLUDED.members,
			status     = EXCLUDED.status,
			source     = EXCLUDED.source
		RETURNING id, created_at, updated_at, (xmax = 0) AS inserted`,
		t.EventID, t.TeamNo, t.Name, t.School, t.Coach, t.GroupCode,
		t.Members, string(t.Status), string(t.Source),
	).Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt, &created)
	return created, mapError(err)
}

// Update 更新队伍的可变字段（不含编号与来源）。
func (s *TeamStore) Update(ctx context.Context, t *model.Team) error {
	tag, err := s.q.Exec(ctx, `
		UPDATE teams SET name=$2, school=$3, coach=$4, group_code=$5, members=$6
		WHERE id=$1`,
		t.ID, t.Name, t.School, t.Coach, t.GroupCode, t.Members)
	return affected(tag, err)
}

// SetStatus 设置队伍状态（弃赛 / 恢复）。
func (s *TeamStore) SetStatus(ctx context.Context, id int64, status model.TeamStatus) error {
	tag, err := s.q.Exec(ctx, `UPDATE teams SET status=$2 WHERE id=$1`, id, string(status))
	return affected(tag, err)
}

// SetGroup 调整队伍组别。
func (s *TeamStore) SetGroup(ctx context.Context, id int64, group string) error {
	tag, err := s.q.Exec(ctx, `UPDATE teams SET group_code=$2 WHERE id=$1`, id, group)
	return affected(tag, err)
}

// Delete 物理删除队伍。带成绩的队伍会被外键 RESTRICT 拦下（返回 ErrInUse）——
// 这正是「弃赛用软删除、删队要拦一道」的落点。
func (s *TeamStore) Delete(ctx context.Context, id int64) error {
	tag, err := s.q.Exec(ctx, `DELETE FROM teams WHERE id=$1`, id)
	return affected(tag, err)
}

// CountByEvent 统计赛项下队伍总数（含弃赛）。
func (s *TeamStore) CountByEvent(ctx context.Context, eventID string) (int, error) {
	var n int
	err := s.q.QueryRow(ctx, `SELECT count(*)::int FROM teams WHERE event_id=$1`, eventID).Scan(&n)
	return n, mapError(err)
}

// affected 把「影响 0 行」翻译成 ErrNotFound，统一各写入方法的行为。
func affected(tag interface{ RowsAffected() int64 }, err error) error {
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}
