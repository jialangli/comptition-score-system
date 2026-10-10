package service_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/service"
)

// ============================================================================
// 场次队伍 = 派生（赛台 × 赛项 × 组别 × 轮次）
//
// 这是「队伍级归台为唯一事实源」的落地验算：场次不再存队伍（POST /slots/{id}/teams
// 已废弃为 410），队伍由归台派生。三条判据逐条验：
//
//  1. 同赛台 + 同赛项 + 同组别 → 命中
//  2. 参赛轮次不覆盖场次轮次 → 不命中（下午场次不收「只打第 1 轮」的队）
//  3. 弃赛队 → 不命中
//
// 外加：顺序必须是**台内顺位**（它就是现场叫号顺序），
// 以及独立场次（加时赛 / 重赛）走场内快照、不参与派生。
// ============================================================================

func TestSlotTeamsDerivedBySeatGroupRound(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")
	ev := mustEvent(t, svc, brainPlanetEvent())
	seat := mustSeat(t, svc, "1 号台", 1)

	morning, err := svc.CreateSlot(ctx, model.SlotDraft{
		SeatID: seat.ID, Period: "上午", TimeRange: "09:00–12:00",
		EventID: ev.ID, GroupCode: "小学组", Type: model.SlotNormal, RoundNo: 1,
	})
	if err != nil {
		t.Fatalf("建上午场次失败: %v", err)
	}
	afternoon, err := svc.CreateSlot(ctx, model.SlotDraft{
		SeatID: seat.ID, Period: "下午", TimeRange: "14:00–17:00",
		EventID: ev.ID, GroupCode: "小学组", Type: model.SlotNormal, RoundNo: 2,
	})
	if err != nil {
		t.Fatalf("建下午场次失败: %v", err)
	}

	both := mustTeam(t, svc, ev.ID, "1001", "两轮队", "小学组")
	only1 := mustTeam(t, svc, ev.ID, "1002", "只打上午队", "小学组")
	only2 := mustTeam(t, svc, ev.ID, "1003", "只打下午队", "小学组")
	gone := mustTeam(t, svc, ev.ID, "1004", "弃赛队", "小学组")

	// 四支队都归到这张台，顺位 1..4（顺位 = 现场叫号顺序）
	for i, tm := range []*model.Team{both, only1, only2, gone} {
		if _, err := svc.AssignTeamSeat(ctx, tm.ID, &seat.ID, i+1, "首次分台"); err != nil {
			t.Fatalf("归台失败: %v", err)
		}
	}
	if _, err := svc.SetTeamSession(ctx, only1.ID, model.SessionRound1, "下午缺席"); err != nil {
		t.Fatalf("设置参赛轮次失败: %v", err)
	}
	if _, err := svc.SetTeamSession(ctx, only2.ID, model.SessionRound2, "上午缺席"); err != nil {
		t.Fatalf("设置参赛轮次失败: %v", err)
	}
	if _, err := svc.WithdrawTeam(ctx, gone.ID, "队伍解散"); err != nil {
		t.Fatalf("弃赛失败: %v", err)
	}

	// 上午场次：两轮队 + 只打上午队（只打下午队不参赛第 1 轮、弃赛队不参赛）
	m := mustSlotTeams(t, svc, morning.ID)
	assertOrder(t, m, []string{"1001", "1002"}, "第 1 轮场次")

	// 下午场次：两轮队 + 只打下午队
	a := mustSlotTeams(t, svc, afternoon.ID)
	assertOrder(t, a, []string{"1001", "1003"}, "第 2 轮场次")

	// 顺位决定顺序：把只打下午队提到顺位 0 之前（改成 1，两轮队改 2）
	if _, err := svc.AssignTeamSeat(ctx, only2.ID, &seat.ID, 1, "现场调顺位"); err != nil {
		t.Fatalf("调顺位失败: %v", err)
	}
	if _, err := svc.AssignTeamSeat(ctx, both.ID, &seat.ID, 2, "现场调顺位"); err != nil {
		t.Fatalf("调顺位失败: %v", err)
	}
	a = mustSlotTeams(t, svc, afternoon.ID)
	assertOrder(t, a, []string{"1003", "1001"}, "调顺位后的第 2 轮场次")
}

// TestSlotTeamsIndependentSlotUsesSnapshot 独立场次（加时赛 / 重赛）走场内快照。
//
// 特别是**重赛**：它与加时赛是同一套机制，必须和加时赛一样能存快照、能读出来 ——
// 早先 store / service 里有几处只判 `== SlotExtra` 的旧特判，重赛场次会「存不进去、
// 也读不出来」，而这类漏点不报错，只是安静地什么都不发生。
func TestSlotTeamsIndependentSlotUsesSnapshot(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")
	ev := mustEvent(t, svc, brainPlanetEvent())
	seat := mustSeat(t, svc, "1 号台", 1)

	for _, c := range []struct {
		typ    model.SlotType
		period string
	}{
		{model.SlotExtra, "下午"},
		{model.SlotRematch, "上午"}, // 同一赛台同时段只能有一个场次（ux_slot_seat_period）
	} {
		typ := c.typ
		sl, err := svc.CreateSlot(ctx, model.SlotDraft{
			SeatID: seat.ID, Period: c.period, EventID: ev.ID,
			GroupCode: "小学组", Type: typ,
		})
		if err != nil {
			t.Fatalf("建 %s 场次失败: %v", typ, err)
		}
		if _, err := svc.SaveSnapshot(ctx, sl.ID, []model.Snapshot{
			{TeamNo: "9001", Name: "临时队", School: "现场学校"},
		}, "场内快照导入"); err != nil {
			t.Fatalf("%s 场次不接受场内快照: %v", typ, err)
		}
		res := mustSlotTeams(t, svc, sl.ID)
		if res.Source != "snapshot" || res.Total != 1 || res.Teams[0].No != "9001" {
			t.Fatalf("%s 场次的队伍应来自场内快照：%+v", typ, res)
		}
		if res.Teams[0].Source != "snapshot" || res.Teams[0].TeamID != 0 {
			t.Fatalf("%s 快照行不该带队伍主库 ID：%+v", typ, res.Teams[0])
		}
	}
}

// ---------------------------------------------------------------------------

func mustSlotTeams(t *testing.T, svc *service.Service, slotID int64) *service.SlotTeamsResult {
	t.Helper()
	res, err := svc.SlotTeams(context.Background(), slotID)
	if err != nil {
		t.Fatalf("读场次队伍失败: %v", err)
	}
	return res
}

// assertOrder 断言派生出的队伍编号与顺序（顺序 = 台内顺位 = 现场叫号顺序）。
func assertOrder(t *testing.T, res *service.SlotTeamsResult, wantNos []string, what string) {
	t.Helper()
	got := make([]string, 0, len(res.Teams))
	for _, row := range res.Teams {
		got = append(got, row.No)
	}
	if fmt.Sprint(got) != fmt.Sprint(wantNos) {
		t.Fatalf("%s 派生结果 = %v，期望 %v（total=%d）", what, got, wantNos, res.Total)
	}
}
