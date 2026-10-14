package service_test

// ============================================================================
// 改分申请单的「用时」—— 迁移 0022 + wireframe P2b「用时（可改 · 改分模式专属）」
//
// 要守住的口径（这三条来自设计稿，不是实现细节）：
//   1. 原用时（before_time）由**服务端**从成绩记录带出 —— 不接受前端传，
//      因为"原用时是多少"正是争议本身要核的事实；
//   2. 裁判长授权落库时，提交的 time 必须**等于**申请单声明的拟改用时，
//      不一致就挡住 —— 用时是并列裁决的 tiebreak（总分↓→用时↑→轮次小者），
//      也是时间奖励的输入，"申请一个数、落另一个数"等于绕过审批改名次；
//   3. 未声明用时的申请不受此闸影响（向后兼容，也是绝大多数改分场景）。
//
// 另有一条容易写错的边界：**0 秒是合法用时**，必须与"没传"（nil）区分 ——
// 所以两列都是 *float64，测试里用 afterTime=0 专门验一次。
// ============================================================================

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/service"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// 测试素材里的用时：mustScoreFor 录的是 100 秒（01:40），申请改成 80 秒（01:20）。
const (
	baseTimeSec     = 100.0
	requestedTime   = 80.0
	mismatchedTime  = 60.0
	changeReasonTxt = "用时复核：停表早按 20 秒，回看录像后修正"
)

// seedTimeChangeFixture 建「赛项 + 队伍 + 已签成绩（用时 100 秒）」，返回队伍 ID。
func seedTimeChangeFixture(t *testing.T, svc *service.Service, ctx context.Context) int64 {
	t.Helper()
	ev := mustEventAt(t, svc, ctx, brainPlanetEvent())
	team := mustTeamAt(t, svc, ctx, ev.ID, "1001", "用时队", "小学组")
	mustScoreFor(t, svc, ctx, team.ID) // DurationSec=100 / Signed=true
	return team.ID
}

// applyWithTime 走「授权落库」路径，用时由参数给。
func applyWithTime(t *testing.T, svc *service.Service, teamID int64, durSec float64) error {
	t.Helper()
	_, err := svc.ApplyScoreChange(operatorCtx("裁判长B"), &model.ScoreRecord{
		TeamID: teamID, RoundNo: 1, DurationSec: durSec, Signed: true,
		TaskValues: map[string]any{"focus": 90.0, "build": 90.0},
	}, "裁判长B", "按申请单复核后授权")
	return err
}

func TestChangeRequestCarriesTimeAndServerDerivesBefore(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("裁判A")
	teamID := seedTimeChangeFixture(t, svc, ctx)

	req, err := svc.ScoreChangeRequest(ctx, teamID, 1, 90.0, fptr(requestedTime), changeReasonTxt)
	if err != nil {
		t.Fatalf("发起带用时的改分申请失败: %v", err)
	}
	if !req.ChangesTime() {
		t.Fatalf("声明了 afterTime 的申请 ChangesTime 应为 true")
	}
	if req.BeforeTime == nil || !almostEqual(*req.BeforeTime, baseTimeSec) {
		t.Errorf("before_time 应由服务端从成绩带出 %.0f 秒，实际 %v", baseTimeSec, req.BeforeTime)
	}
	if req.AfterTime == nil || !almostEqual(*req.AfterTime, requestedTime) {
		t.Errorf("after_time 应为 %.0f 秒，实际 %v", requestedTime, req.AfterTime)
	}

	// 读回确认真的落库了（只断言返回值，等于没验 store 层）
	back, err := svc.PendingChangeOf(ctx, teamID, 1)
	if err != nil {
		t.Fatalf("读回待审批申请失败: %v", err)
	}
	if back.BeforeTime == nil || !almostEqual(*back.BeforeTime, baseTimeSec) ||
		back.AfterTime == nil || !almostEqual(*back.AfterTime, requestedTime) {
		t.Errorf("落库用时应为 %v / %v，实际 %v / %v",
			baseTimeSec, requestedTime, back.BeforeTime, back.AfterTime)
	}

	// 审计要看得见"这单还改了用时"——否则翻留痕只能看到总分变化
	logs, err := svc.AuditLogs(ctx, store.AuditFilter{Action: string(model.ActScore)})
	if err != nil {
		t.Fatalf("读审计失败: %v", err)
	}
	var found bool
	for _, l := range logs {
		if strings.Contains(l.Reason, "用时 01:40 → 01:20") {
			found = true
		}
	}
	if !found {
		t.Errorf("改分申请审计应注明用时变化（用时 01:40 → 01:20），实际未找到")
	}
}

func TestApplyChangeRejectsTimeMismatch(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("裁判A")
	teamID := seedTimeChangeFixture(t, svc, ctx)

	if _, err := svc.ScoreChangeRequest(ctx, teamID, 1, 90.0, fptr(requestedTime), changeReasonTxt); err != nil {
		t.Fatalf("发起申请失败: %v", err)
	}

	// 落库时给一个与申请单不同的用时 → 必须被挡下（这正是修这批要防的洞）
	err := applyWithTime(t, svc, teamID, mismatchedTime)
	var fe *model.FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("用时与申请单不一致时应返回字段错误，实际 %v", err)
	}
	if fe.Field != "time" {
		t.Errorf("错误应挂在 time 字段，实际 %q", fe.Field)
	}
	if !strings.Contains(err.Error(), "01:20") || !strings.Contains(err.Error(), "01:00") {
		t.Errorf("报错应同时给出申请单声明值与本次提交值（01:20 / 01:00），实际: %v", err)
	}

	// 被挡下 = 成绩没动 + 申请单仍在待审批（不能"拦了写"）
	rec, err := svc.GetScore(ctx, teamID, 1)
	if err != nil {
		t.Fatalf("读成绩失败: %v", err)
	}
	if !almostEqual(rec.DurationSec, baseTimeSec) {
		t.Errorf("被拦下时成绩用时不应变化，应为 %.0f，实际 %.0f", baseTimeSec, rec.DurationSec)
	}
	if _, err := svc.PendingChangeOf(ctx, teamID, 1); err != nil {
		t.Errorf("被拦下时申请单应仍是待审批，实际读不到: %v", err)
	}

	// 按申请单声明的 80 秒落库 → 放行，且榜单取优用时随之变化
	if err := applyWithTime(t, svc, teamID, requestedTime); err != nil {
		t.Fatalf("按申请单声明值落库应放行，实际 %v", err)
	}
	rec, err = svc.GetScore(ctx, teamID, 1)
	if err != nil {
		t.Fatalf("读成绩失败: %v", err)
	}
	if !almostEqual(rec.DurationSec, requestedTime) {
		t.Errorf("落库后用时应为 %.0f 秒，实际 %.0f", requestedTime, rec.DurationSec)
	}

	res, err := svc.Standings(ctx, "brain_planet", service.StandingsOptions{})
	if err != nil {
		t.Fatalf("算榜单失败: %v", err)
	}
	var gotTime float64 = -1
	for _, g := range res.Groups {
		for _, row := range g.Rows {
			if row.Team.ID == teamID {
				gotTime = row.Duration
			}
		}
	}
	if gotTime < 0 || !almostEqual(gotTime, requestedTime) {
		t.Errorf("榜单取优用时应为 %.0f 秒（改时后），实际 %.0f", requestedTime, gotTime)
	}
}

// TestChangeRequestWithoutTimeKeepsOldBehaviour 向后兼容：
// 不声明用时的申请（既有前端行为）授权时用时随便给，不该被新闸拦住。
func TestChangeRequestWithoutTimeKeepsOldBehaviour(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("裁判A")
	teamID := seedTimeChangeFixture(t, svc, ctx)

	req, err := svc.ScoreChangeRequest(ctx, teamID, 1, 90.0, nil, "只改分不改时：搭建任务漏记 1 个构件")
	if err != nil {
		t.Fatalf("发起不带用时的申请失败: %v", err)
	}
	if req.ChangesTime() {
		t.Errorf("未声明 afterTime 时 ChangesTime 应为 false")
	}
	if req.BeforeTime != nil || req.AfterTime != nil {
		t.Errorf("未声明用时时两列都应为 nil，实际 %v / %v", req.BeforeTime, req.AfterTime)
	}
	if err := applyWithTime(t, svc, teamID, mismatchedTime); err != nil {
		t.Errorf("未声明用时的申请不应被用时闸拦住，实际 %v", err)
	}
}

// TestChangeRequestZeroTimeIsNotAbsent 0 秒是合法用时：
// 用指针而非 float64 的意义就在这条 —— 若把 0 当"没传"，
// "整体用时记为 0（未启动 / 弃权）"这条改法就会静默变成只改分。
func TestChangeRequestZeroTimeIsNotAbsent(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("裁判A")
	teamID := seedTimeChangeFixture(t, svc, ctx)

	req, err := svc.ScoreChangeRequest(ctx, teamID, 1, 0, fptr(0), "整场未启动，用时计 0")
	if err != nil {
		t.Fatalf("发起 0 秒用时申请失败: %v", err)
	}
	if !req.ChangesTime() || req.AfterTime == nil || !almostEqual(*req.AfterTime, 0) {
		t.Fatalf("0 秒应被当成「改了用时」，实际 ChangesTime=%v after_time=%v", req.ChangesTime(), req.AfterTime)
	}
	back, err := svc.PendingChangeOf(ctx, teamID, 1)
	if err != nil || back.AfterTime == nil || !almostEqual(*back.AfterTime, 0) {
		t.Fatalf("0 秒应原样落库（不是 NULL），实际 %v（err=%v）", back.AfterTime, err)
	}

	// 0 秒的申请同样受一致性闸约束
	if err := applyWithTime(t, svc, teamID, mismatchedTime); err == nil {
		t.Errorf("申请单声明 0 秒时，提交 60 秒应被拦住")
	}
	if err := applyWithTime(t, svc, teamID, 0); err != nil {
		t.Errorf("按申请单声明落 0 秒应放行，实际 %v", err)
	}
}

func TestChangeRequestRejectsNegativeTime(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("裁判A")
	teamID := seedTimeChangeFixture(t, svc, ctx)

	_, err := svc.ScoreChangeRequest(ctx, teamID, 1, 90.0, fptr(-5), "负用时")
	var fe *model.FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("负用时应被字段校验拦下，实际 %v", err)
	}
	if fe.Field != "afterTime" {
		t.Errorf("错误应挂在 afterTime 字段，实际 %q", fe.Field)
	}
}
