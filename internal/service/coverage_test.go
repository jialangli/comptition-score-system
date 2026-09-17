package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/service"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// mustSnapshots 读配置快照清单。
func mustSnapshots(t *testing.T, svc *service.Service) []model.ConfigSnapshot {
	t.Helper()
	snaps, err := svc.ListConfigSnapshots(context.Background(), 50)
	if err != nil {
		t.Fatalf("查快照失败: %v", err)
	}
	return snaps
}

// slotTeamIDs 读某场次的队伍绑定（顺手验证 ListSlots 也会带出队伍）。
func slotTeamIDs(t *testing.T, svc *service.Service, seatID int64, eventID string, slotID int64) []int64 {
	t.Helper()
	slots, err := svc.ListSlots(context.Background(), seatID, eventID)
	if err != nil {
		t.Fatalf("查场次失败: %v", err)
	}
	for i := range slots {
		if slots[i].ID == slotID {
			return slots[i].TeamIDs
		}
	}
	t.Fatalf("场次 %d 不在列表里", slotID)
	return nil
}

// ============================================================================
// 其余用例入口的覆盖测试
//
// 主流程测试证明「对的路走得通」，这里补的是「每条岔路都走到了」：
// 幂等、缺参数、非法组别、删除残留引用、无变化不留噪音……都是 API 层
// 会直接暴露给运营的分支，漏测等于把 Bug 留到现场。
// ============================================================================

// mustEvent 建一个赛项并返回。
func mustEvent(t *testing.T, svc *service.Service, ev *model.Event) *model.Event {
	t.Helper()
	created, err := svc.CreateEvent(operatorCtx("运营A"), ev)
	if err != nil {
		t.Fatalf("建赛项失败: %v", err)
	}
	return created
}

func mustTeam(t *testing.T, svc *service.Service, eventID, no, name, group string) *model.Team {
	t.Helper()
	created, err := svc.CreateTeam(operatorCtx("运营A"), model.TeamDraft{
		EventID: eventID, TeamNo: no, Name: name, GroupCode: group,
	})
	if err != nil {
		t.Fatalf("建队伍 %s 失败: %v", no, err)
	}
	return created
}

func auditCount(t *testing.T, svc *service.Service) int {
	t.Helper()
	logs, err := svc.AuditLogs(operatorCtx("运营A"), store.AuditFilter{Limit: 500})
	if err != nil {
		t.Fatalf("查审计失败: %v", err)
	}
	return len(logs)
}

func TestEventLifecycleAndSnapshot(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")

	// 校验接口（保存前预检）：不落库
	bad := brainPlanetEvent()
	bad.Tasks = nil
	bad.ScoreRule.Template = model.TplSum // 隔离「无任务项」这一条，避免同时触发权重提醒
	if res := svc.ValidateConfig(bad); res.OK() {
		t.Error("无任务项的配置应校验不通过")
	}
	if res := svc.ValidateConfig(brainPlanetEvent()); !res.OK() {
		t.Errorf("合规配置应通过：%v", res.ErrorMessages())
	}

	ev := mustEvent(t, svc, brainPlanetEvent())

	// 手工快照
	before := len(mustSnapshots(t, svc))
	if _, err := svc.CreateConfigSnapshot(ctx, "赛前定版"); err != nil {
		t.Fatalf("手工快照失败: %v", err)
	}
	if after := len(mustSnapshots(t, svc)); after != before+1 {
		t.Errorf("手工快照后数量 = %d，期望 %d", after, before+1)
	}

	// 列赛项 / 读赛项
	events, err := svc.ListEvents(ctx)
	if err != nil {
		t.Fatalf("列赛项失败: %v", err)
	}
	if len(events) != 1 || events[0].ID != ev.ID || len(events[0].Tasks) != 2 {
		t.Errorf("列表应带出任务项：%+v", events)
	}
	if _, err := svc.GetEvent(ctx, "不存在的赛项"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("读不存在的赛项应返回 ErrNotFound，实际 %v", err)
	}

	// 带队伍时删赛项必须被拦下
	team := mustTeam(t, svc, ev.ID, "1001", "星河队", "小学组")
	if err := svc.DeleteEvent(ctx, ev.ID); !errors.Is(err, store.ErrInUse) {
		t.Fatalf("有队伍的赛项删除应返回 ErrInUse，实际 %v", err)
	}

	// 清掉队伍后可以删，并留痕
	if err := svc.DeleteTeam(ctx, team.ID, "清场"); err != nil {
		t.Fatalf("删队伍失败: %v", err)
	}
	if err := svc.DeleteEvent(ctx, ev.ID); err != nil {
		t.Fatalf("删赛项失败: %v", err)
	}
	if _, err := svc.GetEvent(ctx, ev.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("删除后应查不到，实际 %v", err)
	}
	if err := svc.DeleteEvent(ctx, ev.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("重复删除应返回 ErrNotFound，实际 %v", err)
	}
}

func TestTeamServiceBranches(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")
	ev := mustEvent(t, svc, brainPlanetEvent())
	team := mustTeam(t, svc, ev.ID, "1001", "星河队", "小学组")

	t.Run("必填与合法性校验", func(t *testing.T) {
		if _, err := svc.CreateTeam(ctx, model.TeamDraft{
			EventID: ev.ID, TeamNo: "", Name: "无号队", GroupCode: "小学组",
		}); err == nil {
			t.Error("缺编号应报错")
		}
		if _, err := svc.CreateTeam(ctx, model.TeamDraft{
			EventID: ev.ID, TeamNo: "10A1", Name: "乱码队", GroupCode: "小学组",
		}); err == nil {
			t.Error("非纯数字编号应报错")
		}
		if _, err := svc.CreateTeam(ctx, model.TeamDraft{
			EventID: ev.ID, TeamNo: "1009", Name: "错组队", GroupCode: "高中组",
		}); err == nil {
			t.Error("组别不属于赛项应报错")
		}
		if _, err := svc.CreateTeam(ctx, model.TeamDraft{
			EventID: "不存在", TeamNo: "1010", Name: "孤队", GroupCode: "小学组",
		}); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("赛项不存在应返回 ErrNotFound，实际 %v", err)
		}
	})

	t.Run("弃赛与恢复的幂等与原因校验", func(t *testing.T) {
		if _, err := svc.WithdrawTeam(ctx, team.ID, ""); !errors.Is(err, service.ErrReasonRequired) {
			t.Errorf("弃赛未说明原因应返回 ErrReasonRequired，实际 %v", err)
		}
		before := auditCount(t, svc)
		if _, err := svc.WithdrawTeam(ctx, team.ID, "选手临时退赛"); err != nil {
			t.Fatalf("弃赛失败: %v", err)
		}
		// 重复弃赛：幂等，且不再产生第二条痕迹
		if _, err := svc.WithdrawTeam(ctx, team.ID, "再次弃赛"); err != nil {
			t.Fatalf("重复弃赛应幂等，实际 %v", err)
		}
		if after := auditCount(t, svc); after != before+1 {
			t.Errorf("重复弃赛不应重复留痕：%d → %d", before, after)
		}
		// 恢复
		if _, err := svc.RestoreTeam(ctx, team.ID, "误操作"); err != nil {
			t.Fatalf("恢复失败: %v", err)
		}
		before = auditCount(t, svc)
		if _, err := svc.RestoreTeam(ctx, team.ID, "再恢复一次"); err != nil {
			t.Fatalf("重复恢复应幂等，实际 %v", err)
		}
		if after := auditCount(t, svc); after != before {
			t.Errorf("对在册队伍恢复不应留痕：%d → %d", before, after)
		}
	})

	t.Run("改组需原因且组别必须合法", func(t *testing.T) {
		if _, err := svc.UpdateTeam(ctx, team.ID,
			model.Team{Name: "星河队", GroupCode: "初中组"}, ""); !errors.Is(err, service.ErrReasonRequired) {
			t.Errorf("改组未说明原因应返回 ErrReasonRequired，实际 %v", err)
		}
		if _, err := svc.UpdateTeam(ctx, team.ID,
			model.Team{Name: "星河队", GroupCode: "高中组"}, "试试看"); err == nil {
			t.Error("改成不属于本赛项的组别应报错")
		}
		if _, err := svc.UpdateTeam(ctx, team.ID,
			model.Team{Name: "", GroupCode: "小学组"}, "清空名称"); err == nil {
			t.Error("队伍名称为空应报错")
		}
		// 正常改组留「改组」痕迹
		before := auditCount(t, svc)
		if _, err := svc.UpdateTeam(ctx, team.ID,
			model.Team{Name: "星河队", GroupCode: "初中组", Members: "张一 / 李二"}, "现场核实组别"); err != nil {
			t.Fatalf("改组失败: %v", err)
		}
		logs, err := svc.AuditLogs(ctx, store.AuditFilter{Action: string(model.ActRegroup), Limit: 5})
		if err != nil {
			t.Fatalf("查审计失败: %v", err)
		}
		if len(logs) == 0 || logs[0].Before != "组别 小学组" || logs[0].After != "组别 初中组" {
			t.Errorf("改组痕迹的旧值/新值不对：%+v", logs)
		}
		if auditCount(t, svc) != before+1 {
			t.Error("改组应只留一条痕迹")
		}
		// 仅改信息（不改组别）按「改配置」留痕
		if _, err := svc.UpdateTeam(ctx, team.ID,
			model.Team{Name: "星河一队", GroupCode: "初中组", Members: "张一 / 李二"}, ""); err != nil {
			t.Fatalf("改信息失败: %v", err)
		}
		if got, _ := svc.GetTeam(ctx, team.ID); got.Name != "星河一队" {
			t.Errorf("名称未更新：%q", got.Name)
		}
	})

	t.Run("队伍查询与成绩查询", func(t *testing.T) {
		list, err := svc.ListTeams(ctx, ev.ID, true)
		if err != nil {
			t.Fatalf("列队伍失败: %v", err)
		}
		if len(list) != 1 {
			t.Errorf("应有 1 支队伍，实际 %d 支", len(list))
		}
		if _, err := svc.GetTeam(ctx, 999999); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("读不存在的队伍应返回 ErrNotFound，实际 %v", err)
		}
		if _, err := svc.ListScores(ctx, team.ID); err != nil {
			t.Fatalf("列成绩失败: %v", err)
		}
		if _, err := svc.GetScore(ctx, team.ID, 1); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("未录入的成绩应返回 ErrNotFound，实际 %v", err)
		}
		if _, err := svc.ScoresByEvent(ctx, ev.ID); err != nil {
			t.Fatalf("按赛项取成绩失败: %v", err)
		}
	})
}

func TestScoreServiceBranches(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("裁判A")
	ev := mustEvent(t, svc, brainPlanetEvent())
	team := mustTeam(t, svc, ev.ID, "1001", "星河队", "小学组")

	t.Run("轮次越界", func(t *testing.T) {
		_, err := svc.SaveScore(ctx, &model.ScoreRecord{
			TeamID: team.ID, RoundNo: 3,
			TaskValues: map[string]any{"focus": 80.0, "build": 80.0},
		})
		if err == nil {
			t.Error("轮次 3 应被拒绝")
		}
		if _, err := svc.SaveScore(ctx, &model.ScoreRecord{
			TeamID: 999999, RoundNo: 1, TaskValues: map[string]any{"focus": 80.0},
		}); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("队伍不存在应返回 ErrNotFound，实际 %v", err)
		}
	})

	// 录第一轮（未签字）
	rec, err := svc.SaveScore(ctx, &model.ScoreRecord{
		TeamID: team.ID, RoundNo: 1, DurationSec: 100,
		TaskValues: map[string]any{"focus": 80.0, "build": 80.0},
	})
	if err != nil {
		t.Fatalf("录分失败: %v", err)
	}
	if rec.Operator != "裁判A" {
		t.Errorf("记分员应从 context 取，实际 %q", rec.Operator)
	}

	t.Run("未签字可反复编辑且每次都留痕", func(t *testing.T) {
		before := auditCount(t, svc)
		if _, err := svc.SaveScore(ctx, &model.ScoreRecord{
			TeamID: team.ID, RoundNo: 1, DurationSec: 95,
			TaskValues: map[string]any{"focus": 90.0, "build": 90.0},
		}); err != nil {
			t.Fatalf("草稿编辑失败: %v", err)
		}
		logs, err := svc.AuditLogs(ctx, store.AuditFilter{Action: string(model.ActScore), Limit: 5})
		if err != nil {
			t.Fatalf("查审计失败: %v", err)
		}
		if len(logs) == 0 || logs[0].Reason != "同轮草稿编辑（成绩未签字）" {
			t.Errorf("草稿编辑应留痕并写明原因：%+v", logs)
		}
		if auditCount(t, svc) != before+1 {
			t.Error("每次编辑都应留一条痕迹")
		}
	})

	t.Run("改分申请与授权的参数校验", func(t *testing.T) {
		if _, err := svc.ScoreChangeRequest(ctx, team.ID, 1, 99, ""); !errors.Is(err, service.ErrReasonRequired) {
			t.Errorf("改分未说明原因应返回 ErrReasonRequired，实际 %v", err)
		}
		if _, err := svc.ScoreChangeRequest(ctx, team.ID, 2, 99, "申请一个不存在的轮次"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("轮次无记录应返回 ErrNotFound，实际 %v", err)
		}
		cur, err := svc.GetScore(ctx, team.ID, 1)
		if err != nil {
			t.Fatalf("查成绩失败: %v", err)
		}
		if _, err := svc.ApplyScoreChange(ctx, cur, "", "忘了写授权人"); err == nil {
			t.Error("缺授权人应被拒绝")
		}
		if _, err := svc.ApplyScoreChange(ctx, cur, "裁判长C", ""); !errors.Is(err, service.ErrReasonRequired) {
			t.Errorf("改分未说明原因应返回 ErrReasonRequired，实际 %v", err)
		}
	})
}

func TestScheduleServiceBranches(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")
	ev := mustEvent(t, svc, brainPlanetEvent())
	t1 := mustTeam(t, svc, ev.ID, "1001", "星河队", "小学组")
	t2 := mustTeam(t, svc, ev.ID, "1002", "追光队", "小学组")

	seat, err := svc.CreateSeat(ctx, "赛台 1", 0)
	if err != nil {
		t.Fatalf("建赛台失败: %v", err)
	}
	if _, err := svc.CreateSeat(ctx, "  ", 0); err == nil {
		t.Error("空白赛台名应被拒绝")
	}
	if _, err := svc.CreateSeat(ctx, "赛台 2", 1); err != nil {
		t.Fatalf("建赛台失败: %v", err)
	}
	seats, err := svc.ListSeats(ctx)
	if err != nil {
		t.Fatalf("列赛台失败: %v", err)
	}
	if len(seats) != 2 || seats[0].SortOrder > seats[1].SortOrder {
		t.Errorf("赛台应按 sort_order 排列：%+v", seats)
	}
	if _, err := svc.UpdateSeat(ctx, seat.ID, "赛台 A", 5); err != nil {
		t.Fatalf("改赛台失败: %v", err)
	}
	// 名称为空时保留原名
	updated, err := svc.UpdateSeat(ctx, seat.ID, "", 0)
	if err != nil {
		t.Fatalf("改赛台失败: %v", err)
	}
	if updated.Name != "赛台 A" {
		t.Errorf("空名称不应覆盖原名称，实际 %q", updated.Name)
	}

	t.Run("场次参数校验", func(t *testing.T) {
		if _, err := svc.CreateSlot(ctx, model.SlotDraft{SeatID: seat.ID}); err == nil {
			t.Error("缺参数应被拒绝")
		}
		if _, err := svc.CreateSlot(ctx, model.SlotDraft{
			SeatID: seat.ID, Period: "上午", EventID: ev.ID,
			GroupCode: "高中组", Type: model.SlotNormal,
		}); err == nil {
			t.Error("组别不属于赛项应被拒绝")
		}
		if _, err := svc.CreateSlot(ctx, model.SlotDraft{
			SeatID: 999999, Period: "上午", EventID: ev.ID,
			GroupCode: "小学组", Type: model.SlotNormal,
		}); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("赛台不存在应返回 ErrNotFound，实际 %v", err)
		}
		if _, err := svc.CreateSlot(ctx, model.SlotDraft{
			SeatID: seat.ID, Period: "上午", EventID: ev.ID,
			GroupCode: "小学组", Type: model.SlotType("weird"),
		}); err == nil {
			t.Error("非法场次类型应被拒绝")
		}
	})

	slot, err := svc.CreateSlot(ctx, model.SlotDraft{
		SeatID: seat.ID, Period: "上午", TimeRange: "09:00–12:00",
		EventID: ev.ID, GroupCode: "小学组", Type: model.SlotNormal,
	})
	if err != nil {
		t.Fatalf("建场次失败: %v", err)
	}

	t.Run("手动改派与查询", func(t *testing.T) {
		if err := svc.AssignSlotTeams(ctx, slot.ID, []int64{t1.ID}, "现场手动调整"); err != nil {
			t.Fatalf("改派失败: %v", err)
		}
		got := slotTeamIDs(t, svc, seat.ID, ev.ID, slot.ID)
		if len(got) != 1 || got[0] != t1.ID {
			t.Errorf("改派结果 = %v，期望 [%d]", got, t1.ID)
		}
		// 改派留「调赛台」痕迹
		logs, err := svc.AuditLogs(ctx, store.AuditFilter{Action: string(model.ActSeat), Limit: 3})
		if err != nil {
			t.Fatalf("查审计失败: %v", err)
		}
		if len(logs) == 0 || logs[0].Reason != "现场手动调整" {
			t.Errorf("改派应留痕：%+v", logs)
		}
		// 自动分配会把同组两支队都铺进来
		if _, err := svc.AutoAssignSlot(ctx, slot.ID, ""); err != nil {
			t.Fatalf("自动分配失败: %v", err)
		}
		got = slotTeamIDs(t, svc, seat.ID, ev.ID, slot.ID)
		if len(got) != 2 {
			t.Errorf("自动分配后应有 2 支队伍，实际 %v", got)
		}
		seen := map[int64]bool{}
		for _, id := range got {
			seen[id] = true
		}
		if !seen[t1.ID] || !seen[t2.ID] {
			t.Errorf("自动分配应覆盖同组全部队伍，实际 %v（期望含 %d、%d）", got, t1.ID, t2.ID)
		}
	})

	t.Run("删场次与删赛台", func(t *testing.T) {
		// 有场次时删赛台必须先说明原因
		if err := svc.DeleteSeat(ctx, seat.ID, ""); !errors.Is(err, service.ErrReasonRequired) {
			t.Errorf("赛台下有场次时删赛台应要求说明原因，实际 %v", err)
		}
		if err := svc.DeleteSlot(ctx, slot.ID, "场次取消"); err != nil {
			t.Fatalf("删场次失败: %v", err)
		}
		if _, err := svc.ListSlots(ctx, seat.ID, ev.ID); err != nil {
			t.Fatalf("查场次失败: %v", err)
		}
		// 场次清空后可直接删赛台
		if err := svc.DeleteSeat(ctx, seat.ID, ""); err != nil {
			t.Fatalf("删赛台失败: %v", err)
		}
		if err := svc.DeleteSeat(ctx, seat.ID, ""); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("重复删赛台应返回 ErrNotFound，实际 %v", err)
		}
	})

	t.Run("快照参数校验", func(t *testing.T) {
		extraSlot, err := svc.CreateSlot(ctx, model.SlotDraft{
			SeatID: seats[1].ID, Period: "下午", TimeRange: "14:00–17:00",
			EventID: ev.ID, GroupCode: "小学组", Type: model.SlotExtra,
		})
		if err != nil {
			t.Fatalf("建加时赛场次失败: %v", err)
		}
		if _, err := svc.SaveSnapshot(ctx, extraSlot.ID, []model.Snapshot{
			{TeamNo: "abc", Name: "编号不合法"},
		}, ""); err == nil {
			t.Error("非纯数字编号应被拒绝")
		}
		if _, err := svc.SaveSnapshot(ctx, extraSlot.ID, []model.Snapshot{
			{TeamNo: "9001", Name: "  "},
		}, ""); err == nil {
			t.Error("空队名应被拒绝")
		}
		if _, err := svc.SaveSnapshot(ctx, extraSlot.ID, []model.Snapshot{
			{TeamNo: "9001", Name: "新星队", School: "成都", Coach: "陈老师"},
		}, ""); err != nil {
			t.Fatalf("写快照失败: %v", err)
		}
		snaps, err := svc.ListSnapshot(ctx, extraSlot.ID)
		if err != nil {
			t.Fatalf("读快照失败: %v", err)
		}
		if len(snaps) != 1 || snaps[0].TeamNo != "9001" {
			t.Errorf("快照内容不对：%+v", snaps)
		}
	})
}

func TestScreenConfigDefaultsAndNoNoise(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")
	ev := mustEvent(t, svc, brainPlanetEvent())

	// 未配置过也要能拿到默认值 —— 大屏不能因为缺一行配置而白屏
	cfg, err := svc.GetScreenConfig(ctx, ev.ID)
	if err != nil {
		t.Fatalf("读大屏配置失败: %v", err)
	}
	if cfg.PageSize != model.DefaultPageSize || cfg.IntervalSec != model.DefaultIntervalSec {
		t.Errorf("默认值不对：%+v", cfg)
	}

	// 非法值被钳制
	cfg.PageSize = 999
	cfg.IntervalSec = 1
	if _, err := svc.UpdateScreenConfig(ctx, cfg, "填了非法值"); err != nil {
		t.Fatalf("改配置失败: %v", err)
	}
	cfg, err = svc.GetScreenConfig(ctx, ev.ID)
	if err != nil {
		t.Fatalf("读大屏配置失败: %v", err)
	}
	if cfg.PageSize != 50 || cfg.IntervalSec != model.DefaultIntervalSec {
		t.Errorf("越界值应被钳制：%+v", cfg)
	}

	// 无实质变化不留噪音
	before := auditCount(t, svc)
	if _, err := svc.UpdateScreenConfig(ctx, cfg, "重复提交同样的值"); err != nil {
		t.Fatalf("改配置失败: %v", err)
	}
	if after := auditCount(t, svc); after != before {
		t.Errorf("无变化不应留痕：%d → %d", before, after)
	}

	// 锁定会留痕（现场「屏幕被谁定住了」必须可回答）
	if _, err := svc.UpdateScreenConfig(ctx, cfg, ""); err != nil {
		t.Fatalf("改配置失败: %v", err)
	}
	cfg.Pinned = "1001"
	if _, err := svc.UpdateScreenConfig(ctx, cfg, "现场异常"); err != nil {
		t.Fatalf("锁定失败: %v", err)
	}
	if after := auditCount(t, svc); after != before+1 {
		t.Errorf("锁定应留一条痕迹：%d → %d", before, after)
	}
}

func TestRestoreSnapshotErrorPaths(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")
	ev := mustEvent(t, svc, brainPlanetEvent())

	if err := svc.RestoreConfigSnapshot(ctx, 999999, ""); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("回滚不存在的快照应返回 ErrNotFound，实际 %v", err)
	}

	// 快照里没有赛项 → 拒绝回滚（宁可不回滚，也不能把空配置写进库里）
	emptyID, err := svc.Store().Repos().CfgSnaps.Create(ctx, "空快照", map[string]any{"schemaVersion": "v1.0"})
	if err != nil {
		t.Fatalf("造空快照失败: %v", err)
	}
	if err := svc.RestoreConfigSnapshot(ctx, emptyID, "试试空快照"); err == nil {
		t.Error("空快照应被拒绝回滚")
	}
	// 被拒绝后原配置必须原样
	after, err := svc.GetEvent(ctx, ev.ID)
	if err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if after.Name != ev.Name || len(after.Tasks) != 2 {
		t.Errorf("被拒绝的回滚不应改动配置：%+v", after)
	}

	// limit<=0 时走默认值
	if _, err := svc.ListConfigSnapshots(ctx, 0); err != nil {
		t.Fatalf("列快照失败: %v", err)
	}
}

func TestStandingsOptions(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")
	ev := mustEvent(t, svc, brainPlanetEvent())

	if _, err := svc.Standings(ctx, "不存在", service.StandingsOptions{}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("赛项不存在应返回 ErrNotFound，实际 %v", err)
	}

	junior := mustTeam(t, svc, ev.ID, "1003", "晨曦队", "初中组")
	senior := mustTeam(t, svc, ev.ID, "1001", "星河队", "小学组")
	if _, err := svc.SaveScore(ctx, &model.ScoreRecord{
		TeamID: junior.ID, RoundNo: 1, DurationSec: 90, Signed: true,
		TaskValues: map[string]any{"focus": 90.0, "build": 90.0},
	}); err != nil {
		t.Fatalf("录分失败: %v", err)
	}
	// 小学组这支只录了未签字的一轮
	if _, err := svc.SaveScore(ctx, &model.ScoreRecord{
		TeamID: senior.ID, RoundNo: 1, DurationSec: 100, Signed: false,
		TaskValues: map[string]any{"focus": 95.0, "build": 95.0},
	}); err != nil {
		t.Fatalf("录分失败: %v", err)
	}

	// 指定组别：只返回该组
	one, err := svc.Standings(ctx, ev.ID, service.StandingsOptions{Group: "初中组"})
	if err != nil {
		t.Fatalf("出榜单失败: %v", err)
	}
	if len(one.Groups) != 1 || one.Groups[0].Group != "初中组" || len(one.Groups[0].Rows) != 1 {
		t.Errorf("指定组别应只返回该组：%+v", one.Groups)
	}

	// 仅统计已签字：小学组那支的未签字成绩应被剔除（待打分状态）
	signed, err := svc.Standings(ctx, ev.ID, service.StandingsOptions{OnlySigned: true})
	if err != nil {
		t.Fatalf("出榜单失败: %v", err)
	}
	for _, grp := range signed.Groups {
		for _, row := range grp.Rows {
			if row.Team.ID == senior.ID && row.Result.Total != 0 {
				t.Errorf("仅统计已签字时不该算入未签字成绩，实际 %v", row.Result.Total)
			}
		}
	}

	// 严格获奖资格：未完成录入的队伍不获奖
	strict, err := svc.Standings(ctx, ev.ID, service.StandingsOptions{AwardOnlyComplete: true})
	if err != nil {
		t.Fatalf("出榜单失败: %v", err)
	}
	for _, grp := range strict.Groups {
		for _, row := range grp.Rows {
			if !row.Result.Complete && row.Award != "" {
				t.Errorf("未完成录入的队伍不应获奖：%s 得了 %s", row.Team.Name, row.Award)
			}
		}
	}

	// 弃赛队伍默认不参与
	if _, err := svc.WithdrawTeam(ctx, senior.ID, "退赛"); err != nil {
		t.Fatalf("弃赛失败: %v", err)
	}
	after, err := svc.Standings(ctx, ev.ID, service.StandingsOptions{})
	if err != nil {
		t.Fatalf("出榜单失败: %v", err)
	}
	for _, grp := range after.Groups {
		for _, row := range grp.Rows {
			if row.Team.ID == senior.ID {
				t.Error("弃赛队伍默认不应出现在榜单里")
			}
		}
	}
	// 显式要求包含时可以看到
	withdrawn, err := svc.Standings(ctx, ev.ID, service.StandingsOptions{IncludeWithdrawn: true})
	if err != nil {
		t.Fatalf("出榜单失败: %v", err)
	}
	found := false
	for _, grp := range withdrawn.Groups {
		for _, row := range grp.Rows {
			if row.Team.ID == senior.ID {
				found = true
			}
		}
	}
	if !found {
		t.Error("IncludeWithdrawn 时弃赛队伍应出现在榜单里")
	}
}

func TestImportEdgeCases(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")
	ev := mustEvent(t, svc, brainPlanetEvent())

	// 空文件
	empty, err := svc.PreviewImport(ctx, ev.ID, nil)
	if err != nil {
		t.Fatalf("空预览失败: %v", err)
	}
	if len(empty.Rows) != 0 || empty.Summary.Insert != 0 {
		t.Errorf("空文件应产出空结果：%+v", empty)
	}
	if _, err := svc.CommitImport(ctx, ev.ID, nil, []int{1}, "空提交"); err != nil {
		t.Fatalf("空文件提交不应报错: %v", err)
	}

	// 赛项不存在
	if _, err := svc.PreviewImport(ctx, "不存在", nil); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("赛项不存在应返回 ErrNotFound，实际 %v", err)
	}

	// 行号缺省时按序号兜底（解析层没给行号的情况）
	rows := []service.ImportRow{
		{TeamNo: "1001", Name: "星河队", Group: "小学组"},
		{TeamNo: "1002", Name: "追光队", Group: "小学组"},
	}
	preview, err := svc.PreviewImport(ctx, ev.ID, rows)
	if err != nil {
		t.Fatalf("预览失败: %v", err)
	}
	if preview.Rows[0].Line != 1 || preview.Rows[1].Line != 2 {
		t.Errorf("缺省行号应按序号 1、2 兜底，实际 %d、%d", preview.Rows[0].Line, preview.Rows[1].Line)
	}
	if _, err := svc.CommitImport(ctx, ev.ID, rows, []int{1, 2}, "首次导入"); err != nil {
		t.Fatalf("提交失败: %v", err)
	}

	// 勾选「无变化」的行：不报错，但记录为 ignored，且不重复写库
	logEntry, err := svc.CommitImport(ctx, ev.ID, rows, []int{1}, "重复提交")
	if err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	if logEntry.Summary.Skip != 2 {
		t.Errorf("第二次预览应全是无变化，实际 %+v", logEntry.Summary)
	}
	if len(logEntry.Detail) == 0 {
		t.Error("勾选无变化行也应在明细里留一条说明")
	}
	if teams, _ := svc.ListTeams(ctx, ev.ID, true); len(teams) != 2 {
		t.Errorf("不应重复插入队伍，实际 %d 支", len(teams))
	}
}
