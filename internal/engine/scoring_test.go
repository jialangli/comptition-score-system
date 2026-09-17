package engine

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ---------------------------------------------------------------------------
// 构造器
// ---------------------------------------------------------------------------

func numTask(id string, max, weight float64) model.Task {
	return model.Task{
		ID: id, Name: id, Type: model.TaskNumeric,
		MaxScore: f64ptr(max), Weight: weight, Control: model.CtrlSlider,
	}
}

func countTask(id string, perUnit float64) model.Task {
	return model.Task{ID: id, Name: id, Type: model.TaskCount, Weight: perUnit, Control: model.CtrlCounter}
}

func enumTask(id string, m map[string]float64, weight float64) model.Task {
	return model.Task{ID: id, Name: id, Type: model.TaskEnum, EnumMap: m, Weight: weight, Control: model.CtrlSelect}
}

func newEvent(tasks ...model.Task) *model.Event {
	return &model.Event{
		ID: "t", Name: "测试赛项", Groups: []string{"小学组"},
		Tasks:       tasks,
		ScoreRule:   model.ScoreRule{Template: model.TplWeightedSum},
		PenaltyRule: model.PenaltyRule{Template: model.PenaltyNone},
		RankRule:    model.RankRule{TieBreak: []string{"score", "time"}, AwardTiers: map[string]float64{}},
	}
}

func rec(tasks map[string]any, duration float64, yellow, red int) model.ScoreRecord {
	return model.ScoreRecord{RoundNo: 1, TaskValues: tasks, DurationSec: duration, Yellow: yellow, Red: red}
}

// ---------------------------------------------------------------------------
// 单轮计分
// ---------------------------------------------------------------------------

func TestScore(t *testing.T) {
	tests := []struct {
		name  string
		build func() (*model.Event, model.ScoreRecord)
		want  model.ScoreResult
	}{
		{
			name: "加权求和·两项满录入",
			build: func() (*model.Event, model.ScoreRecord) {
				return newEvent(numTask("focus", 100, 0.5), numTask("build", 100, 0.5)),
					rec(map[string]any{"focus": 88.0, "build": 92.0}, 0, 0, 0)
			},
			want: model.ScoreResult{Base: 90, Total: 90, Complete: true},
		},
		{
			name: "加权求和·缺一项则 complete=false 且只算已录入项",
			build: func() (*model.Event, model.ScoreRecord) {
				return newEvent(numTask("focus", 100, 0.5), numTask("build", 100, 0.5)),
					rec(map[string]any{"focus": 80.0}, 0, 0, 0)
			},
			want: model.ScoreResult{Base: 40, Total: 40, Complete: false},
		},
		{
			name: "加权求和·满分非 100 时归一化到百分制",
			build: func() (*model.Event, model.ScoreRecord) {
				return newEvent(numTask("m", 50, 1.0)), rec(map[string]any{"m": 25.0}, 0, 0, 0)
			},
			want: model.ScoreResult{Base: 50, Total: 50, Complete: true},
		},
		{
			name: "字符串录入按数值解析（表单原文场景）",
			build: func() (*model.Event, model.ScoreRecord) {
				return newEvent(numTask("focus", 100, 0.5), numTask("build", 100, 0.5)),
					rec(map[string]any{"focus": "88", "build": "92"}, 0, 0, 0)
			},
			want: model.ScoreResult{Base: 90, Total: 90, Complete: true},
		},
		{
			name: "非法字符串记 0 分但视为已录入（与 JS Number()||0 一致）",
			build: func() (*model.Event, model.ScoreRecord) {
				return newEvent(numTask("focus", 100, 0.5), numTask("build", 100, 0.5)),
					rec(map[string]any{"focus": "abc", "build": 92.0}, 0, 0, 0)
			},
			want: model.ScoreResult{Base: 46, Total: 46, Complete: true},
		},
		{
			name: "空字符串视为未录入（与 0 分区分开）",
			build: func() (*model.Event, model.ScoreRecord) {
				return newEvent(numTask("focus", 100, 0.5), numTask("build", 100, 0.5)),
					rec(map[string]any{"focus": "", "build": 92.0}, 0, 0, 0)
			},
			want: model.ScoreResult{Base: 46, Total: 46, Complete: false},
		},
		{
			name: "0 分是合法得分而非未录入",
			build: func() (*model.Event, model.ScoreRecord) {
				return newEvent(numTask("focus", 100, 0.5), numTask("build", 100, 0.5)),
					rec(map[string]any{"focus": 0.0, "build": 0.0}, 0, 0, 0)
			},
			want: model.ScoreResult{Base: 0, Total: 0, Complete: true},
		},
		{
			name: "直接求和模板不做归一化、不乘权重",
			build: func() (*model.Event, model.ScoreRecord) {
				ev := newEvent(numTask("focus", 100, 0.5), numTask("build", 100, 0.5))
				ev.ScoreRule.Template = model.TplSum
				return ev, rec(map[string]any{"focus": 88.0, "build": 92.0}, 0, 0, 0)
			},
			want: model.ScoreResult{Base: 180, Total: 180, Complete: true},
		},
		{
			name: "取平均模板",
			build: func() (*model.Event, model.ScoreRecord) {
				ev := newEvent(numTask("focus", 100, 0.5), numTask("build", 100, 0.5))
				ev.ScoreRule.Template = model.TplAverage
				return ev, rec(map[string]any{"focus": 88.0, "build": 92.0}, 0, 0, 0)
			},
			want: model.ScoreResult{Base: 90, Total: 90, Complete: true},
		},
		{
			name: "取平均·只录入一项时按已录入项平均",
			build: func() (*model.Event, model.ScoreRecord) {
				ev := newEvent(numTask("focus", 100, 0.5), numTask("build", 100, 0.5))
				ev.ScoreRule.Template = model.TplAverage
				return ev, rec(map[string]any{"focus": 80.0}, 0, 0, 0)
			},
			want: model.ScoreResult{Base: 80, Total: 80, Complete: false},
		},
		{
			name: "取平均·全未录入不为 NaN",
			build: func() (*model.Event, model.ScoreRecord) {
				ev := newEvent(numTask("focus", 100, 0.5), numTask("build", 100, 0.5))
				ev.ScoreRule.Template = model.TplAverage
				return ev, rec(map[string]any{}, 0, 0, 0)
			},
			want: model.ScoreResult{},
		},
		{
			name: "计数任务按 数量×单价 取值",
			build: func() (*model.Event, model.ScoreRecord) {
				ev := newEvent(countTask("blocks", 5))
				ev.ScoreRule.Template = model.TplSum
				return ev, rec(map[string]any{"blocks": 4.0}, 0, 0, 0)
			},
			want: model.ScoreResult{Base: 20, Total: 20, Complete: true},
		},
		{
			name: "等级任务命中映射表",
			build: func() (*model.Event, model.ScoreRecord) {
				return newEvent(enumTask("level", map[string]float64{"优秀": 100, "良好": 80, "合格": 60}, 1.0)),
					rec(map[string]any{"level": "良好"}, 0, 0, 0)
			},
			want: model.ScoreResult{Base: 80, Total: 80, Complete: true},
		},
		{
			name: "等级任务未命中等级记 0 分（不是未录入）",
			build: func() (*model.Event, model.ScoreRecord) {
				return newEvent(enumTask("level", map[string]float64{"优秀": 100}, 1.0)),
					rec(map[string]any{"level": "待定"}, 0, 0, 0)
			},
			want: model.ScoreResult{Base: 0, Total: 0, Complete: true},
		},
		{
			name: "时间奖励·快于基准加分",
			build: func() (*model.Event, model.ScoreRecord) {
				ev := newEvent(numTask("t", 100, 1.0))
				ev.BonusRules = []model.BonusRule{{Template: model.BonusTime, Params: map[string]any{"perSecond": 0.5, "cap": 10.0}}}
				return ev, rec(map[string]any{"t": 50.0}, 100, 0, 0)
			},
			want: model.ScoreResult{Base: 50, Bonus: 10, Total: 60, Complete: true},
		},
		{
			name: "时间奖励·慢于基准不加负分",
			build: func() (*model.Event, model.ScoreRecord) {
				ev := newEvent(numTask("t", 100, 1.0))
				ev.BonusRules = []model.BonusRule{{Template: model.BonusTime, Params: map[string]any{"perSecond": 0.5, "cap": 10.0}}}
				return ev, rec(map[string]any{"t": 50.0}, 140, 0, 0)
			},
			want: model.ScoreResult{Base: 50, Total: 50, Complete: true},
		},
		{
			name: "时间奖励·封顶为 null 时不封顶",
			build: func() (*model.Event, model.ScoreRecord) {
				ev := newEvent(numTask("t", 100, 1.0))
				ev.BonusRules = []model.BonusRule{{Template: model.BonusTime, Params: map[string]any{"perSecond": 0.5, "cap": nil}}}
				return ev, rec(map[string]any{"t": 50.0}, 80, 0, 0)
			},
			want: model.ScoreResult{Base: 50, Bonus: 20, Total: 70, Complete: true},
		},
		{
			name: "时间奖励·未计时（用时 0）不产生加分",
			build: func() (*model.Event, model.ScoreRecord) {
				ev := newEvent(numTask("t", 100, 1.0))
				ev.BonusRules = []model.BonusRule{{Template: model.BonusTime, Params: map[string]any{"perSecond": 0.5}}}
				return ev, rec(map[string]any{"t": 50.0}, 0, 0, 0)
			},
			want: model.ScoreResult{Base: 50, Total: 50, Complete: true},
		},
		{
			name: "计数奖励·派生计数项参与加分但不进基础分",
			build: func() (*model.Event, model.ScoreRecord) {
				ev := newEvent(numTask("base", 100, 1.0))
				ev.BonusRules = []model.BonusRule{
					{Template: model.BonusCount, Params: map[string]any{"taskId": "energy", "perUnit": 5.0, "cap": nil}},
				}
				return ev, rec(map[string]any{"base": 85.0, "energy": 6.0}, 0, 0, 0)
			},
			want: model.ScoreResult{Base: 85, Bonus: 30, Total: 115, Complete: true},
		},
		{
			name: "计数奖励·封顶生效",
			build: func() (*model.Event, model.ScoreRecord) {
				ev := newEvent(numTask("base", 100, 1.0))
				ev.BonusRules = []model.BonusRule{
					{Template: model.BonusCount, Params: map[string]any{"taskId": "energy", "perUnit": 5.0, "cap": 10.0}},
				}
				return ev, rec(map[string]any{"base": 85.0, "energy": 6.0}, 0, 0, 0)
			},
			want: model.ScoreResult{Base: 85, Bonus: 10, Total: 95, Complete: true},
		},
		{
			name: "计数奖励·未指定 taskId 则整条规则跳过",
			build: func() (*model.Event, model.ScoreRecord) {
				ev := newEvent(numTask("base", 100, 1.0))
				ev.BonusRules = []model.BonusRule{
					{Template: model.BonusCount, Params: map[string]any{"perUnit": 5.0}},
				}
				return ev, rec(map[string]any{"base": 85.0, "energy": 6.0}, 0, 0, 0)
			},
			want: model.ScoreResult{Base: 85, Total: 85, Complete: true},
		},
		{
			name: "按牌扣分",
			build: func() (*model.Event, model.ScoreRecord) {
				ev := newEvent(numTask("t", 100, 1.0))
				ev.PenaltyRule = model.PenaltyRule{Template: model.PenaltyPerCard, Params: map[string]any{"yellow": 5.0, "red": 15.0}}
				return ev, rec(map[string]any{"t": 90.0}, 0, 1, 1)
			},
			want: model.ScoreResult{Base: 90, Penalty: 20, Total: 70, Complete: true},
		},
		{
			name: "扣分超过总分时截断到 0（不倒欠）",
			build: func() (*model.Event, model.ScoreRecord) {
				ev := newEvent(numTask("t", 100, 1.0))
				ev.PenaltyRule = model.PenaltyRule{Template: model.PenaltyPerCard, Params: map[string]any{"yellow": 5.0, "red": 15.0}}
				return ev, rec(map[string]any{"t": 15.0}, 0, 10, 0)
			},
			want: model.ScoreResult{Base: 15, Penalty: 50, Total: 0, Complete: true},
		},
		{
			name: "未知计分模板不静默降级（由校验层报错阻断）",
			build: func() (*model.Event, model.ScoreRecord) {
				ev := newEvent(numTask("t", 100, 1.0))
				ev.ScoreRule.Template = model.ScoreTemplate("weird_template")
				return ev, rec(map[string]any{"t": 90.0}, 0, 0, 0)
			},
			want: model.ScoreResult{},
		},
		{
			name: "赛项为空返回零值",
			build: func() (*model.Event, model.ScoreRecord) {
				return nil, rec(map[string]any{"t": 90.0}, 0, 0, 0)
			},
			want: model.ScoreResult{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ev, r := tc.build()
			got := Score(ev, r, DefaultRefTime)
			if got != tc.want {
				t.Errorf("Score()\n  got  = %+v\n  want = %+v", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 数值转换
// ---------------------------------------------------------------------------

func TestNumOr0(t *testing.T) {
	cases := []struct {
		in   any
		want float64
	}{
		{nil, 0}, {0.0, 0}, {12.5, 12.5}, {"", 0}, {"12", 12}, {" 12 ", 12},
		{"abc", 0}, {"12abc", 0}, {true, 1}, {false, 0}, {int(7), 7}, {int64(8), 8},
		{json.Number("9.5"), 9.5}, {json.Number("bad"), 0}, {[]any{}, 0},
		{[]any{5.0}, 5}, {[]any{1.0, 2.0}, 0}, {math.NaN(), 0}, {math.Inf(1), 0},
		{map[string]any{"a": 1}, 0}, {"-3", -3}, {"1e2", 100},
	}
	for _, c := range cases {
		if got := numOr0(c.in); got != c.want {
			t.Errorf("numOr0(%#v) = %v，期望 %v", c.in, got, c.want)
		}
	}
}

func TestJsKey(t *testing.T) {
	cases := []struct {
		in   any
		want string
		ok   bool
	}{
		{"优秀", "优秀", true},
		{80.0, "80", true},
		{80.5, "80.5", true},
		{true, "true", true},
		{int(3), "3", true},
		{nil, "", false},
		{map[string]any{}, "", false},
	}
	for _, c := range cases {
		got, ok := jsKey(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("jsKey(%#v) = (%q,%v)，期望 (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

// ---------------------------------------------------------------------------
// 两轮取优
// ---------------------------------------------------------------------------

func TestBestOf(t *testing.T) {
	ev := newEvent(numTask("t", 100, 1.0))
	mk := func(round int, val float64, dur float64) model.ScoreRecord {
		return model.ScoreRecord{RoundNo: round, TaskValues: map[string]any{"t": val}, DurationSec: dur}
	}

	tests := []struct {
		name      string
		recs      []model.ScoreRecord
		wantRound int
		wantTotal float64
		found     bool
	}{
		{"无记录", nil, 0, 0, false},
		{"仅第一轮", []model.ScoreRecord{mk(1, 80, 100)}, 1, 80, true},
		{"第二轮更高取第二轮", []model.ScoreRecord{mk(1, 80, 100), mk(2, 95, 110)}, 2, 95, true},
		{"第一轮更高取第一轮", []model.ScoreRecord{mk(1, 95, 110), mk(2, 80, 100)}, 1, 95, true},
		{"同分取用时少者", []model.ScoreRecord{mk(1, 90, 120), mk(2, 90, 90)}, 2, 90, true},
		{"同分同用时取轮次小者", []model.ScoreRecord{mk(2, 90, 90), mk(1, 90, 90)}, 1, 90, true},
		{"记录顺序颠倒不影响结果", []model.ScoreRecord{mk(2, 70, 130), mk(1, 85, 100)}, 1, 85, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, found := BestOf(ev, tc.recs, DefaultRefTime)
			if found != tc.found {
				t.Fatalf("found = %v，期望 %v", found, tc.found)
			}
			if !found {
				return
			}
			if got.RoundNo != tc.wantRound || got.Result.Total != tc.wantTotal {
				t.Errorf("取到第 %d 轮 / 总分 %v，期望第 %d 轮 / %v",
					got.RoundNo, got.Result.Total, tc.wantRound, tc.wantTotal)
			}
			if got.Record == nil {
				t.Error("Record 不应为 nil（有记录时）")
			}
		})
	}
}

func TestRoundNumbers(t *testing.T) {
	mk := func(r ...int) []model.ScoreRecord {
		out := make([]model.ScoreRecord, 0, len(r))
		for _, n := range r {
			out = append(out, model.ScoreRecord{RoundNo: n})
		}
		return out
	}
	cases := []struct {
		in   []model.ScoreRecord
		want []int
	}{
		{nil, nil},
		{mk(1), []int{1}},
		{mk(1, 2), []int{1, 2}},
		{mk(2, 1), []int{1, 2}},
		{mk(2, 2, 1), []int{1, 2}},
	}
	for i, c := range cases {
		got := RoundNumbers(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("用例 %d：得到 %v，期望 %v", i, got, c.want)
		}
		for j := range got {
			if got[j] != c.want[j] {
				t.Errorf("用例 %d：得到 %v，期望 %v", i, got, c.want)
			}
		}
	}
}

func TestRefTimeFor(t *testing.T) {
	ev := newEvent()
	if got := RefTimeFor(ev, 0); got != DefaultRefTime {
		t.Errorf("无配置时应回退默认值，得到 %v", got)
	}
	if got := RefTimeFor(ev, 90); got != 90 {
		t.Errorf("fallback 应生效，得到 %v", got)
	}
	ev.ScoreRule.Params = map[string]any{"refTime": 150.0}
	if got := RefTimeFor(ev, 90); got != 150 {
		t.Errorf("赛项级 refTime 应优先，得到 %v", got)
	}
	ev.ScoreRule.Params = map[string]any{"refTime": 0.0}
	if got := RefTimeFor(ev, 90); got != 90 {
		t.Errorf("refTime 为 0 视为未配置，应回退 fallback，得到 %v", got)
	}
	if got := RefTimeFor(nil, 0); got != DefaultRefTime {
		t.Errorf("nil 赛项应回退默认值，得到 %v", got)
	}
}
