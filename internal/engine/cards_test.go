package engine

import (
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 牌面计数与红牌后果（2026/10/09 口径）
//
//	黄牌 —— 记满阈值即升级 1 张红牌并清零黄牌计数（循环计数），计数器上限 = 阈值。
//	红牌 —— 当场取消比赛资格：成绩保留、不排名次、不参评奖项。
//	开关 —— 黄牌计数器关闭 = 整个黄牌体系停用（不显示、不累计、不升级）。
// ============================================================================

func cardRules(enabled bool, threshold int) *model.CardRules {
	return &model.CardRules{
		Enabled:      &enabled,
		RedThreshold: threshold,
	}
}

// ---------------------------------------------------------------- 牌面归一

func TestResolveCardsYellowUpgrade(t *testing.T) {
	rules := cardRules(true, 3)
	cases := []struct {
		yellow, red, wantYellow, wantRed, wantUpgraded int
	}{
		{0, 0, 0, 0, 0},
		{1, 0, 1, 0, 0}, // 不满阈值：黄牌计数原样保留
		{2, 0, 2, 0, 0},
		{3, 0, 0, 1, 1}, // 满 3 张：升级 1 张红牌，**黄牌计数清零**
		{4, 0, 1, 1, 1},
		{6, 0, 0, 2, 2}, // 满 6 张：2 张红牌，计数清零
		{7, 0, 1, 2, 2},
	}
	for _, c := range cases {
		got := ResolveCards(model.ScoreRecord{Yellow: c.yellow, Red: c.red}, rules)
		if got.Red != c.wantRed || got.UpgradedRed != c.wantUpgraded || got.Yellow != c.wantYellow {
			t.Errorf("黄 %d 红 %d：得到 yellow=%d red=%d upgraded=%d，期望 yellow=%d red=%d upgraded=%d",
				c.yellow, c.red, got.Yellow, got.Red, got.UpgradedRed, c.wantYellow, c.wantRed, c.wantUpgraded)
		}
	}
}

// TestNormalizeCardsWritePath 写路径归一：录入端「记满即转红并清零」与服务端落库必须同一口径。
func TestNormalizeCardsWritePath(t *testing.T) {
	rules := cardRules(true, 3)
	cases := []struct{ in, wantCur, wantUp int }{
		{0, 0, 0}, {1, 1, 0}, {2, 2, 0},
		{3, 0, 1}, // 记满 3 张 → 计数清零、升级 1 张红牌
		{4, 1, 1}, {6, 0, 2}, {7, 1, 2},
	}
	for _, c := range cases {
		cur, up := NormalizeCards(c.in, rules)
		if cur != c.wantCur || up != c.wantUp {
			t.Errorf("记 %d 张黄牌：得到 (计数 %d, 升级 %d)，期望 (%d, %d)", c.in, cur, up, c.wantCur, c.wantUp)
		}
	}
	// 计数器关闭：不折算（黄牌不累计、也不升级）
	if cur, up := NormalizeCards(9, cardRules(false, 3)); cur != 9 || up != 0 {
		t.Errorf("关闭黄牌计数器时不应折算，得到 (%d, %d)", cur, up)
	}
	// 阈值缺失 → 回落默认 3
	if cur, up := NormalizeCards(3, &model.CardRules{}); cur != 0 || up != 1 {
		t.Errorf("阈值缺失应回落默认 3，得到 (%d, %d)", cur, up)
	}
}

// TestResolveCardsUpgradedRedField 已升级红牌数**存在字段里**（不靠重算）——
// 这是「计数器上限 = 阈值」能成立的关键：UI 归零后仍能知道已经转出过几张红牌。
func TestResolveCardsUpgradedRedField(t *testing.T) {
	rules := cardRules(true, 3)
	got := ResolveCards(model.ScoreRecord{Yellow: 2, UpgradedRed: 1}, rules)
	if got.Red != 1 || got.UpgradedRed != 1 || got.Yellow != 2 {
		t.Errorf("带已升级字段：得到 yellow=%d red=%d upgraded=%d，期望 2/1/1", got.Yellow, got.Red, got.UpgradedRed)
	}
	// 兼容：字段 + 越界黄牌（老数据、或写入端漏归一）合并归一
	got = ResolveCards(model.ScoreRecord{Yellow: 4, UpgradedRed: 1}, rules)
	if got.Red != 2 || got.UpgradedRed != 2 || got.Yellow != 1 {
		t.Errorf("字段 + 越界黄牌应合并归一，得到 yellow=%d red=%d upgraded=%d，期望 1/2/2",
			got.Yellow, got.Red, got.UpgradedRed)
	}
}

func TestResolveCardsThresholdVariants(t *testing.T) {
	// 阈值可配：2 张升 1 张时，2 黄即产生红牌
	rules := cardRules(true, 2)
	if got := ResolveCards(model.ScoreRecord{Yellow: 2}, rules); got.Red != 1 {
		t.Errorf("阈值 2 时 2 张黄牌应升级 1 张红牌，得到 %d", got.Red)
	}
	// 阈值未配置（0）时回落到默认 3
	rules = &model.CardRules{}
	if got := ResolveCards(model.ScoreRecord{Yellow: 2}, rules); got.Red != 0 {
		t.Errorf("阈值未配置应回落默认 3，2 张黄牌不该升级，得到 %d", got.Red)
	}
	if got := ResolveCards(model.ScoreRecord{Yellow: 3}, rules); got.Red != 1 {
		t.Errorf("阈值未配置应回落默认 3，3 张黄牌应升级 1 张，得到 %d", got.Red)
	}
}

func TestResolveCardsCounterDisabled(t *testing.T) {
	// 关掉黄牌计数器 = 整个黄牌体系停用：黄牌不累计、也不升级红牌
	rules := cardRules(false, 3)
	got := ResolveCards(model.ScoreRecord{Yellow: 9}, rules)
	if got.Red != 0 || got.UpgradedRed != 0 {
		t.Errorf("计数器关闭时不应升级红牌，得到 red=%d upgraded=%d", got.Red, got.UpgradedRed)
	}
	// 但裁判直接记的红牌仍然算 —— 红牌是独立入口，不受黄牌开关影响
	got = ResolveCards(model.ScoreRecord{Yellow: 9, Red: 1}, rules)
	if got.Red != 1 || got.UpgradedRed != 0 {
		t.Errorf("计数器关闭时直接红牌应保留，得到 red=%d upgraded=%d", got.Red, got.UpgradedRed)
	}
}

func TestResolveCardsDirectRedAddsUp(t *testing.T) {
	rules := cardRules(true, 3)
	got := ResolveCards(model.ScoreRecord{Yellow: 3, Red: 1}, rules)
	if got.Red != 2 {
		t.Errorf("直接红牌 1 + 黄牌升级 1 = 2，得到 %d", got.Red)
	}
}

func TestResolveCardsNegativeDefense(t *testing.T) {
	rules := cardRules(true, 3)
	got := ResolveCards(model.ScoreRecord{Yellow: -5, Red: -2}, rules)
	if got.Yellow != 0 || got.Red != 0 {
		t.Errorf("负数应归零，得到 yellow=%d red=%d", got.Yellow, got.Red)
	}
}

// ---------------------------------------------------------------- 开关默认值

func TestCardRulesDefaults(t *testing.T) {
	// 历史数据没有 cardRules 字段 → 指针为 nil，必须按「开 / 3 张」处理，
	// 否则改制前存下的赛项会静默停用黄牌计数器。
	var nilRules *model.CardRules
	if !nilRules.EnabledOrDefault() {
		t.Error("cardRules 为 nil 时应默认启用黄牌计数器")
	}
	if got := nilRules.ThresholdOrDefault(); got != model.DefaultRedThreshold {
		t.Errorf("nil 时阈值应取默认 %d，得到 %d", model.DefaultRedThreshold, got)
	}

	// 空结构体（前端写了 {}）同样按默认处理
	empty := &model.CardRules{}
	if !empty.EnabledOrDefault() || empty.ThresholdOrDefault() != model.DefaultRedThreshold {
		t.Error("空 cardRules 应全部取默认值")
	}

	// 显式关闭必须被尊重
	off := cardRules(false, 5)
	if off.EnabledOrDefault() {
		t.Error("显式 enabled=false 不应被默认值覆盖")
	}
	if off.ThresholdOrDefault() != 5 {
		t.Error("显式阈值应生效")
	}
}

func TestReasonLabels(t *testing.T) {
	rules := &model.CardRules{Reasons: []model.CardReason{
		{Code: "R01", Card: "red", Label: "顶撞裁判"},
		{Code: "Y01", Card: "yellow", Label: "小球出界"},
		{Code: "R02", Card: "red", Label: "   "}, // 空白条目跳过
		{Code: "R03", Card: "red", Label: "恶意撞击"},
	}}
	if got := rules.ReasonLabels("red"); len(got) != 2 || got[0] != "顶撞裁判" || got[1] != "恶意撞击" {
		t.Errorf("红牌事由应为 [顶撞裁判 恶意撞击]，得到 %v", got)
	}
	var nilRules *model.CardRules
	if got := nilRules.ReasonLabels("red"); got != nil {
		t.Errorf("nil 规则应返回 nil，得到 %v", got)
	}
}

// ---------------------------------------------------------------- 红牌后果

func TestDisqualifiedByCardsUsesAnyRound(t *testing.T) {
	rules := cardRules(true, 3)
	recs := []model.ScoreRecord{
		{Yellow: 0, Red: 0},
		{Yellow: 0, Red: 1}, // 任一轮命中即取消资格
	}
	if !DisqualifiedByCards(recs, rules) {
		t.Error("任一轮有红牌即应取消比赛资格")
	}
	if DisqualifiedByCards(recs[:1], rules) {
		t.Error("无红牌不应取消比赛资格")
	}
}

// TestRankDisqualifiedKeepsScoreButLosesRankAndAward 红牌队：成绩保留、
// 不占名次序号、不参评奖项，且排在榜单末尾。
func TestRankDisqualifiedKeepsScoreButLosesRankAndAward(t *testing.T) {
	f := newRankFixture()
	// 甲队（90 分 / 100 秒，原在乙队之后）记 1 张红牌
	f.Score[1] = []model.ScoreRecord{rec(map[string]any{"t": 90.0}, 100, 0, 1)}

	rows := Rank(f.input(), RankOptions{})

	// 丁队弃赛不参与 → 3 行；红牌队**仍在**榜单里（成绩与判罚要可查可公示）
	if len(rows) != 3 {
		t.Fatalf("榜单应有 3 行（含被取消资格的 1 队），得到 %d：%v", len(rows), nos(rows))
	}
	last := rows[len(rows)-1]
	if last.Team.ID != 1 {
		t.Fatalf("被取消资格的队伍应排在最后，实际最后一行是 %s", last.Team.Name)
	}
	if !last.Disqualified {
		t.Error("应标记 Disqualified")
	}
	if last.Rank != 0 {
		t.Errorf("被取消资格的队伍不应有完赛名次，得到 rank=%d", last.Rank)
	}
	if last.Award != "" {
		t.Errorf("被取消资格的队伍不应获奖，得到 award=%q", last.Award)
	}
	// 成绩保留：分数照常算出（留痕与公示说明要用）
	if last.Result.Total != 90 || last.Result.Base != 90 {
		t.Errorf("成绩应保留（90 分），得到 total=%v base=%v", last.Result.Total, last.Result.Base)
	}
	// 名次不留空洞：剩下两队的名次必须是 1、2
	if rows[0].Rank != 1 || rows[1].Rank != 2 {
		t.Errorf("名次应连续为 1、2，得到 %d、%d", rows[0].Rank, rows[1].Rank)
	}
	if rows[0].Team.ID != 2 || rows[1].Team.ID != 3 {
		t.Errorf("名次顺序应为 乙队(1002) → 丙队(1003)，得到 %v", nos(rows))
	}
}

// TestRankDisqualifiedFromUpgradedYellow 黄牌累计升级出来的红牌同样取消资格。
func TestRankDisqualifiedFromUpgradedYellow(t *testing.T) {
	f := newRankFixture()
	f.Event.PenaltyRule = model.PenaltyRule{
		Template:  model.PenaltyRecordOnly,
		CardRules: cardRules(true, 3),
	}
	// 甲队 3 张黄牌 → 升级 1 张红牌 → 取消资格
	f.Score[1] = []model.ScoreRecord{rec(map[string]any{"t": 90.0}, 100, 3, 0)}

	rows := Rank(f.input(), RankOptions{})
	if !rows[len(rows)-1].Disqualified {
		t.Errorf("满 3 张黄牌应升级红牌并取消资格，实际末行 %s 未标记", rows[len(rows)-1].Team.Name)
	}

	// 反证：关掉黄牌计数器后，同样的 3 张黄牌不再升级，也不取消资格
	f2 := newRankFixture()
	f2.Event.PenaltyRule = model.PenaltyRule{
		Template:  model.PenaltyRecordOnly,
		CardRules: cardRules(false, 3),
	}
	f2.Score[1] = []model.ScoreRecord{rec(map[string]any{"t": 90.0}, 100, 3, 0)}
	rows2 := Rank(f2.input(), RankOptions{})
	for _, r := range rows2 {
		if r.Disqualified {
			t.Errorf("黄牌计数器关闭时不应有队伍被取消资格，%s 被标记了", r.Team.Name)
		}
	}
}

// TestRankDisqualifyAppliesEvenWithoutCardRules 红牌取消资格是**赛制固定后果**，
// 不依赖赛项有没有配过牌面规则 —— 只要记录里有红牌就生效。
//
// 注意这是**追溯适用**：口径变更后，旧赛项若重算榜单，带红牌的队伍同样会被取消资格。
// 这是有意为之（红牌含义变了），不是在读旧数据时开倒车。
func TestRankDisqualifyAppliesEvenWithoutCardRules(t *testing.T) {
	f := newRankFixture()
	f.Score[1] = []model.ScoreRecord{rec(map[string]any{"t": 90.0}, 100, 0, 1)}

	rows := Rank(f.input(), RankOptions{})
	if !rows[len(rows)-1].Disqualified {
		t.Error("红牌取消资格与 cardRules 是否配置无关，应生效")
	}
}

// TestRankDisqualifiedRowStillCountsRounds 取消资格不抹掉轮次信息（留痕要完整）。
func TestRankDisqualifiedRowStillCountsRounds(t *testing.T) {
	f := newRankFixture()
	f.Event.PenaltyRule = model.PenaltyRule{CardRules: cardRules(true, 3)}
	f.Score[1] = []model.ScoreRecord{
		{RoundNo: 1, TaskValues: map[string]any{"t": 90.0}, DurationSec: 100, Red: 0},
		{RoundNo: 2, TaskValues: map[string]any{"t": 95.0}, DurationSec: 80, Red: 1},
	}

	rows := Rank(f.input(), RankOptions{})
	last := rows[len(rows)-1]
	if !last.Disqualified {
		t.Fatal("应被取消资格")
	}
	if len(last.Rounds) != 2 {
		t.Errorf("轮次信息应保留（2 轮），得到 %v", last.Rounds)
	}
	if last.Result.Total != 95 {
		t.Errorf("取优轮成绩应保留（95），得到 %v", last.Result.Total)
	}
}
