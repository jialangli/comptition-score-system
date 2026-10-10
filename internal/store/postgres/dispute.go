package postgres

import (
	"context"
	"time"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// DisputeStore 争议工单仓储。
type DisputeStore struct{ q querier }

// verdict 用 COALESCE 兜成空串：待裁定时它是 NULL，而把 NULL 扫进 *string
// 不如直接拿到空串来得直白（空串即「尚未裁定」，转换时判空即可）。
const disputeColumns = `id, team_id, round_no::int, kind, source, status, reason,
	operator, COALESCE(verdict,'') AS verdict, decider, decision_reason,
	created_at, decided_at`

func scanDispute(row interface{ Scan(...any) error }) (*model.Dispute, error) {
	var d model.Dispute
	var kind, source, status, verdict string
	var decided *time.Time
	if err := row.Scan(
		&d.ID, &d.TeamID, &d.RoundNo, &kind, &source, &status,
		&d.Reason, &d.Operator, &verdict, &d.Decider, &d.DecisionReason,
		&d.CreatedAt, &decided,
	); err != nil {
		return nil, notFoundIfNoRows(err)
	}
	d.Kind = model.DisputeKind(kind)
	d.Source = model.DisputeSource(source)
	d.Status = model.DisputeStatus(status)
	if verdict != "" {
		v := model.DisputeVerdict(verdict)
		d.Verdict = &v
	}
	d.DecidedAt = decided
	d.DeriveCode()
	return &d, nil
}

// Create 落库一条争议工单。
//
// 不做「先查有没有待裁定再插」——离线补传是并发的，那样两个请求都会查到
// 「没有」然后都插进去。直接插入，交给 ux_disputes_one_open 部分唯一索引裁决；
// 冲突时 mapError 把 23505 翻译成 store.ErrDuplicate，上层据此提示
// 「该队该轮已有一条同类待裁定工单」。
func (s *DisputeStore) Create(ctx context.Context, d *model.Dispute) error {
	err := mapError(s.q.QueryRow(ctx, `
		INSERT INTO disputes
			(contest_id, team_id, round_no, kind, source, status, reason, operator)
		VALUES ($1,$2,$3,$4,$5,'pending',$6,$7)
		RETURNING id, created_at`,
		store.CurrentContest(ctx), d.TeamID, d.RoundNo, d.Kind, d.Source,
		d.Reason, d.Operator,
	).Scan(&d.ID, &d.CreatedAt))
	if err != nil {
		return err
	}
	d.Status = model.DisputePending
	d.DeriveCode()
	return nil
}

// Get 按工单号读取。
func (s *DisputeStore) Get(ctx context.Context, id int64) (*model.Dispute, error) {
	return scanDispute(s.q.QueryRow(ctx, `
		SELECT `+disputeColumns+` FROM disputes
		WHERE id=$1 AND contest_id=$2`, id, store.CurrentContest(ctx)))
}

// ListOpen 待裁定队列，先到先裁（P8 队列页数据源）。
func (s *DisputeStore) ListOpen(ctx context.Context) ([]model.Dispute, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+disputeColumns+` FROM disputes
		WHERE contest_id=$1 AND status='pending'
		ORDER BY created_at, id`, store.CurrentContest(ctx))
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var out []model.Dispute
	for rows.Next() {
		d, err := scanDispute(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, mapError(rows.Err())
}

// ListByTeam 某队全部历史工单，按时间倒序。
func (s *DisputeStore) ListByTeam(ctx context.Context, teamID int64) ([]model.Dispute, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+disputeColumns+` FROM disputes
		WHERE contest_id=$1 AND team_id=$2
		ORDER BY created_at DESC, id DESC`, store.CurrentContest(ctx), teamID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var out []model.Dispute
	for rows.Next() {
		d, err := scanDispute(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, mapError(rows.Err())
}

// Decide 裁定一条工单。
//
// 刻意**不加** `AND status='pending'` 守卫（与 ChangeStore.Decide 相反）：
// 前端 P8e 明确支持「确需推翻时再裁定一次并留痕」，所以允许 pending → decided
// 与 decided → decided 两种流转；每次裁定都在 service 层写审计，本表只保留最新结论。
//
// 但排除 withdrawn：已撤回的工单等于没提过，不能拿来裁定。
func (s *DisputeStore) Decide(ctx context.Context, id int64, decider string,
	verdict model.DisputeVerdict, reason string) error {
	tag, err := s.q.Exec(ctx, `
		UPDATE disputes
		SET status='decided', verdict=$3, decider=$4, decision_reason=$5, decided_at=now()
		WHERE id=$1 AND contest_id=$2 AND status <> 'withdrawn'`,
		id, store.CurrentContest(ctx), verdict, decider, reason)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// Withdraw 撤回一条工单，仅在待裁定状态下允许。
//
// 带 `AND status='pending'`：已裁定的工单不可撤回（只能再裁定一次并留痕），
// 命中 0 行由 affected 转成 ErrNotFound。
func (s *DisputeStore) Withdraw(ctx context.Context, id int64) error {
	tag, err := s.q.Exec(ctx, `
		UPDATE disputes
		SET status='withdrawn', decided_at=now()
		WHERE id=$1 AND contest_id=$2 AND status='pending'`,
		id, store.CurrentContest(ctx))
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// ListDisqualifiedTeamIDs 某赛项被裁定取消资格（成绩作废）的队伍 ID。
//
// 只认「已裁定 + 结论 disqualify」：已撤回的工单等于没提过，不算；
// 改判为维持原判 / 授权改分后该队自动不再命中 —— 这正是把判据留在工单表
// 而不是另存一份「作废标记」的好处，不需要任何回滚维护。
//
// JOIN teams 是为了按赛项过滤：工单本身只带 team_id（跨赛项的同名队伍会串）。
// DISTINCT 是因为同一队可能有多张工单命中（如两轮各裁一次）。
func (s *DisputeStore) ListDisqualifiedTeamIDs(ctx context.Context, eventID string) ([]int64, error) {
	rows, err := s.q.Query(ctx, `
		SELECT DISTINCT d.team_id
		FROM disputes d
		JOIN teams t ON t.id = d.team_id
		WHERE d.contest_id=$1 AND t.event_id=$2
		  AND d.status='decided' AND d.verdict='disqualify'
		ORDER BY d.team_id`,
		store.CurrentContest(ctx), eventID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, mapError(err)
		}
		out = append(out, id)
	}
	return out, mapError(rows.Err())
}
