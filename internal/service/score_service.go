package service

import (
	"context"
	"fmt"

	"github.com/jialangli/comptition-score-server/internal/engine"
	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// ============================================================================
// 打分用例
//
// 需求确认单里关于改分的原话：
//
//	「裁判提交后禁止直接改分，需发起修改请求；裁判长授权后方可修改，
//	  系统记录修改前后的分数、操作人及审批人，确保全程留痕。」
//
// 落到代码上是两个方法、两条路径，刻意不合成一个：
//
//	RequestScoreChange  裁判发起申请 —— **不写成绩**，只写审计
//	ApplyScoreChange    裁判长授权后落库 —— 写成绩 + 写审计（含审批人）
//
// 为什么坚持分开：如果只给一个「改分」方法，裁判就能自己改自己的分，
// 「需裁判长授权」这句话就只存在于文档里了。
//
// ⚠ 已知边界：本期的「申请」不落库（没有申请单表），因此无法防止同一申请被
// 重复提交、也无法在界面上展示待审批队列。审批队列需要一张
// score_change_requests 表（下一次迁移补），届时不改本文件的方法签名，
// 只需在 RequestScoreChange 里多写一次 INSERT。
// ============================================================================

// SaveScore 录入或修改**未签字**的成绩。
//
// 规则：
//
//	首次录入            → 允许
//	已有记录且未签字     → 允许（同轮草稿编辑，仍然留痕）
//	已有记录且已签字     → **拒绝**，返回 ErrScoreSubmitted，提示走改分申请
func (s *Service) SaveScore(ctx context.Context, rec *model.ScoreRecord) (*model.ScoreRecord, error) {
	team, err := s.ro().Teams.Get(ctx, rec.TeamID)
	if err != nil {
		return nil, err
	}
	ev, err := s.ro().Events.Get(ctx, team.EventID)
	if err != nil {
		return nil, err
	}
	if rec.RoundNo < model.MinRound || rec.RoundNo > model.MaxRound {
		return nil, &model.FieldError{
			Field: "roundNo",
			Msg:   fmt.Sprintf("轮次只能是 %d 或 %d", model.MinRound, model.MaxRound),
		}
	}
	if rec.Operator == "" {
		rec.Operator = CurrentUser(ctx)
	}

	// 先算新成绩，写入前就能把「修改后的总分」写进审计 —— 而不是事后补算
	after := engine.Score(ev, *rec, engine.RefTimeFor(ev, 0))

	old, err := s.ro().Scores.Get(ctx, rec.TeamID, rec.RoundNo)
	switch {
	case err == nil && old.Signed:
		return nil, fmt.Errorf("%w（%s 第 %d 轮，记分员 %s）",
			ErrScoreSubmitted, teamLabel(team), rec.RoundNo, old.Operator)
	case err != nil && err != store.ErrNotFound:
		return nil, err
	}

	before := "未录入"
	reason := "首次录入"
	if old != nil {
		before = scoreLabel(engine.Score(ev, *old, engine.RefTimeFor(ev, 0)))
		reason = "同轮草稿编辑（成绩未签字）"
	}

	if err := s.tx(ctx, func(r store.Repos) error {
		if err := r.Scores.Save(ctx, rec); err != nil {
			return err
		}
		return log(ctx, r, model.ActScore,
			fmt.Sprintf("%s 第 %d 轮", teamLabel(team), rec.RoundNo),
			before, scoreLabel(after), reason)
	}); err != nil {
		return nil, err
	}
	return rec, nil
}

// GetScore 读取某队某轮成绩。
func (s *Service) GetScore(ctx context.Context, teamID int64, round int) (*model.ScoreRecord, error) {
	return s.ro().Scores.Get(ctx, teamID, round)
}

// ListScores 读取某队全部轮次。
func (s *Service) ListScores(ctx context.Context, teamID int64) ([]model.ScoreRecord, error) {
	return s.ro().Scores.ListByTeam(ctx, teamID)
}

// ScoreChangeRequest 裁判发起的改分申请。
//
// 不修改成绩，只写一条审计 —— 这正是「禁止直接改分」在代码层面的体现。
// 返回值里的 Approved 恒为 false，供界面展示「待裁判长授权」。
func (s *Service) ScoreChangeRequest(ctx context.Context, teamID int64, round int,
	after float64, reason string) (*model.ScoreChangeRequest, error) {

	if err := requireReason(reason); err != nil {
		return nil, err
	}
	team, err := s.ro().Teams.Get(ctx, teamID)
	if err != nil {
		return nil, err
	}
	ev, err := s.ro().Events.Get(ctx, team.EventID)
	if err != nil {
		return nil, err
	}
	old, err := s.ro().Scores.Get(ctx, teamID, round)
	if err != nil {
		return nil, err
	}
	before := engine.Score(ev, *old, engine.RefTimeFor(ev, 0)).Total

	req := &model.ScoreChangeRequest{
		ScoreID: old.ID, TeamID: teamID, Before: before, After: after,
		Reason: reason, Operator: CurrentUser(ctx), Approved: false,
	}
	if err := s.tx(ctx, func(r store.Repos) error {
		return log(ctx, r, model.ActScore,
			fmt.Sprintf("%s 第 %d 轮", teamLabel(team), round),
			fmt.Sprintf("总分 %.1f", before),
			fmt.Sprintf("总分 %.1f（申请修改，待裁判长授权）", after),
			reason)
	}); err != nil {
		return nil, err
	}
	return req, nil
}

// ApplyScoreChange 裁判长授权后落库。
//
// 与 SaveScore 的区别就在 approver：这条路径**必须**记录审批人，
// 且允许修改已签字的成绩（这正是改分的意义）。
//
// 依据需求确认单：裁判长本人评分出错时同样是「自己审批自己」，
// 所以这里不禁止 approver == rec.Operator，而是把两个名字都留进审计 ——
// 规则是「可追溯」而非「不可自我批准」。
func (s *Service) ApplyScoreChange(ctx context.Context, rec *model.ScoreRecord,
	approver, reason string) (*model.ScoreRecord, error) {

	if err := requireReason(reason); err != nil {
		return nil, err
	}
	if approver == "" {
		return nil, &model.FieldError{Field: "approver", Msg: "必须记录授权人（裁判长）"}
	}
	team, err := s.ro().Teams.Get(ctx, rec.TeamID)
	if err != nil {
		return nil, err
	}
	ev, err := s.ro().Events.Get(ctx, team.EventID)
	if err != nil {
		return nil, err
	}
	old, err := s.ro().Scores.Get(ctx, rec.TeamID, rec.RoundNo)
	if err != nil {
		return nil, err
	}
	before := engine.Score(ev, *old, engine.RefTimeFor(ev, 0)).Total

	// 保留原记分员：改分不改变「谁打的分」这个事实
	if rec.Operator == "" {
		rec.Operator = old.Operator
	}
	after := engine.Score(ev, *rec, engine.RefTimeFor(ev, 0))

	if err := s.tx(ctx, func(r store.Repos) error {
		if err := r.Scores.Save(ctx, rec); err != nil {
			return err
		}
		// 审批人写进独立列而不是拼进 reason —— 否则「这个月裁判长批了几次改分」
		// 这类问题只能靠文本模糊匹配来回答。
		return logApproved(ctx, r, model.ActScore, approver,
			fmt.Sprintf("%s 第 %d 轮", teamLabel(team), rec.RoundNo),
			fmt.Sprintf("总分 %.1f", before),
			fmt.Sprintf("总分 %.1f", after.Total),
			reason)
	}); err != nil {
		return nil, err
	}
	return rec, nil
}

// ScoresByEvent 一次性取回赛项下全部成绩（榜单计算用）。
func (s *Service) ScoresByEvent(ctx context.Context, eventID string) (map[int64][]model.ScoreRecord, error) {
	return s.ro().Scores.ListByEvent(ctx, eventID)
}

func scoreLabel(r model.ScoreResult) string {
	return fmt.Sprintf("总分 %.1f（基础 %.1f + 加分 %.1f − 扣分 %.1f）",
		r.Total, r.Base, r.Bonus, r.Penalty)
}
