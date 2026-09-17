package engine

import (
	"strings"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
)

func strPtr(s string) *string { return &s }

// validEvent 一份完全合规的配置：基线断言应为 0 error / 0 warning。
func validEvent() *model.Event {
	return &model.Event{
		ID:     "ev",
		Name:   "测试赛项",
		Groups: []string{"小学组", "初中组"},
		Tasks: []model.Task{
			numTask("focus", 100, 0.6),
			numTask("build", 100, 0.4),
		},
		ScoreRule: model.ScoreRule{Template: model.TplWeightedSum},
		BonusRules: []model.BonusRule{
			{Template: model.BonusTime, Params: map[string]any{"perSecond": 0.5, "cap": 10.0}},
		},
		PenaltyRule: model.PenaltyRule{Template: model.PenaltyPerCard, Params: map[string]any{"yellow": 5.0, "red": 15.0}},
		RankRule: model.RankRule{
			TieBreak:   []string{"score", "time"},
			AwardTiers: map[string]float64{"一等奖": 0.1, "二等奖": 0.2, "三等奖": 0.3},
		},
	}
}

func TestValidateEvent(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(*model.Event)
		nilEvent    bool
		wantErrs    int
		wantWarns   int
		errContains []string
		warnContain []string
	}{
		{name: "合规配置无任何问题", mutate: func(*model.Event) {}},

		{name: "赛项为空", nilEvent: true, wantErrs: 1, errContains: []string{"赛项不存在"}},

		{name: "赛项 ID 为空", mutate: func(e *model.Event) { e.ID = "" }, wantErrs: 1, errContains: []string{"赛项 ID 不能为空"}},

		{name: "赛项名称为空", mutate: func(e *model.Event) { e.Name = "  " }, wantErrs: 1, errContains: []string{"赛项名称不能为空"}},

		{name: "未配置组别", mutate: func(e *model.Event) { e.Groups = nil }, wantErrs: 1, errContains: []string{"至少需要配置一个组别"}},

		{name: "存在空白组别", mutate: func(e *model.Event) { e.Groups = []string{"小学组", ""} }, wantErrs: 1, errContains: []string{"空白组别"}},

		{name: "组别重复仅提醒", mutate: func(e *model.Event) { e.Groups = []string{"小学组", "小学组"} }, wantWarns: 1, warnContain: []string{"重复配置"}},

		{
			name: "未配置任务项",
			mutate: func(e *model.Event) {
				e.Tasks = nil
				e.ScoreRule.Template = model.TplSum // 避免同时触发权重和提醒，隔离单一结论
			},
			wantErrs: 1, errContains: []string{"至少需要配置一个任务项"},
		},

		{name: "任务 id 为空", mutate: func(e *model.Event) { e.Tasks[0].ID = "" }, wantErrs: 1, errContains: []string{"id 不能为空"}},

		{name: "任务 id 重复", mutate: func(e *model.Event) { e.Tasks[1].ID = "focus" }, wantErrs: 1, errContains: []string{"任务 id 重复：focus"}},

		{name: "任务未命名", mutate: func(e *model.Event) { e.Tasks[1].Name = "" }, wantErrs: 1, errContains: []string{"未命名"}},

		{
			name: "任务类型非法",
			mutate: func(e *model.Event) {
				e.Tasks[1].Type = model.TaskType("weird")
				e.ScoreRule.Template = model.TplSum
			},
			wantErrs: 1, errContains: []string{"评分方式"},
		},

		{
			name:      "数值任务未设满分仅提醒",
			mutate:    func(e *model.Event) { e.Tasks[0].MaxScore = nil },
			wantWarns: 1, warnContain: []string{"未设满分"},
		},

		{
			name:      "数值任务满分非正仅提醒",
			mutate:    func(e *model.Event) { e.Tasks[0].MaxScore = f64ptr(0) },
			wantWarns: 1, warnContain: []string{"满分应大于 0"},
		},

		{
			name: "等级任务缺少映射表",
			mutate: func(e *model.Event) {
				e.Tasks[0] = enumTask("focus", nil, 0.6)
				e.ScoreRule.Template = model.TplSum
			},
			wantErrs: 1, errContains: []string{"缺少等级映射表"},
		},

		{
			name: "计数任务单价为 0 仅提醒",
			mutate: func(e *model.Event) {
				e.Tasks[1] = countTask("build", 0)
				e.ScoreRule.Template = model.TplSum
			},
			wantWarns: 1, warnContain: []string{"每单位分为 0"},
		},

		{name: "权重之和不等于 1 仅提醒", mutate: func(e *model.Event) { e.Tasks[1].Weight = 0.5 }, wantWarns: 1, warnContain: []string{"权重之和"}},

		{name: "计分模板非法", mutate: func(e *model.Event) { e.ScoreRule.Template = model.ScoreTemplate("weird") }, wantErrs: 1, errContains: []string{"计分模板"}},

		{name: "奖励模板非法", mutate: func(e *model.Event) { e.BonusRules[0].Template = model.BonusTemplate("weird") }, wantErrs: 1, errContains: []string{"不是有效值"}},

		{
			name:      "时间奖励未配每秒加分仅提醒",
			mutate:    func(e *model.Event) { e.BonusRules[0].Params = map[string]any{"cap": 10.0} },
			wantWarns: 1, warnContain: []string{"每秒加分"},
		},

		{
			name:      "时间奖励封顶为负数仅提醒",
			mutate:    func(e *model.Event) { e.BonusRules[0].Params = map[string]any{"perSecond": 0.5, "cap": -1.0} },
			wantWarns: 1, warnContain: []string{"封顶值为负数"},
		},

		{
			name: "计数奖励未指定计数项",
			mutate: func(e *model.Event) {
				e.BonusRules = []model.BonusRule{{Template: model.BonusCount, Params: map[string]any{"perUnit": 5.0}}}
			},
			wantErrs: 1, errContains: []string{"未指定计数项"},
		},

		{
			name: "计数项未在任务列表声明（派生项）仅提醒",
			mutate: func(e *model.Event) {
				e.BonusRules = []model.BonusRule{{Template: model.BonusCount, Params: map[string]any{"taskId": "energy", "perUnit": 5.0}}}
			},
			wantWarns: 1, warnContain: []string{"派生计数项"},
		},

		{
			name: "计数奖励单价为 0 仅提醒",
			mutate: func(e *model.Event) {
				e.BonusRules = []model.BonusRule{{Template: model.BonusCount, Params: map[string]any{"taskId": "focus", "perUnit": 0.0}}}
			},
			wantWarns: 1, warnContain: []string{"每单位分为 0"},
		},

		{name: "扣分模板非法", mutate: func(e *model.Event) { e.PenaltyRule.Template = model.PenaltyTemplate("weird") }, wantErrs: 1, errContains: []string{"扣分模板"}},

		{
			name:      "按牌扣分但扣分值全为 0 仅提醒",
			mutate:    func(e *model.Event) { e.PenaltyRule.Params = map[string]any{"yellow": 0.0, "red": 0.0} },
			wantWarns: 1, warnContain: []string{"均为 0"},
		},

		{name: "同分裁决项非法", mutate: func(e *model.Event) { e.RankRule.TieBreak = []string{"score", "pace"} }, wantErrs: 1, errContains: []string{"同分裁决项"}},

		{name: "未配置同分裁决仅提醒", mutate: func(e *model.Event) { e.RankRule.TieBreak = nil }, wantWarns: 1, warnContain: []string{"未配置同分裁决项"}},

		{
			name:     "奖项占比为负",
			mutate:   func(e *model.Event) { e.RankRule.AwardTiers = map[string]float64{"一等奖": -0.1, "二等奖": 0.2} },
			wantErrs: 1, errContains: []string{"不能为负数"},
		},

		{
			name: "奖项占比之和超过 1 仅提醒",
			mutate: func(e *model.Event) {
				e.RankRule.AwardTiers = map[string]float64{"一等奖": 0.5, "二等奖": 0.5, "三等奖": 0.5}
			},
			wantWarns: 1, warnContain: []string{"不超过 1.0"},
		},

		{
			name:      "奖项占比全为 0 仅提醒",
			mutate:    func(e *model.Event) { e.RankRule.AwardTiers = map[string]float64{"一等奖": 0, "二等奖": 0} },
			wantWarns: 1, warnContain: []string{"不分配奖项"},
		},

		{name: "未配置奖项仅提醒", mutate: func(e *model.Event) { e.RankRule.AwardTiers = nil }, wantWarns: 1, warnContain: []string{"未配置任何奖项"}},

		{name: "customFormula 非空", mutate: func(e *model.Event) { e.CustomFormula = strPtr("return 0;") }, wantErrs: 1, errContains: []string{"customFormula"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var ev *model.Event
			if !tc.nilEvent {
				ev = validEvent()
				if tc.mutate != nil {
					tc.mutate(ev)
				}
			}
			res := ValidateEvent(ev)

			if len(res.Errors) != tc.wantErrs {
				t.Errorf("错误数 = %d，期望 %d；实际：%v", len(res.Errors), tc.wantErrs, res.ErrorMessages())
			}
			if len(res.Warnings) != tc.wantWarns {
				t.Errorf("提醒数 = %d，期望 %d；实际：%v", len(res.Warnings), tc.wantWarns, res.WarningMessages())
			}
			for _, sub := range tc.errContains {
				if !containsSub(res.ErrorMessages(), sub) {
					t.Errorf("错误信息中未包含 %q；实际：%v", sub, res.ErrorMessages())
				}
			}
			for _, sub := range tc.warnContain {
				if !containsSub(res.WarningMessages(), sub) {
					t.Errorf("提醒信息中未包含 %q；实际：%v", sub, res.WarningMessages())
				}
			}
			if tc.wantErrs > 0 && res.OK() {
				t.Error("OK() 应为 false")
			}
			if tc.wantErrs == 0 && !res.OK() {
				t.Error("OK() 应为 true")
			}
		})
	}
}

func TestValidateEventIssueMetadata(t *testing.T) {
	ev := validEvent()
	ev.Tasks[1].ID = "focus"
	res := ValidateEvent(ev)

	if len(res.Errors) != 1 {
		t.Fatalf("期望 1 个错误，实际 %d", len(res.Errors))
	}
	issue := res.Errors[0]
	if issue.Level != LevelError {
		t.Errorf("Level = %q，期望 %q", issue.Level, LevelError)
	}
	if issue.Field != "tasks[1].id" {
		t.Errorf("Field = %q，期望 tasks[1].id（供前端定位高亮）", issue.Field)
	}
	if issue.Msg == "" {
		t.Error("Msg 不应为空")
	}
}

func TestValidateEventIsDeterministic(t *testing.T) {
	// 奖项映射是 map，若直接遍历会让同一配置每次报错顺序不同
	ev := validEvent()
	ev.RankRule.AwardTiers = map[string]float64{"三等奖": -0.1, "一等奖": -0.2, "二等奖": -0.3}
	first := ValidateEvent(ev).ErrorMessages()
	for i := 0; i < 100; i++ {
		got := ValidateEvent(ev).ErrorMessages()
		if len(got) != len(first) {
			t.Fatalf("第 %d 次运行结论数量变化：%v vs %v", i, got, first)
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("第 %d 次运行结论顺序变化：%v vs %v", i, got, first)
			}
		}
	}
}

func containsSub(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
