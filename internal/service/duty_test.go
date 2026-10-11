package service_test

// ============================================================================
// 执裁排班读写（0021，图集 8 号缺口补齐）
//
// 读 = Slot.Judges 随 GET /slots 一起回；写 = SetSlotJudges 整体替换。
// 这里验证口径：去重保序 / 跨赛事拦截 / 已作废拦截 / 清空 / 审计留痕 /
// 独立场次同样可排（加时赛也有裁判）。
// ============================================================================

import (
	"strings"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/service"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// TestSlotJudgesWriteReadDedupAndAudit 主链路：写 → 读回 → 审计 → 清空。
func TestSlotJudgesWriteReadDedupAndAudit(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")

	ev := mustEvent(t, svc, brainPlanetEvent())
	seat := mustSeat(t, svc, "1 号台", 1)
	slot, err := svc.CreateSlot(ctx, model.SlotDraft{
		SeatID: seat.ID, Period: "上午", TimeRange: "09:00–12:00",
		EventID: ev.ID, GroupCode: "小学组", Type: model.SlotNormal, RoundNo: 1,
	})
	if err != nil {
		t.Fatalf("建场次失败: %v", err)
	}

	refA, err := svc.IssueRefereeCode(ctx, "裁判A", model.RoleReferee, ev.ID, "小学组", &seat.ID)
	if err != nil {
		t.Fatalf("建档 A 失败: %v", err)
	}
	refB, err := svc.IssueRefereeCode(ctx, "裁判B", model.RoleChief, ev.ID, "小学组", &seat.ID)
	if err != nil {
		t.Fatalf("建档 B 失败: %v", err)
	}

	// 新场次 = 未排班（judges 为空数组，不是 NULL）
	got, err := svc.ListSlots(ctx, seat.ID, "")
	if err != nil {
		t.Fatalf("读场次失败: %v", err)
	}
	if got[0].Judges == nil || len(got[0].Judges) != 0 {
		t.Errorf("新场次 judges 应为空数组: %+v", got[0].Judges)
	}

	// 写入 + 去重保序：[A,B,A] → [A,B]
	updated, err := svc.SetSlotJudges(ctx, slot.ID, []int64{refA.ID, refB.ID, refA.ID}, "")
	if err != nil {
		t.Fatalf("排班失败: %v", err)
	}
	if len(updated.Judges) != 2 || updated.Judges[0] != refA.ID || updated.Judges[1] != refB.ID {
		t.Errorf("去重保序失败: %+v", updated.Judges)
	}
	// 读回：随场次一起返回，顺序一致
	got, _ = svc.ListSlots(ctx, seat.ID, "")
	if len(got[0].Judges) != 2 || got[0].Judges[0] != refA.ID {
		t.Errorf("读回的排班不符: %+v", got[0].Judges)
	}

	// 审计留痕：动作=排班，before/after 都是可读的人名
	logs, err := svc.AuditLogs(ctx, store.AuditFilter{Action: string(model.ActDuty), Limit: 10})
	if err != nil {
		t.Fatalf("查审计失败: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("应恰好 1 条排班审计，实为 %d", len(logs))
	}
	if !strings.Contains(logs[0].Before, "未排班") || !strings.Contains(logs[0].After, "裁判A") {
		t.Errorf("审计 before/after 应可读: %q → %q", logs[0].Before, logs[0].After)
	}

	// 再写一次：before 变成上一轮名单（A、B）
	if _, err := svc.SetSlotJudges(ctx, slot.ID, []int64{refB.ID}, "换裁判"); err != nil {
		t.Fatalf("二次排班失败: %v", err)
	}
	logs, _ = svc.AuditLogs(ctx, store.AuditFilter{Action: string(model.ActDuty), Limit: 10})
	if len(logs) != 2 || !strings.Contains(logs[0].Before, "裁判A") {
		t.Errorf("第二次审计的 before 应为上一轮名单: %q", logs[0].Before)
	}

	// 清空 = 空数组
	if _, err := svc.SetSlotJudges(ctx, slot.ID, nil, "重新排班"); err != nil {
		t.Fatalf("清空排班失败: %v", err)
	}
	got, _ = svc.ListSlots(ctx, seat.ID, "")
	if len(got[0].Judges) != 0 {
		t.Errorf("清空后 judges 应为空: %+v", got[0].Judges)
	}
}

// TestSlotJudgesRejectForeignAndRevoked 跨赛事裁判 / 已作废裁判都不能排。
func TestSlotJudgesRejectForeignAndRevoked(t *testing.T) {
	svc, db := newSvc(t)
	ctx := operatorCtx("运营A")

	ev := mustEvent(t, svc, brainPlanetEvent())
	seat := mustSeat(t, svc, "1 号台", 1)
	slot, err := svc.CreateSlot(ctx, model.SlotDraft{
		SeatID: seat.ID, Period: "上午", TimeRange: "09:00–12:00",
		EventID: ev.ID, GroupCode: "小学组", Type: model.SlotNormal, RoundNo: 1,
	})
	if err != nil {
		t.Fatalf("建场次失败: %v", err)
	}

	// 本赛事裁判
	refA, err := svc.IssueRefereeCode(ctx, "裁判A", model.RoleReferee, ev.ID, "小学组", &seat.ID)
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}

	// 1) 不存在的裁判 ID → 400 语义
	if _, err := svc.SetSlotJudges(ctx, slot.ID, []int64{99999}, ""); err == nil {
		t.Errorf("不存在的裁判应被拦下")
	} else if !strings.Contains(err.Error(), "裁判档案") {
		t.Errorf("报错应指回裁判档案: %v", err)
	}

	// 2) 另一场赛事的裁判 → 拦（裁判码是赛事级凭证）
	if err := db.Repos().Contests.Create(ctx, &model.Contest{
		ID: "ct_duty_b", Name: "排班 B 场", Status: model.ContestLive,
	}); err != nil {
		t.Fatalf("建赛事失败: %v", err)
	}
	ctxB := service.WithContest(ctx, "ct_duty_b")
	foreign, err := svc.IssueRefereeCode(ctxB, "外场裁判", model.RoleReferee, ev.ID, "小学组", &seat.ID)
	if err != nil {
		t.Fatalf("B 场建档失败: %v", err)
	}
	if _, err := svc.SetSlotJudges(ctx, slot.ID, []int64{foreign.ID}, ""); err == nil {
		t.Errorf("B 场裁判排进 A 场（默认赛事）场次应被拦下")
	} else if !strings.Contains(err.Error(), "赛事") {
		t.Errorf("报错应说明赛事级凭证: %v", err)
	}

	// 3) 已作废裁判 → 拦。作废能力后端暂无写接口，直接置库（真实路径：
	//    「作废」属于下一批的裁判码管理，置库不影响本口径的验证）。
	refC, err := svc.IssueRefereeCode(ctx, "裁判C", model.RoleReferee, ev.ID, "小学组", &seat.ID)
	if err != nil {
		t.Fatalf("建档 C 失败: %v", err)
	}
	if err := db.Repos().Referees.MarkRevoked(ctx, refC.ID); err != nil {
		t.Fatalf("作废裁判失败: %v", err)
	}
	if _, err := svc.SetSlotJudges(ctx, slot.ID, []int64{refA.ID, refC.ID}, ""); err == nil {
		t.Errorf("已作废裁判应被拦下")
	} else if !strings.Contains(err.Error(), "作废") {
		t.Errorf("报错应说明已作废: %v", err)
	}

	// 4) 正常落库不受 3 的脏候选影响
	if _, err := svc.SetSlotJudges(ctx, slot.ID, []int64{refA.ID}, ""); err != nil {
		t.Fatalf("正常排班失败: %v", err)
	}
}

// TestSlotJudgesOnExtraSlot 独立场次（加时赛）同样可排班 —— 加时赛也有裁判。
func TestSlotJudgesOnExtraSlot(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")

	ev := mustEvent(t, svc, brainPlanetEvent())
	seat := mustSeat(t, svc, "1 号台", 1)
	ref, err := svc.IssueRefereeCode(ctx, "裁判A", model.RoleReferee, ev.ID, "小学组", &seat.ID)
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	extra, err := svc.CreateSlot(ctx, model.SlotDraft{
		SeatID: seat.ID, Period: "下午", TimeRange: "18:00–19:00",
		EventID: ev.ID, GroupCode: "小学组", Type: model.SlotExtra, RoundNo: 1,
	})
	if err != nil {
		t.Fatalf("建加时赛场次失败: %v", err)
	}
	if _, err := svc.SetSlotJudges(ctx, extra.ID, []int64{ref.ID}, "加时赛执裁"); err != nil {
		t.Fatalf("加时赛排班失败: %v", err)
	}
	got, err := svc.ListSlots(ctx, seat.ID, "")
	if err != nil {
		t.Fatalf("读场次失败: %v", err)
	}
	for _, sl := range got {
		if sl.ID == extra.ID && len(sl.Judges) != 1 {
			t.Errorf("加时赛场次的排班应落库: %+v", sl.Judges)
		}
	}
}
