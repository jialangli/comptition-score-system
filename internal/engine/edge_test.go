package engine

import (
	"encoding/json"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 边界与防御性分支
//
// 这些分支平时不会走到，但一旦走到就是线上事故：
// 例如 JSONB 里出现 int32/uint 类型的数值、配置里模板字段被清空、
// 或传入了不属于本赛项的队伍。用测试把它们固定住，
// 避免后续重构时无声地删掉兜底逻辑。
// ============================================================================

func TestNumOr0NumericTypeBranches(t *testing.T) {
	cases := []struct {
		in   any
		want float64
	}{
		{float32(2.5), 2.5},
		{int32(3), 3},
		{uint(4), 4},
		{uint64(5), 5},
		{json.Number("6.5"), 6.5},
	}
	for _, c := range cases {
		if got := numOr0(c.in); got != c.want {
			t.Errorf("numOr0(%#v) = %v，期望 %v", c.in, got, c.want)
		}
	}
}

func TestJsKeyExtraBranches(t *testing.T) {
	if got, ok := jsKey(int64(42)); got != "42" || !ok {
		t.Errorf("jsKey(int64(42)) = (%q,%v)，期望 (42,true)", got, ok)
	}
	if got, ok := jsKey(json.Number("7")); got != "7" || !ok {
		t.Errorf("jsKey(json.Number(7)) = (%q,%v)，期望 (7,true)", got, ok)
	}
}

func TestTaskRawScoreNilTask(t *testing.T) {
	if got := taskRawScore(nil, 88.0); got != nil {
		t.Errorf("任务为 nil 时应返回 nil（视为未录入），实际 %v", *got)
	}
}

func TestRaw0NilPointer(t *testing.T) {
	if got := raw0(nil); got != 0 {
		t.Errorf("raw0(nil) = %v，期望 0", got)
	}
}

func TestScoreFallbackBranches(t *testing.T) {
	t.Run("refTime 非正时回退默认基准", func(t *testing.T) {
		ev := newEvent(numTask("t", 100, 1.0))
		ev.BonusRules = []model.BonusRule{{Template: model.BonusTime, Params: map[string]any{"perSecond": 1.0}}}
		// refTime=0 → 回退 DefaultRefTime(120)；用时 100 → 加分 20
		got := Score(ev, rec(map[string]any{"t": 50.0}, 100, 0, 0), 0)
		if got.Bonus != 20 {
			t.Errorf("Bonus = %v，期望 20（refTime 应回退到 120）", got.Bonus)
		}
	})

	t.Run("计分模板为空时按加权求和处理", func(t *testing.T) {
		ev := newEvent(numTask("t", 100, 1.0))
		ev.ScoreRule.Template = ""
		got := Score(ev, rec(map[string]any{"t": 80.0}, 0, 0, 0), DefaultRefTime)
		if got.Base != 80 || !got.Complete {
			t.Errorf("Base = %v / Complete = %v，期望 80 / true", got.Base, got.Complete)
		}
	})

	t.Run("直接求和模板下缺项只累计已录入项", func(t *testing.T) {
		ev := newEvent(numTask("a", 100, 0), numTask("b", 100, 0))
		ev.ScoreRule.Template = model.TplSum
		got := Score(ev, rec(map[string]any{"a": 88.0}, 0, 0, 0), DefaultRefTime)
		if got.Base != 88 || got.Complete {
			t.Errorf("Base = %v / Complete = %v，期望 88 / false", got.Base, got.Complete)
		}
	})
}

func TestRankAllGroupsNilEvent(t *testing.T) {
	if got := RankAllGroups(RankInput{}, RankOptions{}); got != nil {
		t.Errorf("赛项为 nil 时应返回 nil，实际 %v", got)
	}
}

func TestRankAllGroupsSkipsForeignTeams(t *testing.T) {
	f := newRankFixture()
	f.Teams = append(f.Teams, model.Team{ID: 99, EventID: "another_event", TeamNo: "0001", Name: "外队", GroupCode: "小学组", Status: model.TeamActive})

	groups := RankAllGroups(f.input(), RankOptions{})
	for _, g := range groups {
		for _, row := range g.Rows {
			if row.Team.TeamNo == "0001" {
				t.Error("不属于本赛项的队伍不应出现在榜单里")
			}
		}
	}
}

func TestRankTieFallsBackToTeamNo(t *testing.T) {
	// 两队同名、同分、同用时 → 名称比较器返回 0，必须落到队号兜底，否则顺序不确定
	ev := newEvent(numTask("t", 100, 1.0))
	ev.ID = "ev4"
	ev.RankRule = model.RankRule{TieBreak: []string{"score", "time"}, AwardTiers: map[string]float64{}}
	teams := []model.Team{
		{ID: 1, EventID: "ev4", TeamNo: "2002", Name: "同名队", GroupCode: "小学组", Status: model.TeamActive},
		{ID: 2, EventID: "ev4", TeamNo: "2001", Name: "同名队", GroupCode: "小学组", Status: model.TeamActive},
	}
	in := RankInput{Event: ev, Teams: teams, Scores: map[int64][]model.ScoreRecord{
		1: {rec(map[string]any{"t": 80.0}, 90, 0, 0)},
		2: {rec(map[string]any{"t": 80.0}, 90, 0, 0)},
	}}

	rows := Rank(in, RankOptions{})
	if rows[0].Team.TeamNo != "2001" {
		t.Errorf("同名同分同用时应按队号升序，期望 2001 在前，实际 %v", nos(rows))
	}
}

func TestRankSkipsZeroRatioTier(t *testing.T) {
	f := newRankFixture()
	f.Event.RankRule.AwardTiers = map[string]float64{"一等奖": 0, "二等奖": 0.4}
	rows := Rank(f.input(), RankOptions{})
	// 一等奖占比 0 应被跳过，不占用名额
	if rows[0].Award != "二等奖" {
		t.Errorf("第 1 名奖项 = %q，期望 二等奖（占比为 0 的奖项应跳过）", rows[0].Award)
	}
}

func TestRankEmptyTeamSet(t *testing.T) {
	f := newRankFixture()
	f.Teams = nil
	f.Score = map[int64][]model.ScoreRecord{}
	if rows := Rank(f.input(), RankOptions{}); len(rows) != 0 {
		t.Errorf("无队伍时应返回空榜单，实际 %d 行", len(rows))
	}
}

func TestOrderedTiersTieOnRatio(t *testing.T) {
	// 两个自定义奖项占比相同 → 按名称升序，保证顺序确定
	got := orderedTiers(map[string]float64{"银奖": 0.1, "金奖": 0.1})
	if len(got) != 2 || got[0].Name != "金奖" || got[1].Name != "银奖" {
		t.Errorf("占比相同时应按名称升序，实际 %v", []string{got[0].Name, got[1].Name})
	}
}

func TestNameComparatorsEdgeCases(t *testing.T) {
	if got := CompareNameZh("星河队", "星河队"); got != 0 {
		t.Errorf("CompareNameZh 相同字符串应返回 0，实际 %d", got)
	}
	if got := CodepointNameLess("a", "a"); got != 0 {
		t.Errorf("CodepointNameLess 相同字符串应返回 0，实际 %d", got)
	}
	if got := CodepointNameLess("b", "a"); got != 1 {
		t.Errorf("CodepointNameLess(b, a) = %d，期望 1", got)
	}
	if got := CodepointNameLess("a", "b"); got != -1 {
		t.Errorf("CodepointNameLess(a, b) = %d，期望 -1", got)
	}
}

// TestCompareNameZhConcurrency 排序器内部复用了 x/text 的游标，
// 并发调用必须仍然正确（Rank 会被多个 HTTP 请求同时触发）。
func TestCompareNameZhConcurrency(t *testing.T) {
	names := []string{"星河队", "破晓队", "智造队", "晨曦队", "领航队", "创想队"}
	done := make(chan int, len(names)*4)
	for i := 0; i < len(names); i++ {
		for j := 0; j < len(names); j++ {
			a, b := names[i], names[j]
			go func() {
				ab := CompareNameZh(a, b)
				ba := CompareNameZh(b, a)
				if ab != -ba {
					done <- 1
					return
				}
				done <- 0
			}()
		}
	}
	for i := 0; i < len(names)*len(names); i++ {
		if <-done != 0 {
			t.Fatal("并发比较出现不对称结果（CompareNameZh 不是并发安全的）")
		}
	}
}
