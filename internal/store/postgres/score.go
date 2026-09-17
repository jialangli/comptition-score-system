package postgres

import (
	"context"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ScoreStore 打分记录仓储。
type ScoreStore struct{ q querier }

const scoreColumns = `id, team_id, round_no::int, task_values, duration_sec::float8,
	yellow::int, red::int, signed, operator, created_at, updated_at`

func scanScore(row interface{ Scan(...any) error }) (*model.ScoreRecord, error) {
	var r model.ScoreRecord
	var taskRaw []byte
	if err := row.Scan(
		&r.ID, &r.TeamID, &r.RoundNo, &taskRaw, &r.DurationSec,
		&r.Yellow, &r.Red, &r.Signed, &r.Operator, &r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return nil, notFoundIfNoRows(err)
	}
	if err := scanJSONB(taskRaw, &r.TaskValues); err != nil {
		return nil, err
	}
	return &r, nil
}

// Save 按 (team_id, round_no) 合并写入一条打分记录。
//
// 采用 upsert 而非纯插入：裁判在平板上可能反复提交同一轮（改主意、重录），
// 唯一索引 ux_scores_team_round 保证同队同轮只有一条，
// 这里用 ON CONFLICT DO UPDATE 把「重复提交」变成「覆盖上一次草稿」，
// 而不是抛错打断现场操作。
//
// 注意：**改已提交的成绩必须走改分审批**（见 service 层），
// 本方法的「覆盖」语义只服务于同一轮内的连续编辑。
func (s *ScoreStore) Save(ctx context.Context, r *model.ScoreRecord) error {
	return mapError(s.q.QueryRow(ctx, `
		INSERT INTO scores (team_id, round_no, task_values, duration_sec, yellow, red, signed, operator)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (team_id, round_no) DO UPDATE SET
			task_values  = EXCLUDED.task_values,
			duration_sec = EXCLUDED.duration_sec,
			yellow       = EXCLUDED.yellow,
			red          = EXCLUDED.red,
			signed       = EXCLUDED.signed,
			operator     = EXCLUDED.operator
		RETURNING id, created_at, updated_at`,
		r.TeamID, r.RoundNo, jsonArg(r.TaskValues), r.DurationSec,
		r.Yellow, r.Red, r.Signed, r.Operator,
	).Scan(&r.ID, &r.CreatedAt, &r.UpdatedAt))
}

// Get 读取某队某一轮的记录。
func (s *ScoreStore) Get(ctx context.Context, teamID int64, round int) (*model.ScoreRecord, error) {
	return scanScore(s.q.QueryRow(ctx,
		`SELECT `+scoreColumns+` FROM scores WHERE team_id=$1 AND round_no=$2`, teamID, round))
}

// ListByTeam 读取某队全部轮次，按轮次升序。
func (s *ScoreStore) ListByTeam(ctx context.Context, teamID int64) ([]model.ScoreRecord, error) {
	rows, err := s.q.Query(ctx,
		`SELECT `+scoreColumns+` FROM scores WHERE team_id=$1 ORDER BY round_no`, teamID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var out []model.ScoreRecord
	for rows.Next() {
		r, err := scanScore(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, mapError(rows.Err())
}

// ListByEvent 一次性取回赛项下所有队伍的全部轮次记录。
//
// 返回 teamID → 记录切片，供榜单计算使用。刻意做成一次查询：
// 榜单是高频只读接口（大屏每 5 秒轮询），绝不能出现 N+1。
func (s *ScoreStore) ListByEvent(ctx context.Context, eventID string) (map[int64][]model.ScoreRecord, error) {
	rows, err := s.q.Query(ctx, `
		SELECT s.id, s.team_id, s.round_no::int, s.task_values, s.duration_sec::float8,
		       s.yellow::int, s.red::int, s.signed, s.operator, s.created_at, s.updated_at
		FROM scores s
		JOIN teams t ON t.id = s.team_id
		WHERE t.event_id = $1
		ORDER BY s.team_id, s.round_no`, eventID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	out := map[int64][]model.ScoreRecord{}
	for rows.Next() {
		r, err := scanScore(rows)
		if err != nil {
			return nil, err
		}
		out[r.TeamID] = append(out[r.TeamID], *r)
	}
	return out, mapError(rows.Err())
}

// Delete 删除一条打分记录。
func (s *ScoreStore) Delete(ctx context.Context, id int64) error {
	tag, err := s.q.Exec(ctx, `DELETE FROM scores WHERE id=$1`, id)
	return affected(tag, err)
}

// CountByEvent 统计赛项下已录入的打分记录条数（总览 KPI 用）。
func (s *ScoreStore) CountByEvent(ctx context.Context, eventID string) (int, error) {
	var n int
	err := s.q.QueryRow(ctx, `
		SELECT count(*)::int FROM scores s
		JOIN teams t ON t.id = s.team_id
		WHERE t.event_id = $1`, eventID).Scan(&n)
	return n, mapError(err)
}
