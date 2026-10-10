package service_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/service"
)

// rankOf 取某组别里某队的名次；不在榜上返回 0。
func rankOf(t *testing.T, res *service.StandingsResult, group, no string) int {
	t.Helper()
	for _, g := range res.Groups {
		if g.Group != group {
			continue
		}
		for i := range g.Rows {
			if g.Rows[i].Team.TeamNo == no {
				return g.Rows[i].Rank
			}
		}
		return 0
	}
	t.Fatalf("榜单里没有组别 %s", group)
	return 0
}

// TestSubstituteModeControlsRankGap 名次编排（递补规则）经真库生效并能实时切换。
//
// 守的是 2026-10-10 对出来的一处两端不一致：前端 `substituteRule.mode` 默认 'none'
// （不递补，作废队的位置留空 → 公示表出现 4 → 6 这种空洞），而后端原先只有
// 「名次连续发放」一种行为 —— 同一场比赛两边给出的名次不同。
//
// 素材：小学组 甲(90) / 乙(80) / 丙(70)，把**中间**的乙队裁定取消资格。
// 于是「不递补」下丙队应仍是第 3 名（第 2 名空缺），「顺延」下丙队前移为第 2 名。
func TestSubstituteModeControlsRankGap(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")

	// 从未设置过 → 取默认口径（不递补）。这条默认值只由
	// model.SubstituteMode.OrDefault() 定义一处，服务层不得再写一遍。
	rules, err := svc.ContestRules(ctx)
	if err != nil {
		t.Fatalf("读赛事级规则失败: %v", err)
	}
	if rules.SubstituteMode != model.SubstituteNone {
		t.Fatalf("未设置过的赛事应取默认「不递补」，实际 %q", rules.SubstituteMode)
	}

	ev := mustEvent(t, svc, brainPlanetEvent())
	teams := []*model.Team{
		mustTeam(t, svc, ev.ID, "1001", "甲队", "小学组"),
		mustTeam(t, svc, ev.ID, "1002", "乙队", "小学组"),
		mustTeam(t, svc, ev.ID, "1003", "丙队", "小学组"),
	}
	for i, tm := range teams {
		score := 90.0 - float64(i)*10 // 甲 90 / 乙 80 / 丙 70，名次唯一确定
		if _, err := svc.SaveScore(ctx, &model.ScoreRecord{
			TeamID: tm.ID, RoundNo: 1, DurationSec: 100,
			TaskValues: map[string]any{"focus": score, "build": score},
			Signed:     true,
		}); err != nil {
			t.Fatalf("录分 %s 失败: %v", tm.Name, err)
		}
	}

	// 把中间的乙队裁定取消资格 —— 作废的判据是争议工单（唯一事实源），所以真走一遍。
	d, err := svc.ReportDispute(ctx, teams[1].ID, 1, model.DisputeOther, "基准样例：复核实锤")
	if err != nil {
		t.Fatalf("登记争议失败: %v", err)
	}
	if err := svc.DecideDispute(ctx, d.ID, model.VerdictDisqualify, "取消资格", 0, 0); err != nil {
		t.Fatalf("裁定取消资格失败: %v", err)
	}

	// —— 不递补（默认）：第 2 名空缺 ——
	res, err := svc.Standings(ctx, ev.ID, service.StandingsOptions{})
	if err != nil {
		t.Fatalf("出榜单失败: %v", err)
	}
	if res.SubstituteMode != model.SubstituteNone {
		t.Errorf("榜单应带回当前生效的口径，实际 %q", res.SubstituteMode)
	}
	if got := rankOf(t, res, "小学组", "1002"); got != 0 {
		t.Errorf("被裁定取消资格的队伍不该在榜上，实际 rank=%d", got)
	}
	if got := rankOf(t, res, "小学组", "1001"); got != 1 {
		t.Errorf("甲队应为第 1 名，实际 %d", got)
	}
	if got := rankOf(t, res, "小学组", "1003"); got != 3 {
		t.Errorf("不递补时丙队应**仍是第 3 名**（第 2 名空缺），实际 %d —— "+
			"若为 2 就是又回到「恒连续编号」那一档了", got)
	}

	// —— 切到「按名次顺延」：丙队前移 ——
	if _, err := svc.SetSubstituteMode(ctx, model.SubstituteRank, "赛前定：取消资格者位置顺延"); err != nil {
		t.Fatalf("切换名次编排失败: %v", err)
	}
	res2, err := svc.Standings(ctx, ev.ID, service.StandingsOptions{})
	if err != nil {
		t.Fatalf("出榜单失败: %v", err)
	}
	if res2.SubstituteMode != model.SubstituteRank {
		t.Errorf("口径未生效，实际 %q", res2.SubstituteMode)
	}
	if got := rankOf(t, res2, "小学组", "1003"); got != 2 {
		t.Errorf("按名次顺延后丙队应前移为第 2 名，实际 %d", got)
	}

	// —— 切回不递补：名次必须回到第 3 ——
	// 这一条同时证明榜单是**实时读配置**，而不是出榜时把口径快照进去了。
	if _, err := svc.SetSubstituteMode(ctx, model.SubstituteNone, "复核后改回"); err != nil {
		t.Fatalf("切回不递补失败: %v", err)
	}
	res3, err := svc.Standings(ctx, ev.ID, service.StandingsOptions{})
	if err != nil {
		t.Fatalf("出榜单失败: %v", err)
	}
	if got := rankOf(t, res3, "小学组", "1003"); got != 3 {
		t.Errorf("切回不递补后丙队应回到第 3 名，实际 %d", got)
	}
}

// TestSubstituteModeValidationAndAudit 取值校验、留痕与幂等。
func TestSubstituteModeValidationAndAudit(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")

	// 非法取值 → 字段错误（不是 500，也不是静默落成默认值）
	_, err := svc.SetSubstituteMode(ctx, model.SubstituteMode("keep"), "乱填")
	if err == nil {
		t.Fatal("非法名次编排取值应被拒绝")
	}
	var fe *model.FieldError
	if !errors.As(err, &fe) || fe.Field != "substituteMode" {
		t.Errorf("应返回 substituteMode 字段错误，实际 %v", err)
	}
	if rules, _ := svc.ContestRules(ctx); rules.SubstituteMode != model.SubstituteNone {
		t.Errorf("被拒绝的写入不该改到库里的值，实际 %q", rules.SubstituteMode)
	}

	base := len(auditsOf(t, svc, model.ActConfig))

	// 首次设置 → 落库 + 留痕 1 条
	if _, err := svc.SetSubstituteMode(ctx, model.SubstituteRank, "赛前定：按名次顺延"); err != nil {
		t.Fatalf("设置名次编排失败: %v", err)
	}
	got, err := svc.ContestRules(ctx)
	if err != nil {
		t.Fatalf("读规则失败: %v", err)
	}
	if got.SubstituteMode != model.SubstituteRank || got.SubstituteNote != "赛前定：按名次顺延" {
		t.Errorf("规则未正确落库：%+v", got)
	}
	if n := len(auditsOf(t, svc, model.ActConfig)); n != base+1 {
		t.Fatalf("应新增 1 条「改配置」留痕，实际新增 %d", n-base)
	}

	// 同值重复 → 不写库、不留痕（现场"点一下没反应就再点"很常见）
	if _, err := svc.SetSubstituteMode(ctx, model.SubstituteRank, "赛前定：按名次顺延"); err != nil {
		t.Fatalf("同值重复提交不该报错: %v", err)
	}
	if n := len(auditsOf(t, svc, model.ActConfig)); n != base+1 {
		t.Errorf("同值重复提交不该新增留痕，实际新增 %d", n-base)
	}

	// 留空 reason → 服务端按新口径生成说明；留痕写的是**中文口径名**，
	// 不是 none / rank —— 三个月后来查"名次为什么长这样"要读得懂。
	if _, err := svc.SetSubstituteMode(ctx, model.SubstituteNone, "  "); err != nil {
		t.Fatalf("切回不递补失败: %v", err)
	}
	if rules, _ := svc.ContestRules(ctx); rules.SubstituteNote == "" {
		t.Error("留空 reason 时应生成一条说明，而不是留空")
	}
	found := false
	for _, l := range auditsOf(t, svc, model.ActConfig) {
		if l.Before == "按名次顺延" && l.After == "不递补（空缺不补）" {
			found = true
		}
	}
	if !found {
		t.Error("留痕应写「旧口径 → 新口径」的中文名（按名次顺延 → 不递补（空缺不补））")
	}

	// 空白 reason 不该被当成"已设置的理由"存进去
	if note, _ := svc.ContestRules(ctx); strings.TrimSpace(note.SubstituteNote) == "" {
		t.Error("留痕与配置里的理由都不该是空白串")
	}
}
