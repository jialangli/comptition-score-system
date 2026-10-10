package service_test

import (
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// TestEventPhasesRoundTrip 阶段配置必须能存进去、读回来、改得动、清得掉。
//
// 守的是 2026-10-10 修掉的那类「静默丢配置」：events 表原先没有 phases 列，
// 配置经后端保存一次阶段就整个消失（**零报错**），而后端因为看不到阶段，
// 会把时间奖励按 120s（默认值）而不是各阶段之和（未来之城 225s）来算 —— 成绩直接偏掉。
//
// 这类 bug 只有「存进去 → 读回来」这一种测法能抓住：只测 RefTimeFor 抓不到，
// 因为那里的入参是内存里的结构体，根本走不到数据库。
func TestEventPhasesRoundTrip(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")

	ev := brainPlanetEvent()
	ev.Phases = []model.Phase{
		{
			ID: "auto", Name: "自动阶段", DurationSec: 120,
			TimerMode: "countdown", Source: "colorcard",
			Desc:      "选手刷色卡驱动小车自动完成动作",
			TimeBonus: map[string]any{"perSecond": 0.5, "cap": 10.0},
		},
		{ID: "manual", Name: "手动阶段", DurationSec: 105, TimerMode: "countdown", Source: "gamepad"},
	}

	created, err := svc.CreateEvent(ctx, ev)
	if err != nil {
		t.Fatalf("建赛项失败: %v", err)
	}
	if len(created.Phases) != 2 {
		t.Fatalf("建赛项返回的阶段数 = %d，期望 2", len(created.Phases))
	}

	// —— 读回来：字段一个都不能少 ——
	got, err := svc.GetEvent(ctx, ev.ID)
	if err != nil {
		t.Fatalf("读赛项失败: %v", err)
	}
	if len(got.Phases) != 2 {
		t.Fatalf("读回的阶段数 = %d，期望 2（阶段没落库 = 配置静默丢失）", len(got.Phases))
	}
	if got.Phases[0].ID != "auto" || got.Phases[0].Source != "colorcard" ||
		got.Phases[0].TimerMode != "countdown" || got.Phases[0].Desc == "" {
		t.Errorf("阶段元信息未完整往返：%+v", got.Phases[0])
	}
	if got.Phases[0].DurationSec != 120 || got.Phases[1].DurationSec != 105 {
		t.Errorf("阶段时长未完整往返：%v / %v", got.Phases[0].DurationSec, got.Phases[1].DurationSec)
	}
	if tb := got.Phases[0].TimeBonus; tb == nil || tb["perSecond"] != 0.5 {
		t.Errorf("阶段级时间奖励未完整往返：%+v", tb)
	}

	// —— 基准时长：读回来的赛项必须能推出「各阶段之和」——
	// 这是阶段唯一的算分影响面，也是当初两端分叉的地方。
	if s := got.PhaseTotalSec(); s != 225 {
		t.Errorf("读回后的阶段总时长 = %v，期望 225", s)
	}

	// —— 改配置：阶段能被改写 ——
	got.Phases = got.Phases[:1]
	got.Phases[0].DurationSec = 90
	if _, err := svc.UpdateEvent(ctx, got, "阶段时长调整"); err != nil {
		t.Fatalf("改配置失败: %v", err)
	}
	again, err := svc.GetEvent(ctx, ev.ID)
	if err != nil {
		t.Fatalf("改后读回失败: %v", err)
	}
	if len(again.Phases) != 1 || again.Phases[0].DurationSec != 90 {
		t.Errorf("阶段改动未落库：%+v", again.Phases)
	}

	// —— 清空：读回要是空数组，不是 nil ——
	// 库里是 `NOT NULL DEFAULT '[]'`，读点就应该只处理一种空态；
	// 若这里读回 nil，说明有别的写入路径把 phases 写成了 NULL。
	again.Phases = nil
	if _, err := svc.UpdateEvent(ctx, again, "清空阶段"); err != nil {
		t.Fatalf("清空阶段失败: %v", err)
	}
	final, err := svc.GetEvent(ctx, ev.ID)
	if err != nil {
		t.Fatalf("清空后读回失败: %v", err)
	}
	if len(final.Phases) != 0 {
		t.Errorf("清空后阶段数 = %d，期望 0", len(final.Phases))
	}
	if final.Phases == nil {
		t.Error("清空后应读回空数组 []（库里是 NOT NULL DEFAULT '[]'），实际为 nil")
	}
	if s := final.PhaseTotalSec(); s != 0 {
		t.Errorf("清空后阶段总时长 = %v，期望 0", s)
	}
}

// TestEventPhasesWarnOnBadDuration 阶段时长不为正只提醒、不阻断。
//
// 口径：阶段配错不会让赛项"不可用"，但会让时间奖励静默按另一个基准算，
// 所以必须是能看见的黄条，而不是保存失败。
func TestEventPhasesWarnOnBadDuration(t *testing.T) {
	svc, _ := newSvc(t)

	ev := brainPlanetEvent()
	ev.Phases = []model.Phase{{ID: "auto", Name: "自动阶段", DurationSec: 0}}
	res := svc.ValidateConfig(ev)
	if !res.OK() {
		t.Fatalf("阶段时长为 0 不应阻断保存：%v", res.ErrorMessages())
	}
	found := false
	for _, w := range res.Warnings {
		if w.Field == "phases" || w.Field == "phases[0].durationSec" {
			found = true
		}
	}
	if !found {
		t.Errorf("阶段时长不为正应给出提醒，实际提醒项：%+v", res.Warnings)
	}
}
