package service

import (
	"context"
	"errors"
	"fmt"
	"time"

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
// ⚠ 已知边界（0003 迁移后已解除）：早期版本的「申请」不落库（没有申请单表），
// 因此无法防止同一申请被重复提交、也无法在界面上展示待审批队列。
// 现在申请写入 score_change_requests 表：
//   - 防重复由部分唯一索引 ux_scr_one_pending 兜底（同一队同一轮仅一条待审批），
//     冲突时返回 store.ErrDuplicate，**不靠应用层先查再插**（那样有竞态窗口）；
//   - 待审批队列见 ListPendingChanges，授权闭环见 ApproveChange / RejectChange。
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
	// 黄牌写路径归一（2026/10/10 口径）：记满阈值即转 1 张红牌并把黄牌计数清零，
	// 与前端录入端（setYellow）和 engine.NormalizeCards 同一口径 —— 两端各算一套必然漂移。
	// ⚠️ 必须放在算分之前：否则本次算的是未归一的牌面。
	curYellow, upRed := engine.NormalizeCards(rec.Yellow, ev.PenaltyRule.CardRules)
	rec.Yellow, rec.UpgradedRed = curYellow, rec.UpgradedRed+upRed

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
		ScoreID: old.ID, TeamID: teamID, RoundNo: round,
		Before: before, After: after,
		Reason: reason, Operator: CurrentUser(ctx), Approved: false,
	}

	// 申请单与审计写在同一事务里：要么「申请 + 留痕」一起成功，要么一起回滚，
	// 不会出现「有一条申请但审计里查不到」的孤儿记录。
	if err := s.tx(ctx, func(r store.Repos) error {
		if err := r.Changes.Create(ctx, req); err != nil {
			return err
		}
		return log(ctx, r, model.ActScore,
			fmt.Sprintf("%s 第 %d 轮", teamLabel(team), round),
			fmt.Sprintf("总分 %.1f", before),
			fmt.Sprintf("总分 %.1f（申请单 #%d，待裁判长授权）", after, req.ID),
			reason)
	}); err != nil {
		// 唯一索引挡下的重复提交：给一句运营看得懂的话，而不是抛裸错误
		if errors.Is(err, store.ErrDuplicate) {
			return nil, fmt.Errorf("%s 第 %d 轮%w（请先处理已有申请单）",
				teamLabel(team), round, ErrChangePending)
		}
		return nil, err
	}
	return req, nil
}

// ListPendingChanges 待审批队列，按申请时间正序（先到先审）。
func (s *Service) ListPendingChanges(ctx context.Context) ([]model.ScoreChangeRequest, error) {
	return s.ro().Changes.ListPending(ctx)
}

// PendingChangeOf 查某队某轮是否已有待审批申请；没有返回 store.ErrNotFound。
func (s *Service) PendingChangeOf(ctx context.Context, teamID int64, round int) (*model.ScoreChangeRequest, error) {
	return s.ro().Changes.PendingOf(ctx, teamID, round)
}

// decideChange 处理申请单的公共部分：取出待审批单 → 标记 → 写审计。
//
// approved=true 走 ApplyScoreChange 落成绩；本方法只负责申请单本身的状态流转，
// 两条路径刻意分开，避免「驳回」时误写成绩。
func (s *Service) decideChange(ctx context.Context, id int64, approver string,
	approved bool) (*model.ScoreChangeRequest, error) {

	if approver == "" {
		return nil, &model.FieldError{Field: "approver", Msg: "必须记录处理人（裁判长）"}
	}
	req, err := s.ro().Changes.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.Approved {
		return nil, fmt.Errorf("申请单 #%d%w", id, ErrAlreadyDecided)
	}

	verb := "驳回"
	if approved {
		verb = "授权"
	}
	if err := s.tx(ctx, func(r store.Repos) error {
		if err := r.Changes.Decide(ctx, id, approver, approved); err != nil {
			return err
		}
		team, err := r.Teams.Get(ctx, req.TeamID)
		if err != nil {
			return err
		}
		return logApproved(ctx, r, model.ActScore, approver,
			fmt.Sprintf("申请单 #%d · %s 第 %d 轮", id, teamLabel(team), req.RoundNo),
			fmt.Sprintf("总分 %.1f", req.Before),
			fmt.Sprintf("总分 %.1f（裁判长已%s）", req.After, verb),
			req.Reason)
	}); err != nil {
		return nil, err
	}
	req.Approved = approved
	req.Approver = approver
	now := time.Now()
	req.DecidedAt = &now
	return req, nil
}

// RejectChange 裁判长驳回改分申请（成绩不变，申请单与审计留痕）。
func (s *Service) RejectChange(ctx context.Context, id int64, approver string) (*model.ScoreChangeRequest, error) {
	return s.decideChange(ctx, id, approver, false)
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
	// 黄牌写路径归一（2026/10/10 口径）：记满阈值即转 1 张红牌并把黄牌计数清零，
	// 与前端录入端（setYellow）和 engine.NormalizeCards 同一口径 —— 两端各算一套必然漂移。
	// ⚠️ 必须放在算分之前：否则本次算的是未归一的牌面。
	curYellow, upRed := engine.NormalizeCards(rec.Yellow, ev.PenaltyRule.CardRules)
	rec.Yellow, rec.UpgradedRed = curYellow, rec.UpgradedRed+upRed

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
		// 闭环：若该队该轮有挂着的申请单，授权落库时一并把它标记为已处理。
		// 查不到申请单不算错 —— 裁判长遇到紧急情况可以直接改分（仍需留痕），
		// 申请单只是「裁判发起」那条路径的产物。
		if pending, perr := r.Changes.PendingOf(ctx, rec.TeamID, rec.RoundNo); perr == nil {
			if err := r.Changes.Decide(ctx, pending.ID, approver, true); err != nil {
				return err
			}
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
