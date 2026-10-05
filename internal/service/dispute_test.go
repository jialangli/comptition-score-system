package service_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/service"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// ============================================================================
// 争议工单（0005）集成测试 —— 打到真实 PG，不做任何 mock
//
// 覆盖前端 P8 家族的全部流转：上报 → 队列 → 裁定 → 撤回 → 再裁定，
// 以及两条靠数据库兜底的不变式（重复上报去重、补传建单幂等）。
// ============================================================================

// newDisputeFixture 建赛项 + 一支队伍，返回队伍 ID。
func newDisputeFixture(t *testing.T, svc *service.Service, no, name string) int64 {
	t.Helper()
	ctx := operatorCtx("运营A")
	ev, err := svc.CreateEvent(ctx, brainPlanetEvent())
	if err != nil {
		t.Fatalf("建赛项失败: %v", err)
	}
	team, err := svc.CreateTeam(ctx, model.TeamDraft{
		EventID: ev.ID, TeamNo: no, Name: name, GroupCode: "小学组",
	})
	if err != nil {
		t.Fatalf("建队伍失败: %v", err)
	}
	return team.ID
}

// TestDisputeLifecycle 主链路：上报 → 进队列 → 裁定 → 出队列且结论落库 → 留痕。
func TestDisputeLifecycle(t *testing.T) {
	svc, _ := newSvc(t)
	teamID := newDisputeFixture(t, svc, "9001", "争议队")
	ctx := operatorCtx("裁判A")

	d, err := svc.ReportDispute(ctx, teamID, 1, model.DisputeDuplicate, "同一轮出现两份成绩")
	if err != nil {
		t.Fatalf("上报争议失败: %v", err)
	}
	if d.Status != model.DisputePending {
		t.Fatalf("新工单应为待裁定，实际 %q", d.Status)
	}
	if d.Source != model.SourceReferee {
		t.Fatalf("人工上报来源应为 referee，实际 %q", d.Source)
	}
	if want := fmt.Sprintf("D-%03d", d.ID); d.Code != want {
		t.Fatalf("工单号应由 ID 派生：期望 %s，实际 %s", want, d.Code)
	}

	open, err := svc.OpenDisputes(ctx)
	if err != nil {
		t.Fatalf("查待裁定队列失败: %v", err)
	}
	if len(open) != 1 || open[0].ID != d.ID {
		t.Fatalf("待裁定队列应恰有 1 条本工单，实际 %d 条", len(open))
	}

	// 裁定：取消资格（P8c）
	decideCtx := operatorCtx("裁判长C")
	if err := svc.DecideDispute(decideCtx, d.ID, model.VerdictDisqualify, "确认重复提交，取消资格"); err != nil {
		t.Fatalf("裁定失败: %v", err)
	}

	got, err := svc.Dispute(ctx, d.ID)
	if err != nil {
		t.Fatalf("查工单失败: %v", err)
	}
	if got.Status != model.DisputeDecided {
		t.Fatalf("裁定后应为已裁定，实际 %q", got.Status)
	}
	if got.Verdict == nil || *got.Verdict != model.VerdictDisqualify {
		t.Fatalf("裁定结论应为 disqualify，实际 %v", got.Verdict)
	}
	if got.Decider != "裁判长C" {
		t.Fatalf("裁定人应为裁判长C，实际 %q", got.Decider)
	}
	if got.DecidedAt == nil {
		t.Fatal("裁定后 decided_at 不应为空")
	}

	// 已裁定不再占队列
	open2, err := svc.OpenDisputes(ctx)
	if err != nil {
		t.Fatalf("查待裁定队列失败: %v", err)
	}
	if len(open2) != 0 {
		t.Fatalf("已裁定工单不应留在待裁定队列，实际仍有 %d 条", len(open2))
	}

	// 但历史仍可查（成绩单页据此标注「（成绩作废）」）
	hist, err := svc.TeamDisputes(ctx, teamID)
	if err != nil {
		t.Fatalf("查队伍历史工单失败: %v", err)
	}
	if len(hist) != 1 {
		t.Fatalf("已裁定工单应仍在历史里，实际 %d 条", len(hist))
	}

	// 留痕：上报与裁定各一条
	if n := countAction(t, svc, model.ActDisputeReport); n != 1 {
		t.Fatalf("上报争议应留 1 条审计，实际 %d 条", n)
	}
	if n := countAction(t, svc, model.ActDisputeDecide); n != 1 {
		t.Fatalf("裁定争议应留 1 条审计，实际 %d 条", n)
	}
}

// TestDisputeDuplicateReportBlocked 同队同轮同类型重复上报被数据库唯一索引挡下。
func TestDisputeDuplicateReportBlocked(t *testing.T) {
	svc, _ := newSvc(t)
	teamID := newDisputeFixture(t, svc, "9002", "重复上报队")
	ctx := operatorCtx("裁判A")

	if _, err := svc.ReportDispute(ctx, teamID, 1, model.DisputeDuplicate, "第一次上报"); err != nil {
		t.Fatalf("首次上报不应失败: %v", err)
	}
	if _, err := svc.ReportDispute(ctx, teamID, 1, model.DisputeDuplicate, "又点了一次"); !errors.Is(err, service.ErrDisputePending) {
		t.Fatalf("重复上报应返回 ErrDisputePending，实际 %v", err)
	}

	// 换类型不算重复：同一队同一轮的「其他申诉」是另一件事
	if _, err := svc.ReportDispute(ctx, teamID, 1, model.DisputeOther, "另一类问题"); err != nil {
		t.Fatalf("不同类型应可再提，实际失败: %v", err)
	}
	// 换轮次也不算重复
	if _, err := svc.ReportDispute(ctx, teamID, 2, model.DisputeDuplicate, "第 2 轮也重复了"); err != nil {
		t.Fatalf("不同轮次应可再提，实际失败: %v", err)
	}

	open, err := svc.OpenDisputes(ctx)
	if err != nil {
		t.Fatalf("查队列失败: %v", err)
	}
	if len(open) != 3 {
		t.Fatalf("应共 3 条待裁定（重复那条被挡下），实际 %d 条", len(open))
	}
}

// TestDisputeSyncConflictIdempotent 离线补传重复撞车时只建一条工单。
func TestDisputeSyncConflictIdempotent(t *testing.T) {
	svc, _ := newSvc(t)
	teamID := newDisputeFixture(t, svc, "9003", "补传冲突队")
	ctx := operatorCtx("系统")

	d1, err := svc.ReportSyncConflict(ctx, teamID, 1, "")
	if err != nil {
		t.Fatalf("系统建单失败: %v", err)
	}
	// 补传会重试：第二次必须命中既有工单，而不是再建一条
	d2, err := svc.ReportSyncConflict(ctx, teamID, 1, "补传重试")
	if err != nil {
		t.Fatalf("第二次系统建单失败: %v", err)
	}
	if d1.ID != d2.ID {
		t.Fatalf("补传重试应幂等（同一条工单），实际 %d != %d", d1.ID, d2.ID)
	}
	if d1.Source != model.SourceSystem {
		t.Fatalf("来源应为 system，实际 %q", d1.Source)
	}
	if d1.Kind != model.DisputeSync {
		t.Fatalf("类型应为 sync_conflict，实际 %q", d1.Kind)
	}
	if d1.Reason == "" {
		t.Fatal("detail 为空时系统应补一句可解释的原因，实际为空")
	}

	open, err := svc.OpenDisputes(ctx)
	if err != nil {
		t.Fatalf("查队列失败: %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("队列应只有 1 条，实际 %d 条", len(open))
	}
}

// TestDisputeWithdraw 撤回：出队列、留记录；已撤回不可再撤、不可裁定。
func TestDisputeWithdraw(t *testing.T) {
	svc, _ := newSvc(t)
	teamID := newDisputeFixture(t, svc, "9004", "误报队")
	ctx := operatorCtx("裁判A")

	d, err := svc.ReportDispute(ctx, teamID, 1, model.DisputeDuplicate, "手滑提错了")
	if err != nil {
		t.Fatalf("上报失败: %v", err)
	}
	if err := svc.WithdrawDispute(ctx, d.ID); err != nil {
		t.Fatalf("撤回失败: %v", err)
	}

	got, err := svc.Dispute(ctx, d.ID)
	if err != nil {
		t.Fatalf("查工单失败: %v", err)
	}
	if got.Status != model.DisputeWithdrawn {
		t.Fatalf("撤回后应为已撤回，实际 %q", got.Status)
	}

	open, err := svc.OpenDisputes(ctx)
	if err != nil {
		t.Fatalf("查队列失败: %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("撤回后不应留在待裁定队列，实际 %d 条", len(open))
	}

	// 重复撤回
	if err := svc.WithdrawDispute(ctx, d.ID); !errors.Is(err, service.ErrDisputeNotPending) {
		t.Fatalf("重复撤回应返回 ErrDisputeNotPending，实际 %v", err)
	}
	// 已撤回不可裁定
	if err := svc.DecideDispute(operatorCtx("裁判长C"), d.ID, model.VerdictUphold, "试着裁定已撤回的工单"); !errors.Is(err, service.ErrDisputeNotPending) {
		t.Fatalf("裁定已撤回工单应返回 ErrDisputeNotPending，实际 %v", err)
	}

	// 撤回也留痕
	if n := countAction(t, svc, model.ActDisputeWithdraw); n != 1 {
		t.Fatalf("撤回应留 1 条审计，实际 %d 条", n)
	}
}

// TestDisputeRedecide 已裁定工单可再裁定一次（P8e），结论覆盖、每次都留痕。
func TestDisputeRedecide(t *testing.T) {
	svc, _ := newSvc(t)
	teamID := newDisputeFixture(t, svc, "9005", "改判队")
	ctx := operatorCtx("裁判A")
	decideCtx := operatorCtx("裁判长C")

	d, err := svc.ReportDispute(ctx, teamID, 1, model.DisputeDuplicate, "先按维持原判处理")
	if err != nil {
		t.Fatalf("上报失败: %v", err)
	}
	if err := svc.DecideDispute(decideCtx, d.ID, model.VerdictUphold, "证据不足，维持原判"); err != nil {
		t.Fatalf("首次裁定失败: %v", err)
	}
	if err := svc.DecideDispute(decideCtx, d.ID, model.VerdictDisqualify, "新证据出现，改判取消资格"); err != nil {
		t.Fatalf("再裁定失败: %v", err)
	}

	got, err := svc.Dispute(ctx, d.ID)
	if err != nil {
		t.Fatalf("查工单失败: %v", err)
	}
	if got.Verdict == nil || *got.Verdict != model.VerdictDisqualify {
		t.Fatalf("再裁定后结论应为 disqualify，实际 %v", got.Verdict)
	}

	// 本表只保留最新结论，但每次裁定都留痕 —— 历史可追溯
	if n := countAction(t, svc, model.ActDisputeDecide); n != 2 {
		t.Fatalf("两次裁定应留 2 条审计，实际 %d 条", n)
	}
}

// TestDisputeReasonRequired 上报与裁定都必须说明原因。
func TestDisputeReasonRequired(t *testing.T) {
	svc, _ := newSvc(t)
	teamID := newDisputeFixture(t, svc, "9006", "缺原因队")
	ctx := operatorCtx("裁判A")

	if _, err := svc.ReportDispute(ctx, teamID, 1, model.DisputeDuplicate, "x"); !errors.Is(err, service.ErrReasonRequired) {
		t.Fatalf("原因过短应返回 ErrReasonRequired，实际 %v", err)
	}

	d, err := svc.ReportDispute(ctx, teamID, 1, model.DisputeDuplicate, "这次写清楚了")
	if err != nil {
		t.Fatalf("上报失败: %v", err)
	}
	if err := svc.DecideDispute(operatorCtx("裁判长C"), d.ID, model.VerdictAdjust, "短"); !errors.Is(err, service.ErrReasonRequired) {
		t.Fatalf("裁定原因过短应返回 ErrReasonRequired，实际 %v", err)
	}
}

// countAction 统计某类审计动作的条数。
func countAction(t *testing.T, svc *service.Service, action model.AuditAction) int {
	t.Helper()
	logs, err := svc.AuditLogs(operatorCtx("运营A"), store.AuditFilter{
		Action: string(action),
		Limit:  100,
	})
	if err != nil {
		t.Fatalf("查审计失败: %v", err)
	}
	return len(logs)
}
