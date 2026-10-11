package service_test

// ============================================================================
// 图集 13：后台手工登记取消资格（RegisterDisqualification）
// 图集 12：聚合告警（Alerts，只读派生）
//
// 关键口径：
//   - 登记取消资格 = 同一事务内「代建争议单 + 裁定 disqualify」—— 唯一事实源
//     仍是争议工单；榜单即时剔除、已发布单元置「待重发」、两条审计留痕
//   - 弃赛队 / 已作废队不可登记；重复登记拦截
//   - 告警与台账是同一份数据的两种投影：待审批 / 待裁定 / 超时（注入时钟）/
//     自批（申请人=审批人）/ 取消资格
// ============================================================================

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/service"
)

// mustScoreFor 给队伍录一条已签字的完整成绩（供改分单用）。
func mustScoreFor(t *testing.T, svc *service.Service, ctx context.Context, teamID int64) {
	t.Helper()
	if _, err := svc.SaveScore(ctx, &model.ScoreRecord{
		TeamID: teamID, RoundNo: 1, DurationSec: 100, Signed: true,
		TaskValues: map[string]any{"focus": 80.0, "build": 82.0},
	}); err != nil {
		t.Fatalf("录成绩失败: %v", err)
	}
}

func findAlert(rep *service.AlertsReport, typ string) *service.Alert {
	for i := range rep.Alerts {
		if rep.Alerts[i].Type == typ {
			return &rep.Alerts[i]
		}
	}
	return nil
}

func countAlert(rep *service.AlertsReport, typ string) int {
	n := 0
	for i := range rep.Alerts {
		if rep.Alerts[i].Type == typ {
			n++
		}
	}
	return n
}

// TestRegisterDisqualificationAndStandings 主链路：登记 → 剔除榜单 → 重发联动 → 告警可见。
func TestRegisterDisqualificationAndStandings(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")

	ev := mustEvent(t, svc, brainPlanetEvent())
	seat := mustSeat(t, svc, "1 号台", 1)
	teamA := mustTeam(t, svc, ev.ID, "1001", "作废队", "小学组")
	mustTeam(t, svc, ev.ID, "1002", "正常队", "小学组")

	// 发布链走满：ensure → 移交 → 接收 → 发布
	if _, err := svc.EnsureReleaseUnit(ctx, ev.ID, "小学组", &seat.ID); err != nil {
		t.Fatalf("建发布单元失败: %v", err)
	}
	if _, err := svc.HandOverRelease(ctx, ev.ID, "小学组", &seat.ID); err != nil {
		t.Fatalf("移交失败: %v", err)
	}
	units, _ := svc.ReleaseUnits(ctx)
	if _, err := svc.ReceiveRelease(ctx, units[0].ID); err != nil {
		t.Fatalf("接收失败: %v", err)
	}
	if _, err := svc.PublishRelease(ctx, units[0].ID); err != nil {
		t.Fatalf("发布失败: %v", err)
	}

	// 登记取消资格
	d, err := svc.RegisterDisqualification(ctx, teamA.ID,
		"同队同轮重复提交，经裁判长纸质裁定取消资格", "纸质件 2 页 · 存档 A-07")
	if err != nil {
		t.Fatalf("登记取消资格失败: %v", err)
	}
	if d.Status != model.DisputeDecided || d.Verdict == nil || *d.Verdict != model.VerdictDisqualify {
		t.Errorf("登记应直接产生已裁定的取消资格单: %+v", d)
	}
	if d.Code == "" || d.Decider != "运营A" {
		t.Errorf("单号 / 裁定人应回填: code=%q decider=%q", d.Code, d.Decider)
	}
	if !strings.Contains(d.DecisionReason, "存档 A-07") {
		t.Errorf("留底证据应并入裁定原因: %q", d.DecisionReason)
	}

	// 榜单即时剔除（void 判据 = 争议单 disqualify，唯一事实源）
	res, err := svc.Standings(ctx, ev.ID, service.StandingsOptions{})
	if err != nil {
		t.Fatalf("算榜单失败: %v", err)
	}
	board := boardNos(res)
	if strings.Contains(board, "1001") {
		t.Errorf("作废队 1001 应被剔除，榜单=%s", board)
	}
	if !strings.Contains(board, "1002") {
		t.Errorf("正常队 1002 应在榜，榜单=%s", board)
	}

	// 重发联动：已发布单元 → pending（保留发布人）
	units, _ = svc.ReleaseUnits(ctx)
	if units[0].Status != model.ReleasePending {
		t.Errorf("已发布单元应置「待重发」，实为 %s", units[0].Status)
	}

	// 重复登记拦截
	if _, err := svc.RegisterDisqualification(ctx, teamA.ID, "再登记一次", ""); err == nil {
		t.Errorf("重复登记应被拦下")
	} else if !strings.Contains(err.Error(), "不可重复登记") {
		t.Errorf("报错应说明不可重复登记: %v", err)
	}

	// 聚合告警里可见取消资格（最高危档）
	rep, err := svc.Alerts(ctx)
	if err != nil {
		t.Fatalf("读告警失败: %v", err)
	}
	a := findAlert(rep, "取消资格")
	if a == nil || a.Level != "danger" || !strings.Contains(a.Target, "1001") {
		t.Fatalf("应有 danger 档「取消资格」告警: %+v", rep.Alerts)
	}
	if rep.Summary["取消资格"] != 1 {
		t.Errorf("summary 计数应为 1: %+v", rep.Summary)
	}
}

// TestRegisterDisqualificationRejectsWithdrawn 弃赛队不可登记（两条链不能混）。
func TestRegisterDisqualificationRejectsWithdrawn(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")
	ev := mustEvent(t, svc, brainPlanetEvent())
	team := mustTeam(t, svc, ev.ID, "1001", "弃赛队", "小学组")
	if _, err := svc.WithdrawTeam(ctx, team.ID, "主动退赛"); err != nil {
		t.Fatalf("弃赛失败: %v", err)
	}
	if _, err := svc.RegisterDisqualification(ctx, team.ID, "理由充分", ""); err == nil {
		t.Errorf("弃赛队登记取消资格应被拦下")
	} else if !strings.Contains(err.Error(), "弃赛") {
		t.Errorf("报错应说明两条链不能混: %v", err)
	}
}

// TestAlertsAggregation 四类告警的派生口径（注入时钟验证超时）。
func TestAlertsAggregation(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")

	ev := mustEvent(t, svc, brainPlanetEvent())
	teamA := mustTeam(t, svc, ev.ID, "1001", "改分队", "小学组")
	teamB := mustTeam(t, svc, ev.ID, "1002", "自批队", "小学组")

	// ① 待审批改分 → 改分待审批
	mustScoreFor(t, svc, ctx, teamA.ID)
	if _, err := svc.ScoreChangeRequest(ctx, teamA.ID, 1, 95.0, "申诉复核"); err != nil {
		t.Fatalf("发起改分失败: %v", err)
	}
	rep, err := svc.Alerts(ctx)
	if err != nil {
		t.Fatalf("读告警失败: %v", err)
	}
	if findAlert(rep, "改分待审批") == nil {
		t.Fatalf("应有「改分待审批」告警: %+v", rep.Alerts)
	}

	// ② 待裁定争议 → 争议待裁定
	if _, err := svc.ReportDispute(ctx, teamA.ID, 1, model.DisputeOther, "用时记录有误"); err != nil {
		t.Fatalf("上报争议失败: %v", err)
	}
	rep, _ = svc.Alerts(ctx)
	if findAlert(rep, "争议待裁定") == nil {
		t.Fatalf("应有「争议待裁定」告警: %+v", rep.Alerts)
	}

	// ③ 超时催办：注入 31 分钟后的时钟
	future := time.Now().Add(31 * time.Minute)
	rep, err = svc.AlertsAt(ctx, future)
	if err != nil {
		t.Fatalf("读告警失败: %v", err)
	}
	if countAlert(rep, "超时催办") < 2 {
		t.Errorf("待审批 + 待裁定都应转超时催办（≥2 条）: %+v", rep.Alerts)
	}
	if findAlert(rep, "改分待审批") != nil || findAlert(rep, "争议待裁定") != nil {
		t.Errorf("超时后不应再以待办形态出现")
	}

	// ④ 自批：申请人与审批人同一账号（可自批但必须告警可见）
	mustScoreFor(t, svc, ctx, teamB.ID)
	if _, err := svc.ScoreChangeRequest(ctx, teamB.ID, 1, 90.0, "自批验证"); err != nil {
		t.Fatalf("发起改分失败: %v", err)
	}
	rec, err := svc.GetScore(ctx, teamB.ID, 1)
	if err != nil {
		t.Fatalf("读成绩失败: %v", err)
	}
	rec.TaskValues = map[string]any{"focus": 90.0, "build": 92.0}
	if _, err := svc.ApplyScoreChange(ctx, rec, "运营A", "自批授权"); err != nil {
		t.Fatalf("授权改分失败: %v", err)
	}
	rep, _ = svc.Alerts(ctx)
	a := findAlert(rep, "自批告警")
	if a == nil || a.Level != "danger" {
		t.Fatalf("应有 danger 档「自批告警」: %+v", rep.Alerts)
	}

	// ⑤ 取消资格告警（走 P8c 裁定链的也应出现）
	if _, err := svc.ReportDispute(ctx, teamB.ID, 2, model.DisputeOther, "红牌争议"); err != nil {
		t.Fatalf("上报争议失败: %v", err)
	}
	dqs, err := svc.TeamDisputes(ctx, teamB.ID)
	if err != nil {
		t.Fatalf("读工单失败: %v", err)
	}
	var dID int64
	for _, d := range dqs {
		if d.IsOpen() && d.RoundNo == 2 {
			dID = d.ID
		}
	}
	if dID == 0 {
		t.Fatal("应有第 2 轮待裁定工单")
	}
	if err := svc.DecideDispute(ctx, dID, model.VerdictDisqualify, "顶撞裁判，经证据判定取消资格", 0, 0); err != nil {
		t.Fatalf("裁定失败: %v", err)
	}
	rep, _ = svc.Alerts(ctx)
	if findAlert(rep, "取消资格") == nil {
		t.Fatalf("裁定链的取消资格也应在告警中: %+v", rep.Alerts)
	}
	if rep.Summary["取消资格"] != 1 {
		t.Errorf("取消资格计数应为 1: %+v", rep.Summary)
	}
}

// TestAlertsContestScoped 告警按赛事分区：B 场的工单不进默认赛事的收件箱。
func TestAlertsContestScoped(t *testing.T) {
	svc, db := newSvc(t)
	ctx := operatorCtx("运营A")
	if err := db.Repos().Contests.Create(ctx, &model.Contest{
		ID: "ct_alert_b", Name: "告警 B 场", Status: model.ContestLive,
	}); err != nil {
		t.Fatalf("建赛事失败: %v", err)
	}
	ctxA := service.WithContest(ctx, "ct_alert_b")
	evA := mustEventAt(t, svc, ctxA, brainPlanetEvent())
	teamA := mustTeamAt(t, svc, ctxA, evA.ID, "2001", "B场队", "小学组")
	mustScoreFor(t, svc, ctxA, teamA.ID)
	if _, err := svc.ScoreChangeRequest(ctxA, teamA.ID, 1, 95.0, "B 场申诉"); err != nil {
		t.Fatalf("B 场发起改分失败: %v", err)
	}

	repDefault, err := svc.Alerts(ctx)
	if err != nil {
		t.Fatalf("读默认赛事告警失败: %v", err)
	}
	if countAlert(repDefault, "改分待审批") != 0 {
		t.Errorf("默认赛事不应看到 B 场的告警: %+v", repDefault.Alerts)
	}
	repA, err := svc.Alerts(ctxA)
	if err != nil {
		t.Fatalf("读 B 场告警失败: %v", err)
	}
	if countAlert(repA, "改分待审批") != 1 {
		t.Errorf("B 场应看到自己的 1 条待审批: %+v", repA.Alerts)
	}
}

// ---------- 小工具 ----------

func boardNos(res *service.StandingsResult) string {
	nos := make([]string, 0)
	for _, g := range res.Groups {
		for _, row := range g.Rows {
			nos = append(nos, row.Team.TeamNo)
		}
	}
	return strings.Join(nos, ",")
}

// mustEventAt / mustTeamAt 带赛事上下文的建单工具（多赛事用例用）。
func mustEventAt(t *testing.T, svc *service.Service, ctx context.Context, ev *model.Event) *model.Event {
	t.Helper()
	created, err := svc.CreateEvent(ctx, ev)
	if err != nil {
		t.Fatalf("建赛项失败: %v", err)
	}
	return created
}

func mustTeamAt(t *testing.T, svc *service.Service, ctx context.Context, eventID, no, name, group string) *model.Team {
	t.Helper()
	created, err := svc.CreateTeam(ctx, model.TeamDraft{
		EventID: eventID, TeamNo: no, Name: name, GroupCode: group,
	})
	if err != nil {
		t.Fatalf("建队伍 %s 失败: %v", no, err)
	}
	return created
}
