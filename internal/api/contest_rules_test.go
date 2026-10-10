package api_test

import (
	"net/http"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// TestContestRulesOverHTTP 赛事级规则（名次编排 / 递补）经 HTTP 读写。
//
// 三件事一起守：
//  1. **未设置过返回默认口径而不是 404** —— 界面要永远能渲染出"当前生效的是什么"；
//  2. 切换真的落库（GET 读回），带中文留痕；
//  3. 非法取值被拦成 400，而不是静默落成默认值（那会让名次算法悄悄换一档）。
func TestContestRulesOverHTTP(t *testing.T) {
	ts := newTestServer(t)

	var got model.ContestRules
	ts.do(t, http.MethodGet, "/api/v1/contest/rules", nil, "").expect(t, http.StatusOK).as(t, &got)
	if got.SubstituteMode != model.SubstituteNone {
		t.Fatalf("未设置过的赛事应返回默认「不递补」，实际 %q", got.SubstituteMode)
	}

	var next model.ContestRules
	ts.do(t, http.MethodPut, "/api/v1/contest/rules",
		map[string]any{"substituteMode": "rank", "reason": "赛前定：取消资格者位置顺延"}, "运营A").
		expect(t, http.StatusOK).as(t, &next)
	if next.SubstituteMode != model.SubstituteRank {
		t.Fatalf("切换后应返回新口径，实际 %q", next.SubstituteMode)
	}

	// 读回：落库而不是只改了返回值
	ts.do(t, http.MethodGet, "/api/v1/contest/rules", nil, "").expect(t, http.StatusOK).as(t, &got)
	if got.SubstituteMode != model.SubstituteRank || got.SubstituteNote == "" {
		t.Errorf("规则未落库或理由为空：%+v", got)
	}

	// 非法取值 → 400（不能静默落成默认值：那等于悄悄把名次算法换了一档）
	ts.do(t, http.MethodPut, "/api/v1/contest/rules",
		map[string]any{"substituteMode": "keep"}, "运营A").
		expect(t, http.StatusBadRequest)
	ts.do(t, http.MethodGet, "/api/v1/contest/rules", nil, "").expect(t, http.StatusOK).as(t, &got)
	if got.SubstituteMode != model.SubstituteRank {
		t.Errorf("被拒绝的写入不该改到库里的值，实际 %q", got.SubstituteMode)
	}
}

// TestStandingsCarriesSubstituteMode 榜单要带回当前口径。
//
// 名次出现空洞（4 → 6）时，看榜的人第一反应是"是不是漏了一队"。
// 把口径一并返回，公示页才能自己解释「第 5 名空缺 = 该名次队伍被取消资格，本场不递补」。
func TestStandingsCarriesSubstituteMode(t *testing.T) {
	ts := newTestServer(t)
	ev := ts.createEvent(t, brainPlanetBody())

	var res struct {
		EventID        string               `json:"eventId"`
		SubstituteMode model.SubstituteMode `json:"substituteMode"`
	}
	ts.do(t, http.MethodGet, "/api/v1/events/"+ev.ID+"/standings", nil, "").
		expect(t, http.StatusOK).as(t, &res)
	if res.SubstituteMode != model.SubstituteNone {
		t.Errorf("未设置过时应按默认口径返回，实际 %q", res.SubstituteMode)
	}

	ts.do(t, http.MethodPut, "/api/v1/contest/rules",
		map[string]any{"substituteMode": "rank"}, "运营A").expect(t, http.StatusOK)

	ts.do(t, http.MethodGet, "/api/v1/events/"+ev.ID+"/standings", nil, "").
		expect(t, http.StatusOK).as(t, &res)
	if res.SubstituteMode != model.SubstituteRank {
		t.Errorf("切换口径后榜单应跟着变，实际 %q", res.SubstituteMode)
	}
}
