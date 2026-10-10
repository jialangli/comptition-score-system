package service_test

import (
	"testing"

	"github.com/jialangli/comptition-score-server/internal/engine"
	"github.com/jialangli/comptition-score-server/internal/model"
)

// TestTaskUnitRoundTrip 量词（题卡上的「每颗 +100」）要能存进去、读回来，**且一个字都不影响算分**。
//
// 守的是「静默丢配置」那一类：tasks 表原先没有 unit 列，配置经后端保存一次量词就整个消失
// （零报错），而打分页只会回落成「每单位 N 分」—— 现场看到的现象是
// 「我在后台明明填了『颗』，裁判端就是不显示」，谁都没报错，也没人知道该找谁。
//
// 这类 bug 只有「存进去 → 读回来」这一种测法能抓住：只测 model 结构或 engine 纯函数都抓不到，
// 因为那些入参是内存里的结构体，根本走不到数据库。
func TestTaskUnitRoundTrip(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")

	ev := brainPlanetEvent()
	ev.ScoreRule.Template = model.TplSum // 本用例与权重归一无关，避开加权求和那条提醒
	ev.Tasks = []model.Task{
		{ID: "ball", Name: "能源球运输", Type: model.TaskCount, MaxScore: fptr(160), Weight: 20, Control: model.CtrlCounter, Unit: "颗"},
		// 故意不配量词：它必须原样读回**空串**，而不是 "null" / 空格 ——
		// 空态只留一种，界面才能安全地用它决定「回落成『每单位』」。
		{ID: "mine", Name: "矿石运输入仓", Type: model.TaskCount, MaxScore: fptr(90), Weight: 15, Control: model.CtrlCounter},
	}
	if _, err := svc.CreateEvent(ctx, ev); err != nil {
		t.Fatalf("建赛项失败: %v", err)
	}

	got, err := svc.GetEvent(ctx, ev.ID)
	if err != nil {
		t.Fatalf("读赛项失败: %v", err)
	}
	if len(got.Tasks) != 2 {
		t.Fatalf("任务数 = %d，期望 2", len(got.Tasks))
	}
	if got.Tasks[0].Unit != "颗" {
		t.Errorf("配了量词的任务读回 = %q，期望「颗」（列没加 / 写入漏了 / scan 没接）", got.Tasks[0].Unit)
	}
	if got.Tasks[1].Unit != "" {
		t.Errorf("未配量词的任务读回 = %q，期望空串（空态应当只有一种）", got.Tasks[1].Unit)
	}
	// 其余字段不能被这次加列带坏（列顺序与 scan 顺序是一一对应的，错位会静默换值）。
	if got.Tasks[0].Name != "能源球运输" || got.Tasks[0].Weight != 20 || got.Tasks[0].Control != model.CtrlCounter {
		t.Errorf("同一行的其它字段被串位了：%+v", got.Tasks[0])
	}

	// —— 量词不参与算分：把量词清空后再算一遍，总分必须一模一样 ——
	rec := model.ScoreRecord{RoundNo: 1, TaskValues: map[string]any{"ball": 3.0, "mine": 2.0}, DurationSec: 100}
	withUnit := engine.Score(got, rec, 0)

	cleared := *got
	cleared.Tasks = append([]model.Task(nil), got.Tasks...)
	for i := range cleared.Tasks {
		cleared.Tasks[i].Unit = ""
	}
	withoutUnit := engine.Score(&cleared, rec, 0)

	// 数字分开写，让「总分为什么是 100」在测试里也说得清：
	//   基础分 = 数量 × 每单位分 = 3×20 + 2×15 = 90
	//   时间奖励 = (赛项总时长 120s − 本队用时 100s) × 0.5 = 10（封顶 10）
	// 只写「期望 100」的话，下一个人改了本用例的用时就会一头雾水。
	const wantBase = 3*20 + 2*15
	if withUnit.Base != wantBase {
		t.Errorf("基础分 = %v，期望 %v（数量 × 每单位分）", withUnit.Base, wantBase)
	}
	if withUnit.Bonus != 10 {
		t.Errorf("时间奖励 = %v，期望 10（基准 120s − 用时 100s，每秒 0.5，封顶 10）", withUnit.Bonus)
	}
	if withUnit.Total != withoutUnit.Total {
		t.Errorf("量词影响了算分：带量词 %v / 不带 %v —— 量词只是文案，绝不能进公式",
			withUnit.Total, withoutUnit.Total)
	}
}
