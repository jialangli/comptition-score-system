package api_test

// ============================================================================
// 改分申请单「用时」的接线（迁移 0022 / wireframe P2b）
//
// service 层已验证口径（见 service/change_time_test.go），这里只验接线：
//  1. POST change-requests 能收 afterTime，并把服务端带出的原用时回给前端；
//  2. POST apply-change 的 time 与申请单声明值不一致时 → 400（不是静默落库）；
//  3. 一致时 → 200，且成绩真的改了；
//  4. 严格模式：afterTime 写错类型 → 400（不静默忽略）。
// ============================================================================

import (
	"net/http"
	"strings"
	"testing"
)

func TestChangeRequestTimeOverHTTP(t *testing.T) {
	ts := newTestServer(t)
	ev := ts.createEvent(t, brainPlanetBody())
	team := ts.createTeam(t, ev.ID, "1001", "用时队", "小学组")
	base := "/api/v1/teams/" + itoa(team.ID) + "/scores"

	// 先录一轮成绩：用时 100 秒（01:40）
	ts.do(t, http.MethodPut, base+"/1", map[string]any{
		"tasks": map[string]any{"focus": 80, "build": 82}, "time": 100, "signed": true,
	}, "裁判A").expect(t, http.StatusOK)

	// 申请：连用时一起改（100 → 80 秒）
	var req struct {
		ID         int64    `json:"id"`
		Before     float64  `json:"before"`
		After      float64  `json:"after"`
		BeforeTime *float64 `json:"beforeTime"`
		AfterTime  *float64 `json:"afterTime"`
		Approved   bool     `json:"approved"`
	}
	ts.do(t, http.MethodPost, base+"/1/change-requests", map[string]any{
		"after": 90, "afterTime": 80, "reason": "用时复核：停表早按 20 秒",
	}, "裁判A").expect(t, http.StatusOK).as(t, &req)

	if req.Approved {
		t.Error("改分申请不应自动获批")
	}
	if req.BeforeTime == nil || *req.BeforeTime != 100 {
		t.Errorf("原用时应由服务端带出 100 秒，实际 %v", req.BeforeTime)
	}
	if req.AfterTime == nil || *req.AfterTime != 80 {
		t.Errorf("拟改用时应回显 80 秒，实际 %v", req.AfterTime)
	}

	// 授权落库时给了另一个用时 → 400，且报错要点出两端值
	bad := ts.do(t, http.MethodPost, base+"/1/apply-change", map[string]any{
		"tasks": map[string]any{"focus": 90, "build": 90}, "time": 60, "signed": true,
		"reason": "按申请单复核", "approver": "裁判长B",
	}, "裁判长B")
	bad.expect(t, http.StatusBadRequest)
	if !strings.Contains(bad.Msg, "01:20") || !strings.Contains(bad.Msg, "01:00") {
		t.Errorf("400 应同时给出申请单声明值与本次提交值（01:20 / 01:00），实际 %q", bad.Msg)
	}

	// 按申请单声明值落库 → 200，且成绩用时真的变了
	var rec struct {
		Time float64 `json:"time"`
	}
	ts.do(t, http.MethodPost, base+"/1/apply-change", map[string]any{
		"tasks": map[string]any{"focus": 90, "build": 90}, "time": 80, "signed": true,
		"reason": "按申请单复核", "approver": "裁判长B",
	}, "裁判长B").expect(t, http.StatusOK).as(t, &rec)
	if rec.Time != 80 {
		t.Errorf("落库用时应为 80 秒，实际 %v", rec.Time)
	}

	// 读回确认（不只看写响应）
	var got struct {
		Time float64 `json:"time"`
	}
	ts.do(t, http.MethodGet, base+"/1", nil, "裁判A").expect(t, http.StatusOK).as(t, &got)
	if got.Time != 80 {
		t.Errorf("GET 读回用时应为 80 秒，实际 %v", got.Time)
	}
}

// TestChangeRequestTimeRejectsBadInput 严格模式与字段校验：
// 类型写错要 400（不能静默当成"没传"），负数要 400。
func TestChangeRequestTimeRejectsBadInput(t *testing.T) {
	ts := newTestServer(t)
	ev := ts.createEvent(t, brainPlanetBody())
	team := ts.createTeam(t, ev.ID, "1002", "校验队", "小学组")
	base := "/api/v1/teams/" + itoa(team.ID) + "/scores"

	ts.do(t, http.MethodPut, base+"/1", map[string]any{
		"tasks": map[string]any{"focus": 80, "build": 82}, "time": 100, "signed": true,
	}, "裁判A").expect(t, http.StatusOK)

	// 类型写错：字符串必须是 400，而不是"忽略该字段后照常建单"
	ts.do(t, http.MethodPost, base+"/1/change-requests", map[string]any{
		"after": 90, "afterTime": "80s", "reason": "类型写错",
	}, "裁判A").expect(t, http.StatusBadRequest)

	// 负数 → 400（字段级报错）
	neg := ts.do(t, http.MethodPost, base+"/1/change-requests", map[string]any{
		"after": 90, "afterTime": -1, "reason": "负用时",
	}, "裁判A")
	neg.expect(t, http.StatusBadRequest)
	if !strings.Contains(neg.Msg, "负数") {
		t.Errorf("400 应说明用时不能为负，实际 %q", neg.Msg)
	}
}
