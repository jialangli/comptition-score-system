package model

import (
	"fmt"
	"testing"
)

func TestTeamSessionValid(t *testing.T) {
	cases := []struct {
		session TeamSession
		want    bool
	}{
		{SessionRound1, true},
		{SessionRound2, true},
		{SessionBoth, true},
		{"", false},
		{"1 ", false},
		{"01", false},
		{"all", false},
		{SessionRound1 + SessionRound2, false},
	}
	for _, c := range cases {
		if got := c.session.Valid(); got != c.want {
			t.Errorf("TeamSession(%q).Valid() = %v, 期望 %v", c.session, got, c.want)
		}
	}
}

func TestTeamSessionLabel(t *testing.T) {
	cases := []struct {
		session TeamSession
		want    string
	}{
		{SessionRound1, "仅第 1 轮"},
		{SessionRound2, "仅第 2 轮"},
		{SessionBoth, "两轮"},
		{"", ""}, // 空值由调用方（sessionText）翻成「两轮（默认）」
	}
	for _, c := range cases {
		if got := c.session.Label(); got != c.want {
			t.Errorf("TeamSession(%q).Label() = %q, 期望 %q", c.session, got, c.want)
		}
	}
}

// TestTeamSessionRounds 锁住赛项级的「该队要打哪几轮」判据。
//
// 这张表就是现场会遇到的全部组合：两轮赛项下一队下午缺席（session='1'）、
// 一轮赛项、以及**一轮赛项却被标成只打第 2 轮**这种事前不该发生、
// 但改了赛项配置之后必然出现的组合。
//
// 最后那种组合刻意**回落到赛项轮次**（与前端 teamRounds 逐字同口径）：
// 回落口径下该队仍留在「待完成名单」里，运营会看到它并去修配置；
// 若算成空，该队就静默消失了 —— 没人会发现配置错了。
func TestTeamSessionRounds(t *testing.T) {
	cases := []struct {
		name        string
		session     TeamSession
		eventRounds []int
		want        string
	}{
		{"两轮赛项 · 两轮都打", SessionBoth, []int{1, 2}, "[1 2]"},
		{"两轮赛项 · 只打第 1 轮（下午缺席）", SessionRound1, []int{1, 2}, "[1]"},
		{"两轮赛项 · 只打第 2 轮", SessionRound2, []int{1, 2}, "[2]"},
		{"一轮赛项 · 两轮都打 → 交集只有第 1 轮", SessionBoth, []int{1}, "[1]"},
		{"一轮赛项 · 只打第 1 轮", SessionRound1, []int{1}, "[1]"},
		{"一轮赛项却标成只打第 2 轮 → 交集为空，回落赛项轮次", SessionRound2, []int{1}, "[1]"},
		{"赛项无轮次配置", SessionBoth, nil, "[]"},
		{"空值按两轮口径", "", []int{1, 2}, "[1 2]"},
		{"非法值按两轮口径（漏传不该变成一轮都不打）", TeamSession("x"), []int{1, 2}, "[1 2]"},
	}
	for _, c := range cases {
		got := c.session.Rounds(c.eventRounds)
		if fmt.Sprint(got) != c.want {
			t.Errorf("%s：%q.Rounds(%v) = %s, 期望 %s",
				c.name, c.session, c.eventRounds, fmt.Sprint(got), c.want)
		}
	}
}

// TestTeamSessionIncludes 锁住**场次级**判据（场次队伍派生的匹配条件）。
//
// 与 Rounds 的区别：Includes 不需要知道赛项计划 —— 赛项只有 1 轮时
// 压根不存在 round=2 的场次，所以「该队该不该上这个场次」只看这一个值。
func TestTeamSessionIncludes(t *testing.T) {
	cases := []struct {
		name    string
		session TeamSession
		round   int
		want    bool
	}{
		{"两轮都打 · 第 1 轮", SessionBoth, 1, true},
		{"两轮都打 · 第 2 轮", SessionBoth, 2, true},
		{"只打第 1 轮 · 第 1 轮", SessionRound1, 1, true},
		{"只打第 1 轮 · 第 2 轮（下午不该出现）", SessionRound1, 2, false},
		{"只打第 2 轮 · 第 2 轮", SessionRound2, 2, true},
		{"只打第 2 轮 · 第 1 轮", SessionRound2, 1, false},
		{"空值按两轮口径", "", 2, true},
		{"非法值按两轮口径", TeamSession("x"), 1, true},
	}
	for _, c := range cases {
		if got := c.session.Includes(c.round); got != c.want {
			t.Errorf("%s：%q.Includes(%d) = %v, 期望 %v", c.name, c.session, c.round, got, c.want)
		}
	}
}
