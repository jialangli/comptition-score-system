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
			name: "是否完成任务合法（评分方式三类之一，不报错）",
			mutate: func(e *model.Event) {
				e.Tasks[1] = toggleTask("build", f64ptr(500))
				e.ScoreRule.Template = model.TplSum // 避免同时触发权重和提醒，隔离单一结论
			},
		},

		{
			name: "是否完成任务未设满分仅提醒",
			mutate: func(e *model.Event) {
				e.Tasks[1] = toggleTask("build", nil)
				e.ScoreRule.Template = model.TplSum
			},
			wantWarns: 1, warnContain: []string{"完成也不会得分"},
		},

		{
			name: "是否完成任务满分为 0 仅提醒",
			mutate: func(e *model.Event) {
				e.Tasks[1] = toggleTask("build", f64ptr(0))
				e.ScoreRule.Template = model.TplSum
			},
			wantWarns: 1, warnContain: []string{"完成也不会得分"},
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

		{name: "计分规则非法", mutate: func(e *model.Event) { e.ScoreRule.Template = model.ScoreTemplate("weird") }, wantErrs: 1, errContains: []string{"计分规则"}},

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

		{name: "判罚模板非法", mutate: func(e *model.Event) { e.PenaltyRule.Template = model.PenaltyTemplate("weird") }, wantErrs: 1, errContains: []string{"判罚模板"}},

		// —— 判罚模板：「仅记录不扣分」是当前唯一口径；历史值仍须能通过校验 ——
		{name: "判罚模板 record_only 合法", mutate: func(e *model.Event) { e.PenaltyRule.Template = model.PenaltyRecordOnly }},
		{name: "判罚模板 none（历史值）仍合法", mutate: func(e *model.Event) { e.PenaltyRule.Template = model.PenaltyNone }},
		{name: "判罚模板空值视为未配置", mutate: func(e *model.Event) { e.PenaltyRule.Template = "" }},

		// —— 牌面计数规则（黄牌计数器开关 / 累计升级阈值）——
		{
			name: "累计升级阈值为负数报错",
			mutate: func(e *model.Event) {
				e.PenaltyRule.CardRules = &model.CardRules{RedThreshold: -1}
			},
			wantErrs: 1, errContains: []string{"累计升级阈值"},
		},
		{
			// 关掉黄牌计数器本身不是矛盾：红牌是独立入口，仍可直接记录。
			//（原来这里还有一条「关了计数器 + 禁止直接记红牌」的警告，随该开关一起删除。）
			name: "关掉黄牌计数器不产生警告",
			mutate: func(e *model.Event) {
				e.PenaltyRule.CardRules = &model.CardRules{Enabled: boolPtr(false)}
			},
		},
		{
			name: "牌面规则未配置不产生任何提示（历史数据）",
			mutate: func(e *model.Event) {
				e.PenaltyRule.CardRules = nil
			},
		},

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

// boolPtr 取布尔指针，用于构造「显式关闭」的牌面规则（nil 表示未配置）。
func boolPtr(b bool) *bool { return &b }

// TestValidateTaskUnit 量词（题卡上的「每颗 +100」）的三条底线。
//
// 量词不参与算分，所以校验只该管两件事：**别静默无效**（非计数项填了量词，
// 界面根本不会显示 → 与其让人以为配了没生效，不如当场提醒）、
// **别撑坏版式**（它是「个 / 颗 / 块」这一档的计量字，不是说明文字）。
//
// 反例与正例都要有：只写反例的话，一条「有没有量词就报警」的错实现也能过。
func TestValidateTaskUnit(t *testing.T) {
	cases := []struct {
		name      string
		task      model.Task
		wantErrs  int
		wantWarns int
	}{
		{"计数项 · 量词合法",
			model.Task{ID: "ball", Name: "能源球运输", Type: model.TaskCount, Weight: 20, Unit: "颗"}, 0, 0},
		{"计数项 · 未配量词（允许：界面回落「每单位」）",
			model.Task{ID: "ball", Name: "能源球运输", Type: model.TaskCount, Weight: 20}, 0, 0},
		{"四个字是上限（刚好合法）",
			model.Task{ID: "ball", Name: "能源球运输", Type: model.TaskCount, Weight: 20, Unit: "颗颗颗颗"}, 0, 0},
		{"量词过长 → 拦下",
			model.Task{ID: "ball", Name: "能源球运输", Type: model.TaskCount, Weight: 20, Unit: "颗颗颗颗颗"}, 1, 0},
		{"量词首尾有空格 → 拦下（会被当成另一个值）",
			model.Task{ID: "ball", Name: "能源球运输", Type: model.TaskCount, Weight: 20, Unit: " 颗"}, 1, 0},
		{"非计数项填了量词 → 提醒（界面不会显示）",
			model.Task{ID: "focus", Name: "专注力任务", Type: model.TaskNumeric, MaxScore: f64ptr(100), Weight: 1, Unit: "颗"}, 0, 1},
		{"是否完成填了量词 → 同样提醒",
			model.Task{ID: "tower", Name: "能源塔激活", Type: model.TaskToggle, MaxScore: f64ptr(500), Weight: 1, Unit: "座"}, 0, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev := validEvent()
			ev.ScoreRule.Template = model.TplSum // 单任务时避开「权重之和 = 1」那条与本用例无关的提醒
			ev.Tasks = []model.Task{c.task}

			res := ValidateEvent(ev)
			if len(res.Errors) != c.wantErrs {
				t.Errorf("errors = %d（期望 %d）：%v", len(res.Errors), c.wantErrs, res.ErrorMessages())
			}
			if len(res.Warnings) != c.wantWarns {
				t.Errorf("warnings = %d（期望 %d）：%v", len(res.Warnings), c.wantWarns, res.WarningMessages())
			}
		})
	}
}
