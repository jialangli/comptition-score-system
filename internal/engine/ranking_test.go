package engine

import (
	"fmt"
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

// TestRankAwardsUseFloorNotCeil 锁定「名额用 floor、不是 ceil」。
//
// 这条断言是为了防止将来有人把 floor 改回 ceil：
// 小组赛人数少时，ceil 的放大效应会让「全员获奖」——
// 例如 2 队各占 0.5，ceil(1)=1+1=2，于是两队都拿奖；
// 改成 floor 后 ceil(1)=1，一等奖 1 个、二等奖 1 个，仍是全员获奖；
// 真正需要 floor 的是 3 队各 0.34 这种：ceil(1.02)=2 会多发一个。
func TestRankAwardsUseFloorNotCeil(t *testing.T) {
	ev := newEvent(numTask("t", 100, 1.0))
	ev.ID = "evfloor"
	ev.RankRule = model.RankRule{
		TieBreak: []string{"score"},
		// 单档 100%：floor 与 ceil 完全一致，隔离掉保底逻辑的干扰
		AwardTiers: map[string]float64{"一等奖": 0.5, "二等奖": 0.25},
	}

	teams := []model.Team{
		{ID: 1, EventID: "evfloor", TeamNo: "1", Name: "A", GroupCode: "小学组", Status: model.TeamActive},
		{ID: 2, EventID: "evfloor", TeamNo: "2", Name: "B", GroupCode: "小学组", Status: model.TeamActive},
		{ID: 3, EventID: "evfloor", TeamNo: "3", Name: "C", GroupCode: "小学组", Status: model.TeamActive},
		{ID: 4, EventID: "evfloor", TeamNo: "4", Name: "D", GroupCode: "小学组", Status: model.TeamActive},
	}
	in := RankInput{Event: ev, Teams: teams, Scores: map[int64][]model.ScoreRecord{
		1: {rec(map[string]any{"t": 90.0}, 10, 0, 0)},
		2: {rec(map[string]any{"t": 80.0}, 20, 0, 0)},
		3: {rec(map[string]any{"t": 70.0}, 30, 0, 0)},
		4: {rec(map[string]any{"t": 60.0}, 40, 0, 0)},
	}}

	got := awards(Rank(in, RankOptions{}))
	// 4 队 × 0.5 = 2.00 → floor 2（ceil 也是 2，此档不区分）
	// 4 队 × 0.25 = 1.00 → floor 1（ceil 也是 1，此档不区分）
	// 这组数据无法区分 floor/ceil，改用下面的 0.34 场景。
	_ = got

	// 关键场景：3 队 × 0.34 = 1.02
	//   floor → 1 个名额；ceil → 2 个名额。
	// 断言只发 1 个，以此锁定 floor 语义。
	ev2 := newEvent(numTask("t", 100, 1.0))
	ev2.ID = "evfloor2"
	ev2.RankRule = model.RankRule{
		TieBreak:   []string{"score"},
		AwardTiers: map[string]float64{"一等奖": 0.34},
	}
	teams2 := []model.Team{
		{ID: 1, EventID: "evfloor2", TeamNo: "1", Name: "A", GroupCode: "小学组", Status: model.TeamActive},
		{ID: 2, EventID: "evfloor2", TeamNo: "2", Name: "B", GroupCode: "小学组", Status: model.TeamActive},
		{ID: 3, EventID: "evfloor2", TeamNo: "3", Name: "C", GroupCode: "小学组", Status: model.TeamActive},
	}
	in2 := RankInput{Event: ev2, Teams: teams2, Scores: map[int64][]model.ScoreRecord{
		1: {rec(map[string]any{"t": 90.0}, 10, 0, 0)},
		2: {rec(map[string]any{"t": 80.0}, 20, 0, 0)},
		3: {rec(map[string]any{"t": 70.0}, 30, 0, 0)},
	}}
	got2 := awards(Rank(in2, RankOptions{}))
	n1 := 0
	for _, a := range got2 {
		if a == "一等奖" {
			n1++
		}
	}
	if n1 != 1 {
		t.Errorf("3 队 × 0.34 应按 floor 发 1 个一等奖，实际发了 %d 个（若为 2 说明被改回了 ceil）：%v", n1, got2)
	}
}

// TestRankVoidedTeams 被裁定「取消资格」的队伍**整行**不进榜单（成绩作废）。
//
// 与红牌（Disqualified）刻意分开是本条的重点：红牌成绩**保留** —— 留在榜内、
// 名次置 0、分数照给；裁定作废是成绩**作废** —— 连行都不给。
// 所以这里不是打标记，是在建行时就剔除。
func TestRankVoidedTeams(t *testing.T) {
	f := newRankFixture()

	t.Run("整行剔除（KeepGap=false 时名次连续）", func(t *testing.T) {
		// 这一档是「按名次顺延」；产品默认是「不递补」（KeepGap=true，名次留空），
		// 见 TestRankKeepGap。默认值由 model.SubstituteMode.KeepGap() 决定。
		rows := Rank(f.input(), RankOptions{Group: "小学组", VoidedTeams: map[int64]bool{2: true}})
		if got := nos(rows); !reflect.DeepEqual(got, []string{"1001"}) {
			t.Fatalf("乙队被裁定作废后小学组应只剩甲队，实际 %v", got)
		}
		if rows[0].Rank != 1 {
			t.Errorf("按名次顺延时名次前移，实际 %d", rows[0].Rank)
		}
		if rows[0].Result.Total == 0 {
			t.Errorf("在榜队伍的成绩应照常给出")
		}
		if rows[0].Disqualified {
			t.Errorf("裁定作废 ≠ 红牌：不该同时打上红牌标记")
		}
	})

	t.Run("奖项名额按剔除后的榜数算", func(t *testing.T) {
		// 小学组 2 队剔除 1 队 → 榜数 1；一等奖占比 0.1 → floor(0.1)=0 → 保底 1 个
		rows := Rank(f.input(), RankOptions{Group: "小学组", VoidedTeams: map[int64]bool{2: true}})
		if rows[0].Award != "一等奖" {
			t.Errorf("唯一在榜的甲队应拿到保底名额，实际 %q", rows[0].Award)
		}
	})

	t.Run("改判后自动恢复（判据派生自工单，无回滚动作）", func(t *testing.T) {
		rows := Rank(f.input(), RankOptions{Group: "小学组"})
		if got := nos(rows); !reflect.DeepEqual(got, []string{"1002", "1001"}) {
			t.Errorf("撤销作废后应按原规则排序（乙队用时更少），实际 %v", got)
		}
	})

	t.Run("不影响其它组别", func(t *testing.T) {
		rows := Rank(f.input(), RankOptions{Group: "初中组", VoidedTeams: map[int64]bool{2: true}})
		if got := nos(rows); !reflect.DeepEqual(got, []string{"1003"}) {
			t.Errorf("初中组不应受影响，实际 %v", got)
		}
	})
}

// TestRankKeepGap 名次编排两档：不递补（保留空缺）vs 按名次顺延。
//
// 守的是 2026-10-10 对出来的一处两端不一致：前端 `substituteRule.mode` 默认 'none'
// （不递补）→ 公示表上会出现 4 → 6 这种空洞；而后端原先只有「连续编号」一种行为，
// 于是同一场比赛两边给出的名次不同（前端说第 5 名空缺、后端说第 5 名另有其人）。
//
// 素材：小学组 甲(90 分 / 100 秒)、乙(90 分 / 90 秒) —— 两人同分，乙用时更少。
// 排序后乙在第 1 位、甲在第 2 位；**让乙被裁定取消资格**，
// 于是"不递补"时甲的名次应**仍是 2**（第 1 名空缺）。
// 这也正好是那个容易写错的组合：甲的前一行（乙）与它同分，但已作废。
func TestRankKeepGap(t *testing.T) {
	f := newRankFixture()
	voided := map[int64]bool{2: true}

	t.Run("不递补：作废队的位置留空", func(t *testing.T) {
		rows := Rank(f.input(), RankOptions{Group: "小学组", VoidedTeams: voided, KeepGap: true})
		if got := nos(rows); !reflect.DeepEqual(got, []string{"1001"}) {
			t.Fatalf("作废队应整行不进榜单，实际 %v", got)
		}
		if rows[0].Rank != 2 {
			t.Errorf("不递补时甲队应保留原位置（第 2 名，第 1 名空缺），实际 %d", rows[0].Rank)
		}
		if rows[0].Tie {
			t.Error("甲队是唯一有资格的队伍，不该被判为并列 —— " +
				"并列要与「上一支有资格的队伍」比，不是与上一行比（上一行可能是作废队）")
		}
	})

	t.Run("按名次顺延：编号连续", func(t *testing.T) {
		rows := Rank(f.input(), RankOptions{Group: "小学组", VoidedTeams: voided})
		if rows[0].Rank != 1 {
			t.Errorf("按名次顺延时应前移为第 1 名，实际 %d", rows[0].Rank)
		}
	})

	t.Run("没有作废队时两档结果完全一样", func(t *testing.T) {
		a := Rank(f.input(), RankOptions{Group: "小学组", KeepGap: true})
		b := Rank(f.input(), RankOptions{Group: "小学组"})
		if !reflect.DeepEqual(a, b) {
			t.Error("没有作废队时递补规则不该产生任何差异 —— " +
				"这条能挡住「默认值接错」与「不该留空洞时却留了」")
		}
	})

	t.Run("红牌不产生空洞（与作废是两条路径）", func(t *testing.T) {
		// 甲队红牌 → 留在榜内、名次 0、排在最后；乙队的名次不受影响。
		ff := newRankFixture()
		ff.Score[1] = []model.ScoreRecord{rec(map[string]any{"t": 90.0}, 90, 0, 1)}
		rows := Rank(ff.input(), RankOptions{Group: "小学组", KeepGap: true})
		if got := nos(rows); !reflect.DeepEqual(got, []string{"1002", "1001"}) {
			t.Fatalf("红牌队应留在榜内并排最后，实际 %v", got)
		}
		if rows[0].Rank != 1 {
			t.Errorf("红牌队不占名次序号，乙队应仍是第 1 名，实际 %d", rows[0].Rank)
		}
		if !rows[1].Disqualified || rows[1].Rank != 0 {
			t.Errorf("红牌队应为「留在榜内、名次 0」，实际 dq=%v rank=%d",
				rows[1].Disqualified, rows[1].Rank)
		}
	})

	t.Run("空缺按组别算，不跨组挪动", func(t *testing.T) {
		gs := RankAllGroups(f.input(), RankOptions{VoidedTeams: voided, KeepGap: true})
		byGroup := map[string][]int{}
		for _, g := range gs {
			for i := range g.Rows {
				byGroup[g.Group] = append(byGroup[g.Group], g.Rows[i].Rank)
			}
		}
		if got := byGroup["小学组"]; !reflect.DeepEqual(got, []int{2}) {
			t.Errorf("小学组应为第 2 名（第 1 名空缺），实际 %v", got)
		}
		if got := byGroup["初中组"]; !reflect.DeepEqual(got, []int{1}) {
			t.Errorf("初中组的名次不该被别组的空缺挤走，实际 %v", got)
		}
	})
}

// TestRankVoidedTeamFreesAwardSlot 作废队不占奖项名额（2026-10-10 定案）。
//
// 口径：被裁定「取消资格」的队伍**整行剔除**，因此既不参与名额的分母
// （名额 = floor(在榜队数 × 占比)），也不当获奖人 —— 让出的名额由后面的队伍顶上。
// 与默认的「不递补」（名次留空）不矛盾：名次是身份标识（空缺要留着、后面的队伍不许改号），
// 奖项是名额分配（作废队不在榜上，就不占名额）。**名次上的空洞保留、奖项上的名额不留**。
//
// ⚠️ 素材必须够大，否则分不清两种口径：现有 rankFixture 只有 2 支在榜队，
// floor + 每档保底 1 会把「按剔除后榜数算」与「按原榜数算」算出同一个结果
// （floor(0.1×1)=0→保底 1，floor(0.1×2)=0→保底 1）——
// "断言在、却分不清两档"的用例只给虚假的安心，所以这里用 10 支队伍。
func TestRankVoidedTeamFreesAwardSlot(t *testing.T) {
	ev := newEvent(numTask("t", 100, 1.0))
	ev.ID = "ev1"
	ev.Groups = []string{"小学组"}
	ev.RankRule = model.RankRule{
		TieBreak:   []string{"score", "time"},
		AwardTiers: map[string]float64{"一等奖": 0.1, "二等奖": 0.2, "三等奖": 0.3},
	}

	const total = 10
	teams := make([]model.Team, 0, total)
	scores := make(map[int64][]model.ScoreRecord, total)
	for i := 1; i <= total; i++ {
		id := int64(i)
		teams = append(teams, model.Team{
			ID: id, EventID: "ev1",
			TeamNo:    fmt.Sprintf("10%02d", i),
			Name:      fmt.Sprintf("第%d队", i),
			GroupCode: "小学组", Status: model.TeamActive,
		})
		// 分数递减：名次顺序 = 队序，便于逐位断言
		scores[id] = []model.ScoreRecord{rec(map[string]any{"t": float64(101 - i)}, 100, 0, 0)}
	}
	in := RankInput{Event: ev, Teams: teams, Scores: scores}

	t.Run("基准：10 队在榜 → 1 / 2 / 3 个名额", func(t *testing.T) {
		want := []string{"一等奖", "二等奖", "二等奖", "三等奖", "三等奖", "三等奖",
			"", "", "", ""}
		if got := awards(Rank(in, RankOptions{})); !reflect.DeepEqual(got, want) {
			t.Fatalf("无作废队时奖项分布 = %v，期望 %v", got, want)
		}
	})

	t.Run("作废 1 队 → 名额按剔除后的榜数算（分母也剔除）", func(t *testing.T) {
		rows := Rank(in, RankOptions{VoidedTeams: map[int64]bool{3: true}, KeepGap: true})

		// 作废队整行不进榜单：连成绩都不再给出
		wantNos := []string{"1001", "1002", "1004", "1005", "1006", "1007", "1008", "1009", "1010"}
		if got := nos(rows); !reflect.DeepEqual(got, wantNos) {
			t.Fatalf("作废队应整行剔除，实际 %v", got)
		}

		// 名额 = floor(在榜队数 9 × 占比)：一等奖 0.9 → 0（保底 1）、二等奖 1.8 → 1、三等奖 2.7 → 2。
		// 若分母仍按 10 队算，会发出 6 个名额 —— 这条断言正是用来分清这两档的。
		want := []string{"一等奖", "二等奖", "三等奖", "三等奖", "", "", "", "", ""}
		if got := awards(rows); !reflect.DeepEqual(got, want) {
			t.Fatalf("奖项分布 = %v，期望 %v（名额按在榜队数 9 算，不是 10）", got, want)
		}
	})

	t.Run("名次的空洞不传递到奖项（名额按榜内顺序数）", func(t *testing.T) {
		rows := Rank(in, RankOptions{VoidedTeams: map[int64]bool{3: true}, KeepGap: true})

		ranks := make([]int, 0, len(rows))
		for i := range rows {
			ranks = append(ranks, rows[i].Rank)
		}
		if want := []int{1, 2, 4, 5, 6, 7, 8, 9, 10}; !reflect.DeepEqual(ranks, want) {
			t.Fatalf("名次 = %v，期望 %v（不递补：第 3 名空缺）", ranks, want)
		}
		// 奖项仍从榜内第 1 位起连续数名额：第 4 名照常拿到三等奖 —— 空缺不占名额。
		if rows[2].Team.TeamNo != "1004" || rows[2].Award != "三等奖" {
			t.Errorf("第 4 名应照常拿到三等奖（空缺不占名额），实际 %s award=%q",
				rows[2].Team.TeamNo, rows[2].Award)
		}
	})

	t.Run("递补规则只影响名次编号，不影响奖项名额", func(t *testing.T) {
		voided := map[int64]bool{3: true}
		gap := awards(Rank(in, RankOptions{VoidedTeams: voided, KeepGap: true}))
		seq := awards(Rank(in, RankOptions{VoidedTeams: voided}))
		if !reflect.DeepEqual(gap, seq) {
			t.Errorf("两档的奖项名额应完全一致（只差名次编号）：%v / %v", gap, seq)
		}
	})
}
