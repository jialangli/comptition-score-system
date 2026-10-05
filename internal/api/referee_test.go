package api_test

import (
	"net/http"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 裁判码 HTTP 集成测试
//
// 重点：激活失败的**状态码必须可区分** —— 前端靠它分流到 P1.5b（码无效）
// 与 P1.5c（姓名不匹配）两个独立页面。做成统一 401 就没法分流了。
// ============================================================================

func TestRefereeCodesOverHTTP(t *testing.T) {
	ts := newTestServer(t)
	ev := ts.createEvent(t, brainPlanetBody())

	// ---------- 赛前建档发码 ----------
	var issued struct {
		Code        model.RefereeCode `json:"code"`
		RoleLabel   string            `json:"roleLabel"`
		StatusLabel string            `json:"statusLabel"`
	}
	ts.do(t, http.MethodPost, "/api/v1/referee-codes", map[string]any{
		"name": "张老师", "role": "chief", "eventId": ev.ID, "groupCode": "小学组",
	}, "运营A").expect(t, http.StatusCreated).as(t, &issued)

	if len(issued.Code.Code) != 6 {
		t.Fatalf("裁判码应为 6 位，实际 %q", issued.Code.Code)
	}
	if issued.Code.Status != model.RefereeUnused {
		t.Fatalf("新建应为未激活，实际 %q", issued.Code.Status)
	}
	if issued.RoleLabel != "裁判长" {
		t.Fatalf("身份文案应为「裁判长」，实际 %q", issued.RoleLabel)
	}
	if issued.Code.EventID != ev.ID || issued.Code.GroupCode != "小学组" {
		t.Fatalf("预绑范围不对：%q / %q", issued.Code.EventID, issued.Code.GroupCode)
	}

	// 姓名为空 → 400（双因子缺一半不成立）
	ts.do(t, http.MethodPost, "/api/v1/referee-codes", map[string]any{
		"name": "   ", "eventId": ev.ID, "groupCode": "小学组",
	}, "运营A").expect(t, http.StatusBadRequest)

	// ---------- 激活成功 ----------
	var act struct {
		Name      string `json:"name"`
		Role      string `json:"role"`
		RoleLabel string `json:"roleLabel"`
		EventID   string `json:"eventId"`
		GroupCode string `json:"groupCode"`
		Scope     string `json:"scope"`
		Status    string `json:"status"`
	}
	ts.do(t, http.MethodPost, "/api/v1/referee-codes/activate",
		map[string]any{"code": issued.Code.Code, "name": "张老师"}, "张老师").
		expect(t, http.StatusOK).as(t, &act)

	if act.Status != string(model.RefereeActivated) {
		t.Fatalf("激活后状态应为 activated，实际 %q", act.Status)
	}
	if act.RoleLabel != "裁判长" {
		t.Fatalf("身份应回传，实际 %q", act.RoleLabel)
	}
	if act.EventID != ev.ID || act.GroupCode != "小学组" {
		t.Fatalf("应回传预绑执裁范围：%q / %q", act.EventID, act.GroupCode)
	}
	if act.Scope == "" {
		t.Fatal("应回传执裁范围文案，供 P1.5 只读展示")
	}

	// ---------- 激活失败：三种必须可区分 ----------
	// 姓名不匹配 → 400（P1.5c）
	ts.do(t, http.MethodPost, "/api/v1/referee-codes/activate",
		map[string]any{"code": issued.Code.Code, "name": "李老师"}, "李老师").
		expect(t, http.StatusBadRequest)

	// 码无效 → 404（P1.5b）
	ts.do(t, http.MethodPost, "/api/v1/referee-codes/activate",
		map[string]any{"code": "ZZZZZZ", "name": "张老师"}, "张老师").
		expect(t, http.StatusNotFound)

	// ---------- 换平板重装：重复激活应幂等成功 ----------
	ts.do(t, http.MethodPost, "/api/v1/referee-codes/activate",
		map[string]any{"code": issued.Code.Code, "name": "张老师"}, "张老师").
		expect(t, http.StatusOK)

	// ---------- 列表 ----------
	var list struct {
		Codes     []model.RefereeCode `json:"codes"`
		Total     int                 `json:"total"`
		Activated int                 `json:"activated"`
	}
	ts.do(t, http.MethodGet, "/api/v1/referee-codes", nil, "").
		expect(t, http.StatusOK).as(t, &list)
	if list.Total != 1 {
		t.Fatalf("列表应有 1 条，实际 %d", list.Total)
	}
	if list.Activated != 1 {
		t.Fatalf("应统计到 1 条已激活，实际 %d", list.Activated)
	}
}
