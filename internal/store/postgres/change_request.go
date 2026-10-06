package postgres

import (
	"context"
	"time"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// ChangeStore 改分申请单仓储。
type ChangeStore struct{ q querier }

const changeColumns = `id, score_id, team_id, round_no::int, before_total::float8,
	after_total::float8, reason, operator, approved, approver, created_at, decided_at,
	appeal_evidence_id`

func scanChange(row interface{ Scan(...any) error }) (*model.ScoreChangeRequest, error) {
	var r model.ScoreChangeRequest
	var decided *time.Time
	if err := row.Scan(
		&r.ID, &r.ScoreID, &r.TeamID, &r.RoundNo, &r.Before, &r.After,
		&r.Reason, &r.Operator, &r.Approved, &r.Approver, &r.CreatedAt, &decided,
		&r.AppealEvidenceID,
	); err != nil {
		return nil, notFoundIfNoRows(err)
	}
	r.DecidedAt = decided
	return &r, nil
}

// Create 落库一条改分申请。
//
// 不做「先查有没有待审批再插」——那样在并发下两个请求都会查到「没有」然后都插入。
// 直接插入，让 ux_scr_one_pending 部分唯一索引来裁决；冲突时 mapError 会把
// 23505 翻译成 store.ErrDuplicate，上层据此提示「该队该轮已有待审批申请」。
func (s *ChangeStore) Create(ctx context.Context, r *model.ScoreChangeRequest) error {
	return mapError(s.q.QueryRow(ctx, `
		INSERT INTO score_change_requests
			(score_id, team_id, round_no, before_total, after_total, reason, operator)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING id, created_at`,
		r.ScoreID, r.TeamID, r.RoundNo, r.Before, r.After, r.Reason, r.Operator,
	).Scan(&r.ID, &r.CreatedAt))
}

// Get 按申请单号读取。
func (s *ChangeStore) Get(ctx context.Context, id int64) (*model.ScoreChangeRequest, error) {
	return scanChange(s.q.QueryRow(ctx,
		`SELECT `+changeColumns+` FROM score_change_requests WHERE id=$1`, id))
}

// PendingOf 取某队某轮尚未处理的申请。
func (s *ChangeStore) PendingOf(ctx context.Context, teamID int64, round int) (*model.ScoreChangeRequest, error) {
	return scanChange(s.q.QueryRow(ctx, `
		SELECT `+changeColumns+` FROM score_change_requests
		WHERE team_id=$1 AND round_no=$2 AND NOT approved
		LIMIT 1`, teamID, round))
}

// ListPending 待审批队列，先到先审。
func (s *ChangeStore) ListPending(ctx context.Context) ([]model.ScoreChangeRequest, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+changeColumns+` FROM score_change_requests
		WHERE NOT approved
		ORDER BY created_at, id`)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var out []model.ScoreChangeRequest
	for rows.Next() {
		r, err := scanChange(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, mapError(rows.Err())
}

// Decide 标记申请已处理。
//
// 故意带 `AND NOT approved` 条件：重复处理同一单会命中 0 行，
// 由 affected 转成 ErrNotFound；若调用方明知已处理仍重复提交，
// 则由 service 层先取一次再决定，避免静默覆盖审批结论。
func (s *ChangeStore) Decide(ctx context.Context, id int64, approver string, approved bool) error {
	tag, err := s.q.Exec(ctx, `
		UPDATE score_change_requests
		SET approved = $2, approver = $3, decided_at = now()
		WHERE id = $1 AND NOT approved`, id, approved, approver)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// SetAppeal 关联申述书照片证据（appeal_evidence_id）。
//
// 用于 P8b 授权改分生成改分单时，从争议单继承同一张申述书照片，实现「两处都挂」。
// 不存在返回 ErrNotFound；appealEvidenceID 必须指向一条真实存在的 evidence。
func (s *ChangeStore) SetAppeal(ctx context.Context, id int64, appealEvidenceID int64) error {
	tag, err := s.q.Exec(ctx, `
		UPDATE score_change_requests
		SET appeal_evidence_id = $2
		WHERE id = $1`, id, appealEvidenceID)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// ListByTeam 某队历史申请，按时间倒序。
func (s *ChangeStore) ListByTeam(ctx context.Context, teamID int64) ([]model.ScoreChangeRequest, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+changeColumns+` FROM score_change_requests
		WHERE team_id=$1
		ORDER BY created_at DESC, id DESC`, teamID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var out []model.ScoreChangeRequest
	for rows.Next() {
		r, err := scanChange(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, mapError(rows.Err())
}
