package service_test

import (
	"context"
	"errors"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jialangli/comptition-score-server/internal/config"
	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/service"
	"github.com/jialangli/comptition-score-server/internal/store"
	"github.com/jialangli/comptition-score-server/internal/store/postgres"
)

// ============================================================================
// 集成测试（需要真实 PostgreSQL）
//
// 前置：bash scripts/test_db.sh   （重建并迁移 neuroscore_test）
// 连接串：环境变量 TEST_DATABASE_URL，未设置时用默认值。
//
// 连不上测试库时**跳过**而不是失败：`go test ./...` 在没有 PG 的机器上
// 不应该红。但一旦跑起来，就是打到真库上的真实验证 —— 不做任何 mock。
// ============================================================================

const defaultTestDSN = "postgres://postgres@127.0.0.1:5432/neuroscore_test?sslmode=disable"

func openTestDB(t *testing.T) *postgres.DB {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = defaultTestDSN
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	db, err := postgres.Open(ctx, &config.Config{DatabaseURL: dsn, MaxOpenConns: 8})
	if err != nil {
		t.Skipf("跳过集成测试：连不上测试库\n  原因：%v\n  修复：bash scripts/test_db.sh（或设置 TEST_DATABASE_URL）", err)
	}
	t.Cleanup(db.Close)
	return db
}

// newSvc 返回一个已清空数据的 Service —— 每个用例都从干净状态开始。
func newSvc(t *testing.T) (*service.Service, *postgres.DB) {
	t.Helper()
	db := openTestDB(t)
	if err := db.TruncateAll(context.Background()); err != nil {
		t.Fatalf("清空测试库失败: %v", err)
	}
	return service.New(db), db
}

// operatorCtx 模拟 api 层中间件写入当前操作人（本期唯一的鉴权插槽）。
func operatorCtx(name string) context.Context {
	return service.WithUser(context.Background(), name)
}

func fptr(f float64) *float64 { return &f }

func almostEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// brainPlanetEvent 一份最小可用的脑机星球配置。
func brainPlanetEvent() *model.Event {
	return &model.Event{
		ID:     "brain_planet",
		Name:   "脑机星球",
		Groups: []string{"小学组", "初中组"},
		Tasks: []model.Task{
			{ID: "focus", Name: "专注力任务", Type: model.TaskNumeric, MaxScore: fptr(100), Weight: 0.5, Control: model.CtrlSlider},
			{ID: "build", Name: "搭建任务", Type: model.TaskNumeric, MaxScore: fptr(100), Weight: 0.5, Control: model.CtrlSlider},
		},
		ScoreRule:   model.ScoreRule{Template: model.TplWeightedSum},
		BonusRules:  []model.BonusRule{{Template: model.BonusTime, Params: map[string]any{"perSecond": 0.5, "cap": 10.0}}},
		PenaltyRule: model.PenaltyRule{Template: model.PenaltyPerCard, Params: map[string]any{"yellow": 5.0, "red": 15.0}},
		RankRule: model.RankRule{
			TieBreak:   []string{"score", "time"},
			AwardTiers: map[string]float64{"一等奖": 0.1, "二等奖": 0.2, "三等奖": 0.3},
		},
	}
}

// ---------------------------------------------------------------------------

// TestEndToEndMainFlow 主链路：建赛项 → 改配置留档 → 导队伍 → 录成绩 → 出榜单，
// 最后核对六类必留痕操作是否齐全。
func TestEndToEndMainFlow(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")

	// —— ① 建赛项：赛项 + 任务项 + 基线快照 + 审计 ——
	ev, err := svc.CreateEvent(ctx, brainPlanetEvent())
	if err != nil {
		t.Fatalf("建赛项失败: %v", err)
	}
	if len(ev.Tasks) != 2 {
		t.Fatalf("任务项应写入 2 条，实际 %d 条", len(ev.Tasks))
	}
	if ev.Tasks[0].SortOrder != 0 || ev.Tasks[1].SortOrder != 1 {
		t.Errorf("任务排序未按下标落库: %+v", ev.Tasks)
	}
	// NUMERIC 列的往返：max_score 存 NUMERIC，读回来必须是确定的 float64
	if ev.Tasks[0].MaxScore == nil || *ev.Tasks[0].MaxScore != 100 {
		t.Errorf("maxScore 未正确往返: %+v", ev.Tasks[0].MaxScore)
	}

	// —— ② 改配置：必须留痕（含字段级差异）+ 自动快照旧配置 ——
	updated := *ev
	updated.Tasks = []model.Task{
		{ID: "focus", Name: "专注力任务", Type: model.TaskNumeric, MaxScore: fptr(100), Weight: 0.6, Control: model.CtrlSlider},
		{ID: "build", Name: "搭建任务", Type: model.TaskNumeric, MaxScore: fptr(100), Weight: 0.4, Control: model.CtrlSlider},
		{ID: "teamwork", Name: "协作任务", Type: model.TaskNumeric, MaxScore: fptr(100), Weight: 0, Control: model.CtrlSlider},
	}
	if _, err := svc.UpdateEvent(ctx, &updated, "赛前规则复核"); err != nil {
		t.Fatalf("改配置失败: %v", err)
	}
	reloaded, err := svc.GetEvent(ctx, ev.ID)
	if err != nil {
		t.Fatalf("回读赛项失败: %v", err)
	}
	if len(reloaded.Tasks) != 3 {
		t.Fatalf("任务项应被整体替换为 3 条，实际 %d 条", len(reloaded.Tasks))
	}

	logs, err := svc.AuditLogs(ctx, store.AuditFilter{Action: string(model.ActConfig), Limit: 50})
	if err != nil {
		t.Fatalf("查审计失败: %v", err)
	}
	var configLog *model.AuditLog
	for i := range logs {
		if logs[i].Action == model.ActConfig && logs[i].Reason == "赛前规则复核" {
			configLog = &logs[i]
		}
	}
	if configLog == nil {
		t.Fatal("改配置没有留下审计记录")
	}
	if configLog.Operator != "运营A" {
		t.Errorf("审计操作人 = %q，期望 运营A（应从 context 取）", configLog.Operator)
	}
	if !strings.Contains(configLog.Before, "2 项") || !strings.Contains(configLog.After, "3 项") {
		t.Errorf("审计应记录任务项数量变化：before=%q after=%q", configLog.Before, configLog.After)
	}
	if !strings.Contains(configLog.After, "协作任务") {
		t.Errorf("审计应列出新增的任务项：after=%q", configLog.After)
	}

	snaps, err := svc.ListConfigSnapshots(ctx, 10)
	if err != nil {
		t.Fatalf("查快照失败: %v", err)
	}
	if len(snaps) != 2 {
		t.Errorf("配置快照应有 2 份（新建基线 + 改配置前留档），实际 %d 份", len(snaps))
	}

	// —— ③ 不合法的配置必须被拒绝，且不落库 ——
	bad := *reloaded
	bad.Tasks = append([]model.Task{}, reloaded.Tasks...)
	bad.Tasks[1].ID = "focus" // 任务 id 重复
	_, err = svc.UpdateEvent(ctx, &bad, "故意写坏")
	var ve *service.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("期望 ValidationError，实际 %v", err)
	}
	if len(ve.Result.Errors) == 0 {
		t.Error("ValidationError 应携带具体问题")
	}
	still, err := svc.GetEvent(ctx, ev.ID)
	if err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if len(still.Tasks) != 3 {
		t.Errorf("校验失败不应改动数据，任务项数 = %d", len(still.Tasks))
	}

	// —— ④ 报名导入：预览（四色）→ 勾选入库 ——
	rows := []service.ImportRow{
		{TeamNo: "1001", Name: "星河队", School: "杭州实验小学", Coach: "张老师", Group: "小学组", Members: "张一 / 李二", LineNo: 2},
		{TeamNo: "1002", Name: "追光队", School: "杭州第二实验小学", Coach: "孙老师", Group: "小学组", Members: "王三 / 赵四", LineNo: 3},
		{TeamNo: "1003", Name: "晨曦队", School: "", Coach: "李老师", Group: "初中组", Members: "孙五 / 周六", LineNo: 4},
		{TeamNo: "1003", Name: "晨曦队（重复行）", School: "", Coach: "李老师", Group: "初中组", LineNo: 5},
		{TeamNo: "1004", Name: "极客队", School: "杭州实验小学", Coach: "周老师", Group: "高中组", LineNo: 6},
	}
	preview, err := svc.PreviewImport(ctx, ev.ID, rows)
	if err != nil {
		t.Fatalf("导入预览失败: %v", err)
	}
	if preview.Summary.Insert != 3 || preview.Summary.Conflict != 2 {
		t.Fatalf("预览统计 = %+v，期望 新增3 / 冲突2", preview.Summary)
	}
	for _, r := range preview.Rows {
		if r.Status == service.ImportConflict && r.Selectable {
			t.Errorf("冲突行不应可勾选：%+v", r.Row)
		}
	}
	if preview.Rows[3].FirstSeenLine != 4 {
		t.Errorf("重复编号应指向首次出现的行号 4，实际 %d", preview.Rows[3].FirstSeenLine)
	}
	// 服务端重算：即使客户端把冲突行也勾上（第 6 行 = 组别不属于本赛项），也必须拒绝
	if _, err := svc.CommitImport(ctx, ev.ID, rows, []int{2, 6}, nil, "试图越权导入冲突行"); !errors.Is(err, service.ErrConflictRows) {
		t.Fatalf("勾选冲突行应返回 ErrConflictRows，实际 %v", err)
	}
	// 未勾选任何行 = 明确报错，而不是静默什么都不做
	if _, err := svc.CommitImport(ctx, ev.ID, rows, nil, nil, "什么都没选"); err == nil {
		t.Error("未勾选任何行时应返回错误")
	}

	// 只勾前三行（第 2/3/4 行），第 5 行的重复编号自然被排除
	importLog, err := svc.CommitImport(ctx, ev.ID, rows, []int{2, 3, 4}, nil, "首次导入")
	if err != nil {
		t.Fatalf("导入入库失败: %v", err)
	}
	if importLog.ID == 0 {
		t.Error("导入审计未回填 ID")
	}
	teams, err := svc.ListTeams(ctx, ev.ID, true)
	if err != nil {
		t.Fatalf("查队伍失败: %v", err)
	}
	if len(teams) != 3 {
		t.Fatalf("入库后应有 3 支队伍，实际 %d 支", len(teams))
	}

	// 二次导入：一支无变化 → skip，一支改了学校 → update（含字段级差异）
	rows2 := []service.ImportRow{
		{TeamNo: "1001", Name: "星河队", School: "杭州实验小学", Coach: "张老师", Group: "小学组", Members: "张一 / 李二", LineNo: 2},
		{TeamNo: "1002", Name: "追光队", School: "杭州第二实验小学（新校区）", Coach: "孙老师", Group: "小学组", Members: "王三 / 赵四", LineNo: 3},
	}
	preview2, err := svc.PreviewImport(ctx, ev.ID, rows2)
	if err != nil {
		t.Fatalf("二次预览失败: %v", err)
	}
	if preview2.Summary.Skip != 1 || preview2.Summary.Update != 1 {
		t.Fatalf("二次预览统计 = %+v，期望 无变化1 / 更新1", preview2.Summary)
	}
	if len(preview2.Rows[1].Changes) != 1 || preview2.Rows[1].Changes[0].Field != "school" {
		t.Errorf("更新行应给出字段级变化，实际 %+v", preview2.Rows[1].Changes)
	}
	if _, err := svc.CommitImport(ctx, ev.ID, rows2, []int{3}, nil, "学校更名"); err != nil {
		t.Fatalf("二次导入失败: %v", err)
	}
	t1002, err := svc.GetTeam(ctx, teams[1].ID)
	if err != nil {
		t.Fatalf("查队伍失败: %v", err)
	}
	if t1002.School != "杭州第二实验小学（新校区）" {
		t.Errorf("学校未更新：%q", t1002.School)
	}

	// 「一号一队」：手工再录一个同编号队伍必须被拦下
	if _, err := svc.CreateTeam(ctx, model.TeamDraft{
		EventID: ev.ID, TeamNo: "1001", Name: "重复编号队", GroupCode: "小学组",
	}); !errors.Is(err, store.ErrDuplicate) {
		t.Fatalf("重复编号应返回 ErrDuplicate，实际 %v", err)
	}

	// —— ⑤ 录成绩：两轮取优 ——
	byNo := map[string]model.Team{}
	for _, tm := range teams {
		byNo[tm.TeamNo] = tm
	}
	star := byNo["1001"]
	if _, err := svc.SaveScore(ctx, &model.ScoreRecord{
		TeamID: star.ID, RoundNo: 1, DurationSec: 100, Signed: true,
		TaskValues: map[string]any{"focus": 80.0, "build": 82.0, "teamwork": 60.0},
	}); err != nil {
		t.Fatalf("第一轮录分失败: %v", err)
	}
	if _, err := svc.SaveScore(ctx, &model.ScoreRecord{
		TeamID: star.ID, RoundNo: 2, DurationSec: 90, Signed: true,
		TaskValues: map[string]any{"focus": 95.0, "build": 92.0, "teamwork": 70.0},
	}); err != nil {
		t.Fatalf("第二轮录分失败: %v", err)
	}

	res, err := svc.Standings(ctx, ev.ID, service.StandingsOptions{})
	if err != nil {
		t.Fatalf("出榜单失败: %v", err)
	}
	if len(res.Groups) != 2 {
		t.Fatalf("应返回 2 个组别，实际 %d 个", len(res.Groups))
	}
	starRow := findRow(res, star.ID)
	if starRow == nil {
		t.Fatal("榜单里找不到星河队")
	}
	if starRow.BestRound != 2 {
		t.Errorf("应取优到第 2 轮，实际第 %d 轮", starRow.BestRound)
	}
	if len(starRow.Rounds) != 2 {
		t.Errorf("应记录两轮成绩，实际 %v", starRow.Rounds)
	}
	// 第二轮：95×0.6 + 92×0.4 + 70×0 = 93.8；时间奖励 (120−90)×0.5 = 15 → 封顶 10
	if !almostEqual(starRow.Result.Base, 93.8) {
		t.Errorf("第二轮基础分 = %v，期望 93.8", starRow.Result.Base)
	}
	if !almostEqual(starRow.Result.Bonus, 10) {
		t.Errorf("第二轮加分 = %v，期望 10（封顶生效）", starRow.Result.Bonus)
	}

	// —— ⑥ 已签字成绩禁止直接改分 ——
	direct := *round2Record(t, svc, star.ID)
	direct.TaskValues = map[string]any{"focus": 100.0, "build": 100.0, "teamwork": 100.0}
	if _, err := svc.SaveScore(ctx, &direct); !errors.Is(err, service.ErrScoreSubmitted) {
		t.Fatalf("已签字成绩应禁止直接修改，实际 %v", err)
	}

	// —— ⑦ 改分：裁判发起（只留痕）→ 裁判长授权后落库 ——
	req, err := svc.ScoreChangeRequest(ctx, star.ID, 2, 99.0, "申诉复核：搭建任务漏计 1 个构件")
	if err != nil {
		t.Fatalf("改分申请失败: %v", err)
	}
	if req.Approved {
		t.Error("改分申请不应自动获批")
	}
	mid, err := svc.GetScore(ctx, star.ID, 2)
	if err != nil {
		t.Fatalf("查成绩失败: %v", err)
	}
	if mid.TaskValues["focus"] != 95.0 {
		t.Errorf("改分申请阶段不应改动成绩数据，实际 focus=%v", mid.TaskValues["focus"])
	}

	approved := *mid
	approved.TaskValues = map[string]any{"focus": 100.0, "build": 92.0, "teamwork": 70.0}
	if _, err := svc.ApplyScoreChange(ctx, &approved, "裁判长C", "申诉成立，授权修改"); err != nil {
		t.Fatalf("授权改分失败: %v", err)
	}
	final, err := svc.GetScore(ctx, star.ID, 2)
	if err != nil {
		t.Fatalf("查成绩失败: %v", err)
	}
	if final.TaskValues["focus"] != 100.0 {
		t.Errorf("授权后应落库，实际 focus=%v", final.TaskValues["focus"])
	}
	if final.Operator != mid.Operator {
		t.Errorf("改分不应改变原记分员：%q → %q", mid.Operator, final.Operator)
	}
	// 授权改分的审计必须同时留下操作人与审批人，且审批人可检索
	approvedLogs, err := svc.AuditLogs(ctx, store.AuditFilter{Approver: "裁判长C"})
	if err != nil {
		t.Fatalf("按审批人查审计失败: %v", err)
	}
	if len(approvedLogs) != 1 {
		t.Fatalf("按审批人应查到 1 条改分记录，实际 %d 条", len(approvedLogs))
	}
	if approvedLogs[0].Operator == "" || approvedLogs[0].Approver != "裁判长C" {
		t.Errorf("审计应同时记录操作人与审批人：%+v", approvedLogs[0])
	}
	if !strings.Contains(approvedLogs[0].Reason, "申诉成立") {
		t.Errorf("审批理由应保留：%q", approvedLogs[0].Reason)
	}

	// —— ⑧ 六类操作全部留痕 ——
	if _, err := svc.WithdrawTeam(ctx, byNo["1003"].ID, "选手临时退赛"); err != nil {
		t.Fatalf("弃赛失败: %v", err)
	}
	if _, err := svc.RestoreTeam(ctx, byNo["1003"].ID, "误操作，恢复"); err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	if _, err := svc.UpdateTeam(ctx, byNo["1003"].ID,
		model.Team{Name: "晨曦队", GroupCode: "小学组", Members: "孙五 / 周六"}, "现场核实组别报错"); err != nil {
		t.Fatalf("改组失败: %v", err)
	}
	if err := svc.DeleteTeam(ctx, byNo["1003"].ID, "误录队伍清理"); err != nil {
		t.Fatalf("删队失败: %v", err)
	}
	// 带成绩的队伍必须删不掉（数据库外键 RESTRICT 兜底）
	if err := svc.DeleteTeam(ctx, star.ID, "试图删除带成绩队伍"); !errors.Is(err, store.ErrInUse) {
		t.Fatalf("带成绩队伍应返回 ErrInUse，实际 %v", err)
	}

	seat, err := svc.CreateSeat(ctx, "赛台 1", 0)
	if err != nil {
		t.Fatalf("建赛台失败: %v", err)
	}
	slot, err := svc.CreateSlot(ctx, model.SlotDraft{
		SeatID: seat.ID, Period: "上午", TimeRange: "09:00–12:00",
		EventID: ev.ID, GroupCode: "小学组", Type: model.SlotNormal,
	})
	if err != nil {
		t.Fatalf("建场次失败: %v", err)
	}
	if _, err := svc.AutoAssignSlot(ctx, slot.ID, ""); err != nil {
		t.Fatalf("自动分配失败: %v", err)
	}
	assigned, err := svc.ListSlots(ctx, seat.ID, ev.ID)
	if err != nil {
		t.Fatalf("查场次失败: %v", err)
	}
	if len(assigned) != 1 || len(assigned[0].TeamIDs) != 2 {
		t.Errorf("自动分配后场次应有 2 支队伍，实际 %+v", assigned)
	}

	summary, err := svc.AuditSummary(ctx)
	if err != nil {
		t.Fatalf("审计统计失败: %v", err)
	}
	for _, action := range svc.RequiredAuditActions() {
		if summary[string(action)] == 0 {
			t.Errorf("六类必留痕操作缺少「%s」（统计：%v）", action, summary)
		}
	}
	if summary[string(model.ActImport)] == 0 {
		t.Error("缺少导入队伍审计")
	}
	if importLogs, err := svc.ImportLogs(ctx, 10); err != nil {
		t.Fatalf("查导入日志失败: %v", err)
	} else if len(importLogs) != 2 {
		t.Errorf("导入日志应有 2 条（两次导入），实际 %d 条", len(importLogs))
	}

	// —— ④b 显式裁决「以文件为准」：结构性冲突的显式出口 ——
	// 放在测试末尾：它会改动 1001 的组别，若插在中途会改掉前面「榜单组别数」等期望值。
	//
	// 星河队（1001）已录过成绩；把它的组别从「小学组」改成「初中组」属于结构性变更，
	// 服务端判为冲突，但标 CanOverride —— 这类是"现场确实要改"的场景，需要一个明确出口。
	rows3 := []service.ImportRow{
		{TeamNo: "1001", Name: "星河队", School: "杭州实验小学", Coach: "张老师", Group: "初中组", Members: "张一 / 李二", LineNo: 2},
	}
	pre3, err := svc.PreviewImport(ctx, ev.ID, rows3)
	if err != nil {
		t.Fatalf("预览失败: %v", err)
	}
	if pre3.Rows[0].Status != service.ImportConflict || !pre3.Rows[0].CanOverride {
		t.Fatalf("已有成绩的队改组别应判为「可覆盖的冲突」，实际 status=%s canOverride=%v",
			pre3.Rows[0].Status, pre3.Rows[0].CanOverride)
	}
	// 不声明裁决 → 拒绝整批
	if _, err := svc.CommitImport(ctx, ev.ID, rows3, []int{2}, nil, "未裁决"); !errors.Is(err, service.ErrConflictRows) {
		t.Fatalf("未声明裁决的冲突行应被拒绝，实际 %v", err)
	}
	// 裁决为「以库内为准 / 跳过」→ 语义上就是不写入，仍不放行
	for _, mode := range []service.ImportOverrideMode{service.OverrideDB, service.OverrideSkip} {
		if _, err := svc.CommitImport(ctx, ev.ID, rows3, []int{2},
			[]service.ImportOverride{{Line: 2, Mode: mode}}, "非覆盖裁决"); !errors.Is(err, service.ErrConflictRows) {
			t.Fatalf("裁决 %q 不应放行冲突行写入，实际 %v", mode, err)
		}
	}
	// 裁决为「以文件为准」→ 放行，明细标 forced，组别确实落库
	log3, err := svc.CommitImport(ctx, ev.ID, rows3, []int{2},
		[]service.ImportOverride{{Line: 2, Mode: service.OverrideFile}}, "人工裁决覆盖")
	if err != nil {
		t.Fatalf("显式裁决「以文件为准」应放行，实际 %v", err)
	}
	if !hasForcedDetail(log3.Detail) {
		t.Errorf("强制覆盖的明细里应标 forced=true，实际 %+v", log3.Detail)
	}
	if act := firstForcedAct(log3.Detail); act != "update" {
		t.Errorf("覆盖已有队伍时明细动作应为 update，实际 %q", act)
	}
	afterCover, err := svc.ListTeams(ctx, ev.ID, true)
	if err != nil {
		t.Fatalf("查队伍失败: %v", err)
	}
	covered := false
	for _, tm := range afterCover {
		if tm.TeamNo == "1001" && tm.GroupCode == "初中组" {
			covered = true
		}
	}
	if !covered {
		t.Error("强制覆盖未落库（1001 的组别应已改为初中组）")
	}

	// 反向：**数据本身的错误不允许覆盖** —— 「组别不属于赛项」覆盖只会把脏数据写进库
	rowsBad := []service.ImportRow{
		{TeamNo: "1009", Name: "脏数据队", School: "某学校", Coach: "钱老师", Group: "高中组", LineNo: 2},
	}
	preBad, err := svc.PreviewImport(ctx, ev.ID, rowsBad)
	if err != nil {
		t.Fatalf("预览失败: %v", err)
	}
	if preBad.Rows[0].CanOverride {
		t.Error("「组别不属于赛项」这类数据错误不应允许覆盖")
	}
	if _, err := svc.CommitImport(ctx, ev.ID, rowsBad, []int{2},
		[]service.ImportOverride{{Line: 2, Mode: service.OverrideFile}}, "试图覆盖脏数据"); !errors.Is(err, service.ErrConflictRows) {
		t.Fatalf("数据错误即使声明覆盖也应被拒绝，实际 %v", err)
	}

}

// （而不是直接依赖 pgx）的价值所在。
func TestAuditFailureRollsBackBusiness(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")

	ev, err := svc.CreateEvent(ctx, brainPlanetEvent())
	if err != nil {
		t.Fatalf("建赛项失败: %v", err)
	}
	team, err := svc.CreateTeam(ctx, model.TeamDraft{
		EventID: ev.ID, TeamNo: "9001", Name: "回滚验证队", GroupCode: "小学组",
	})
	if err != nil {
		t.Fatalf("建队伍失败: %v", err)
	}

	broken := service.New(&failingAuditStore{Store: svc.Store()})
	if _, err := broken.WithdrawTeam(ctx, team.ID, "弃赛，但审计会写失败"); err == nil {
		t.Fatal("审计写失败时，弃赛应当返回错误")
	}

	// 关键断言：业务改动必须已经回滚
	after, err := svc.GetTeam(ctx, team.ID)
	if err != nil {
		t.Fatalf("查队伍失败: %v", err)
	}
	if after.Status != model.TeamActive {
		t.Fatalf("审计写失败后队伍状态仍被改成 %q —— 说明审计与业务不在同一事务！", after.Status)
	}

	// 反证：同一方法在正常情况下确实会改数据
	if _, err := svc.WithdrawTeam(ctx, team.ID, "真实弃赛"); err != nil {
		t.Fatalf("正常弃赛失败: %v", err)
	}
	after, err = svc.GetTeam(ctx, team.ID)
	if err != nil {
		t.Fatalf("查队伍失败: %v", err)
	}
	if after.Status != model.TeamWithdrawn {
		t.Fatalf("正常路径下应已弃赛，实际 %q", after.Status)
	}
}

// ---------------------------------------------------------------------------
// 测试替身
// ---------------------------------------------------------------------------

// failingAuditStore 把审计仓储换成「写入必失败」，其余能力走真实实现。
//
// 用嵌入接口的方式实现装饰器：store 新增方法时不需要同步改这里。
type failingAuditStore struct {
	store.Store
}

func (s *failingAuditStore) Repos() store.Repos {
	r := s.Store.Repos()
	r.Audits = &failingAuditRepo{AuditRepo: r.Audits}
	return r
}

func (s *failingAuditStore) WithTx(ctx context.Context, fn func(store.Repos) error) error {
	return s.Store.WithTx(ctx, func(r store.Repos) error {
		r.Audits = &failingAuditRepo{AuditRepo: r.Audits}
		return fn(r)
	})
}

type failingAuditRepo struct {
	store.AuditRepo
}

func (r *failingAuditRepo) Append(context.Context, model.AuditEntry) error {
	return errors.New("模拟：审计存储不可用")
}

// ---------------------------------------------------------------------------

func findRow(res *service.StandingsResult, teamID int64) *model.StandingRow {
	for gi := range res.Groups {
		for ri := range res.Groups[gi].Rows {
			if res.Groups[gi].Rows[ri].Team.ID == teamID {
				return &res.Groups[gi].Rows[ri]
			}
		}
	}
	return nil
}

func round2Record(t *testing.T, svc *service.Service, teamID int64) *model.ScoreRecord {
	t.Helper()
	r, err := svc.GetScore(context.Background(), teamID, 2)
	if err != nil {
		t.Fatalf("查第 2 轮成绩失败: %v", err)
	}
	return r
}

// hasForcedDetail 判断导入明细里是否含「强制覆盖」标记。
func hasForcedDetail(detail []any) bool {
	for _, d := range detail {
		if m, ok := d.(map[string]any); ok {
			if f, _ := m["forced"].(bool); f {
				return true
			}
		}
	}
	return false
}

// firstForcedAct 取第一条强制覆盖明细的动作（insert / update）。
func firstForcedAct(detail []any) string {
	for _, d := range detail {
		if m, ok := d.(map[string]any); ok {
			if f, _ := m["forced"].(bool); f {
				if s, _ := m["act"].(string); s != "" {
					return s
				}
			}
		}
	}
	return ""
}
