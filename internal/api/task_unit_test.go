package api_test

import (
	"net/http"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// TestTaskUnitOverHTTP 量词经 HTTP 往返不丢，且过长的量词会被拦下。
//
// 服务层已有同口径的存储往返用例，这里补的是**HTTP 编解码这一段**：
// 请求体字段名写错、或 handler 换成了别的 DTO，一样会丢，而那样只有集成测试能发现。
//
// 还要守一条容易忽略的兼容性：请求体是 DisallowUnknownFields 严格模式 ——
// 前端一旦开始发 unit，后端必须已经能收，否则**整条请求 400**。
// 所以这个用例既证明「能收」，也证明「能读回」。
func TestTaskUnitOverHTTP(t *testing.T) {
	ts := newTestServer(t)

	body := brainPlanetBody()
	body["tasks"] = []map[string]any{
		{"id": "ball", "name": "能源球运输", "type": "count", "maxScore": 160, "weight": 20, "control": "counter", "unit": "颗"},
		{"id": "mine", "name": "矿石运输入仓", "type": "count", "maxScore": 90, "weight": 15, "control": "counter"},
	}
	ev := ts.createEvent(t, body)
	if len(ev.Tasks) != 2 || ev.Tasks[0].Unit != "颗" {
		t.Fatalf("建赛项返回的量词不对：%+v", ev.Tasks)
	}

	// 读回来还在 —— 这一步才是「真落库了」的证据
	var got model.Event
	ts.do(t, http.MethodGet, "/api/v1/events/brain_planet", nil, "").expect(t, http.StatusOK).as(t, &got)
	if len(got.Tasks) != 2 {
		t.Fatalf("读回的任务数 = %d，期望 2", len(got.Tasks))
	}
	if got.Tasks[0].Unit != "颗" {
		t.Errorf("读回量词 = %q，期望「颗」", got.Tasks[0].Unit)
	}
	if got.Tasks[1].Unit != "" {
		t.Errorf("未配量词的任务读回 = %q，期望空串", got.Tasks[1].Unit)
	}

	// 改配置路径同样要能带上量词（PUT 走的是另一个 DTO，最容易漏）
	updated := brainPlanetBody()
	updated["tasks"] = []map[string]any{
		{"id": "ball", "name": "能源球运输", "type": "count", "maxScore": 160, "weight": 20, "control": "counter", "unit": "个"},
	}
	updated["reason"] = "口径更正：能源球用「个」"
	ts.do(t, http.MethodPut, "/api/v1/events/brain_planet", updated, "运营A").expect(t, http.StatusOK)

	ts.do(t, http.MethodGet, "/api/v1/events/brain_planet", nil, "").expect(t, http.StatusOK).as(t, &got)
	if got.Tasks[0].Unit != "个" {
		t.Errorf("改配置后量词 = %q，期望「个」（PUT 路径把量词吞了）", got.Tasks[0].Unit)
	}

	// 过长的量词 → 400，且响应要把原因说清楚（而不是笼统的「参数错误」）
	bad := brainPlanetBody()
	bad["tasks"] = []map[string]any{
		{"id": "ball", "name": "能源球运输", "type": "count", "maxScore": 160, "weight": 20, "control": "counter",
			"unit": "颗颗颗颗颗"},
	}
	bad["reason"] = "试一个过长的量词"
	res := ts.do(t, http.MethodPut, "/api/v1/events/brain_planet", bad, "运营A")
	res.expect(t, http.StatusBadRequest)
	var out struct {
		Errors []engineIssue `json:"errors"`
	}
	res.as(t, &out)
	if len(out.Errors) == 0 {
		t.Fatal("400 响应应在 data 里带出具体校验问题")
	}
}
