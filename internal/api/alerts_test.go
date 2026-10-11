package api_test

// ============================================================================
// 图集 12 / 13 接线：POST /teams/{id}/disqualify + GET /alerts
//
// service 层已验证口径，这里验证「接线」：
//  1. 两个路由都挂上了（不是 404 / 410）
//  2. 登记取消资格 → 201 返回已裁定争议单；GET /alerts 立即可见「取消资格」
//  3. 重复登记 → 400；未知字段 → 400（严格模式）
// ============================================================================

import (
	"net/http"
	"strconv"
	"testing"
)

// TestDisqualifyAndAlertsEndpoint 登记取消资格 → 告警立即派生（读写闭环）。
func TestDisqualifyAndAlertsEndpoint(t *testing.T) {
	ts := newTestServer(t)

	ev := ts.createEvent(t, brainPlanetBody())
	team := ts.createTeam(t, ev.ID, "1001", "作废队", "小学组")

	dqPath := "/api/v1/teams/" + strconv.FormatInt(team.ID, 10) + "/disqualify"

	// 成功登记：201 + 已裁定争议单（含单号）
	var d struct {
		ID     int64  `json:"id"`
		Code   string `json:"code"`
		Status string `json:"status"`
	}
	ts.do(t, http.MethodPost, dqPath, map[string]any{
		"reason": "重复提交，纸质裁定取消资格", "evidence": "纸质件 2 页",
	}, "运营A").expect(t, http.StatusCreated).as(t, &d)
	if d.Status != "decided" || d.Code == "" {
		t.Errorf("应返回已裁定且带单号的争议单: %+v", d)
	}

	// GET /alerts：取消资格告警立即派生（无需任何「发告警」动作）
	var rep struct {
		TimeoutMin int            `json:"timeoutMin"`
		Summary    map[string]int `json:"summary"`
		Alerts     []struct {
			Type  string `json:"type"`
			Level string `json:"level"`
		} `json:"alerts"`
	}
	ts.do(t, http.MethodGet, "/api/v1/alerts", nil, "运营A").expect(t, http.StatusOK).as(t, &rep)
	found := false
	for _, a := range rep.Alerts {
		if a.Type == "取消资格" {
			found = true
		}
	}
	if !found || rep.Summary["取消资格"] != 1 {
		t.Errorf("取消资格告警应立即派生: %+v", rep)
	}
	if rep.TimeoutMin != 30 {
		t.Errorf("超时阈值应与 demo 同口径（30 分钟）: %d", rep.TimeoutMin)
	}

	// 重复登记 → 400
	ts.do(t, http.MethodPost, dqPath, map[string]any{"reason": "再登记"}, "运营A").
		expect(t, http.StatusBadRequest)

	// 未知字段 → 400（严格模式）
	ts.do(t, http.MethodPost, dqPath, map[string]any{"reason": "x", "what": 1}, "运营A").
		expect(t, http.StatusBadRequest)
}
