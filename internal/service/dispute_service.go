package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jialangli/comptition-score-server/internal/engine"
	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// ============================================================================
// 争议工单（0005）
//
// 对应前端 P8 家族：
//
//	P8   待裁定队列（本文件的 OpenDisputes）
//	P8a/P8b/P8c  裁定三档位结论：维持原判 / 授权改分 / 取消资格（DecideDispute）
//	P8e  已裁定只读态，支持再裁定一次（DecideDispute 允许对已裁定工单再执行）
//	P2「我的申请」→ 撤回（WithdrawDispute）
//
// 两个建单入口按来源区分，共用同一张表与同一套裁定流程：
//
//	ReportDispute       裁判人工上报（source=referee）
//	ReportSyncConflict  离线补传撞车时系统自动建单（source=system）
// ============================================================================

// ReportDispute 裁判人工上报一条争议。
//
// 原因必填：没有说明的争议等于把判断成本全推给裁判长，事后也无法复盘。
// 重复上报由 ux_disputes_one_open 在数据库层挡下，这里翻译成 ErrDisputePending。
func (s *Service) ReportDispute(ctx context.Context, teamID int64, round int,
	kind model.DisputeKind, reason string) (*model.Dispute, error) {
	if err := requireReason(reason); err != nil {
		return nil, err
	}

	d := &model.Dispute{
		TeamID:   teamID,
		RoundNo:  round,
		Kind:     kind,
		Source:   model.SourceReferee,
		Status:   model.DisputePending,
		Reason:   reason,
		Operator: CurrentUser(ctx),
	}
	if err := s.tx(ctx, func(r store.Repos) error {
		if err := r.Disputes.Create(ctx, d); err != nil {
			return err
		}
		return log(ctx, r, model.ActDisputeReport,
			disputeTarget(d), "", d.Status.Label(), reason)
	}); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return nil, ErrDisputePending
		}
		return nil, err
	}
	return d, nil
}

// ReportSyncConflict 离线补传发现同队同轮已存在服务端记录时，自动建一条同步冲突工单。
//
// 与 ReportDispute 的三点不同：
//
//  1. **幂等**：同一队同一轮若已有一条待裁定的同步冲突工单，直接返回它。
//     补传会重试，每次都建一单会把队列冲垮；而这类冲突本质上是同一件事。
//  2. **原因由系统生成**：detail 为空时补一句可解释的文案，不强制人工填写 ——
//     现场没人给这条工单填原因，留空会让裁判长无从下手。
//  3. **operator 写「系统 · 补传」**：队列的「提出人 / 来源」列据此与人工上报区分开。
func (s *Service) ReportSyncConflict(ctx context.Context, teamID int64, round int,
	detail string) (*model.Dispute, error) {
	if open, err := s.ro().Disputes.ListByTeam(ctx, teamID); err == nil {
		for _, d := range open {
			if d.IsOpen() && d.Kind == model.DisputeSync && d.RoundNo == round {
				return &d, nil
			}
		}
	}

	if detail == "" {
		detail = fmt.Sprintf("离线补传发现第 %d 轮已存在服务端记录，两份成绩待裁定", round)
	}
	d := &model.Dispute{
		TeamID:   teamID,
		RoundNo:  round,
		Kind:     model.DisputeSync,
		Source:   model.SourceSystem,
		Status:   model.DisputePending,
		Reason:   detail,
		Operator: model.SourceSystem.Label(),
	}
	if err := s.tx(ctx, func(r store.Repos) error {
		if err := r.Disputes.Create(ctx, d); err != nil {
			return err
		}
		return log(ctx, r, model.ActDisputeReport,
			disputeTarget(d), "", d.Status.Label(), detail)
	}); err != nil {
		// 并发补传同时建单时，唯一索引会挡下后来者 —— 这也是幂等的，
		// 直接当作已有工单处理，不向调用方抛错（/sync 的主流程不能被它打断）。
		if errors.Is(err, store.ErrDuplicate) {
			return d, nil
		}
		return nil, err
	}
	return d, nil
}

// WithdrawDispute 撤回一条工单（上报人在裁判长裁定前取消）。
//
// 只对「待裁定」成立：已裁定的不可撤，只能再裁定一次并留痕（P8e 口径）。
// 撤回保留记录而不是物理删除 —— 提错了也要能回答「谁提的、什么时候撤的」。
//
// ⚠️ 本期不校验「撤回人 == 提出人」：鉴权仅预留插槽（api.CurrentUser），
// 等接入真实登录后在中间件补这一条判断，本方法签名不变。
func (s *Service) WithdrawDispute(ctx context.Context, id int64) error {
	return s.tx(ctx, func(r store.Repos) error {
		d, err := r.Disputes.Get(ctx, id)
		if err != nil {
			return err
		}
		if !d.IsOpen() {
			return ErrDisputeNotPending
		}
		if err := r.Disputes.Withdraw(ctx, id); err != nil {
			return err
		}
		return log(ctx, r, model.ActDisputeWithdraw,
			disputeTarget(d), d.Status.Label(), model.DisputeWithdrawn.Label(), "上报人撤回")
	})
}

// DecideDispute 裁定一条工单。
//
// 允许对**已裁定**的工单再执行一次（推翻原结论），每次都留痕，本表只保留最新结论 ——
// 这是 P8e「确需推翻时才走再裁定一次并留痕」的落地。
// 已撤回的工单不可裁定（它等于没提过）。
//
// 结论只记录、不联动改榜：verdict=disqualify 后的名次顺延与递补由工作人员
// 在后台执行（P8c note 7 口径），避免「裁定」与「执行」耦合。
//
// verdict=adjust（P8b 授权改分）：必须同时给出「采纳轮次 + 目标分数」，
// 本方法在**同一事务**内生成一张 Approved=false 的改分申请单（P9 路径），
// 并把该争议单的申述书照片继承到这张改分单（两处都挂）。
// 真正的分数写入仍留在 P9（ApplyScoreChange），本页不本页改分。
// 该轮尚无成绩时裁定失败并整体回滚（不留空改分单）。
func (s *Service) DecideDispute(ctx context.Context, id int64,
	verdict model.DisputeVerdict, reason string,
	adoptRoundNo int, targetScore float64) error {

	if err := requireReason(reason); err != nil {
		return err
	}
	if verdict == model.VerdictAdjust {
		if adoptRoundNo < model.MinRound || adoptRoundNo > model.MaxRound {
			return ErrAdjustRequiresTarget
		}
		if targetScore < 0 {
			return ErrAdjustRequiresTarget
		}
	}
	decider := CurrentUser(ctx)

	return s.tx(ctx, func(r store.Repos) error {
		d, err := r.Disputes.Get(ctx, id)
		if err != nil {
			return err
		}
		if d.Status == model.DisputeWithdrawn {
			return ErrDisputeNotPending
		}
		if err := r.Disputes.Decide(ctx, id, decider, verdict, reason); err != nil {
			return err
		}

		// P8b：授权改分 → 在同一事务内生成待审批改分单 + 继承申述书照片
		if verdict == model.VerdictAdjust {
			if err := s.spawnChangeRequestFromDispute(ctx, r, d, decider,
				adoptRoundNo, targetScore, reason); err != nil {
				return err
			}
		}

		before := d.Status.Label()
		if d.Verdict != nil {
			before += "（" + d.Verdict.Label() + "）"
		}
		after := model.DisputeDecided.Label() + "（" + verdict.Label() + "）"
		return log(ctx, r, model.ActDisputeDecide, disputeTarget(d), before, after, reason)
	})
}

// spawnChangeRequestFromDispute 在裁定为「授权改分」时，生成一张待审批改分单（P8b）。
//
// 与裁判主动发起的 ScoreChangeRequest 共用同一张表与同一套待审批队列，
// 区别仅在于触发源是争议裁定、且从争议单继承申述书照片（两处都挂）。
// 该采纳轮次尚未录入成绩时返回 FieldError，由外层事务整体回滚（不留空改分单）。
func (s *Service) spawnChangeRequestFromDispute(ctx context.Context, r store.Repos,
	d *model.Dispute, decider string, adoptRoundNo int, targetScore float64,
	reason string) error {

	team, err := r.Teams.Get(ctx, d.TeamID)
	if err != nil {
		return err
	}
	ev, err := r.Events.Get(ctx, team.EventID)
	if err != nil {
		return err
	}
	old, err := r.Scores.Get(ctx, d.TeamID, adoptRoundNo)
	if err != nil {
		// 该轮尚无成绩：无法生成改分单，整体回滚
		if errors.Is(err, store.ErrNotFound) {
			return &model.FieldError{
				Field: "adoptRoundNo",
				Msg:   fmt.Sprintf("%s 第 %d 轮尚无成绩，无法生成授权改分单", teamLabel(team), adoptRoundNo),
			}
		}
		return err
	}
	before := engine.Score(ev, *old, engine.RefTimeFor(ev, 0)).Total

	req := &model.ScoreChangeRequest{
		ScoreID: old.ID, TeamID: d.TeamID, RoundNo: adoptRoundNo,
		Before: before, After: targetScore,
		Reason: reason, Operator: decider, Approved: false,
	}
	if err := r.Changes.Create(ctx, req); err != nil {
		return err
	}

	// 两处都挂：把该争议单的申述书照片继承到这张改分单
	if appealID, aerr := s.AppealEvidenceIDOfDispute(ctx, d.ID); aerr != nil {
		return aerr
	} else if appealID != nil {
		if err := r.Changes.SetAppeal(ctx, req.ID, *appealID); err != nil {
			return err
		}
	}

	return log(ctx, r, model.ActScore,
		fmt.Sprintf("%s 第 %d 轮", teamLabel(team), adoptRoundNo),
		fmt.Sprintf("总分 %.1f", before),
		fmt.Sprintf("总分 %.1f（授权改分申请单 #%d，待裁判长授权）", targetScore, req.ID),
		reason)
}

// OpenDisputes 待裁定队列（P8 队列页数据源），先到先裁。
func (s *Service) OpenDisputes(ctx context.Context) ([]model.Dispute, error) {
	return s.ro().Disputes.ListOpen(ctx)
}

// Dispute 按工单号读取。
func (s *Service) Dispute(ctx context.Context, id int64) (*model.Dispute, error) {
	return s.ro().Disputes.Get(ctx, id)
}

// TeamDisputes 某队全部历史工单（含已裁定 / 已撤回），按时间倒序。
//
// 用于成绩单页判断该队是否被判取消资格（前端在队名后标红「（成绩作废）」）。
func (s *Service) TeamDisputes(ctx context.Context, teamID int64) ([]model.Dispute, error) {
	return s.ro().Disputes.ListByTeam(ctx, teamID)
}

// disputeTarget 审计的「操作对象」文案。
func disputeTarget(d *model.Dispute) string {
	return fmt.Sprintf("工单 %s · 队伍 #%d 第 %d 轮 · %s（来源 %s）",
		d.Code, d.TeamID, d.RoundNo, d.Kind.Label(), d.Source.Label())
}
