package service_test

import (
	"errors"
	"fmt"
	"reflect"
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
	if err := svc.DecideDispute(decideCtx, d.ID, model.VerdictDisqualify, "确认重复提交，取消资格", 0, 0); err != nil {
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
	if err := svc.DecideDispute(operatorCtx("裁判长C"), d.ID, model.VerdictUphold, "试着裁定已撤回的工单", 0, 0); !errors.Is(err, service.ErrDisputeNotPending) {
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
	if err := svc.DecideDispute(decideCtx, d.ID, model.VerdictUphold, "证据不足，维持原判", 0, 0); err != nil {
		t.Fatalf("首次裁定失败: %v", err)
	}
	if err := svc.DecideDispute(decideCtx, d.ID, model.VerdictDisqualify, "新证据出现，改判取消资格", 0, 0); err != nil {
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
	if err := svc.DecideDispute(operatorCtx("裁判长C"), d.ID, model.VerdictAdjust, "短", 0, 0); !errors.Is(err, service.ErrReasonRequired) {
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

// TestDisputeAdjustGeneratesChangeRequest P8b 核心：授权改分裁定后，
// 生成一张待审批改分单（P9 路径）并把申述书照片继承过去（两处都挂）。
func TestDisputeAdjustGeneratesChangeRequest(t *testing.T) {
	svc, _ := newSvc(t)
	teamID := newDisputeFixture(t, svc, "9007", "改分队")
	ctx := operatorCtx("裁判A")
	decideCtx := operatorCtx("裁判长C")

	// 先录一份第 1 轮成绩（Before 取这一份）
	if _, err := svc.SaveScore(ctx, &model.ScoreRecord{
		TeamID: teamID, RoundNo: 1, DurationSec: 100, Signed: true,
		TaskValues: map[string]any{"focus": 80.0, "build": 82.0},
	}); err != nil {
		t.Fatalf("录入成绩失败: %v", err)
	}

	// 上报争议
	d, err := svc.ReportDispute(ctx, teamID, 1, model.DisputeDuplicate, "同一轮出现两份成绩")
	if err != nil {
		t.Fatalf("上报争议失败: %v", err)
	}

	// 挂一张申述书照片（选手手写·裁判拍照）
	appeal, err := svc.RecordEvidence(ctx, &model.Evidence{
		TeamID: teamID, RoundNo: intPtr(1),
		Kind:       model.EvAppeal,
		Source:     model.SrcRefereeAppeal,
		FileName:   "appeal_T9007_R1.jpg",
		Status:     model.EvLocal,
		Operator:   "裁判A",
		DisputeID:  &d.ID,
		StorageURL: "appeal_local.jpg",
	})
	if err != nil {
		t.Fatalf("登记申述书失败: %v", err)
	}

	// 授权改分：采纳第 1 轮，目标分数 120.5
	if err := svc.DecideDispute(decideCtx, d.ID, model.VerdictAdjust,
		"复核后应以裁判长认定的成绩为准", 1, 120.5); err != nil {
		t.Fatalf("授权改分裁定失败: %v", err)
	}

	// 待审批队列里应出现这张改分单
	cr, err := svc.PendingChangeOf(ctx, teamID, 1)
	if err != nil {
		t.Fatalf("应生成待审批改分单: %v", err)
	}
	if cr.Approved {
		t.Fatal("改分单应为待审批（Approved=false），待 P9 授权落库")
	}
	if cr.After != 120.5 {
		t.Fatalf("改分单目标分数应为 120.5，实际 %.1f", cr.After)
	}
	if cr.Operator != "裁判长C" {
		t.Fatalf("改分单申请人应为裁定人裁判长C，实际 %q", cr.Operator)
	}
	if cr.TeamID != teamID || cr.RoundNo != 1 {
		t.Fatalf("改分单队伍/轮次不符：team=%d round=%d", cr.TeamID, cr.RoundNo)
	}
	if cr.AppealEvidenceID == nil {
		t.Fatal("改分单应挂上申述书照片（两处都挂），实际为空")
	}
	if *cr.AppealEvidenceID != appeal.ID {
		t.Fatalf("改分单挂的申述书 ID 应为 %d，实际 %d", appeal.ID, *cr.AppealEvidenceID)
	}

	// 裁定结论本身也应为 adjust
	got, err := svc.Dispute(ctx, d.ID)
	if err != nil {
		t.Fatalf("查工单失败: %v", err)
	}
	if got.Verdict == nil || *got.Verdict != model.VerdictAdjust {
		t.Fatalf("裁定结论应为 adjust，实际 %v", got.Verdict)
	}
}

// TestDisputeAdjustWithoutScoreRollback 采纳轮次尚无成绩时裁定整体回滚，
// 不留下空改分单，工单仍保持待裁定。
func TestDisputeAdjustWithoutScoreRollback(t *testing.T) {
	svc, _ := newSvc(t)
	teamID := newDisputeFixture(t, svc, "9008", "空分改分队")
	ctx := operatorCtx("裁判A")
	decideCtx := operatorCtx("裁判长C")

	d, err := svc.ReportDispute(ctx, teamID, 1, model.DisputeDuplicate, "第 2 轮分数有争议")
	if err != nil {
		t.Fatalf("上报争议失败: %v", err)
	}

	// 第 2 轮从未录入成绩，却要采纳第 2 轮 → 应失败
	err = svc.DecideDispute(decideCtx, d.ID, model.VerdictAdjust,
		"想改第 2 轮但第 2 轮没成绩", 2, 100.0)
	if err == nil {
		t.Fatal("采纳轮次无成绩时应裁定失败")
	}

	// 不应留下任何待审批改分单
	if _, rerr := svc.PendingChangeOf(ctx, teamID, 2); !errors.Is(rerr, store.ErrNotFound) {
		t.Fatalf("不应生成改分单，实际 err=%v", rerr)
	}
	// 工单本身仍应是待裁定（裁定回滚，未落结论）
	got, err := svc.Dispute(ctx, d.ID)
	if err != nil {
		t.Fatalf("查工单失败: %v", err)
	}
	if got.Status != model.DisputePending {
		t.Fatalf("裁定回滚后工单应仍待裁定，实际 %q", got.Status)
	}
}

// TestDisputeUpholdDisqualifyNoChangeRequest 维持原判 / 取消资格不产生改分单。
func TestDisputeUpholdDisqualifyNoChangeRequest(t *testing.T) {
	svc, _ := newSvc(t)
	teamID := newDisputeFixture(t, svc, "9009", "非改分队")
	ctx := operatorCtx("裁判A")

	if _, err := svc.SaveScore(ctx, &model.ScoreRecord{
		TeamID: teamID, RoundNo: 1, DurationSec: 100, Signed: true,
		TaskValues: map[string]any{"focus": 80.0, "build": 82.0},
	}); err != nil {
		t.Fatalf("录入成绩失败: %v", err)
	}

	d, err := svc.ReportDispute(ctx, teamID, 1, model.DisputeDuplicate, "证据不足")
	if err != nil {
		t.Fatalf("上报争议失败: %v", err)
	}

	if err := svc.DecideDispute(operatorCtx("裁判长C"), d.ID, model.VerdictUphold,
		"证据不足，维持原判", 0, 0); err != nil {
		t.Fatalf("维持原判裁定失败: %v", err)
	}
	if _, rerr := svc.PendingChangeOf(ctx, teamID, 1); !errors.Is(rerr, store.ErrNotFound) {
		t.Fatalf("维持原判不应生成改分单，实际 err=%v", rerr)
	}
}

// dqRowsOf 取某组别的榜单行（榜单按组别分组返回）。
func dqRowsOf(t *testing.T, res *service.StandingsResult, group string) []model.StandingRow {
	t.Helper()
	for _, g := range res.Groups {
		if g.Group == group {
			return g.Rows
		}
	}
	t.Fatalf("榜单里没有组别 %q", group)
	return nil
}

func dqNos(rows []model.StandingRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Team.TeamNo)
	}
	return out
}

// dqRanks 取榜单行的名次序列（用于断言"留空 / 连续"两档口径）。
func dqRanks(rows []model.StandingRow) []int {
	out := make([]int, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Rank)
	}
	return out
}

// TestStandingsExcludeDisqualifiedTeam 裁定「取消资格」→ 该队整行不进榜单（成绩作废），改判后自动恢复。
//
// 补的是后端此前的断点：DecideDispute 只把结论写进工单，榜单根本不读争议 →
// 「裁定完取消资格，榜单纹丝不动」，作废了却无人认领（wireframe P8c note 7 点的正是这个风险）。
// 本用例同时钉住三件事：① 整行剔除且名次连续；② 榜单能解释「为什么少了一队」（voidedTeamIds）；
// ③ 改判后自动恢复 —— 判据派生自工单，不需要任何回滚维护。
func TestStandingsExcludeDisqualifiedTeam(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")

	ev, err := svc.CreateEvent(ctx, brainPlanetEvent())
	if err != nil {
		t.Fatalf("建赛项失败: %v", err)
	}

	teams := map[string]int64{}
	for _, spec := range []struct {
		no, name string
		task     float64
		dur      float64
	}{
		{"9101", "甲队", 90, 100},
		{"9102", "乙队", 80, 90},
		{"9103", "丙队", 70, 80},
	} {
		team, terr := svc.CreateTeam(ctx, model.TeamDraft{
			EventID: ev.ID, TeamNo: spec.no, Name: spec.name, GroupCode: "小学组",
		})
		if terr != nil {
			t.Fatalf("建队伍失败: %v", terr)
		}
		teams[spec.no] = team.ID
		if _, serr := svc.SaveScore(ctx, &model.ScoreRecord{
			TeamID: team.ID, RoundNo: 1, DurationSec: spec.dur, Signed: true,
			TaskValues: map[string]any{"focus": spec.task, "build": spec.task},
		}); serr != nil {
			t.Fatalf("录成绩失败: %v", serr)
		}
	}

	// 乙队被裁定「取消资格」
	d, err := svc.ReportDispute(ctx, teams["9102"], 1, model.DisputeDuplicate, "同队同轮出现两份成绩")
	if err != nil {
		t.Fatalf("上报争议失败: %v", err)
	}
	if err := svc.DecideDispute(operatorCtx("裁判长C"), d.ID, model.VerdictDisqualify,
		"确认重复提交，取消资格", 0, 0); err != nil {
		t.Fatalf("裁定取消资格失败: %v", err)
	}

	res, err := svc.Standings(ctx, ev.ID, service.StandingsOptions{})
	if err != nil {
		t.Fatalf("取榜单失败: %v", err)
	}
	rows := dqRowsOf(t, res, "小学组")
	if got := dqNos(rows); !reflect.DeepEqual(got, []string{"9101", "9103"}) {
		t.Fatalf("被裁定作废的乙队应整行不进榜单，实际 %v", got)
	}
	// 名次：口径由赛事级「递补规则」决定（默认 = 不递补 → 作废队的位置留空）。
	//
	// 两档都要断言。这个用例原先写死「应连续发放（作废不留空洞）」，
	// 把当时唯一的实现当成了规格 —— 0019 补上另一档之后它就假红了。
	// 名次编号本身不是本用例的主题（主题是"作废队整行不进榜单"），
	// 但那句话若只写一档，就分不清"规则接对了"与"规则根本没接"。
	if got := dqRanks(rows); !reflect.DeepEqual(got, []int{1, 3}) {
		t.Errorf("默认「不递补」时名次应为 1 / 3（第 2 名空缺 = 被作废的乙队），实际 %v", got)
	}
	if _, err := svc.SetSubstituteMode(ctx, model.SubstituteRank, "用例：切到按名次顺延"); err != nil {
		t.Fatalf("切换递补规则失败: %v", err)
	}
	resRank, err := svc.Standings(ctx, ev.ID, service.StandingsOptions{})
	if err != nil {
		t.Fatalf("取榜单失败: %v", err)
	}
	if got := dqRanks(dqRowsOf(t, resRank, "小学组")); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Errorf("按名次顺延时应连续发放 1 / 2，实际 %v", got)
	}
	// 复位（后续断言按默认口径读榜）
	if _, err := svc.SetSubstituteMode(ctx, model.SubstituteNone, "用例：复位"); err != nil {
		t.Fatalf("复位递补规则失败: %v", err)
	}
	if len(res.VoidedTeamIDs) != 1 || res.VoidedTeamIDs[0] != teams["9102"] {
		t.Errorf("榜单要能解释「为什么少了一队」，voidedTeamIds = %v", res.VoidedTeamIDs)
	}

	// 改判维持原判 → 作废自动撤销
	if err := svc.DecideDispute(operatorCtx("裁判长C"), d.ID, model.VerdictUphold,
		"复核后确认是计时器重复触发，撤销作废", 0, 0); err != nil {
		t.Fatalf("改判维持原判失败: %v", err)
	}
	res2, err := svc.Standings(ctx, ev.ID, service.StandingsOptions{})
	if err != nil {
		t.Fatalf("取榜单失败: %v", err)
	}
	if got := dqNos(dqRowsOf(t, res2, "小学组")); !reflect.DeepEqual(got, []string{"9101", "9102", "9103"}) {
		t.Fatalf("改判后乙队应回到榜单（按总分降序：90/80/70），实际 %v", got)
	}
	if len(res2.VoidedTeamIDs) != 0 {
		t.Errorf("改判后不应再有作废队伍，实际 %v", res2.VoidedTeamIDs)
	}
}
