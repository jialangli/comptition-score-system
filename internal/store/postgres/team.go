package postgres

import (
	"context"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// TeamStore 队伍仓储。
type TeamStore struct{ q querier }

const teamColumns = `id, event_id, team_no, name, school, coach, group_code,
	members, status, source, created_at, updated_at, contest_id,
	seat_id, seat_order, session`

func scanTeam(row interface{ Scan(...any) error }) (*model.Team, error) {
	var t model.Team
	var status, source, session string
	// seat_id 可空（NULL = 未排台），用 *int64 承接：nil 与 0 在这里是两件事
	// （未排台 vs 排到了 id=0 的赛台 —— 后者不可能存在，但类型上要能区分）。
	var seatID *int64
	if err := row.Scan(
		&t.ID, &t.EventID, &t.TeamNo, &t.Name, &t.School, &t.Coach, &t.GroupCode,
		&t.Members, &status, &source, &t.CreatedAt, &t.UpdatedAt, &t.ContestID,
		&seatID, &t.SeatOrder, &session,
	); err != nil {
		return nil, notFoundIfNoRows(err)
	}
	t.Status = model.TeamStatus(status)
	t.Source = model.TeamSource(source)
	t.SeatID = seatID
	// 未排台（seat_id NULL）时顺位没有意义，一律读成 0。
	//
	// 为什么需要这道归一：删赛台走的是 ON DELETE SET NULL，它只置空 seat_id，
	// **会把 seat_order 留在原位**（出现「未排台但顺位 3」）。删台路径已经显式
	// 清了顺位，这里再归一一道 —— 库是所有人共用的，读点不能假设写点都守规矩。
	if t.SeatID == nil {
		t.SeatOrder = 0
	}
	t.Session = model.TeamSession(session)
	return &t, nil
}

// Create 新增队伍并回填自增 ID 与时间戳。
//
// 不带 seat_id / seat_order：归台是「分台与顺位」页的动作，新建时队伍一律**未排台**。
func (s *TeamStore) Create(ctx context.Context, t *model.Team) error {
	if t.Status == "" {
		t.Status = model.TeamActive
	}
	if t.Source == "" {
		t.Source = model.SourceManual
	}
	if t.Session == "" {
		t.Session = model.SessionBoth
	}
	t.ContestID = store.CurrentContest(ctx)
	return mapError(s.q.QueryRow(ctx, `
		INSERT INTO teams (event_id, team_no, name, school, coach, group_code, members, status, source, contest_id, session)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING id, created_at, updated_at`,
		t.EventID, t.TeamNo, t.Name, t.School, t.Coach, t.GroupCode,
		t.Members, string(t.Status), string(t.Source), t.ContestID, string(t.Session),
	).Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt))
}

// Get 按 ID 读取队伍。
func (s *TeamStore) Get(ctx context.Context, id int64) (*model.Team, error) {
	return scanTeam(s.q.QueryRow(ctx,
		`SELECT `+teamColumns+` FROM teams WHERE id = $1 AND contest_id = $2`,
		id, store.CurrentContest(ctx)))
}

// GetByNo 按「赛项 + 编号」读取队伍 —— 导入比对的主键查找。
func (s *TeamStore) GetByNo(ctx context.Context, eventID, teamNo string) (*model.Team, error) {
	return scanTeam(s.q.QueryRow(ctx,
		`SELECT `+teamColumns+` FROM teams WHERE event_id = $1 AND team_no = $2 AND contest_id = $3`,
		eventID, teamNo, store.CurrentContest(ctx)))
}

// ListByEvent 按录入顺序返回赛项下的队伍。
//
// includeWithdrawn=false 时过滤掉弃赛队伍（打分 / 排名场景）；
// 队伍管理页需要看到弃赛记录，传 true。
func (s *TeamStore) ListByEvent(ctx context.Context, eventID string, includeWithdrawn bool) ([]model.Team, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+teamColumns+` FROM teams
		WHERE event_id = $1 AND ($2 OR status = 'active') AND contest_id = $3
		ORDER BY id`, eventID, includeWithdrawn, store.CurrentContest(ctx))
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
//
// ⚠️ 冲突分支**不更新 session**：报名表里没有「参赛轮次」这一列，
// 若跟着导入一起覆盖，运营在分台页手工设的「仅第 1 轮」会被无声改回 both。
// 参赛轮次属于现场编排，来源是分台页而不是报名表。
func (s *TeamStore) Upsert(ctx context.Context, t *model.Team) (bool, error) {
	if t.Status == "" {
		t.Status = model.TeamActive
	}
	if t.Source == "" {
		t.Source = model.SourceExcel
	}
	if t.Session == "" {
		t.Session = model.SessionBoth
	}
	t.ContestID = store.CurrentContest(ctx)
	var created bool
	// ON CONFLICT 目标列必须是 0004 后的复合唯一约束 (contest_id, event_id, team_no)：
	// 队伍编号只在赛事内唯一，跨赛事可重复。
	err := s.q.QueryRow(ctx, `
		INSERT INTO teams (event_id, team_no, name, school, coach, group_code, members, status, source, contest_id, session)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (contest_id, event_id, team_no) DO UPDATE SET
			name       = EXCLUDED.name,
			school     = EXCLUDED.school,
			coach      = EXCLUDED.coach,
			group_code = EXCLUDED.group_code,
			members    = EXCLUDED.members,
			status     = EXCLUDED.status,
			source     = EXCLUDED.source
		RETURNING id, created_at, updated_at, (xmax = 0) AS inserted`,
		t.EventID, t.TeamNo, t.Name, t.School, t.Coach, t.GroupCode,
		t.Members, string(t.Status), string(t.Source), t.ContestID, string(t.Session),
	).Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt, &created)
	return created, mapError(err)
}

// Update 更新队伍的可变字段（不含编号与来源）。
func (s *TeamStore) Update(ctx context.Context, t *model.Team) error {
	tag, err := s.q.Exec(ctx, `
		UPDATE teams SET name=$2, school=$3, coach=$4, group_code=$5, members=$6
		WHERE id=$1 AND contest_id=$7`,
		t.ID, t.Name, t.School, t.Coach, t.GroupCode, t.Members, store.CurrentContest(ctx))
	return affected(tag, err)
}

// SetStatus 设置队伍状态（弃赛 / 恢复）。
func (s *TeamStore) SetStatus(ctx context.Context, id int64, status model.TeamStatus) error {
	tag, err := s.q.Exec(ctx,
		`UPDATE teams SET status=$2 WHERE id=$1 AND contest_id=$3`,
		id, string(status), store.CurrentContest(ctx))
	return affected(tag, err)
}

// SetGroup 调整队伍组别。
func (s *TeamStore) SetGroup(ctx context.Context, id int64, group string) error {
	tag, err := s.q.Exec(ctx,
		`UPDATE teams SET group_code=$2 WHERE id=$1 AND contest_id=$3`,
		id, group, store.CurrentContest(ctx))
	return affected(tag, err)
}

// SetSeat 归台：写入赛台与台内顺位；seatID = nil 表示**取消归台**（回到未排台）。
//
// 不做「赛台是否存在」的前置查询：外键会兜底。服务层另有一道显式校验，
// 为的是在落库前给出可读提示（而不是让外键错误码冒到界面上）。
func (s *TeamStore) SetSeat(ctx context.Context, id int64, seatID *int64, order int) error {
	tag, err := s.q.Exec(ctx, `
		UPDATE teams SET seat_id=$2, seat_order=$3 WHERE id=$1 AND contest_id=$4`,
		id, seatID, order, store.CurrentContest(ctx))
	return affected(tag, err)
}

// SetSession 设置队伍参赛轮次。
func (s *TeamStore) SetSession(ctx context.Context, id int64, session model.TeamSession) error {
	tag, err := s.q.Exec(ctx,
		`UPDATE teams SET session=$2 WHERE id=$1 AND contest_id=$3`,
		id, string(session), store.CurrentContest(ctx))
	return affected(tag, err)
}

// ListBySeat 某赛台下已归台的队伍，按台内顺位排序（顺位相同时按 id，保证结果稳定可复现）。
//
// 这是「场次队伍派生」的取数入口：场次 = 赛台 × 赛项 × 组别 × 轮次 ——
// 队伍先按赛台取回，再按赛项 / 组别 / 参赛轮次（TeamSession.Rounds）过滤。
//
// 只取在册队伍：弃赛队不该出现在任何派生结果里。
func (s *TeamStore) ListBySeat(ctx context.Context, seatID int64) ([]model.Team, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+teamColumns+` FROM teams
		WHERE seat_id = $1 AND status = 'active' AND contest_id = $2
		ORDER BY seat_order, id`, seatID, store.CurrentContest(ctx))
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

// ListByIDs 按 ID 批量取队伍（返回顺序不保证）。
//
// 用于「已经拿到派生的 ID 列表、再补队伍详情」的场景（场次队伍 / 队列视图）：
// 顺序由调用方按自己的规则（如台内顺位）重排，避免逐个 Get 的 N+1。
// 空入参直接返回，不发一条 `ANY('{}')` 的空查询。
func (s *TeamStore) ListByIDs(ctx context.Context, ids []int64) ([]model.Team, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.q.Query(ctx,
		`SELECT `+teamColumns+` FROM teams WHERE id = ANY($1) AND contest_id = $2`,
		ids, store.CurrentContest(ctx))
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

// Delete 物理删除队伍。带成绩的队伍会被外键 RESTRICT 拦下（返回 ErrInUse）——
// 这正是「弃赛用软删除、删队要拦一道」的落点。
func (s *TeamStore) Delete(ctx context.Context, id int64) error {
	tag, err := s.q.Exec(ctx,
		`DELETE FROM teams WHERE id=$1 AND contest_id=$2`, id, store.CurrentContest(ctx))
	return affected(tag, err)
}

// CountByEvent 统计赛项下队伍总数（含弃赛）。
func (s *TeamStore) CountByEvent(ctx context.Context, eventID string) (int, error) {
	var n int
	err := s.q.QueryRow(ctx,
		`SELECT count(*)::int FROM teams WHERE event_id=$1 AND contest_id=$2`,
		eventID, store.CurrentContest(ctx)).Scan(&n)
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
