package engine

import (
	"reflect"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// rankFixture 造一份最小可用的排名素材：
//
//	甲队（小学组）90 分 / 100 秒
//	乙队（小学组）90 分 /  90 秒   ← 与甲队同分，用时更少
//	丙队（初中组）80 分 / 100 秒
//	丁队（初中组）99 分 /  50 秒   ← 已弃赛
type rankFixture struct {
	Event *model.Event
	Teams []model.Team
	Score map[int64][]model.ScoreRecord
}

func newRankFixture() rankFixture {
	ev := newEvent(numTask("t", 100, 1.0))
	ev.ID = "ev1"
	ev.Groups = []string{"小学组", "初中组"}
	ev.RankRule = model.RankRule{
		TieBreak:   []string{"score", "time"},
		AwardTiers: map[string]float64{"一等奖": 0.1, "二等奖": 0.2, "三等奖": 0.3},
	}

	teams := []model.Team{
		{ID: 1, EventID: "ev1", TeamNo: "1001", Name: "甲队", GroupCode: "小学组", Status: model.TeamActive},
		{ID: 2, EventID: "ev1", TeamNo: "1002", Name: "乙队", GroupCode: "小学组", Status: model.TeamActive},
		{ID: 3, EventID: "ev1", TeamNo: "1003", Name: "丙队", GroupCode: "初中组", Status: model.TeamActive},
		{ID: 4, EventID: "ev1", TeamNo: "1004", Name: "丁队", GroupCode: "初中组", Status: model.TeamWithdrawn},
	}
	score := map[int64][]model.ScoreRecord{
		1: {rec(map[string]any{"t": 90.0}, 100, 0, 0)},
		2: {rec(map[string]any{"t": 90.0}, 90, 0, 0)},
		3: {rec(map[string]any{"t": 80.0}, 100, 0, 0)},
		4: {rec(map[string]any{"t": 99.0}, 50, 0, 0)},
	}
	return rankFixture{Event: ev, Teams: teams, Score: score}
}

func (f rankFixture) input() RankInput {
	return RankInput{Event: f.Event, Teams: f.Teams, Scores: f.Score}
}

// nos 取榜单中的队伍编号序列，便于断言顺序。
func nos(rows []model.StandingRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Team.TeamNo)
	}
	return out
}

func awards(rows []model.StandingRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Award)
	}
	return out
}

func TestRankOrderingAndWithdrawal(t *testing.T) {
	f := newRankFixture()

	t.Run("默认排除弃赛队伍且同分按用时排序", func(t *testing.T) {
		rows := Rank(f.input(), RankOptions{})
		want := []string{"1002", "1001", "1003"} // 乙(90,90) → 甲(90,100) → 丙(80,100)
		if got := nos(rows); !reflect.DeepEqual(got, want) {
			t.Errorf("顺序 = %v，期望 %v", got, want)
		}
		for i, r := range rows {
			if r.Rank != i+1 {
				t.Errorf("第 %d 行的 Rank 字段 = %d", i+1, r.Rank)
			}
		}
	})

	t.Run("IncludeWithdrawn 后弃赛队伍参与排名", func(t *testing.T) {
		rows := Rank(f.input(), RankOptions{IncludeWithdrawn: true})
		want := []string{"1004", "1002", "1001", "1003"}
		if got := nos(rows); !reflect.DeepEqual(got, want) {
			t.Errorf("顺序 = %v，期望 %v", got, want)
		}
	})

	t.Run("按组别筛选", func(t *testing.T) {
		rows := Rank(f.input(), RankOptions{Group: "小学组"})
		if got := nos(rows); !reflect.DeepEqual(got, []string{"1002", "1001"}) {
			t.Errorf("小学组顺序 = %v", got)
		}
		rows = Rank(f.input(), RankOptions{Group: "初中组"})
		if got := nos(rows); !reflect.DeepEqual(got, []string{"1003"}) {
			t.Errorf("初中组顺序 = %v", got)
		}
	})

	t.Run("其他赛项的队伍被忽略", func(t *testing.T) {
		in := f.input()
		in.Teams = append(in.Teams, model.Team{ID: 9, EventID: "other", TeamNo: "9999", Name: "外队", GroupCode: "小学组", Status: model.TeamActive})
		in.Scores[9] = []model.ScoreRecord{rec(map[string]any{"t": 100.0}, 10, 0, 0)}
		if got := nos(Rank(in, RankOptions{})); len(got) != 3 {
			t.Errorf("应只保留本赛项的 3 支队伍，实际 %v", got)
		}
	})

	t.Run("赛项为空返回 nil", func(t *testing.T) {
		if rows := Rank(RankInput{}, RankOptions{}); rows != nil {
			t.Errorf("期望 nil，得到 %v", rows)
		}
	})
}

func TestRankTieBreakConfig(t *testing.T) {
	f := newRankFixture()

	t.Run("配置 time 时用时少者优先", func(t *testing.T) {
		f.Event.RankRule.TieBreak = []string{"score", "time"}
		rows := Rank(f.input(), RankOptions{Group: "小学组"})
		if got := nos(rows); got[0] != "1002" {
			t.Errorf("顺序 = %v，期望 1002 在前", got)
		}
	})

	t.Run("未配置 time 时落到队名兜底比较", func(t *testing.T) {
		f.Event.RankRule.TieBreak = []string{"score"}
		rows := Rank(f.input(), RankOptions{Group: "小学组"})
		// 甲 vs 乙 同分且不比较用时 → 名称兜底：甲(jiǎ) < 乙(yǐ)
		if got := nos(rows); got[0] != "1001" {
			t.Errorf("顺序 = %v，期望按拼音序 1001 在前", got)
		}
	})

	t.Run("并列标记", func(t *testing.T) {
		f.Event.RankRule.TieBreak = []string{"score", "time"}
		rows := Rank(f.input(), RankOptions{Group: "小学组"})
		if rows[0].Tie {
			t.Error("第 1 名不应带并列标记")
		}
		if !rows[1].Tie {
			t.Error("与上一名同分的第 2 名应带并列标记")
		}
	})
}

func TestRankNameFallbackComparators(t *testing.T) {
	// 智造队 vs 破晓队：拼音序 破(p) < 智(z)，码点序 智(U+667A) < 破(U+7834)
	ev := newEvent(numTask("t", 100, 1.0))
	ev.ID = "ev2"
	ev.RankRule = model.RankRule{TieBreak: []string{"score", "time"}, AwardTiers: map[string]float64{}}
	teams := []model.Team{
		{ID: 1, EventID: "ev2", TeamNo: "4003", Name: "智造队", GroupCode: "小学组", Status: model.TeamActive},
		{ID: 2, EventID: "ev2", TeamNo: "4004", Name: "破晓队", GroupCode: "小学组", Status: model.TeamActive},
	}
	in := RankInput{Event: ev, Teams: teams, Scores: map[int64][]model.ScoreRecord{
		1: {rec(nil, 0, 0, 0)},
		2: {rec(nil, 0, 0, 0)},
	}}

	zh := Rank(in, RankOptions{})
	if got := nos(zh); got[0] != "4004" {
		t.Errorf("默认拼音序应让破晓队在前，实际 %v", got)
	}
	cp := Rank(in, RankOptions{NameLess: CodepointNameLess})
	if got := nos(cp); got[0] != "4003" {
		t.Errorf("码点序应让智造队在前，实际 %v", got)
	}
}

func TestRankAwardAssignment(t *testing.T) {
	f := newRankFixture()

	t.Run("按占比向上取整从第 1 名依次分配", func(t *testing.T) {
		rows := Rank(f.input(), RankOptions{})
		// 3 支队伍，占比 0.1/0.2/0.3 → ceil 后各 1 名
		want := []string{"一等奖", "二等奖", "三等奖"}
		if got := awards(rows); !reflect.DeepEqual(got, want) {
			t.Errorf("奖项 = %v，期望 %v", got, want)
		}
	})

	t.Run("占比之和不足 1 时靠后队伍无奖项", func(t *testing.T) {
		f.Event.RankRule.AwardTiers = map[string]float64{"一等奖": 0.3}
		rows := Rank(f.input(), RankOptions{})
		want := []string{"一等奖", "", ""}
		if got := awards(rows); !reflect.DeepEqual(got, want) {
			t.Errorf("奖项 = %v，期望 %v", got, want)
		}
	})

	t.Run("自定义奖项按占比降序接在标准奖项之后", func(t *testing.T) {
		// 金奖/银奖都不在标准奖项表里 → 归入「自定义」，按占比降序决定先后：
		// 银奖 0.2 先于金奖 0.1，因此银奖发给第 1 名。
		f.Event.RankRule.AwardTiers = map[string]float64{"金奖": 0.1, "银奖": 0.2}
		rows := Rank(f.input(), RankOptions{})
		want := []string{"银奖", "金奖", ""}
		if got := awards(rows); !reflect.DeepEqual(got, want) {
			t.Errorf("奖项 = %v，期望 %v（自定义奖项按占比降序接在标准奖项之后）", got, want)
		}
	})

	t.Run("不配置奖项则全部为空", func(t *testing.T) {
		f.Event.RankRule.AwardTiers = nil
		rows := Rank(f.input(), RankOptions{})
		for _, a := range awards(rows) {
			if a != "" {
				t.Errorf("不应分配奖项，实际 %q", a)
			}
		}
	})
}

func TestRankAwardOnlyComplete(t *testing.T) {
	ev := newEvent(numTask("t", 100, 1.0), numTask("u", 100, 0.0))
	ev.ID = "ev3"
	ev.RankRule = model.RankRule{TieBreak: []string{"score"}, AwardTiers: map[string]float64{"三等奖": 1.0}}

	teams := []model.Team{
		{ID: 1, EventID: "ev3", TeamNo: "1", Name: "完成队", GroupCode: "小学组", Status: model.TeamActive},
		{ID: 2, EventID: "ev3", TeamNo: "2", Name: "未赛一队", GroupCode: "小学组", Status: model.TeamActive},
		{ID: 3, EventID: "ev3", TeamNo: "3", Name: "未赛二队", GroupCode: "小学组", Status: model.TeamActive},
	}
	in := RankInput{Event: ev, Teams: teams, Scores: map[int64][]model.ScoreRecord{
		1: {rec(map[string]any{"t": 90.0, "u": 90.0}, 100, 0, 0)}, // 唯一完成的队伍
		2: {rec(nil, 0, 0, 0)},
		3: {rec(nil, 0, 0, 0)},
	}}

	lenient := Rank(in, RankOptions{})
	if got := awards(lenient); got[0] != "三等奖" || got[1] != "三等奖" || got[2] != "三等奖" {
		t.Errorf("默认（与前端一致）应给全部队伍发奖，实际 %v", got)
	}

	strict := Rank(in, RankOptions{AwardOnlyComplete: true})
	want := []string{"三等奖", "", ""}
	if got := awards(strict); !reflect.DeepEqual(got, want) {
		t.Errorf("AwardOnlyComplete 下应只给完成录入的队伍发奖，实际 %v（期望 %v）", got, want)
	}
	// 名次不受影响
	if strict[0].Rank != 1 || strict[2].Rank != 3 {
		t.Error("限制获奖资格不应改变名次")
	}
}

func TestRankOnlySigned(t *testing.T) {
	f := newRankFixture()
	// 甲队第一轮 90 分未签字，第二轮 60 分已签字
	f.Score[1] = []model.ScoreRecord{
		{RoundNo: 1, TaskValues: map[string]any{"t": 90.0}, DurationSec: 100},
		{RoundNo: 2, TaskValues: map[string]any{"t": 60.0}, DurationSec: 80, Signed: true},
	}

	all := Rank(f.input(), RankOptions{Group: "小学组"})
	for _, r := range all {
		if r.Team.TeamNo == "1001" && r.Result.Total != 90 {
			t.Errorf("不限签字时应取优到 90，实际 %v", r.Result.Total)
		}
	}

	signed := Rank(f.input(), RankOptions{Group: "小学组", OnlySigned: true})
	for _, r := range signed {
		if r.Team.TeamNo == "1001" {
			if r.Result.Total != 60 {
				t.Errorf("仅统计已签字时应取 60，实际 %v", r.Result.Total)
			}
			if r.BestRound != 2 {
				t.Errorf("取优轮次应为 2，实际 %d", r.BestRound)
			}
		}
	}
}

func TestRankFillsRoundsAndBestRound(t *testing.T) {
	f := newRankFixture()
	// 甲队两轮：第二轮更高 → 取优到第二轮
	f.Score[1] = []model.ScoreRecord{
		{RoundNo: 1, TaskValues: map[string]any{"t": 70.0}, DurationSec: 100},
		{RoundNo: 2, TaskValues: map[string]any{"t": 88.0}, DurationSec: 95},
	}
	// 新增一支尚未产生任何记录的队伍
	f.Teams = append(f.Teams, model.Team{ID: 5, EventID: "ev1", TeamNo: "1005", Name: "未赛队", GroupCode: "小学组", Status: model.TeamActive})

	rows := Rank(f.input(), RankOptions{Group: "小学组"})

	var seenBest, seenEmpty bool
	for _, r := range rows {
		switch r.Team.TeamNo {
		case "1001":
			seenBest = true
			if !reflect.DeepEqual(r.Rounds, []int{1, 2}) {
				t.Errorf("Rounds = %v，期望 [1 2]", r.Rounds)
			}
			if r.BestRound != 2 || r.Result.Total != 88 {
				t.Errorf("BestRound = %d / total = %v，期望 2 / 88", r.BestRound, r.Result.Total)
			}
			if r.Duration != 95 {
				t.Errorf("Duration = %v，期望取优轮用时 95", r.Duration)
			}
		case "1005":
			seenEmpty = true
			if r.BestRound != 0 || len(r.Rounds) != 0 {
				t.Errorf("无记录队伍应为 BestRound=0 / Rounds 空，实际 %d / %v", r.BestRound, r.Rounds)
			}
			if r.Result.Complete || r.Result.Total != 0 {
				t.Errorf("无记录队伍应为未完成且总分 0，实际 %+v", r.Result)
			}
		}
	}
	if !seenBest || !seenEmpty {
		t.Fatalf("断言未覆盖到目标队伍（seenBest=%v seenEmpty=%v）", seenBest, seenEmpty)
	}
}

func TestRankAllGroups(t *testing.T) {
	f := newRankFixture()
	groups := RankAllGroups(f.input(), RankOptions{})

	if len(groups) != 2 {
		t.Fatalf("应产出 2 个组别，实际 %d", len(groups))
	}
	// 组别顺序跟随赛项配置（小学组在上）
	if groups[0].Group != "小学组" || groups[1].Group != "初中组" {
		t.Errorf("组别顺序 = %s, %s，期望按赛项配置顺序", groups[0].Group, groups[1].Group)
	}
	// 组内独立名次：小学组第 1 名与初中组第 1 名都从 1 开始
	if groups[0].Rows[0].Rank != 1 || groups[1].Rows[0].Rank != 1 {
		t.Error("各组第 1 名的 Rank 均应为 1（组内独立排名）")
	}
	// 组内独立授奖：小学组只有 2 队，占比 0.1 → ceil(0.2)=1 个一等奖
	if len(groups[0].Rows) != 2 || len(groups[1].Rows) != 1 {
		t.Errorf("组内队伍数 = %d / %d，期望 2 / 1", len(groups[0].Rows), len(groups[1].Rows))
	}
	if groups[0].Rows[0].Award != "一等奖" || groups[1].Rows[0].Award != "一等奖" {
		t.Errorf("各组应各自产生一等奖，实际 %q / %q", groups[0].Rows[0].Award, groups[1].Rows[0].Award)
	}
}

func TestRankAllGroupsAppendsUndeclaredGroups(t *testing.T) {
	f := newRankFixture()
	f.Teams = append(f.Teams, model.Team{ID: 8, EventID: "ev1", TeamNo: "1008", Name: "高中队", GroupCode: "高中组", Status: model.TeamActive})
	f.Score[8] = []model.ScoreRecord{rec(map[string]any{"t": 70.0}, 100, 0, 0)}

	groups := RankAllGroups(f.input(), RankOptions{})
	if len(groups) != 3 {
		t.Fatalf("应产出 3 个组别，实际 %d", len(groups))
	}
	if groups[2].Group != "高中组" {
		t.Errorf("未在赛项中声明的组别应追加在末尾，实际顺序 %s/%s/%s",
			groups[0].Group, groups[1].Group, groups[2].Group)
	}
}

func TestRankIsDeterministic(t *testing.T) {
	// 奖项映射是 map，若不小心按 map 迭代顺序分配，这里就会随机失败。
	// 反复跑 200 次做回归保护。
	f := newRankFixture()
	f.Event.RankRule.AwardTiers = map[string]float64{
		"一等奖": 0.1, "二等奖": 0.2, "三等奖": 0.3, "优胜奖": 0.1,
	}
	first := awards(Rank(f.input(), RankOptions{}))
	snapshot := nos(Rank(f.input(), RankOptions{}))
	for i := 0; i < 200; i++ {
		if got := awards(Rank(f.input(), RankOptions{})); !reflect.DeepEqual(got, first) {
			t.Fatalf("第 %d 次运行的奖项分配发生变化：%v vs %v", i, got, first)
		}
		if got := nos(Rank(f.input(), RankOptions{})); !reflect.DeepEqual(got, snapshot) {
			t.Fatalf("第 %d 次运行的名次顺序发生变化：%v vs %v", i, got, snapshot)
		}
	}
}

func TestOrderedTiers(t *testing.T) {
	got := orderedTiers(map[string]float64{"三等奖": 0.3, "一等奖": 0.1, "二等奖": 0.2, "特别奖": 0.05})
	want := []string{"一等奖", "二等奖", "三等奖", "特别奖"} // 标准奖项按档位，自定义按占比降序接后
	if len(got) != len(want) {
		t.Fatalf("数量不符：%v", got)
	}
	for i := range want {
		if got[i].Name != want[i] {
			t.Errorf("第 %d 项 = %s，期望 %s", i, got[i].Name, want[i])
		}
	}
	if orderedTiers(nil) != nil {
		t.Error("空奖项映射应返回 nil")
	}
}
