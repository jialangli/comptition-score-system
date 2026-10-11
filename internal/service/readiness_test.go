package service_test

// ============================================================================
// 开赛就绪度聚合（readiness_service.go）—— 与 demo「总览」页逐项对齐的验收
//
// 口径都写在实现注释里，这里验证的是**行为**：
//  1. 空赛事 → 全红、ready=false（还没有赛项这条先卡住）
//  2. 全就位 → ready=true（六项全绿）
//  3. 逐项破坏 → 对应计数与检查项变红：
//     未分台（含弃赛队不放水）/ 归台指向已删赛台 / 已分台排不进场次 / 空场次
//     / 裁判未激活
//  4. 独立场次（加时赛）不进场次计数 —— demo 口径 1
//  5. 就绪度按当前赛事分区（X-Contest 同款语义在 service 层的对应物）
// ============================================================================

import (
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/service"
)

// findItem 按中文键取检查项。
func findItem(rep *service.ReadinessReport, key string) *service.ReadinessItem {
	for i := range rep.Items {
		if rep.Items[i].Key == key {
			return &rep.Items[i]
		}
	}
	return nil
}

// TestReadinessEmptyContest 空赛事：第一项「规则已校验」就该红。
func TestReadinessEmptyContest(t *testing.T) {
	svc, _ := newSvc(t)
	rep, err := svc.Readiness(operatorCtx("运营A"))
	if err != nil {
		t.Fatalf("读就绪度失败: %v", err)
	}
	if rep.Ready {
		t.Errorf("空赛事不应 ready")
	}
	if rep.Counters.Events != 0 || rep.Counters.Teams != 0 {
		t.Errorf("空赛事计数应为 0: %+v", rep.Counters)
	}
	if item := findItem(rep, "规则已校验"); item == nil || item.OK {
		t.Errorf("还没有赛项时「规则已校验」应为红")
	}
	if item := findItem(rep, "场次已排班"); item == nil {
		t.Errorf("检查项里应有「场次已排班」（0021 排班接口已接，不再是 gaps）")
	}
	if len(rep.Gaps) != 0 {
		t.Errorf("排班已接入后不应再有 gaps: %v", rep.Gaps)
	}
}

// TestReadinessAllGreenAndBreakEachItem 先拼出一个全绿赛事，再逐项破坏。
func TestReadinessAllGreenAndBreakEachItem(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")

	ev := mustEvent(t, svc, brainPlanetEvent())
	seat := mustSeat(t, svc, "1 号台", 1)

	slot1, err := svc.CreateSlot(ctx, model.SlotDraft{
		SeatID: seat.ID, Period: "上午", TimeRange: "09:00–12:00",
		EventID: ev.ID, GroupCode: "小学组", Type: model.SlotNormal, RoundNo: 1,
	})
	if err != nil {
		t.Fatalf("建场次失败: %v", err)
	}

	team := mustTeam(t, svc, ev.ID, "1001", "两轮队", "小学组")
	if _, err := svc.AssignTeamSeat(ctx, team.ID, &seat.ID, 1, "首次分台"); err != nil {
		t.Fatalf("归台失败: %v", err)
	}

	rc, err := svc.IssueRefereeCode(ctx, "裁判A", model.RoleReferee, ev.ID, "小学组", &seat.ID)
	if err != nil {
		t.Fatalf("建档裁判码失败: %v", err)
	}

	// 阶段 1：裁判未激活 → 只有「裁判已激活」红
	rep, err := svc.Readiness(ctx)
	if err != nil {
		t.Fatalf("读就绪度失败: %v", err)
	}
	c := rep.Counters
	if c.Events != 1 || c.Teams != 1 || c.Slots != 1 || c.Refs != 1 {
		t.Errorf("基础计数不符: %+v", c)
	}
	if c.RuleBad != 0 || c.Unseated != 0 || c.Unslotted != 0 || c.EmptySlots != 0 {
		t.Errorf("前三段不应有红: %+v", c)
	}
	if c.Idle != 1 {
		t.Errorf("建档未用的裁判应记为未激活，idle=%d", c.Idle)
	}
	if c.Unstaffed != 1 {
		t.Errorf("没排裁判的正式场次应记入未排班，unstaffed=%d", c.Unstaffed)
	}
	if item := findItem(rep, "裁判已激活"); item == nil || item.OK {
		t.Errorf("裁判已激活应为红")
	}
	if item := findItem(rep, "场次已排班"); item == nil || item.OK {
		t.Errorf("场次没排裁判时「场次已排班」应为红")
	}
	if rep.Ready {
		t.Errorf("裁判未激活时不应 ready")
	}

	// 排班（走 0021 写入口）+ 激活 → 全绿
	if _, err := svc.SetSlotJudges(ctx, slot1.ID, []int64{rc.ID}, "首场排班"); err != nil {
		t.Fatalf("排班失败: %v", err)
	}
	if _, err := svc.ActivateReferee(ctx, rc.Code, "裁判A"); err != nil {
		t.Fatalf("激活裁判失败: %v", err)
	}
	rep, _ = svc.Readiness(ctx)
	if !rep.Ready {
		t.Fatalf("全就位后应 ready，未过项: %+v", rep.Items)
	}

	// ---- 破坏 1：弃赛队没归台照样算「未分台」（demo 口径 2，不放水）----
	willWithdraw := mustTeam(t, svc, ev.ID, "1002", "弃赛队", "小学组")
	if _, err := svc.WithdrawTeam(ctx, willWithdraw.ID, "队伍解散"); err != nil {
		t.Fatalf("弃赛失败: %v", err)
	}
	rep, _ = svc.Readiness(ctx)
	if rep.Counters.Unseated != 1 {
		t.Errorf("弃赛队没归台也应记入未分台，unseated=%d", rep.Counters.Unseated)
	}
	if item := findItem(rep, "队伍已分台"); item == nil || item.OK {
		t.Errorf("有未分台队伍时「队伍已分台」应为红")
	}

	// 归台后恢复（弃赛队有了台就不再计红）
	if _, err := svc.AssignTeamSeat(ctx, willWithdraw.ID, &seat.ID, 2, "补录分台"); err != nil {
		t.Fatalf("归台失败: %v", err)
	}
	rep, _ = svc.Readiness(ctx)
	if rep.Counters.Unseated != 0 {
		t.Errorf("归台后未分台应清零: %+v", rep.Counters)
	}

	// ---- 破坏 2：归台指向已删赛台（队伍不能隐身绿灯）----
	seat2 := mustSeat(t, svc, "2 号台", 2)
	lonely := mustTeam(t, svc, ev.ID, "1003", "错台队", "小学组")
	if _, err := svc.AssignTeamSeat(ctx, lonely.ID, &seat2.ID, 1, "临时归台"); err != nil {
		t.Fatalf("归台失败: %v", err)
	}
	if err := svc.DeleteSeat(ctx, seat2.ID, "误建的台"); err != nil {
		t.Fatalf("删台失败: %v", err)
	}
	rep, _ = svc.Readiness(ctx)
	if rep.Counters.Unseated != 1 {
		t.Errorf("归台指向已删赛台应记为未分台，unseated=%d", rep.Counters.Unseated)
	}
	if _, err := svc.AssignTeamSeat(ctx, lonely.ID, &seat.ID, 3, "改回正台"); err != nil {
		t.Fatalf("重新归台失败: %v", err)
	}

	// ---- 破坏 3：已分台但排不进任何场次（组别没有对应场次）----
	mismatch := mustTeam(t, svc, ev.ID, "1004", "错组别队", "初中组")
	if _, err := svc.AssignTeamSeat(ctx, mismatch.ID, &seat.ID, 4, "首次分台"); err != nil {
		t.Fatalf("归台失败: %v", err)
	}
	rep, _ = svc.Readiness(ctx)
	if rep.Counters.Unslotted != 1 {
		t.Errorf("初中组队伍没有对应场次应记为排不进，unslotted=%d", rep.Counters.Unslotted)
	}
	if item := findItem(rep, "分台已排进场次"); item == nil || item.OK {
		t.Errorf("有队伍排不进场次时该项应为红")
	}
	if err := svc.DeleteTeam(ctx, mismatch.ID, "测试清理"); err != nil {
		t.Fatalf("删队失败: %v", err)
	}

	// ---- 破坏 4：空场次（二号台没有队伍归台）----
	seat3 := mustSeat(t, svc, "3 号台", 3)
	if _, err := svc.CreateSlot(ctx, model.SlotDraft{
		SeatID: seat3.ID, Period: "上午", TimeRange: "09:00–12:00",
		EventID: ev.ID, GroupCode: "小学组", Type: model.SlotNormal, RoundNo: 1,
	}); err != nil {
		t.Fatalf("建空场次失败: %v", err)
	}
	rep, _ = svc.Readiness(ctx)
	if rep.Counters.Slots != 2 || rep.Counters.EmptySlots != 1 {
		t.Errorf("场次计数不符: slots=%d empty=%d", rep.Counters.Slots, rep.Counters.EmptySlots)
	}
	if item := findItem(rep, "场次已排队伍"); item == nil || item.OK {
		t.Errorf("有空场次时该项应为红")
	}

	// ---- 破坏 5：独立场次（加时赛）不进场次计数（demo 口径 1）----
	extraSlot, err := svc.CreateSlot(ctx, model.SlotDraft{
		SeatID: seat.ID, Period: "下午", TimeRange: "18:00–19:00",
		EventID: ev.ID, GroupCode: "小学组", Type: model.SlotExtra, RoundNo: 1,
	})
	if err != nil {
		t.Fatalf("建加时赛场次失败: %v", err)
	}
	if _, err := svc.SaveSnapshot(ctx, extraSlot.ID, []model.Snapshot{
		{TeamNo: "9001", Name: "加时队"},
	}, "加时赛"); err != nil {
		t.Fatalf("写快照失败: %v", err)
	}
	rep, _ = svc.Readiness(ctx)
	if rep.Counters.Slots != 2 {
		t.Errorf("独立场次不应计入场次计数，slots=%d", rep.Counters.Slots)
	}
	if rep.Counters.EmptySlots != 1 {
		t.Errorf("独立场次不该进空场次统计，empty=%d", rep.Counters.EmptySlots)
	}
}

// TestReadinessScopesToContest 同名赛项在两场赛事各存一套，就绪度各算各的 ——
// 与「跨赛事查询必须显式带 contest_id」的项目纪律对齐。
func TestReadinessScopesToContest(t *testing.T) {
	svc, db := newSvc(t)
	ctx := operatorCtx("运营A")

	// A 场建赛项；默认赛事保持为空
	if err := db.Repos().Contests.Create(ctx, &model.Contest{
		ID: "ct_rd_a", Name: "就绪度 A 场", Status: model.ContestLive,
	}); err != nil {
		t.Fatalf("建赛事失败: %v", err)
	}
	ctxA := service.WithContest(ctx, "ct_rd_a")
	if _, err := svc.CreateEvent(ctxA, brainPlanetEvent()); err != nil {
		t.Fatalf("A 场建赛项失败: %v", err)
	}

	empty, err := svc.Readiness(ctx) // 默认赛事
	if err != nil {
		t.Fatalf("读就绪度失败: %v", err)
	}
	if empty.Counters.Events != 0 {
		t.Errorf("默认赛事不应看到 A 场的赛项，events=%d", empty.Counters.Events)
	}
	full, err := svc.Readiness(ctxA)
	if err != nil {
		t.Fatalf("读 A 场就绪度失败: %v", err)
	}
	if full.Counters.Events != 1 {
		t.Errorf("A 场应看到自己的 1 个赛项，events=%d", full.Counters.Events)
	}
}
